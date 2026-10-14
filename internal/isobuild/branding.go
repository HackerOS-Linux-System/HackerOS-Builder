package isobuild

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// brandingSourceDirs zwraca katalogi (wzgledem katalogu projektu), w ktorych
// builder szuka obrazow brandingu instalatora, w kolejnosci priorytetu:
//
//  1. config/includes.chroot_after_packages/etc/calamares/branding/debian
//  2. config/includes.chroot/etc/calamares/branding/debian
//  3. config/includes.chroot_before_packages/etc/calamares/branding/debian
//  4. config/installer-branding  -- kopia zapasowa robiona przez skrypty
//     build/build-hackeros-* PRZED usunieciem config/.../etc/calamares
//     (usuwaja go, zeby live-build-owa konfiguracja Calamares nie trafila do
//     obrazu OCI -- obrazy brandingu zachowujemy tu).
func brandingSourceDirs(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	cfg := filepath.Join(projectDir, "config")
	var dirs []string
	for _, inc := range []string{"includes.chroot_after_packages", "includes.chroot", "includes.chroot_before_packages"} {
		dirs = append(dirs, filepath.Join(cfg, inc, "etc", "calamares", "branding", "debian"))
	}
	return append(dirs, filepath.Join(cfg, "installer-branding"))
}

// brandingFile ma nazwe zrodlowa (w projekcie HackerOS) i docelowa (w
// /etc/calamares/branding/hackeros, zgodna z branding.desc).
type brandingFile struct {
	srcName string
	dstName string
}

var projectBrandingFiles = []brandingFile{
	{"debian-logo.png", "logo.png"}, // logo HackerOS
	{"welcome.png", "welcome.png"},  // banner HackerOS
}

// loadProjectBranding wczytuje z projektu logo (debian-logo.png) i banner
// (welcome.png). Zwraca nil dla pliku, ktorego nie znaleziono lub ktory nie
// jest poprawnym PNG -- wolajacy uzywa wtedy obrazu generowanego
// (brandingPNGs). Zwracana mapa: dstName -> zawartosc.
func loadProjectBranding(projectDir string) map[string][]byte {
	out := map[string][]byte{}
	for _, bf := range projectBrandingFiles {
		for _, dir := range brandingSourceDirs(projectDir) {
			path := filepath.Join(dir, bf.srcName)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
				util.Warnf("branding: %s nie jest poprawnym PNG (%v) -- pomijam", path, err)
				continue
			}
			util.Infof("  branding instalatora: %s -> %s", path, bf.dstName)
			out[bf.dstName] = data
			break
		}
		if _, ok := out[bf.dstName]; !ok {
			util.Warnf("branding: nie znaleziono %s w projekcie (szukano w %v) -- uzywam obrazu generowanego",
				bf.srcName, brandingSourceDirs(projectDir))
		}
	}
	return out
}
