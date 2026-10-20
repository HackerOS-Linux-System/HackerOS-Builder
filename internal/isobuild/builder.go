package isobuild

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/rootfs"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// BuildParams to dane potrzebne do zbudowania obrazu ISO.
type BuildParams struct {
	RootfsDir  string // rozpakowany rootfs (z obrazu OCI lub bezposrednio z build)
	OutputISO  string // docelowa sciezka pliku .iso
	WorkDir    string // katalog tymczasowy na squashfs/initrd/iso-tree
	VolumeName string // etykieta woluminu ISO (np. "HACKEROS") -- ISO9660 woli wielkie litery
	// ProjectDir to katalog projektu (zawiera config/) -- zrodlo obrazow
	// brandingu instalatora (patrz branding.go). Puste = obrazy generowane.
	ProjectDir string

	// MenuTitle to nazwa wyswietlana w menu GRUB (np. "HackerOS"). Puste
	// = "HackerOS". Oddzielona od VolumeName, bo etykieta woluminu ISO
	// zostaje wielkimi literami, a w menu ma byc "HackerOS".
	MenuTitle string

	// SkipInstaller pomija wstrzykniecie graficznego instalatora (Calamares)
	// do RootfsDir -- przydatne dla obrazow rescue/serwerowych, gdzie ISO
	// ma byc tylko live-medium bez kreatora instalacji. Domyslnie false:
	// kazde "build iso" produkuje gotowy do instalacji nosnik, bootujacy
	// PROSTO w instalator (patrz installer.go), bez posredniego pulpitu live.
	SkipInstaller bool

	// InstallerVariant okresla branding/dodatkowe narzedzia instalatora
	// (patrz InstallerVariant / brandingDescFor w installer.go). Wartosc
	// zerowa "" jest traktowana jak InstallerVariantDefault. Ignorowane
	// gdy SkipInstaller=true.
	InstallerVariant InstallerVariant

	// InstallerHooks to skrypty z config/hooks/installer/ (patrz
	// liveparse.Project.InstallerHooks / liveparse.ParseInstallerHooks) --
	// wykonywane TUTAJ, PO InjectInstaller, WYLACZNIE w tej kopii rootfs
	// uzywanej do zbudowania ISO (nigdy nie trafiaja do systemu docelowego).
	// Ignorowane gdy SkipInstaller=true (nie ma czego customizowac, skoro
	// instalator w ogole nie jest wstrzykiwany).
	InstallerHooks []liveparse.HookScript

	// Arch to architektura docelowa ([release] -> arch w config.hk, patrz
	// config.Config.EffectiveArch()) -- uzywana WYLACZNIE do ostrzezenia
	// (patrz warnIfForeignArch nizej), NIE do faktycznego przekazania
	// "-d <platforma>" do grub-mkrescue (to wymagaloby pakietow
	// grub-efi-<arch>-bin dla architektur INNYCH niz hosta, ktorych
	// hackeros-builder nie instaluje automatycznie -- patrz komentarz przy
	// warnIfForeignArch). Puste "" == architektura hosta (bez ostrzezenia).
	Arch string
}

// excludeFromSquash to WZORCE (mksquashfs -wildcards) wykluczajace z
// squashfs ZAWARTOSC katalogow specyficznych dla danego boota hosta
// (nie dla obrazu systemu) -- ale NIE same katalogi.
//
// KRYTYCZNY BUG (przyczyna paniki "Attempted to kill init!" na live-ISO):
// wczesniej bylo tu []string{"proc","sys","dev","tmp","run"} z flaga "-e"
// BEZ -wildcards -- a "-e dev" wycina caly katalog /dev (podobnie /proc,
// /sys, /run, /tmp) z obrazu, nie tylko jego zawartosc. Docelowy rootfs
// nie mial wtedy ani /dev/console, ani punktow montowania; /init z
// initramfs-tools (po zamontowaniu squashfs przez live-boot) robi
// "mount --move" /sys,/proc,/run do ${rootmnt} i na koniec
//
//	exec run-init ... <"${rootmnt}/dev/console" >"${rootmnt}/dev/console"
//
// co wywala sie na braku pliku -> /init (PID 1) konczy sie kodem != 0 ->
// "Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100".
// Dlatego: wzorce "<katalog>/*" (zostaje pusty katalog), a /dev jest
// obslugiwany osobno (patrz prepareRuntimeSkeleton).
var excludeFromSquash = []string{"proc/*", "sys/*", "run/*", "tmp/*"}

