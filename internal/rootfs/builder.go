package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/download"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hkgen"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hooklang"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/sandbox"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/toolchain"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// defaultMirror -- PRZENIESIONE do config.DefaultMirror ([release] -> mirror
// jest teraz konfigurowalny). Alias zostawiony zeby nie lamac ewentualnych
// zewnetrznych odwolan -- kod w tym pakiecie uzywa juz
// b.Config.EffectiveMirror()/EffectiveArch().
const defaultMirror = config.DefaultMirror

// Builder buduje rootfs na podstawie sparsowanego projektu i konfiguracji.
type Builder struct {
	Project   *liveparse.Project
	Config    *config.Config
	RootfsDir string // katalog docelowy rootfs
	WorkDir   string // katalog roboczy buildu (na toolchain/bin/ i inne temp)

	// OriginRefspec to refspec obrazu OCI wpisywany do /etc/hammer/oci.hk
	OriginRefspec string

	// ContainerMode: gdy true, Build() POMIJA CALKOWICIE wstrzykiwanie
	// hammer i generowanie /etc/hammer/oci.hk -- uzywane przez
	// buildflow.BuildContainer ([project] -> type = container) dla
	// zwyklych kontenerow roboczych (podman/docker), ktore NIE sa
	// zarzadzane atomowo i nie maja hammer w ogole. Ustawiane recznie
	// przez wolajacego (nie jest wyliczane automatycznie z Config, zeby
	// decyzja "czy ten konkretny build ma hammer" byla jawna w miejscu
	// wywolania, a nie ukryta w logice Buildera).
	ContainerMode bool

	// layerPaths gromadzi sciezki KOLEJNYCH warstw OCI przyrostowych
	// (baza/pakiety/hooki/runtime) zbudowanych podczas Build() -- patrz
	// internal/rootfs/layers.go i LayerTarballs() ponizej. Puste dopoki
	// Build() sie nie powiedzie.
	layerPaths []string
}

// LayerTarballs zwraca sciezki do KOLEJNYCH warstw OCI przyrostowych
// zbudowanych podczas ostatniego udanego Build() (w kolejnosci w jakiej
// maja zostac dolozone do obrazu przez mutate.AppendLayers -- baza
// najpierw). Puste warstwy (brak zmian miedzy dwoma punktami kontrolnymi)
// sa juz pominiete -- kazda sciezka na tej liscie odpowiada faktycznie
// istniejacemu, niepustemu plikowi tar.gz. Wywolywane przez
// internal/buildflow.BuildCloud PO udanym Build(), zeby przekazac warstwy
// do internal/ociimage.BuildAndPushLayers zamiast (jak do wersji 0.8.0)
// pakowac caly rootfs w jedna warstwe od nowa.
func (b *Builder) LayerTarballs() []string {
	return b.layerPaths
}

// New tworzy nowy Builder.
func New(project *liveparse.Project, cfg *config.Config, rootfsDir, workDir string) *Builder {
	return &Builder{Project: project, Config: cfg, RootfsDir: rootfsDir, WorkDir: workDir}
}

// macPackages zwraca liste pakietow apt wymaganych przez wybrany system
// kontroli dostepu obowiazkowego (MAC) z [project] -> selinux.
//
// AppArmor (domyslny dla Debiana):
//   - apparmor            -- glowna implementacja AppArmor w kernelu + narzedzia
//   - apparmor-profiles   -- predefiniowane profile (przydatne dla Firefox, Cups itp.)
//   - apparmor-utils      -- aa-status, aa-enforce, aa-complain (diagnostyka)
//
// SELinux (kiedy [project] -> selinux = true):
//   - selinux-basics      -- bazowa konfiguracja SELinux dla Debiana (selinux-activate itp.)
//   - selinux-policy-default -- polityki SELinux (targeted -- najbardziej praktyczna)
//   - policycoreutils     -- setenforce, getenforce, restorecon, sestatus
//   - policycoreutils-python-utils -- semanage (zarzadzanie politykami przez CLI)
//   - auditd              -- daemon audytu (SELinux loguje AVC denials przez audit)
//
// Oba systemy sa wzajemnie wylaczne -- instalacja obu jest mozliwa technicznie
// ale nie ma sensu. hackeros-builder instaluje JEDEN z nich w zaleznosci od
// konfiguracji, nigdy oba.
func (b *Builder) macPackages() []string {
	if b.Config.Project.MAC == config.MACSELinux {
		return []string{
			"selinux-basics",
			"selinux-policy-default",
			"policycoreutils",
			"policycoreutils-python-utils",
			"auditd",
		}
	}
	// AppArmor (domyslne)
	return []string{
		"apparmor",
		"apparmor-profiles",
		"apparmor-utils",
	}
}

// cybersecurityPackages zwraca liste pakietow apt instalowanych dla
// [project] -> type = cybersecurity -- odpowiednik "Cybersecurity Edition"
// znanej z glownego repozytorium HackerOS (nastawionej na Red Team), w
// zakresie mozliwym do zrealizowania samym apt z repozytoriow Debiana
// (bez proprietarnych narzedzi HackerOS, ktore sa poza zakresem tego
// buildera -- te doinstalowuje sie normalnie przez wlasny package-lists
// projektu / hooks, dokladnie tak jak kazdy inny dodatkowy pakiet).
//
// Dobor pakietow: rekonesans sieciowy (nmap, netcat-openbsd, tcpdump,
// wireshark, whois, dnsutils), audyt/lamanie hasel (hydra, john, hashcat),
// audyt sieci bezprzewodowych (aircrack-ng), audyt aplikacji webowych
// (sqlmap, nikto), analiza binarna/forensyka (radare2, binwalk, foremost,
// steghide) oraz debugowanie niskopoziomowe (gdb). Wszystkie sa dostepne
// wprost w domyslnych repozytoriach Debiana (main), wiec nie wymagaja
// dodatkowych [archives] w config/config.hk.
func cybersecurityPackages() []string {
	return []string{
		// rekonesans / siec
		"nmap",
		"netcat-openbsd",
		"tcpdump",
		"wireshark",
		"whois",
		"dnsutils",
		// audyt uwierzytelniania
		"hydra",
		"john",
		"hashcat",
		// audyt sieci bezprzewodowych
		"aircrack-ng",
		// audyt aplikacji webowych
		"sqlmap",
		"nikto",
		// analiza binarna / forensyka
		"radare2",
		"binwalk",
		"foremost",
		"steghide",
		// debugowanie niskopoziomowe
		"gdb",
	}
}

