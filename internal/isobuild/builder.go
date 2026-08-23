package isobuild

import (
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
	VolumeName string // etykieta woluminu ISO (np. "HACKEROS")

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

// excludeFromSquash to katalogi ktore NIE powinny trafic do squashfs
// (sa specyficzne dla danego boota hosta, nie dla obrazu systemu).
var excludeFromSquash = []string{"proc", "sys", "dev", "tmp", "run"}

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

	totalSteps := 6
	if !p.SkipInstaller && len(p.InstallerHooks) > 0 {
		totalSteps = 7
	}
	step := 0

	if !p.SkipInstaller {
		variant := p.InstallerVariant
		if variant == "" {
			variant = InstallerVariantDefault
		}
		step++
		util.Step(step, totalSteps, "instalator GUI (Calamares, wariant: %s)...", variantLabel(variant))
		if err := InjectInstaller(p.RootfsDir, p.WorkDir, variant); err != nil {
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
	if err := writeGrubConfig(isoTree, p.VolumeName); err != nil {
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

	args := []string{rootfsDir, squashPath, "-comp", "xz", "-noappend"}
	for _, ex := range excludeFromSquash {
		args = append(args, "-e", ex)
	}

	return util.RunStreaming("", "mksquashfs", args...)
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
func writeGrubConfig(isoTree, volumeName string) error {
	grubDir := filepath.Join(isoTree, "boot", "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return err
	}

	cfg := fmt.Sprintf(`set timeout=5
set default=0

menuentry "%s" {
    linux /live/vmlinuz boot=live quiet splash
    initrd /live/initrd.img
}

menuentry "%s (safe graphics)" {
    linux /live/vmlinuz boot=live quiet nomodeset
    initrd /live/initrd.img
}
`, volumeName, volumeName)

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