// requiredSquashEntries to wpisy, ktore MUSZA byc w filesystem.squashfs --
// verifySquashfs sprawdza je po zbudowaniu obrazu i przerywa build z jasnym
// komunikatem zamiast produkowac ISO, ktore panikuje przy starcie.
var requiredSquashEntries = []string{
	"squashfs-root/dev",
	"squashfs-root/dev/console",
	"squashfs-root/dev/null",
	"squashfs-root/proc",
	"squashfs-root/sys",
	"squashfs-root/run",
	"squashfs-root/tmp",
	"squashfs-root/etc",
	"squashfs-root/usr",
}

// archToDebianArch mapuje runtime.GOARCH (architektura HOSTA, na ktorym
// dziala akurat ten proces hackeros-builder) na nazewnictwo architektur
// Debiana -- potrzebne do porownania z [release] -> arch (ktore uzywa
// nazw Debiana: "amd64", "arm64", ...).
var archToDebianArch = map[string]string{
	"amd64":    "amd64",
	"arm64":    "arm64",
	"arm":      "armhf",
	"386":      "i386",
	"mips":     "mipsel",
	"mips64le": "mips64el",
	"ppc64le":  "ppc64el",
	"riscv64":  "riscv64",
	"s390x":    "s390x",
}

// warnIfForeignArch ostrzega gdy docelowa architektura ISO (arch) NIE
// odpowiada architekturze hosta budujacego -- grub-mkrescue (wywolywane
// nizej w runGrubMkrescue) NIE dostaje jawnego "-d <platforma>", wiec
// korzysta z modulow GRUB zainstalowanych NA HOSCIE (np. pakiet
// grub-efi-amd64-bin na typowym amd64 builderze). Dla architektury INNEJ
// niz hosta potrzebne sa DODATKOWE pakiety (np. grub-efi-arm64-bin do
// zbudowania ISO rozruchowego na arm64), ktorych hackeros-builder NIE
// instaluje automatycznie -- bez nich grub-mkrescue albo zawiedzie, albo
// (gorzej) po cichu zbuduje ISO ktore wyglada poprawnie, ale nie wstanie
// na docelowym sprzecie. To swiadomie NIE jest twardy blad (build moze byc
// uruchomiony na maszynie ktora faktycznie MA zainstalowane
// wielo-architekturowe pakiety GRUB), tylko jawne ostrzezenie -- patrz
// ROADMAP w README.md, "pelne wsparcie cross-arch ISO" jest osobnym,
// wiekszym zadaniem (przekazanie "-d" + walidacja obecnosci pakietow
// grub-efi-<arch>-bin PRZED probą budowy).
func warnIfForeignArch(arch string) {
	if arch == "" {
		return
	}
	hostArch, known := archToDebianArch[runtime.GOARCH]
	if !known {
		return
	}
	if arch == hostArch {
		return
	}
	util.Warnf(
		"[release] -> arch=%q rozni sie od architektury hosta budujacego (%q) -- "+
			"grub-mkrescue uzyje modulow GRUB Z HOSTA (hackeros-builder nie przekazuje "+
			"jawnego \"-d <platforma>\"), co dla INNEJ architektury zazwyczaj oznacza ISO "+
			"ktore NIE WSTANIE na docelowym sprzecie, chyba ze host ma recznie zainstalowane "+
			"wielo-architekturowe pakiety GRUB (np. grub-efi-%s-bin) I samodzielnie "+
			"skonfigurowany odpowiedni --target. Pelne wsparcie cross-arch ISO to osobna "+
			"pozycja w ROADMAP (README.md) -- na razie [release] -> arch niezawodnie wplywa "+
			"TYLKO na sam rootfs (debootstrap), nie na bootowalnosc finalnego ISO.",
		arch, hostArch, arch)
}

