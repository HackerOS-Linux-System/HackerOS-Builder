package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/download"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hkgen"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/sandbox"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/toolchain"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// defaultMirror to domyslny mirror Debiana uzywany przez debootstrap gdy
// uzytkownik nie poda innego (na razie sztywno -- ROADMAP: konfigurowalny
// mirror per-projekt w config.hk).
const defaultMirror = "http://deb.debian.org/debian"

// Builder buduje rootfs na podstawie sparsowanego projektu i konfiguracji.
type Builder struct {
	Project   *liveparse.Project
	Config    *config.Config
	RootfsDir string // katalog docelowy rootfs
	WorkDir   string // katalog roboczy buildu (na toolchain/bin/ i inne temp)

	// OriginRefspec to refspec obrazu OCI wpisywany do /etc/hammer/oci.hk
	OriginRefspec string
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
	args := append([]string{
		"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgs...)
	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("instalacja pakietow %s (%v): %w", macName, pkgs, err)
	}
	return nil
}

// Build wykonuje caly przeplyw budowy rootfs.
// Narzedzia (debootstrap, mksquashfs itp.) sa pobierane tymczasowo jesli
// brakuje ich na hoscie -- bez instalacji, bez konfliktow zaleznosci.
func (b *Builder) Build() error {
	if err := b.prepareDir(); err != nil {
		return err
	}

	// --- toolchain: przygotuj narzedzia build-time ---
	util.Infof("Krok 0/9: sprawdzanie/pobieranie narzedzi build-time...")
	tc := toolchain.New(b.WorkDir)
	if err := tc.PrepareAll(); err != nil {
		return fmt.Errorf("toolchain: %w", err)
	}
	// Ustaw PATH tak by toolchain/bin/ byl pierwszy -- procesy potomne
	// (debootstrap, apt-get w sandbox) automatycznie znajda tymczasowe binarki.
	if err := os.Setenv("PATH", tc.Env()[0][len("PATH="):]); err != nil {
		return fmt.Errorf("ustawienie PATH toolchain: %w", err)
	}

	util.Infof("Krok 1/9: debootstrap (%s)...", b.Config.Release)
	if err := b.runDebootstrap(); err != nil {
		return fmt.Errorf("debootstrap: %w", err)
	}

	util.Infof("Krok 2/9: preseed debconf + sudo-stub...")
	if err := b.seedDebconf(); err != nil {
		return fmt.Errorf("preseed debconf: %w", err)
	}
	if err := b.installSudoStub(); err != nil {
		return fmt.Errorf("sudo stub: %w", err)
	}

	if b.Project.IncludesChrootBeforePackages != "" {
		util.Infof("Krok 3/9: kopiowanie includes.chroot_before_packages...")
		if err := b.copyDirToRootfs(b.Project.IncludesChrootBeforePackages); err != nil {
			return fmt.Errorf("includes.chroot_before_packages: %w", err)
		}
	} else {
		util.Infof("Krok 3/9: brak includes.chroot_before_packages -- pominieto")
	}

	if len(b.Project.ExtraSources) > 0 {
		util.Infof("Krok 4/9: dodatkowe zrodla apt (%d)...", len(b.Project.ExtraSources))
		if err := b.applyExtraSources(); err != nil {
			return fmt.Errorf("extra sources: %w", err)
		}
	} else {
		util.Infof("Krok 4/9: brak dodatkowych zrodel apt -- pominieto")
	}

	util.Infof("Krok 5/9: instalacja systemu MAC ([project] -> selinux=%v)...",
		b.Config.Project.MAC == config.MACSELinux)
	if err := b.installMACPackages(); err != nil {
		return fmt.Errorf("instalacja MAC: %w", err)
	}

	util.Infof("Krok 6/9: instalacja %d pakiet(ow)...", len(b.Project.Packages))
	if err := b.installPackages(); err != nil {
		return fmt.Errorf("instalacja pakietow: %w", err)
	}

	hasAfterIncludes := b.Project.IncludesChroot != "" || b.Project.IncludesChrootAfterPackages != ""
	if hasAfterIncludes {
		util.Infof("Krok 7/9: kopiowanie includes.chroot / includes.chroot_after_packages...")
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
		util.Infof("Krok 7/9: brak includes.chroot/includes.chroot_after_packages -- pominieto")
	}

	util.Infof("Krok 8/9: wykonywanie %d hook(ow)...", len(b.Project.Hooks))
	if err := b.runHooks(); err != nil {
		return fmt.Errorf("hooks: %w", err)
	}

	util.Infof("Krok 9/9: wstrzykiwanie hammer + generowanie /etc/hammer/oci.hk...")
	if err := b.injectHammer(); err != nil {
		return fmt.Errorf("hammer injection: %w", err)
	}
	if err := b.installHammerDeps(); err != nil {
		return fmt.Errorf("hammer biblioteki dynamiczne: %w", err)
	}
	if err := b.generateHammerConfig(); err != nil {
		return fmt.Errorf("generowanie /etc/hammer/oci.hk: %w", err)
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
func (b *Builder) runDebootstrap() error {
	return util.RunStreaming("", "debootstrap",
		"--arch=amd64",
		b.Config.Release,
		b.RootfsDir,
		defaultMirror,
	)
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
	args := append([]string{
		"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
		"-o", "APT::Get::Assume-Yes=true",
	}, b.Project.Packages...)
	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("apt-get install: %w", err)
	}
	return nil
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
	for _, h := range b.Project.Hooks {
		util.Infof("  hook: %s", h.Name)
		tmpName := "/tmp-hackeros-hook-" + h.Name
		destOnHost := filepath.Join(b.RootfsDir, tmpName)
		if err := copyFile(h.Path, destOnHost, 0o755); err != nil {
			return fmt.Errorf("kopiowanie hooka %s: %w", h.Name, err)
		}
		err := sandbox.ExecHook(b.RootfsDir, tmpName)
		os.Remove(destOnHost)
		if err != nil {
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
			return fmt.Errorf("wykonanie hooka %s: %w", h.Name, err)
		}
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
