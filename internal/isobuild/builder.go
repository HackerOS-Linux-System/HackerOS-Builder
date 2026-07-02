package isobuild

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
}

// excludeFromSquash to katalogi ktore NIE powinny trafic do squashfs
// (sa specyficzne dla danego boota hosta, nie dla obrazu systemu).
var excludeFromSquash = []string{"proc", "sys", "dev", "tmp", "run"}

// Build wykonuje caly przeplyw budowy ISO:
//  1. mksquashfs rootfs -> iso-tree/live/filesystem.squashfs
//  2. kopiowanie jadra+initrd z rootfs/boot -> iso-tree/live/
//  3. generowanie konfiguracji GRUB (BIOS+UEFI) w iso-tree/boot/grub/
//  4. grub-mkrescue -> OutputISO, hybrid BIOS+UEFI (xorriso pod maska)
func Build(p BuildParams) error {
	isoTree := filepath.Join(p.WorkDir, "iso-tree")
	if err := os.RemoveAll(isoTree); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", isoTree, err)
	}

	if !p.SkipInstaller {
		util.Infof("Krok 1/5: instalator GUI (Calamares)...")
		if err := InjectInstaller(p.RootfsDir, p.WorkDir); err != nil {
			return fmt.Errorf("instalator GUI: %w", err)
		}
	} else {
		util.Infof("Krok 1/5: instalator GUI pominiety (SkipInstaller)")
	}

	util.Infof("Krok 2/5: tworzenie squashfs z rootfs...")
	if err := buildSquashfs(p.RootfsDir, isoTree); err != nil {
		return fmt.Errorf("squashfs: %w", err)
	}

	util.Infof("Krok 3/5: kopiowanie jadra i initrd...")
	if err := copyKernelAndInitrd(p.RootfsDir, isoTree); err != nil {
		return fmt.Errorf("kernel/initrd: %w", err)
	}

	util.Infof("Krok 4/5: generowanie konfiguracji GRUB (BIOS+UEFI)...")
	if err := writeGrubConfig(isoTree, p.VolumeName); err != nil {
		return fmt.Errorf("grub config: %w", err)
	}

	util.Infof("Krok 5/5: budowanie hybrydowego ISO (grub-mkrescue)...")
	if err := runGrubMkrescue(isoTree, p.OutputISO, p.VolumeName); err != nil {
		return fmt.Errorf("grub-mkrescue: %w", err)
	}

	util.Infof("ISO zbudowane: %s", p.OutputISO)
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
func copyKernelAndInitrd(rootfsDir, isoTree string) error {
	bootDir := filepath.Join(rootfsDir, "boot")
	liveDir := filepath.Join(isoTree, "live")

	kernelPath, err := findGlob(bootDir, "vmlinuz-*")
	if err != nil {
		return fmt.Errorf("nie znaleziono jadra w %s (oczekiwano vmlinuz-*): %w", bootDir, err)
	}
	initrdPath, err := findGlob(bootDir, "initrd.img-*")
	if err != nil {
		return fmt.Errorf("nie znaleziono initrd w %s (oczekiwano initrd.img-*): %w", bootDir, err)
	}

	if err := copyFile(kernelPath, filepath.Join(liveDir, "vmlinuz")); err != nil {
		return fmt.Errorf("kopiowanie jadra: %w", err)
	}
	if err := copyFile(initrdPath, filepath.Join(liveDir, "initrd.img")); err != nil {
		return fmt.Errorf("kopiowanie initrd: %w", err)
	}
	return nil
}

// findGlob zwraca sciezke odpowiadajaca wzorcowi glob w danym katalogu
// (ostatnia alfabetycznie jesli jest wiele dopasowan -- numery wersji jadra
// Debiana sortuja sie rosnaco, wiec to typowo najnowsza wersja).
func findGlob(dir, pattern string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("brak plikow odpowiadajacych wzorcowi %s", pattern)
	}
	return matches[len(matches)-1], nil
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
	return util.RunStreaming("", "grub-mkrescue",
		"-o", outputISO,
		isoTree,
		"--",
		"-volid", volumeName,
	)
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