// Build wykonuje caly przeplyw budowy ISO:
//  1. mksquashfs rootfs -> iso-tree/live/filesystem.squashfs
//  2. kopiowanie jadra+initrd z rootfs/boot -> iso-tree/live/
//  3. generowanie konfiguracji GRUB (BIOS+UEFI) w iso-tree/boot/grub/
//  4. grub-mkrescue -> OutputISO, hybrid BIOS+UEFI (xorriso pod maska)
func Build(p BuildParams) error {
	util.Section("Budowa ISO")
	warnIfForeignArch(p.Arch)

	isoTree := filepath.Join(p.WorkDir, "iso-tree")
	if err := os.RemoveAll(isoTree); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", isoTree, err)
	}

	totalSteps := 7
	if !p.SkipInstaller && len(p.InstallerHooks) > 0 {
		totalSteps = 8
	}
	step := 0

	if !p.SkipInstaller {
		variant := p.InstallerVariant
		if variant == "" {
			variant = InstallerVariantDefault
		}
		step++
		util.Step(step, totalSteps, "instalator GUI (Calamares, wariant: %s)...", variantLabel(variant))
		if err := InjectInstaller(p.RootfsDir, p.WorkDir, variant, p.ProjectDir, p.Arch); err != nil {
			return fmt.Errorf("instalator GUI: %w", err)
		}

		if len(p.InstallerHooks) > 0 {
			step++
			util.Step(step, totalSteps, "hooki instalatora (config/hooks/installer/, %d)...", len(p.InstallerHooks))
			if err := runInstallerHooks(p.RootfsDir, p.WorkDir, p.InstallerHooks); err != nil {
				return fmt.Errorf("hooki instalatora: %w", err)
			}
		}
	} else {
		step++
		util.Step(step, totalSteps, "instalator GUI pominiety (SkipInstaller)")
	}

	step++
	util.Step(step, totalSteps, "niezmiennosc systemu (/usr, /boot, /opt tylko do odczytu)...")
	if err := writeImmutability(p.RootfsDir); err != nil {
		return fmt.Errorf("niezmiennosc systemu: %w", err)
	}

	// Usuwamy apt/apt-get DOKLADNIE tutaj -- PO ewentualnym wstrzynkieciu
	// Calamares i hookow instalatora (ktore jeszcze potrzebuja apt-get do
	// zainstalowania siebie/swoich zaleznosci -- Xorg, openbox, interpretery
	// jezykow itd. -- w rootfs pociagnietym z registry, gdzie apt-get juz
	// nie byl usuwany na etapie "build cloud", patrz komentarz w
	// internal/rootfs/builder.go), ale PRZED zbudowaniem squashfs.img. Ten
	// squashfs.img jest tym, co Calamares PozNIEJ kopiuje 1:1 na dysk
	// uzytkownika -- czyli od tego miejsca w przeplywie budowy, "finalny
	// dysk" uzytkownika jest juz gwarantowany jako wolny od apt/apt-get,
	// przy zachowanej bazie dpkg (na ktorej nadal polega hammer).
	step++
	util.Step(step, totalSteps, "usuwanie apt/apt-get (finalny dysk uzytkownika ma byc bez nich; baza dpkg pozostaje dla hammer)...")
	if err := rootfs.RemoveAptTooling(p.RootfsDir); err != nil {
		return fmt.Errorf("usuwanie apt/apt-get: %w", err)
	}

	step++
	util.Step(step, totalSteps, "tworzenie squashfs z rootfs...")
	if err := buildSquashfs(p.RootfsDir, isoTree); err != nil {
		return fmt.Errorf("squashfs: %w", err)
	}

	step++
	util.Step(step, totalSteps, "kopiowanie jadra i initrd...")
	if err := copyKernelAndInitrd(p.RootfsDir, isoTree); err != nil {
		return fmt.Errorf("kernel/initrd: %w", err)
	}

	step++
	util.Step(step, totalSteps, "generowanie konfiguracji GRUB (BIOS+UEFI)...")
	menuTitle := p.MenuTitle
	if menuTitle == "" {
		menuTitle = "HackerOS"
	}
	if err := writeGrubConfig(isoTree, menuTitle, !p.SkipInstaller); err != nil {
		return fmt.Errorf("grub config: %w", err)
	}

	step++
	util.Step(step, totalSteps, "budowanie hybrydowego ISO (grub-mkrescue)...")
	if err := runGrubMkrescue(isoTree, p.OutputISO, p.VolumeName); err != nil {
		return fmt.Errorf("grub-mkrescue: %w", err)
	}

	util.Infof("ISO zbudowane: %s", util.Underline(p.OutputISO))
	return nil
}