// aptInstallWithProgress instaluje pkgs przez apt-get, pokazujac REALNY
// pasek postepu (internal/util.ProgressBar): total jest wyliczany PRZED
// instalacja przez "apt-get install -y -s" (symulacja typu dry-run, apt
// NIC nie zmienia w systemie, tylko wypisuje plan -- kazdy pakiet ktory
// faktycznie zostanie zainstalowany/zaktualizowany pojawia sie jako linia
// "Inst ..."), a postep w trakcie faktycznej instalacji jest zliczany na
// podstawie linii "Setting up ..." (apt wypisuje ja dokladnie raz na
// kazdy skonfigurowany pakiet, niezaleznie od kolejnosci rozpakowywania) --
// to jest wiec postep policzony z FAKTYCZNIE wykonanej pracy, nie
// przyblizenie "na oko".
//
// extraArgs to dodatkowe opcje apt (przed lista pakietow), np.
// "--no-install-recommends" dla instalatora GUI w internal/isobuild.
func (b *Builder) aptInstallWithProgress(label string, pkgs []string, extraArgs ...string) error {
	if len(pkgs) == 0 {
		return nil
	}

	simArgs := append(append([]string{"install", "-y", "-s"}, extraArgs...), pkgs...)
	total := countAptPlannedOps(b.RootfsDir, simArgs)

	installArgs := append([]string{
		"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, extraArgs...)
	installArgs = append(installArgs, pkgs...)

	bar := util.NewProgressBar(label, int64(total), "pakietow")
	var done int64
	onLine := func(line string) {
		util.Debugf("apt: %s", line)
		if strings.HasPrefix(line, "Setting up ") {
			done++
			bar.Set(done)
		}
	}
	if err := sandbox.ExecWithLines(b.RootfsDir, nil, onLine, "apt-get", installArgs...); err != nil {
		bar.Fail("apt-get install")
		return fmt.Errorf("instalacja pakietow %s (%v): %w", label, pkgs, err)
	}
	bar.Finish()
	return nil
}

// countAptPlannedOps uruchamia "apt-get install -y -s <simArgs...>" (dry
// run, NIE zmienia systemu) i liczy linie "Inst " -- dokladna liczba
// pakietow ktore apt faktycznie zainstaluje/zaktualizuje w tym wywolaniu
// (moze byc WIEKSZA niz len(pkgs), bo apt dociaga zaleznosci). Blad
// symulacji (np. konflikt zaleznosci wykryty juz na tym etapie) NIE
// przerywa builda tutaj -- total=0 daje pasek w trybie "sam licznik", a
// faktyczny (nie-symulowany) apt-get install i tak zaraz potem zawiedzie z
// pelnym, prawdziwym komunikatem bledu apt.
func countAptPlannedOps(rootfsDir string, simArgs []string) int {
	count := 0
	onLine := func(line string) {
		if strings.HasPrefix(line, "Inst ") {
			count++
		}
	}
	_ = sandbox.ExecWithLines(rootfsDir, nil, onLine, "apt-get", simArgs...)
	return count
}

// installCybersecurityPackages instaluje cybersecurityPackages() wewnatrz
// rootfs -- wolane TYLKO gdy b.Config.Project.IsCybersecurity() (patrz
// Build()). Wykonywane PO systemie MAC a PRZED pakietami wlasnymi projektu,
// zeby package-lists/hooks projektu mogly w razie potrzeby nadpisac/rozszerzyc
// domyslny zestaw narzedzi cybersecurity (np. dopisac wlasna liste w
// config/package-lists/*.list.chroot).
//
// UWAGA: pojedynczy pakiet z tej listy moze czasem nie byc dostepny w
// wybranym [release] -> name (np. usuniety chwilowo z "sid"/"unstable") --
// w takim wypadku apt-get install konczy caly build bledem, tak samo jak
// dla kazdego innego brakujacego pakietu w Project.Packages. To celowe:
// cichy fallback pomijajacy pojedyncze pakiety maskowalby realny problem
// (np. literowke w nazwie pakietu wprowadzona przy przyszlej rozbudowie tej
// listy) zamiast zglosic go od razu, w miejscu powstania.
func (b *Builder) installCybersecurityPackages() error {
	pkgs := cybersecurityPackages()
	source := "wbudowana lista domyslna"
	if len(b.Config.Project.CybersecurityPackages) > 0 {
		// [project] -> cybersecurity_packages w config.hk NADPISUJE
		// (nie rozszerza) wbudowana liste -- jesli ktos chce jednoczesnie
		// wbudowana liste I swoje dodatki, powinien po prostu przepisac
		// wbudowana liste do config.hk i dopisac swoje pozycje (patrz
		// SupportedLanguages-owy wzorzec w README dla przykladu tresci
		// wbudowanej listy do skopiowania).
		pkgs = b.Config.Project.CybersecurityPackages
		source = "[project] -> cybersecurity_packages w config.hk"
	}
	util.Infof("  cybersecurity ([project] -> type=cybersecurity): instalacja %d pakietow (%s)...", len(pkgs), source)
	return b.aptInstallWithProgress("cybersecurity", pkgs)
}

// installMACPackages instaluje pakiety systemu kontroli dostepu (AppArmor
// lub SELinux) wewnatrz rootfs przez sandbox. Wykonywane PRZED hookami
// uzytkownika zeby hooki mogly juz zakladac ze MAC jest dostepny.
func (b *Builder) installMACPackages() error {
	pkgs := b.macPackages()
	macName := "AppArmor"
	if b.Config.Project.MAC == config.MACSELinux {
		macName = "SELinux"
	}
	util.Infof("  MAC (%s): instalacja %d pakietow...", macName, len(pkgs))

	// UWAGA: celowo BEZ --no-install-recommends. Domyslne zachowanie apt
	// (i live-build, ktore ma AptRecommends: true chyba ze projekt jawnie
	// ustawi --apt-recommends false) instaluje tez pakiety z pola
	// "Recommends" -- HackerOS polega na tym (np. "sudo" jest ciagniete
	// ubocznie jako Recommends wielu pakietow desktopowych, zamiast byc
	// jawnie wpisane w kazdej liscie pakietow). Wylaczenie recommends
	// dawalo mniejszy, bardziej "czysty" obraz, ale lamalo zalozenia
	// projektu przygotowanego pod normalne apt/live-build.
	return b.aptInstallWithProgress("MAC:"+macName, pkgs)
}

// Build wykonuje caly przeplyw budowy rootfs.
// Narzedzia (debootstrap, mksquashfs itp.) sa pobierane tymczasowo jesli
// brakuje ich na hoscie -- bez instalacji, bez konfliktow zaleznosci.
func (b *Builder) Build() error {
	sectionLabel := "Budowa rootfs"
	if b.ContainerMode {
		sectionLabel = "Budowa rootfs (kontener roboczy)"
	}
	util.Section(sectionLabel)

	if err := b.prepareDir(); err != nil {
		return err
	}

	layersDir := filepath.Join(b.WorkDir, "layers")
	if err := os.MkdirAll(layersDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu warstw OCI (%s): %w", layersDir, err)
	}
	b.layerPaths = nil
	checkpoint, err := snapshotTree(b.RootfsDir)
	if err != nil {
		return fmt.Errorf("migawka poczatkowa rootfs: %w", err)
	}
	// nextLayer zamyka biezacy punkt kontrolny (migawka "checkpoint" wyzej)
	// w nowa warstwe OCI o nazwie name -- migawkuje AKTUALNY stan rootfs,
	// liczy roznice wzgledem poprzedniego punktu kontrolnego, zapisuje
	// warstwe (jesli niepusta) do layersDir/<name>.tar.gz i PRZESUWA punkt
	// kontrolny na biezacy stan (kolejne wywolanie liczy juz roznice OD TEGO
	// MIEJSCA). Blad migawki/zapisu przerywa caly Build() -- niepelna,
	// blednie policzona warstwa OCI nigdy nie powinna trafic do registry.
	nextLayer := func(name string) error {
		after, err := snapshotTree(b.RootfsDir)
		if err != nil {
			return fmt.Errorf("migawka rootfs po etapie %q: %w", name, err)
		}
		changed, removed := diffSnapshots(checkpoint, after)
		layerPath := filepath.Join(layersDir, name+".tar.gz")
		wrote, err := writeIncrementalLayer(b.RootfsDir, changed, removed, layerPath)
		if err != nil {
			return fmt.Errorf("zapis warstwy OCI %q: %w", name, err)
		}
		if wrote {
			b.layerPaths = append(b.layerPaths, layerPath)
			util.Debugf("warstwa OCI %q: %d zmienionych, %d usunietych sciezek -> %s",
				name, len(changed), len(removed), layerPath)
		} else {
			util.Debugf("warstwa OCI %q: brak zmian -- pominieta", name)
		}
		checkpoint = after
		return nil
	}

	// --- toolchain: przygotuj narzedzia build-time ---
	util.Step(0, 10, "sprawdzanie/pobieranie narzedzi build-time...")
	tc := toolchain.New(b.WorkDir)
	if err := tc.PrepareAll(); err != nil {
		return fmt.Errorf("toolchain: %w", err)
	}
	// Ustaw PATH tak by toolchain/bin/ byl pierwszy -- procesy potomne
	// (debootstrap, apt-get w sandbox) automatycznie znajda tymczasowe binarki.
	if err := os.Setenv("PATH", tc.Env()[0][len("PATH="):]); err != nil {
		return fmt.Errorf("ustawienie PATH toolchain: %w", err)
	}

	util.Step(1, 10, "debootstrap (%s)...", b.Config.Release)
	if err := b.runDebootstrap(); err != nil {
		return fmt.Errorf("debootstrap: %w", err)
	}

	util.Step(2, 10, "preseed debconf + sudo-stub...")
	if err := b.seedDebconf(); err != nil {
		return fmt.Errorf("preseed debconf: %w", err)
	}
	if err := b.installSudoStub(); err != nil {
		return fmt.Errorf("sudo stub: %w", err)
	}

	if b.Project.IncludesChrootBeforePackages != "" {
		util.Step(3, 10, "kopiowanie includes.chroot_before_packages...")
		if err := b.copyDirToRootfs(b.Project.IncludesChrootBeforePackages); err != nil {
			return fmt.Errorf("includes.chroot_before_packages: %w", err)
		}
	} else {
		util.Step(3, 10, "brak includes.chroot_before_packages -- pominieto")
	}

	if len(b.Project.ExtraSources) > 0 {
		util.Step(4, 10, "dodatkowe zrodla apt (%d)...", len(b.Project.ExtraSources))
		if err := b.applyExtraSources(); err != nil {
			return fmt.Errorf("extra sources: %w", err)
		}
	} else {
		util.Step(4, 10, "brak dodatkowych zrodel apt -- pominieto")
	}

	util.Step(5, 10, "instalacja systemu MAC ([project] -> selinux=%v)...",
		b.Config.Project.MAC == config.MACSELinux)
	if err := b.installMACPackages(); err != nil {
		return fmt.Errorf("instalacja MAC: %w", err)
	}

	if b.Config.Project.IsCybersecurity() {
		util.Step(6, 10, "pakiety cybersecurity ([project] -> type=cybersecurity)...")
		if err := b.installCybersecurityPackages(); err != nil {
			return fmt.Errorf("instalacja pakietow cybersecurity: %w", err)
		}
	} else {
		util.Step(6, 10, "[project] -> type != cybersecurity -- pominieto")
	}

	if err := nextLayer("base"); err != nil {
		return fmt.Errorf("warstwa OCI 'base': %w", err)
	}

	util.Step(7, 10, "instalacja %d pakiet(ow)...", len(b.Project.Packages))
	if err := b.installPackages(); err != nil {
		return fmt.Errorf("instalacja pakietow: %w", err)
	}

	if err := nextLayer("packages"); err != nil {
		return fmt.Errorf("warstwa OCI 'packages': %w", err)
	}

	hasAfterIncludes := b.Project.IncludesChroot != "" || b.Project.IncludesChrootAfterPackages != ""
	if hasAfterIncludes {
		util.Step(8, 10, "kopiowanie includes.chroot / includes.chroot_after_packages...")
		if b.Project.IncludesChroot != "" {
			if err := b.copyDirToRootfs(b.Project.IncludesChroot); err != nil {
				return fmt.Errorf("includes.chroot: %w", err)
			}
		}
		if b.Project.IncludesChrootAfterPackages != "" {
			if err := b.copyDirToRootfs(b.Project.IncludesChrootAfterPackages); err != nil {
				return fmt.Errorf("includes.chroot_after_packages: %w", err)
			}
		}
	} else {
		util.Step(8, 10, "brak includes.chroot/includes.chroot_after_packages -- pominieto")
	}

	util.Step(9, 10, "wykonywanie %d hook(ow)...", len(b.Project.Hooks))
	if err := b.runHooks(); err != nil {
		return fmt.Errorf("hooks: %w", err)
	}

	// Jawne, KONCOWE przebudowanie initrd -- domyka luke jaka zostawia
	// samo poleganie na triggerach dpkg (uruchamianych automatycznie przy
	// KAZDYM "apt-get install", ale NIE po includes.chroot_after_packages
	// ani po hookach, ktore moga dopisac pliki wplywajace na initrd -- np.
	// wlasny modul jadra, /etc/crypttab, config dla dracut/initramfs-tools
	// -- albo, jak dla [project] -> type=cybersecurity, PODMIENIC caly
	// kernel PO tym jak "packages" zainstalowalo juz live-boot; patrz
	// helpers/cybersecurity-default/hooks/normal/install-kernel.hook.chroot
	// w repo HackerOS -- ten hook wywoluje apt-get install lokalnych .deb,
	// co samo w sobie odpala trigger initramfs-tools, ALE jest to ostatnia
	// gwarancja bez wzgledu na to co jeszcze zrobi PRZYSZLY hook/includes).
	// Wykonywane TYLKO gdy w /boot faktycznie jest jakis kernel (build
	// mogl swiadomie NIE instalowac zadnego -- np. [project] -> type =
	// container, ktore w ogole nie trafia do internal/isobuild) -- w takim
	// wypadku nie ma czego regenerowac i "update-initramfs -u -k all"
	// zakonczylby sie tylko myloncym bledem "W: There is no valid
	// initrd.img" o niczym nie informujacym.
	if hasAnyKernel, err := bootHasAnyKernel(b.RootfsDir); err != nil {
		return fmt.Errorf("sprawdzanie obecnosci jadra w /boot: %w", err)
	} else if hasAnyKernel {
		util.Infof("  koncowe przebudowanie initrd (update-initramfs -u -k all)...")
		if err := b.sandboxExec("update-initramfs", "-u", "-k", "all"); err != nil {
			return fmt.Errorf(
				"update-initramfs -u -k all: %w -- initrd.img w /boot moze NIE zawierac "+
					"hookow live-boot/dodatkowych modulow dopisanych PO instalacji kernela "+
					"(includes.chroot_after_packages, hooki) -- bez poprawnego initrd live-medium "+
					"konczy sie panika \"Attempted to kill init!\" przy starcie", err)
		}
	} else {
		util.Infof("  brak jadra w /boot -- pomijam koncowe update-initramfs (typowe dla ContainerMode)")
	}

	if err := nextLayer("hooks"); err != nil {
		return fmt.Errorf("warstwa OCI 'hooks': %w", err)
	}

	if b.ContainerMode {
		// Kontener roboczy ([project] -> type=container) NIE jest
		// zarzadzany atomowo -- brak hammer, brak /etc/hammer/oci.hk.
		// apt/apt-get rowniez NIE sa usuwane (to i tak dzieje sie tylko
		// w internal/isobuild, ktore w ogole nie jest wywolywane dla
		// tego trybu -- patrz buildflow.BuildContainer).
		util.Step(10, 10, "tryb kontenera roboczego (ContainerMode) -- pomijam wstrzykiwanie hammer/oci.hk")
	} else {
		util.Step(10, 10, "wstrzykiwanie hammer + generowanie /etc/hammer/oci.hk...")
		if err := b.injectHammer(); err != nil {
			return fmt.Errorf("hammer injection: %w", err)
		}
		if err := b.installHammerDeps(); err != nil {
			return fmt.Errorf("hammer biblioteki dynamiczne: %w", err)
		}
		if err := b.generateHammerConfig(); err != nil {
			return fmt.Errorf("generowanie /etc/hammer/oci.hk: %w", err)
		}

		if err := nextLayer("runtime"); err != nil {
			return fmt.Errorf("warstwa OCI 'runtime': %w", err)
		}
	}

	// UWAGA: apt/apt-get NIE sa usuwane tutaj. Ten sam rootfs jest pakowany
	// i wypychany jako obraz OCI ("build cloud"), a nastepnie -- w ramach
	// "build iso" / "build all" -- SCIAGANY Z POWROTEM z registry i
	// wzbogacany o graficzny instalator Calamares (ktory rowniez korzysta
	// z apt-get, patrz internal/isobuild/installer.go), zanim trafi do
	// finalnego squashfs.img na plycie ISO. Gdybysmy usuneli apt/apt-get
	// juz tutaj, wstrzykiwanie Calamares w ISO przestaloby dzialac.
	//
	// Faktyczne usuniecie apt/apt-get z tego co trafia na "finalny dysk"
	// uzytkownika nastepuje w internal/isobuild/builder.go -- PO
	// wstrzynkieciu Calamares, ale PRZED zbudowaniem squashfs.img (czyli
	// dokladnie w tresci ktora Calamares pozniej kopiuje 1:1 na dysk
	// uzytkownika). Patrz rootfs.RemoveAptTooling (w tym pakiecie, ponizej)
	// oraz jej wywolanie w internal/isobuild/builder.go.

	util.Infof("Rootfs zbudowany: %s", b.RootfsDir)
	return nil
}

func (b *Builder) prepareDir() error {
	if err := os.RemoveAll(b.RootfsDir); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", b.RootfsDir, err)
	}
	if err := os.MkdirAll(b.RootfsDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie %s: %w", b.RootfsDir, err)
	}
	return nil
}

// runDebootstrap wywoluje "debootstrap <suite> <target> <mirror>". To jest
// JEDYNA czesc procesu ktora delegujemy do istniejacego narzedzia Debiana --
// reimplementacja debootstrap (rozwiazywanie zaleznosci bazowego systemu od
// zera) wykraczalaby daleko poza zakres hackeros-builder.
// runDebootstrap uruchamia debootstrap z REALNYM paskiem postepu: total nie
// jest znany z gory (debootstrap nie ma trybu "policz ile bedzie pakietow"
// ktory dawalby wiarygodna liczbe bez pobierania indeksu Packages, a robienie
// tego jako osobny krok podwajaloby czas startu), wiec pasek dziala w trybie
// "rosnacy licznik" -- kazda linia "Retrieving X" / "Validating X" /
// "Extracting X" z wyjscia debootstrap to JEDNO tyknięcie postepu, real-time,
// bezposrednio z faktycznie wykonywanej pracy (nie animacja na czas).
func (b *Builder) runDebootstrap() error {
	bar := util.NewProgressBar("debootstrap", 0, "operacji")
	var ticks int64
	onLine := func(line string) {
		util.Debugf("debootstrap: %s", line)
		if strings.HasPrefix(line, "I: Retrieving ") ||
			strings.HasPrefix(line, "I: Validating ") ||
			strings.HasPrefix(line, "I: Extracting ") ||
			strings.HasPrefix(line, "I: Unpacking ") ||
			strings.HasPrefix(line, "I: Configuring ") {
			ticks++
			bar.Set(ticks)
		}
	}
	err := util.RunStreamingWithLines("", onLine, "debootstrap",
		"--arch="+b.Config.EffectiveArch(),
		b.Config.Release,
		b.RootfsDir,
		b.Config.EffectiveMirror(),
	)
	if err != nil {
		bar.Fail("debootstrap")
		return err
	}
	bar.Finish()
	return nil
}

// installSudoStub instaluje /usr/local/sbin/sudo wewnatrz rootfs jako
// prosty wrapper "exec $@" (uruchamia komende bezposrednio, bez faktycznych
// uprawnien sudo). Ma to jeden cel: hooki uzytkownikow czesto zaczynaja linie
// od "sudo apt-get install ..." / "sudo curl ..." bo sa pisane z myśla o
// uruchomieniu na normalnej maszynie z userem. Wewnatrz kontenera nspawn
// build ZAWSZE biegnie jako root (uid=0), wiec sudo jest zbedne -- ale jego
// BRAK powoduje "sudo: not found" i natychmiastowe wyjscie ze statusem != 0,
// co konczy caly build bledem (dokladnie ten komunikat widac na zrzucie
// ekranu: "/tmp-hackeros-hook-install-mullvad.hook.chroot: 2: sudo: not found").
//
// Stub jest instalowany w /usr/local/sbin/sudo (ma priorytet nad ewentualnym
// pakietowym /usr/bin/sudo jezeli ten zostanie doinstalowany przez hooks --
// nie. /usr/local/sbin jest pierwsze w $PATH wewnatrz nspawn, wiec stub
// zawsze wygrywa na czas builda). Zostaje usuniety w ostatnim kroku by nie
// trafic do finalnego obrazu.
func (b *Builder) installSudoStub() error {
	stubDir := filepath.Join(b.RootfsDir, "usr", "local", "sbin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		return err
	}
	stub := "#!/bin/sh\n# sudo-stub wygenerowany przez hackeros-builder.\n# Hooki pisane z myslą o normalnej maszynie uzywaja 'sudo <cmd>' -- wewnatrz\n# kontenera nspawn build jest rootem, wiec sudo jest zbedne. Ten stub\n# po prostu odpala komende bezposrednio, eliminujac \"sudo: not found\".\nexec \"$@\"\n"
	stubPath := filepath.Join(stubDir, "sudo")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		return fmt.Errorf("zapis sudo-stub: %w", err)
	}
	util.Infof("  sudo-stub zainstalowany: %s", stubPath)
	return nil
}

