package util

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// MakeDev odpowiada makedev(3) z glibc dla linuxowego, 32-bitowego numeru
// urzadzenia (wystarczajaco dla wszystkich standardowych wezlow /dev --
// major < 4096, minor < 256 daje po prostu (major << 8) | minor).
func MakeDev(major, minor uint32) int {
	ma, mi := uint64(major), uint64(minor)
	return int((ma&0xfff)<<8 | (mi & 0xff) | ((mi &^ 0xff) << 12) | ((ma &^ 0xfff) << 32))
}

// Mknod tworzy wezel urzadzenia (znakowy/blokowy) lub FIFO w path z
// DOKLADNIE zadanym trybem (omija umask przez Chmod po utworzeniu) --
// potrzebuje uprawnien roota (CAP_MKNOD), jak caly hackeros-builder.
// typeBits to syscall.S_IFCHR / S_IFBLK / S_IFIFO.
func Mknod(path string, typeBits uint32, perm os.FileMode, major, minor uint32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Nadpisujemy istniejacy wpis (np. pusty plik zastepczy) -- Lstat zeby nie
	// podazac za symlinkiem.
	if _, err := os.Lstat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := syscall.Mknod(path, typeBits|uint32(perm.Perm()), MakeDev(major, minor)); err != nil {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	return os.Chmod(path, perm.Perm())
}

// EssentialDevNodes to minimalny zestaw wezlow /dev, ktore MUSZA istniec w
// rootfs lezacym POD devtmpfs (tym, co jadro montuje na /dev) -- tak jak
// robi to debootstrap. Najwazniejszy jest /dev/console: skrypt /init z
// initramfs-tools konczy sie przez
//
//	exec run-init ... <"${rootmnt}/dev/console" >"${rootmnt}/dev/console"
//
// i bez tego pliku w docelowym rootfs powloka umiera na bledzie
// przekierowania, /init (PID 1) wychodzi z kodem != 0, a jadro zglasza
// "Kernel panic - not syncing: Attempted to kill init!".
var EssentialDevNodes = []struct {
	Name         string
	Perm         os.FileMode
	Major, Minor uint32
}{
	{"console", 0o600, 5, 1},
	{"null", 0o666, 1, 3},
	{"zero", 0o666, 1, 5},
	{"full", 0o666, 1, 7},
	{"random", 0o666, 1, 8},
	{"urandom", 0o666, 1, 9},
	{"tty", 0o666, 5, 0},
}

// EnsureDevSkeleton gwarantuje, ze rootfsDir ma katalogi punktow
// montowania (dev, proc, sys, run, tmp -- z poprawnymi trybami) oraz
// podstawowe wezly /dev. Istniejace wezly/pliki nie sa ruszane.
// Zwraca liste ostrzezen (np. brak CAP_MKNOD) -- wolajacy decyduje czy
// blad jest fatalny.
func EnsureDevSkeleton(rootfsDir string) (warnings []string) {
	dirs := []struct {
		name string
		perm os.FileMode
	}{
		{"dev", 0o755}, {"proc", 0o555}, {"sys", 0o555}, {"run", 0o755},
		{"tmp", 0o1777}, {"mnt", 0o755}, {"media", 0o755},
	}
	for _, d := range dirs {
		p := filepath.Join(rootfsDir, d.name)
		if err := os.MkdirAll(p, d.perm); err != nil {
			warnings = append(warnings, fmt.Sprintf("mkdir %s: %v", d.name, err))
			continue
		}
		if d.name == "tmp" {
			// sticky bit + zapis dla wszystkich (umask by go zjadl)
			_ = os.Chmod(p, os.ModeSticky|0o777)
		}
	}
	for _, n := range EssentialDevNodes {
		p := filepath.Join(rootfsDir, "dev", n.Name)
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			continue
		}
		if err := Mknod(p, syscall.S_IFCHR, n.Perm, n.Major, n.Minor); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return warnings
}