// buildSquashfs wywoluje "mksquashfs rootfsDir isoTree/live/filesystem.squashfs",
// wykluczajac katalogi wirtualne (proc/sys/dev/tmp/run) ktore nie powinny
// trafic do obrazu dystrybuowanego (to nie sa dane systemu, tylko punkty
// montowania kernela na czas dzialania).
func buildSquashfs(rootfsDir, isoTree string) error {
	liveDir := filepath.Join(isoTree, "live")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		return err
	}

	squashPath := filepath.Join(liveDir, "filesystem.squashfs")

	if err := prepareRuntimeSkeleton(rootfsDir); err != nil {
		return err
	}

	args := []string{rootfsDir, squashPath, "-comp", "xz", "-noappend", "-wildcards", "-e"}
	args = append(args, excludeFromSquash...)

	if err := util.RunStreaming("", "mksquashfs", args...); err != nil {
		return err
	}
	return verifySquashfs(squashPath)
}

// prepareRuntimeSkeleton upewnia sie, ze rootfs (z ktorego powstanie
// squashfs) ma katalogi punktow montowania i podstawowe wezly /dev
// (console, null, ...) -- patrz komentarz przy excludeFromSquash. Rootfs
// pochodzi z obrazu OCI, a wezly urzadzen byly historycznie gubione przy
// rozpakowywaniu warstw, wiec odtwarzamy je tu niezaleznie (idempotentnie).
//
// Zabezpieczenie: gdyby cokolwiek bylo zamontowane POD rootfs/dev
// (np. wyciek bind-mounta z sandboxa), NIE dotykamy tego katalogu --
// mknod w devtmpfs hosta i pakowanie go do obrazu byloby grozne.
func prepareRuntimeSkeleton(rootfsDir string) error {
	if mounts := mountsUnder(rootfsDir); len(mounts) > 0 {
		return fmt.Errorf(
			"pod %s sa nadal zamontowane systemy plikow (%s) -- odmontuj je przed budowa "+
				"squashfs (inaczej do obrazu trafilaby zawartosc hosta)",
			rootfsDir, strings.Join(mounts, ", "))
	}
	for _, w := range util.EnsureDevSkeleton(rootfsDir) {
		util.Warnf("rootfs skeleton: %s", w)
	}
	// /dev/console jest twardym wymaganiem -- bez niego live-medium panikuje.
	if fi, err := os.Lstat(filepath.Join(rootfsDir, "dev", "console")); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf(
			"brak wezla znakowego %s/dev/console i nie udalo sie go utworzyc (uruchom hackeros-builder jako root) "+
				"-- bez niego initramfs nie wystartuje systemu (kernel panic: Attempted to kill init!)",
			rootfsDir)
	}
	return nil
}

// mountsUnder zwraca punkty montowania lezace wewnatrz dir (wg
// /proc/self/mountinfo). Gdy mountinfo jest nieczytelny -- zwraca nil.
func mountsUnder(dir string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := strings.ReplaceAll(f[4], "\\040", " ")
		if mp == abs || strings.HasPrefix(mp, abs+"/") {
			out = append(out, mp)
		}
	}
	return out
}

// verifySquashfs przeglada zawartosc zbudowanego filesystem.squashfs
// ("unsquashfs -l") i sprawdza obecnosc wpisow z requiredSquashEntries.
func verifySquashfs(squashPath string) error {
	cmd := exec.Command("unsquashfs", "-l", squashPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("weryfikacja squashfs (unsquashfs -l): %w", err)
	}
	need := make(map[string]bool, len(requiredSquashEntries))
	for _, e := range requiredSquashEntries {
		need[e] = true
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		delete(need, strings.TrimSpace(sc.Text()))
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("weryfikacja squashfs (unsquashfs -l): %w", err)
	}
	if len(need) > 0 {
		var missing []string
		for k := range need {
			missing = append(missing, strings.TrimPrefix(k, "squashfs-root"))
		}
		sort.Strings(missing)
		return fmt.Errorf(
			"filesystem.squashfs nie zawiera wymaganych wpisow: %s -- taki obraz konczy sie "+
				"panika jadra \"Attempted to kill init!\" przy starcie", strings.Join(missing, ", "))
	}
	util.Infof("  squashfs zweryfikowany: /dev (z /dev/console), /proc, /sys, /run, /tmp obecne")
	return nil
}