// removeSudoStub usuwa stub sudo z rootfs po wykonaniu hookow -- nie powinien
// trafic do finalnego obrazu (w zainstalowanym systemie sudo jest normalnym
// pakietem z SUID, nie wrapperem).
func (b *Builder) removeSudoStub() {
	stubPath := filepath.Join(b.RootfsDir, "usr", "local", "sbin", "sudo")
	if err := os.Remove(stubPath); err != nil && !os.IsNotExist(err) {
		util.Warnf("Nie mozna usunac sudo-stub %s: %v", stubPath, err)
	}
}

// bootHasAnyKernel zglasza czy rootfsDir/boot zawiera choc jeden plik
// vmlinuz-* -- uzywane wylacznie by zdecydowac, czy koncowe
// "update-initramfs -u -k all" (patrz Build()) ma jakikolwiek sens.
func bootHasAnyKernel(rootfsDir string) (bool, error) {
	matches, err := filepath.Glob(filepath.Join(rootfsDir, "boot", "vmlinuz-*"))
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// installPackages wykonuje apt-get update + apt-get install wewnatrz
// izolowanego kontenera nspawn (nie plain chroot -- patrz Build() i
// util.RunNspawnStreaming dla uzasadnienia).
func (b *Builder) installPackages() error {
	if err := b.sandboxExec("apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update: %w", err)
	}

	if len(b.Project.Packages) == 0 {
		return nil
	}

	// UWAGA: celowo BEZ --no-install-recommends -- patrz komentarz w
	// installMACPackages powyzej. Zachowanie ma odpowiadac zwyklemu
	// "apt-get install" / live-build z domyslnym AptRecommends: true.
	return b.aptInstallWithProgress("pakiety projektu", b.Project.Packages, "-o", "APT::Get::Assume-Yes=true")
}