// copyKernelAndInitrd kopiuje vmlinuz i initrd.img z rootfs/boot do
// isoTree/live/ -- nazwy plikow jadra w Debianie maja format
// vmlinuz-<wersja> / initrd.img-<wersja>, wiec szukamy wzorca z glob.
//
// UWAGA (bylo zrodlem realnego bledu): wczesniej vmlinuz-* i initrd.img-*
// byly wyszukiwane DWOMA NIEZALEZNYMI wywolaniami findGlob, kazde z osobna
// wybierajace "ostatnie dopasowanie alfabetycznie" -- bez ZADNEJ gwarancji
// ze wybrany vmlinuz-<A> i wybrany initrd.img-<B> odpowiadaja TEJ SAMEJ
// wersji jadra. W typowym przypadku (dokladnie jeden kernel w /boot) nie ma
// to znaczenia, ale gdy w /boot znajdzie sie WIECEJ NIZ JEDEN kernel (np.
// projekt instaluje wlasny kernel hookiem PO tym jak jakis inny pakiet --
// posrednio, przez Recommends/Depends, patrz "celowo BEZ
// --no-install-recommends" w internal/rootfs/builder.go -- juz sciagnal
// standardowy "linux-image-amd64"), niezalezne sortowanie moglo dobrac
// NIEDOPASOWANA pare vmlinuz/initrd (initrd zbudowany dla INNEJ wersji
// jadra niz to, ktore faktycznie bootuje) -- initrd taki nie ma poprawnego
// katalogu modulow dla uruchomionego jadra, wiec live-boot/squashfs/overlay
// moze nie zaladowac sie poprawnie, co konczy sie martwym PID 1 w initrd
// (initramfs-tools "/init" wyczerpuje fallbacki i konczy dzialanie) --
// kernel panikuje z "Attempted to kill init! exitcode=0x00000100".
//
// Teraz: znajdujemy WSZYSTKIE pary (vmlinuz-V, initrd.img-V) o TEJ SAMEJ
// wersji V, i wybieramy najnowsza (sortowanie wersji Debiana). Jesli w
// /boot jest wiecej niz jedna wersja jadra, to jest jawnie zglaszane w
// logu (nie po cichu) -- bo to zazwyczaj oznacza ze projekt przypadkiem
// sciaga dwa jadra (np. glowna lista pakietow I dodatkowy hook), co samo w
// sobie warto naprawic w konfiguracji projektu, a nie tylko "obejsc" tutaj.
func copyKernelAndInitrd(rootfsDir, isoTree string) error {
	bootDir := filepath.Join(rootfsDir, "boot")
	liveDir := filepath.Join(isoTree, "live")

	versions, err := kernelVersionsWithInitrd(bootDir)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf(
			"nie znaleziono w %s ani jednej PELNEJ pary vmlinuz-<wersja>/initrd.img-<wersja> "+
				"(sam vmlinuz bez initrd, lub sam initrd bez vmlinuz, sie nie licza -- "+
				"boot musialby uzyc niedopasowanej pary, co konczy sie panika "+
				"\"Attempted to kill init!\" na starcie live-medium)", bootDir)
	}
	if len(versions) > 1 {
		util.Warnf(
			"w %s znaleziono %d roznych wersji jadra z kompletna para vmlinuz/initrd (%s) -- "+
				"wybieram najnowsza (%s); jesli to nieoczekiwane, sprawdz czy projekt (package-lists "+
				"lub hooks) nie instaluje przypadkiem DWOCH jader (np. standardowego linux-image-amd64 "+
				"jako zaleznosc posrednia I wlasnego kernela hookiem)",
			bootDir, len(versions), strings.Join(versions, ", "), versions[len(versions)-1])
	}
	chosen := versions[len(versions)-1]

	kernelPath := filepath.Join(bootDir, "vmlinuz-"+chosen)
	initrdPath := filepath.Join(bootDir, "initrd.img-"+chosen)

	if err := copyFile(kernelPath, filepath.Join(liveDir, "vmlinuz")); err != nil {
		return fmt.Errorf("kopiowanie jadra: %w", err)
	}
	if err := copyFile(initrdPath, filepath.Join(liveDir, "initrd.img")); err != nil {
		return fmt.Errorf("kopiowanie initrd: %w", err)
	}
	return nil
}

// kernelVersionsWithInitrd zwraca posortowane rosnaco (wg debianowej
// kolejnosci wersji) listy sufiksow wersji <V>, dla ktorych w bootDir
// istnieje JEDNOCZESNIE "vmlinuz-<V>" ORAZ "initrd.img-<V>" -- czyli
// faktycznie uzywalne, DOPASOWANE pary. Wersje z samym vmlinuz albo samym
// initrd (niekompletne, np. po nieudanym/przerwanym update-initramfs) sa
// pomijane.
func kernelVersionsWithInitrd(bootDir string) ([]string, error) {
	vmlinuzMatches, err := filepath.Glob(filepath.Join(bootDir, "vmlinuz-*"))
	if err != nil {
		return nil, err
	}
	initrdMatches, err := filepath.Glob(filepath.Join(bootDir, "initrd.img-*"))
	if err != nil {
		return nil, err
	}

	hasInitrd := make(map[string]bool, len(initrdMatches))
	for _, p := range initrdMatches {
		hasInitrd[strings.TrimPrefix(filepath.Base(p), "initrd.img-")] = true
	}

	var versions []string
	for _, p := range vmlinuzMatches {
		v := strings.TrimPrefix(filepath.Base(p), "vmlinuz-")
		if hasInitrd[v] {
			versions = append(versions, v)
		}
	}

	// UWAGA: sortowanie leksykograficzne, NIE pelne porownanie wersji
	// Debiana (jak np. "dpkg --compare-versions") -- ta sama, juz wczesniej
	// istniejaca w tym pliku uproszczona zasada "ostatni alfabetycznie =
	// najnowszy" (patrz poprzednia wersja findGlob). W typowym przypadku
	// (dokladnie jedna wersja jadra w /boot) nie ma to znaczenia -- realny
	// problem, ktory ta funkcja naprawia, to DOPASOWANIE pary vmlinuz/initrd
	// do TEJ SAMEJ wersji, nie idealny wybor "najnowszej" sposrod wielu.
	sort.Strings(versions)
	return versions, nil
}

// writeGrubConfig generuje minimalna konfiguracje GRUB dla obrazu live --
// wpis bootujacy jadro z initrd, z parametrem "boot=live" wskazujacym na
// squashfs (konwencja live-boot uzywana takze przez live-build).
func writeGrubConfig(isoTree, menuTitle string, installer bool) error {
	grubDir := filepath.Join(isoTree, "boot", "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return err
	}

	// Wpisy:
	//  1. normalny start (quiet splash),
	//  2. safe graphics (nomodeset),
	//  3. DEBUG -- bez quiet/splash, z "debug" dla initramfs i konsola
	//     szeregowa; przy awarii live-boot (np. "Unable to find a medium
	//     containing a live file system") widac prawdziwy komunikat zamiast
	//     gołej paniki jadra.
	// "live-media-path=/live" jest domyslne w live-boot, ale podajemy je jawnie
	// zeby konfiguracja byla samodokumentujaca sie.
	// Gdy ISO ma instalator (hackeros-installer.service, patrz installer.go),
	// live musi wstac w multi-user.target -- inaczej obok instalatora
	// wystartowalby menedzer logowania/pulpit z rootfs i oba walczylyby o
	// X :0 / tty. Robimy to parametrem jadra, a NIE nadpisaniem
	// default.target w rootfs, bo ten rootfs kopiuje na dysk Calamares.
	unit := ""
	if installer {
		// Bez Plymouth: "splash" zostawial niebieski ekran na tty1/DRM i X
		// instalatora nie mogl go przejac (widac bylo sam gradient).
		unit = " systemd.unit=multi-user.target plymouth.enable=0"
	}
	splash := " splash"
	if installer {
		splash = ""
	}

	cfg := fmt.Sprintf(`set timeout=5
set default=0

menuentry "%[1]s" {
    linux /live/vmlinuz boot=live live-media-path=/live%[2]s quiet%[3]s
    initrd /live/initrd.img
}

menuentry "%[1]s (safe graphics)" {
    linux /live/vmlinuz boot=live live-media-path=/live%[2]s quiet nomodeset
    initrd /live/initrd.img
}

menuentry "%[1]s (debug - bez quiet/splash)" {
    linux /live/vmlinuz boot=live live-media-path=/live%[2]s nomodeset debug ignore_loglevel earlyprintk=vga console=tty0 console=ttyS0,115200
    initrd /live/initrd.img
}
`, menuTitle, unit, splash)

	return os.WriteFile(filepath.Join(grubDir, "grub.cfg"), []byte(cfg), 0o644)
}