// applyExtraSources dopisuje config/archives/*.list.chroot do
// rootfs/etc/apt/sources.list.d/hackeros-extra.list i importuje klucze GPG.
func (b *Builder) applyExtraSources() error {
	sourcesDir := filepath.Join(b.RootfsDir, "etc", "apt", "sources.list.d")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return err
	}
	listPath := filepath.Join(sourcesDir, "hackeros-extra.list")
	f, err := os.Create(listPath)
	if err != nil {
		return fmt.Errorf("tworzenie %s: %w", listPath, err)
	}
	defer f.Close()
	for _, line := range b.Project.ExtraSources {
		if _, err := fmt.Fprintln(f, line); err != nil {
			return err
		}
	}
	for _, keyPath := range b.Project.ExtraKeys {
		destName := filepath.Base(keyPath)
		destPath := filepath.Join(b.RootfsDir, "etc", "apt", "trusted.gpg.d", destName)
		if err := copyFile(keyPath, destPath, 0o644); err != nil {
			return fmt.Errorf("kopiowanie klucza GPG %s: %w", keyPath, err)
		}
	}
	return nil
}

// copyDirToRootfs kopiuje rekurencyjnie srcDir do korzenia rootfs, zachowujac
// uprawnienia plikow (1:1 jak live-build). Uzywana dla wszystkich trzech
// wariantow includes.chroot* (before_packages / legacy / after_packages).
//
// SEMANTYKA NADPISYWANIA (jak w live-build): pliki z includes.chroot* MAJA
// nadpisywac to, co juz jest w rootfs -- w tym pliki/symlinki utworzone
// przez pakiety zainstalowane wczesniej (np. adwaita-icon-theme tworzy
// /usr/share/icons/Adwaita/cursors/arrow jako symlink, a projekt moze
// chciec nadpisac go wlasnym kursorem przez includes.chroot_after_packages).
// os.Symlink/os.OpenFile(O_CREATE) BEZ wczesniejszego usuniecia istniejacego
// wpisu konczy sie bledem "file exists" -- stad removeExistingEntry ponizej,
// wywolywane PRZED kazda proba utworzenia symlinku lub pliku.
func (b *Builder) copyDirToRootfs(srcDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dest := filepath.Join(b.RootfsDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode())
		}
		if err := removeExistingEntry(dest); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dest)
		}
		return copyFile(path, dest, info.Mode())
	})
}

// removeExistingEntry usuwa dest jesli istnieje jako plik regularny lub
// symlink, zeby kolejne os.Symlink/os.OpenFile(O_CREATE) mogly bezpiecznie
// utworzyc nowy wpis w jego miejsce (nadpisanie, nie blad "file exists").
//
// Jesli dest istnieje jako KATALOG, to jest to zwracane jako czytelny blad
// konfiguracji (nie usuwamy cichcem calego poddrzewa katalogow -- zamiana
// katalogu na plik/symlink przez includes.chroot to prawie zawsze pomylka
// w projekcie, ktora powinna byc widoczna, a nie po cichu "naprawiona"
// przez rekurencyjne skasowanie czegos, co moze zawierac wiele plikow).
func removeExistingEntry(dest string) error {
	fi, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sprawdzanie istniejacego wpisu %s: %w", dest, err)
	}
	if fi.IsDir() {
		return fmt.Errorf(
			"%s jest juz katalogiem w rootfs, a includes.chroot probuje "+
				"nadpisac go plikiem/symlinkiem -- to prawdopodobnie blad w "+
				"strukturze projektu (sciezka zajeta przez pakiet lub inny "+
				"wczesniejszy krok), sprawdz zawartosc includes.chroot* recznie",
			dest)
	}
	if err := os.Remove(dest); err != nil {
		return fmt.Errorf("usuwanie istniejacego wpisu %s przed nadpisaniem: %w", dest, err)
	}
	return nil
}