// runGrubMkrescue wywoluje "grub-mkrescue" do zbudowania hybrydowego ISO
// (bootowalnego zarowno przez legacy BIOS jak i UEFI) -- grub-mkrescue
// generuje wewnetrznie poprawna strukture El Torito + GPT/MBR hybrid przez
// xorriso, bez koniecznosci recznego sklejania wywolan xorriso.
func runGrubMkrescue(isoTree, outputISO, volumeName string) error {
	if err := os.MkdirAll(filepath.Dir(outputISO), 0o755); err != nil {
		return err
	}

	xorrisoWrapper, err := writeXorrisoIsoLevelWrapper(filepath.Dir(outputISO))
	if err != nil {
		return fmt.Errorf("przygotowanie wrappera xorriso (iso-level 3): %w", err)
	}

	return util.RunStreaming("", "grub-mkrescue",
		"--xorriso="+xorrisoWrapper,
		"-o", outputISO,
		isoTree,
		"--",
		"-volid", volumeName,
	)
}

// writeXorrisoIsoLevelWrapper tworzy maly skrypt-wrapper podstawiany pod
// grub-mkrescue przez flage --xorriso=FILE, ktorego JEDYNYM zadaniem jest
// wstrzykniecie "-compliance iso_9660_level=3" jako PIERWSZEGO argumentu
// przekazywanego do prawdziwego xorriso -- i zwrocenie sciezki do niego.
//
// To wlacza obsluge plikow > 4 GiB w ISO9660 (mechanizm "multi-extent" --
// xorriso dzieli duzy plik na wiele extentow ISO9660 i przezroczyscie
// skleja je z powrotem przy odczycie). Bez tego, domyslny poziom ISO9660
// ogranicza pojedynczy plik do 4294967295 bajtow (4 GiB - 1), co jest za
// malo dla filesystem.squashfs pelnego srodowiska graficznego (~5 GB) --
// xorriso przerywa wtedy z "File exceeds size limit of 4294967295 bytes".
//
// DLACZEGO WRAPPER, A NIE PO PROSTU DOPISANIE "-compliance ..." DO
// ARGUMENTOW PO "--": grub-mkrescue buduje WLASNA, wewnetrzna linie
// polecen xorriso (wlasne "-outdev" i "-map" dla zawartosci GRUB-a oraz
// dla isoTree) i dopiero na SAM KONIEC doklada to, co my przekazemy po
// "--". Polecenia xorriso wykonuja sie SEKWENCYJNIE -- ustawienie
// "-compliance" PO tym jak pliki zostaly juz dodane przez "-map" (co
// weryfikowalismy bezposrednio: xorriso i tak konczy sie identycznym
// "File exceeds size limit", mimo poprawnej skladni -compliance) nie ma
// juz zadnego efektu. Podstawiajac cala binarke xorriso wrapperem,
// wymuszamy "-compliance iso_9660_level=3" jako pierwszy argument, czyli
// PRZED jakimkolwiek "-map" grub-mkrescue -- zweryfikowane bezposrednio
// (plik testowy > 4 GiB, przez prawdziwy grub-mkrescue, zakonczone
// sukcesem).
func writeXorrisoIsoLevelWrapper(dir string) (string, error) {
	realXorriso, err := exec.LookPath("xorriso")
	if err != nil {
		return "", fmt.Errorf("nie znaleziono 'xorriso' w PATH: %w", err)
	}

	wrapperPath := filepath.Join(dir, "xorriso-iso-level-wrapper.sh")
	script := "#!/bin/sh\nexec " + shellQuote(realXorriso) +
		" -compliance iso_9660_level=3 \"$@\"\n"

	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("zapis wrappera do %s: %w", wrapperPath, err)
	}
	return wrapperPath, nil
}

// shellQuote otacza s pojedynczymi cudzyslowami dla bezpiecznego uzycia w
// wygenerowanym skrypcie POSIX sh (na wypadek gdyby sciezka do xorriso
// zawierala spacje lub inne znaki specjalne).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// copyFile kopiuje zawartosc pliku src do dst (tworzac dst od nowa).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