// runHooks wykonuje kazdy skrypt hooks wewnatrz izolowanego kontenera nspawn.
// Sudo-stub instalowany w kroku 2 jest USUWANY po wykonaniu WSZYSTKICH hookow
// (patrz installSudoStub / removeSudoStub).
//
// Kazdy hook ma limit czasu (sandbox.DefaultHookTimeout, nadpisywalny
// HACKEROS_HOOK_TIMEOUT_SECONDS) i dziala z DEBIAN_FRONTEND=noninteractive +
// pelnym zestawem zmiennych tlumiacych interaktywne pytania (needrestart,
// ucf, apt-listchanges -- patrz internal/sandbox/sandbox.go) oraz stdin
// zawsze ustawionym na /dev/null. Jesli hook mimo to probuje o cos zapytac
// (np. odpali whiptail bez terminala, albo czeka na wejscie ktore nigdy nie
// nadejdzie), zostanie zabity po limicie czasu z jasnym komunikatem --
// zamiast wisiec w nieskonczonosc albo przerwac build od razu z niejasnym
// bledem niskiego poziomu.
func (b *Builder) runHooks() error {
	defer b.removeSudoStub()

	if len(b.Project.Hooks) == 0 {
		util.Infof("  brak hookow (config/hooks/normal|live/*.hook.chroot) -- pominieto")
		return nil
	}

	if err := b.ensureHookInterpreters(b.Project.Hooks); err != nil {
		return fmt.Errorf("przygotowanie interpreterow hookow: %w", err)
	}

	bar := util.NewProgressBar("hooki", int64(len(b.Project.Hooks)), "hookow")
	for i, h := range b.Project.Hooks {
		util.SubStep("[%d/%d] %s  %s", i+1, len(b.Project.Hooks), h.Name,
			util.Colorize(util.ColorMagenta, "("+hooklang.Label(h.Interpreter)+")"))
		tmpName := "/tmp-hackeros-hook-" + h.Name
		destOnHost := filepath.Join(b.RootfsDir, tmpName)
		if err := copyFile(h.Path, destOnHost, 0o755); err != nil {
			bar.Fail(h.Name)
			return fmt.Errorf("kopiowanie hooka %s: %w", h.Name, err)
		}
		err := sandbox.ExecHook(b.RootfsDir, tmpName)
		os.Remove(destOnHost)
		if err != nil {
			bar.Fail(h.Name)
			var timeoutErr *sandbox.ExecTimeoutError
			if errors.As(err, &timeoutErr) {
				return fmt.Errorf(
					"hook %s: %w -- hook prawdopodobnie czeka na interaktywna odpowiedz "+
						"(np. whiptail/dialog/apt-get bez -y/'read' na cos co nigdy nie nadejdzie); "+
						"nienadzorowany build ('hackeros-builder build ...') NIE MOZE dostarczyc takiej "+
						"odpowiedzi -- popraw hook zeby dzialal w pelni bez interakcji "+
						"(np. dopisz -y do apt-get/dpkg, ustaw DEBIAN_FRONTEND=noninteractive we wlasnych "+
						"podprocesach hooka), albo podnies limit zmienna HACKEROS_HOOK_TIMEOUT_SECONDS "+
						"jesli hook LEGALNIE potrzebuje wiecej czasu (kompilacja, duze pobieranie)",
					h.Name, err)
			}
			if h.Interpreter != "" && !hooklang.IsRecognized(h.Interpreter) {
				return fmt.Errorf(
					"wykonanie hooka %s (interpreter %q z shebang) nie powiodlo sie: %w -- "+
						"%q NIE jest jednym z jezykow z automatyczna instalacja interpretera "+
						"(patrz lista nizej); jesli %q to faktycznie prawidlowy interpreter, "+
						"dodaj odpowiedni pakiet recznie w config/package-lists/*.list.chroot "+
						"PRZED tym hookiem (numeracja prefiksow decyduje o kolejnosci). "+
						"Jezyki z automatyczna instalacja: %s",
					h.Name, h.Interpreter, err, h.Interpreter, h.Interpreter,
					strings.Join(hooklang.SupportedLanguages(), "; "))
			}
			return fmt.Errorf("wykonanie hooka %s: %w", h.Name, err)
		}
		bar.Add(1)
	}
	bar.Finish()
	return nil
}

// ensureHookInterpreters skanuje shebang wszystkich hooks i doinstalowuje
// (JEDNYM wywolaniem apt-get, dla wszystkich brakujacych naraz) kazdy
// interpreter ktory nie jest juz czescia bazowego systemu Debian --
// PRZED probą wykonania jakiegokolwiek hooka. Bez tego pierwszy hook w
// np. Pythonie konczylby sie kryptycznym bledem chroot/exec zamiast po
// prostu zadzialac.
func (b *Builder) ensureHookInterpreters(hooks []liveparse.HookScript) error {
	needed := make(map[string]bool)
	var order []string
	for _, h := range hooks {
		pkg, ok := hooklang.InterpreterPackage(h.Interpreter)
		if !ok {
			continue
		}
		if !needed[pkg] {
			needed[pkg] = true
			order = append(order, pkg)
		}
	}
	if len(order) == 0 {
		return nil
	}

	sort.Strings(order)
	util.Infof("  interpretery hookow: instalacja %s...", strings.Join(order, ", "))
	args := append([]string{
		"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, order...)
	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("instalacja interpreterow hookow (%v): %w", order, err)
	}
	return nil
}

// injectHammer sciaga najnowsza wersje hammer z GitHub Releases (archiwum
// oci-mode.tar.gz, tryb dedykowany systemom atomowym/immutable -- lub
// wersje wskazana przez HAMMER_VERSION jesli ustawiona, przydatne do
// pinowania konkretnej wersji / testow offline), rozpakowuje z niego
// pojedyncza binarke "hammer" i umieszcza w rootfs/usr/bin/hammer z
// uprawnieniami a+x.
//
// hackeros-builder jest CALKOWICIE NIEZALEZNY od deb-ostree -- jedynym
// narzedziem zarzadzania pakietami/atomowoscia wstrzykiwanym do rootfs
// jest hammer.
func (b *Builder) injectHammer() error {
	version := os.Getenv("HAMMER_VERSION")
	if version == "" {
		v, err := download.LatestHammerVersion()
		if err != nil {
			return fmt.Errorf("wykrywanie najnowszej wersji hammer: %w", err)
		}
		version = v
	}

	destPath := filepath.Join(b.RootfsDir, "usr", "bin", "hammer")
	util.Infof("  hammer %s (oci-mode) -> %s", version, destPath)

	if err := download.DownloadHammer(version, destPath); err != nil {
		return err
	}
	return nil
}

// generateHammerConfig wywoluje hkgen, by wygenerowac kompletny plik
// /etc/hammer/oci.hk wewnatrz rootfs, gotowy do uzycia przez hammer
// natychmiast po pierwszym boocie zbudowanego systemu.
//
// Wartosci sciezek (sysroot/ostree/apt/sources) sa pozostawione jako
// domyslne hammer (przekazujemy puste stringi -> hkgen wypelni je
// wartosciami domyslnymi odwzorowanymi ze sciezek widocznych w binarce
// hammer -- patrz internal/hkgen/hammer_config.go).
// OriginRefspec jest wypelniany przez b.OriginRefspec, jesli zostal ustawiony
// przez wolajacego (typowo PO komendzie "build cloud", patrz internal/buildflow/cloud.go).
func (b *Builder) generateHammerConfig() error {
	destPath := filepath.Join(b.RootfsDir, "etc", "hammer", "oci.hk")

	// Katalog /etc/hammer/ moze nie istniec w bazowym rootfs debootstrap --
	// tworzy go tylko pakiet hammer, a my wstrzykujemy binarke hammer
	// recznie (nie przez apt), wiec musimy sami zadbac o katalog.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu %s: %w", filepath.Dir(destPath), err)
	}

	params := hkgen.HammerConfigParams{
		OSName:        "debian",
		RequireGPG:    true,
		OriginRefspec: b.OriginRefspec,
	}

	if err := hkgen.WriteHammerConfig(destPath, params); err != nil {
		return fmt.Errorf("zapis %s: %w", destPath, err)
	}
	util.Infof("  wygenerowano: %s", destPath)
	return nil
}

// aptBinariesToRemove to lista plikow dostarczanych przez pakiet "apt"
// (oraz jego pomocnicze narzedzia transportowe w /usr/lib/apt) ktore
// hackeros-builder usuwa z FINALNEGO rootfs -- zbudowany system NIE MA
// miec dzialajacego apt/apt-get, poniewaz zarzadzanie pakietami przejmuje
// w calosci hammer (ktory czyta baze dpkg bezposrednio, patrz
// aptDatabasePathsToKeep ponizej).
//
// WAZNE -- to, co jest USUWANE, to tylko binarki-frontend (apt, apt-get,
// apt-cache, apt-config, apt-mark, apt-cdrom, apt-key jesli obecny) oraz
// katalog /usr/lib/apt/ (metody pobierania, solver zaleznosci) -- NIE
// dpkg i NIE baza dpkg (/var/lib/dpkg/*), ktora zostaje nietknieta, bo to
// wlasnie z niej hammer odczytuje liste zainstalowanych pakietow
// ("hammer _import" czyta wprost /var/lib/dpkg/status).
var aptBinariesToRemove = []string{
	"usr/bin/apt",
	"usr/bin/apt-get",
	"usr/bin/apt-cache",
	"usr/bin/apt-config",
	"usr/bin/apt-cdrom",
	"usr/bin/apt-mark",
	"usr/bin/apt-key",
}

// aptDirsToRemove to katalogi calego drzewa /usr/lib/apt (metody transportu
// http/https/copy/gpgv, solver zaleznosci itp.) -- nie sa to pojedyncze
// pliki tylko caly podkatalog pakietu apt, wiec usuwamy go rekurencyjnie.
var aptDirsToRemove = []string{
	"usr/lib/apt",
}

// RemoveAptTooling usuwa z rootfsDir binarki apt/apt-get (i pomocnicze
// narzedzia transportowe w /usr/lib/apt), zostawiajac NIETKNIETA baze
// dpkg (/var/lib/dpkg/*) oraz sam dpkg -- hammer polega bezposrednio na
// tej bazie do odczytu listy zainstalowanych pakietow, wiec jej usuniecie
// zlamaloby hammer, a usuniecie samego dpkg zlamaloby wewnetrzne
// wywolania hammer do dpkg-query/dpkg-divert/dpkg-statoverride
// wykorzystywane przez tryb kompatybilnosci "polecenie dpkg/apt".
//
// Eksportowana (nie metoda Buildera) bo jest wolana z dwoch miejsc:
//   - internal/isobuild/builder.go: PO wstrzynkieciu Calamares, PRZED
//     zbudowaniem squashfs.img -- to jest sciezka realizujaca dokladnie to
//     czego oczekuje uzytkownik: "jak zainstaluje juz finalna wersje na
//     finalnym dysku, ma byc bez apt/apt-get" -- squashfs.img jest 1:1
//     kopiowany przez Calamares na dysk, wiec usuniecie apt tutaj = brak
//     apt na finalnym zainstalowanym dysku.
//   - opcjonalnie recznie, dla scenariusza bezposredniego wdrozenia obrazu
//     OCI przez "hammer oci deploy" bez ISO/Calamares (patrz komentarz w
//     Builder.Build() powyzej dla wyjasnienia dlaczego NIE jest to
//     wolane automatycznie w przeplywie "build cloud").
func RemoveAptTooling(rootfsDir string) error {
	removed := 0
	for _, rel := range aptBinariesToRemove {
		path := filepath.Join(rootfsDir, rel)
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("usuwanie %s: %w", rel, err)
		}
		removed++
	}
	for _, rel := range aptDirsToRemove {
		path := filepath.Join(rootfsDir, rel)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("sprawdzanie %s: %w", rel, err)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("usuwanie katalogu %s: %w", rel, err)
		}
		removed++
	}
	util.Infof("  usunieto %d element(ow) apt/apt-get -- baza dpkg (/var/lib/dpkg) pozostawiona nietknieta dla hammer", removed)
	return nil
}

// Uwaga: pelny zestaw zmiennych srodowiskowych tlumiacych interaktywne
// pytania (DEBIAN_FRONTEND, needrestart, ucf, apt-listchanges, ...) jest
// zdefiniowany w JEDNYM miejscu -- internal/sandbox/sandbox.go,
// noninteractiveEnv -- i wstrzykiwany automatycznie do KAZDEGO wywolania
// sandbox.Exec/ExecEnv/ExecWithStdin/ExecHook, w tym do kazdego hooka.

// sandboxExec uruchamia komende wewnatrz rootfs w izolowanym srodowisku
// (unshare + chroot) -- patrz internal/sandbox/sandbox.go.
func (b *Builder) sandboxExec(command string, args ...string) error {
	return sandbox.Exec(b.RootfsDir, command, args...)
}

// sandboxExecWithStdin jak sandboxExec ale z danymi na stdin.
func (b *Builder) sandboxExecWithStdin(data []byte, command string, args ...string) error {
	return sandbox.ExecWithStdin(b.RootfsDir, data, command, args...)
}

// seedDebconf preseeduje debconf rozsadnymi wartosciami domyslnymi dla
// najczesciej "gadatliwych" pakietow (keyboard-configuration, tzdata,
// locales) PRZED instalacja pakietow -- DEBIAN_FRONTEND=noninteractive samo
// w sobie wystarcza zeby nie pokazac okienka, ale bez zadnej odpowiedzi w
// bazie debconf niektore postinst i tak potrafia "utknac" na braku wartosci.
// Preseed jest wykonywany przez "debconf-set-selections" wewnatrz chroot.
func (b *Builder) seedDebconf() error {
	preseed := "keyboard-configuration\tkeyboard-configuration/layout\tselect\tEnglish (US)\n" +
		"keyboard-configuration\tkeyboard-configuration/layoutcode\tstring\tus\n" +
		"keyboard-configuration\tkeyboard-configuration/variant\tselect\tEnglish (US)\n" +
		"keyboard-configuration\tkeyboard-configuration/modelcode\tstring\tpc105\n" +
		"keyboard-configuration\tkeyboard-configuration/model\tselect\tGeneric 105-key PC (intl.)\n" +
		"keyboard-configuration\tkeyboard-configuration/altgr\tselect\tThe default for the keyboard layout\n" +
		"keyboard-configuration\tkeyboard-configuration/unsupported_layout\tboolean\ttrue\n" +
		"keyboard-configuration\tkeyboard-configuration/unsupported_options\tboolean\ttrue\n" +
		"tzdata\ttzdata/Areas\tselect\tEtc\n" +
		"tzdata\ttzdata/Zones/Etc\tselect\tUTC\n" +
		"locales\tlocales/default_environment_locale\tselect\tC.UTF-8\n" +
		"locales\tlocales/locales_to_be_generated\tmultiselect\ten_US.UTF-8 UTF-8\n" +
		"debconf\tdebconf/frontend\tselect\tNoninteractive\n" +
		"debconf\tdebconf/priority\tselect\tcritical\n" +
		"man-db\tman-db/auto-update\tboolean\tfalse\n"

	return b.sandboxExecWithStdin([]byte(preseed), "debconf-set-selections")
}

// copyFile kopiuje plik src do dst, ustawiajac podane uprawnienia (mode)
// i tworzac katalogi nadrzedne jesli potrzebne.
//
// Jesli dst juz istnieje jako symlink, jest USUWANY przed zapisem --
// inaczej O_TRUNC podazylby za symlinkiem i obcial plik, na ktory ten
// symlink wskazuje (mogloby to byc cos zupelnie niezwiazanego, np. plik
// docelowy alternatywy systemowej), zamiast utworzyc nowy plik regularny
// dokladnie w miejscu dst -- co jest oczekiwana semantyka nadpisania przy
// kopiowaniu z includes.chroot*.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(dst); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("usuwanie istniejacego symlinku %s przed nadpisaniem: %w", dst, err)
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
}
