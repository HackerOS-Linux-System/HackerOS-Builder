package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// Tool opisuje jedno narzedzie build-time: binarka + pakiet apt z ktorego
// pochodzi + opcjonalne dodatkowe pakiety (zaleznosci nie rozwiazywane przez
// dpkg w trybie standalone-extract, ale wymagane przez skrypt).
type Tool struct {
	Binary      string   // nazwa binarki szukanej w $PATH / toolchain-bin/
	AptPackages []string // pakiety .deb do pobrania i rozpakowania
	UsedBy      string   // opis kroku builda (do logow i komunikatow bledu)
}

// buildTools to PELNA lista narzedzi wymaganych przez hackeros-builder.
// Kazde narzedzie jest najpierw szukane w $PATH systemu hosta; jesli nie ma
// -- pobierane tymczasowo. Uzytkownik nie musi instalowac ZADNEGO z nich.
var buildTools = []Tool{
	{
		Binary:      "debootstrap",
		AptPackages: []string{"debootstrap"},
		UsedBy:      "build cloud: tworzenie bazowego rootfs Debiana",
	},
	{
		Binary:      "mksquashfs",
		AptPackages: []string{"squashfs-tools"},
		UsedBy:      "build iso: pakowanie rootfs do filesystem.squashfs",
	},
	{
		Binary:      "grub-mkrescue",
		AptPackages: []string{"grub-common", "grub-pc-bin", "grub-efi-amd64-bin"},
		UsedBy:      "build iso: budowa hybrydowego ISO BIOS+UEFI",
	},
	{
		Binary:      "xorriso",
		AptPackages: []string{"xorriso"},
		UsedBy:      "build iso: tworzenie obrazu ISO (uzywane przez grub-mkrescue)",
	},
	// unshare i chroot sa z util-linux/coreutils -- zawsze obecne na Debianie,
	// pomijamy je w auto-download (i tak sa zawsze dostepne).
}

// toolchainBinDir to nazwa podkatalogu katalogu roboczego buildu przeznaczonego
// na tymczasowe binarki pobrane przez toolchain.
const toolchainBinDir = "toolchain-bin"

// Manager zarzadza zestawem narzedzi build-time dla konkretnego buildu.
type Manager struct {
	// WorkDir to katalog roboczy buildu (np. ./hackeros-build-work).
	// Narzedzia sa rozpakowywane do WorkDir/toolchain-bin/.
	WorkDir string

	// binDir to pelna sciezka do toolchain-bin/ -- obliczana raz w Init.
	binDir string

	// preparedPath to wartosc $PATH z dopisanym binDir na pocztaku,
	// uzywana przez Env() i ustawiana przez PrepareAll.
	preparedPath string
}

// New tworzy nowy Manager dla podanego katalogu roboczego buildu.
func New(workDir string) *Manager {
	return &Manager{
		WorkDir: workDir,
		binDir:  filepath.Join(workDir, toolchainBinDir),
	}
}

// PrepareAll sprawdza i/lub pobiera WSZYSTKIE narzedzia build-time.
// Wywolywane raz na poczatku kazdego buildu (przed debootstrap, przed
// mksquashfs itp.). Jesli narzedzie jest juz dostepne w $PATH lub w
// binDir, jest pomijane (cache). Zwraca blad jesli pobranie jakiegokolwiek
// narzedzia sie nie powiedzie.
func (m *Manager) PrepareAll() error {
	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return fmt.Errorf("toolchain: tworzenie %s: %w", m.binDir, err)
	}

	m.preparedPath = m.binDir + ":" + os.Getenv("PATH")

	var missing []Tool
	for _, t := range buildTools {
		if m.toolAvailable(t.Binary) {
			util.Debugf("toolchain: %s -- dostepne w PATH lub cache, pomijam pobieranie", t.Binary)
			continue
		}
		missing = append(missing, t)
	}

	if len(missing) == 0 {
		util.Infof("toolchain: wszystkie narzedzia dostepne (bez pobierania)")
		return nil
	}

	util.Infof("toolchain: brakuje %d narzedzi -- pobieranie tymczasowe do %s/", len(missing), toolchainBinDir)
	util.Infof("  (narzedzia sa pobierane jako .deb i rozpakowywane lokalnie,")
	util.Infof("   NIE sa instalowane na systemie hosta -- zero konfliktow zaleznosci)")

	for _, t := range missing {
		util.Infof("  pobieranie: %s (pakiety: %s)...", t.Binary, strings.Join(t.AptPackages, ", "))
		if err := m.downloadAndExtract(t); err != nil {
			return fmt.Errorf("toolchain: nie mozna przygotowac narzedzia %q: %w", t.Binary, err)
		}
		util.Infof("  ok: %s -> %s/", t.Binary, toolchainBinDir)
	}

	return nil
}

// Env zwraca zmienne srodowiskowe ktore powinny byc przekazane do procesow
// potomnych (apt-get, debootstrap itp.) tak by uzywaly tymczasowych binarek
// z toolchain-bin/ jesli potrzebne. Wywolaj po PrepareAll.
//
// Zwraca []string w formacie "KLUCZ=WARTOSC" gotowy do os.Environ() / exec.Cmd.Env.
func (m *Manager) Env() []string {
	if m.preparedPath == "" {
		m.preparedPath = m.binDir + ":" + os.Getenv("PATH")
	}
	return []string{"PATH=" + m.preparedPath}
}

// BinPath zwraca pelna sciezke do binarki w toolchain-bin/, jesli tam istnieje.
// Zwraca pusty string jesli binarka nie jest w toolchain (jest w systemowym PATH).
func (m *Manager) BinPath(binary string) string {
	p := filepath.Join(m.binDir, binary)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// toolAvailable sprawdza czy binarka jest dostepna w toolchain-bin/ LUB
// w systemowym $PATH (w tej kolejnosci -- toolchain-bin/ ma priorytet).
func (m *Manager) toolAvailable(binary string) bool {
	// najpierw toolchain-bin/
	if _, err := os.Stat(filepath.Join(m.binDir, binary)); err == nil {
		return true
	}
	// potem systemowy PATH
	if _, err := exec.LookPath(binary); err == nil {
		return true
	}
	return false
}

// downloadAndExtract pobiera pakiety .deb dla danego Tool przez "apt-get download"
// do tymczasowego podkatalogu w binDir, rozpakuje je przez "dpkg-deb --extract"
// i kopiuje binarki (usr/bin/*, usr/sbin/*, sbin/*, bin/*) do binDir.
// Nie modyfikuje bazy danych dpkg hosta.
func (m *Manager) downloadAndExtract(t Tool) error {
	// Tymczasowy katalog na pobrane .deb i rozpakowane drzewa dla tego narzedzia
	tmpDir := filepath.Join(m.binDir, ".tmp-"+t.Binary)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmpDir, err)
	}
	defer os.RemoveAll(tmpDir) // sprzatamy po sobie bez wzgledu na wynik

	// apt-get download pobiera .deb bez instalacji -- nie wymaga roota
	// (chyba ze katalog docelowy jest poza $HOME, ale tutaj to workDir
	// usera), nie modyfikuje /var/lib/dpkg.
	downloadArgs := append([]string{"download"}, t.AptPackages...)
	dlCmd := exec.Command("apt-get", downloadArgs...)
	dlCmd.Dir = tmpDir
	dlCmd.Stdout = os.Stdout
	dlCmd.Stderr = os.Stderr
	if err := dlCmd.Run(); err != nil {
		return fmt.Errorf("apt-get download %v: %w", t.AptPackages, err)
	}

	// Znajdz pobrane .deb i rozpakuj kazdy przez dpkg-deb --extract
	debs, err := filepath.Glob(filepath.Join(tmpDir, "*.deb"))
	if err != nil || len(debs) == 0 {
		return fmt.Errorf("brak pobranych plikow .deb w %s", tmpDir)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	for _, deb := range debs {
		extractCmd := exec.Command("dpkg-deb", "--extract", deb, extractDir)
		extractCmd.Stdout = os.Stdout
		extractCmd.Stderr = os.Stderr
		if err := extractCmd.Run(); err != nil {
			return fmt.Errorf("dpkg-deb --extract %s: %w", filepath.Base(deb), err)
		}
	}

	// Kopiuj binarki z typowych lokalizacji do m.binDir
	binDirs := []string{
		filepath.Join(extractDir, "usr", "bin"),
		filepath.Join(extractDir, "usr", "sbin"),
		filepath.Join(extractDir, "bin"),
		filepath.Join(extractDir, "sbin"),
		filepath.Join(extractDir, "usr", "lib", "grub"), // dla grub-mkrescue
	}
	copied := 0
	for _, src := range binDirs {
		n, err := copyBinaries(src, m.binDir)
		if err != nil {
			return fmt.Errorf("kopiowanie binarek z %s: %w", src, err)
		}
		copied += n
	}

	// Specjalny przypadek: debootstrap to skrypt instalowany w /usr/sbin/debootstrap
	// ale potrzebuje katalogu /usr/share/debootstrap/ z suite-scriptami.
	// Kopiujemy caly /usr/share/debootstrap/ do binDir/../share/debootstrap/
	// i lapiemy debootstrap przez wrapper ktory ustawia DEBOOTSTRAP_DIR.
	if t.Binary == "debootstrap" {
		if err := m.installDebootstrapData(extractDir); err != nil {
			return fmt.Errorf("debootstrap data: %w", err)
		}
	}

	if copied == 0 {
		return fmt.Errorf("nie skopiowano zadnych binarek dla %s -- sprawdz nazwy pakietow apt", t.Binary)
	}

	// Weryfikacja: czy docelowa binarka jest teraz dostepna?
	if _, err := os.Stat(filepath.Join(m.binDir, t.Binary)); err != nil {
		return fmt.Errorf("binarka %q nie znaleziona w toolchain-bin/ po rozpakowaniu -- "+
			"sprawdz czy pakiet %v rzeczywiscie ja dostarcza", t.Binary, t.AptPackages)
	}

	return nil
}

// installDebootstrapData kopiuje /usr/share/debootstrap/ do katalogu obok
// binDir i tworzy wrapper-skrypt "debootstrap" ktory ustawia DEBOOTSTRAP_DIR
// zanim uruchomi prawdziwy skrypt. Bez tego debootstrap nie znajdzie skryptow
// suite (bookworm, trixie itp.) i pada z "E: Unknown suite".
func (m *Manager) installDebootstrapData(extractDir string) error {
	shareDir := filepath.Join(m.WorkDir, "toolchain-share", "debootstrap")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		return err
	}

	srcShare := filepath.Join(extractDir, "usr", "share", "debootstrap")
	if _, err := os.Stat(srcShare); err != nil {
		return fmt.Errorf("brak %s w rozpakowanym debootstrap", srcShare)
	}

	// Kopiuj rekurencyjnie
	copyCmd := exec.Command("cp", "-a", srcShare+"/.", shareDir)
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("kopiowanie debootstrap share: %w", err)
	}

	// Skrypt oryginalny (skopiowany do binDir przez copyBinaries) zastep
	// wrapperem ktory ustawia DEBOOTSTRAP_DIR przed wywolaniem oryginalu.
	origPath := filepath.Join(m.binDir, "debootstrap")
	realPath := filepath.Join(m.binDir, "debootstrap.real")

	// Przemianuj oryginalny skrypt na .real
	if err := os.Rename(origPath, realPath); err != nil {
		return fmt.Errorf("rename debootstrap -> debootstrap.real: %w", err)
	}

	wrapper := fmt.Sprintf(`#!/bin/sh
# Wrapper debootstrap wygenerowany przez hackeros-builder toolchain.
# Ustawia DEBOOTSTRAP_DIR tak by debootstrap znalazl skrypty suite
# (scripts/bookworm, scripts/trixie itp.) rozpakowane do katalogu roboczego
# buildu, NIE szukal ich w /usr/share/debootstrap (ktory moze nie istniec
# jesli debootstrap nie jest zainstalowany na hoscie).
export DEBOOTSTRAP_DIR=%q
exec %q "$@"
`, shareDir, realPath)

	if err := os.WriteFile(origPath, []byte(wrapper), 0o755); err != nil {
		return fmt.Errorf("zapis wrappera debootstrap: %w", err)
	}

	return nil
}

// copyBinaries kopiuje wszystkie wykonywalne pliki z srcDir do dstDir.
// Zwraca liczbe skopiowanych plikow.
func copyBinaries(srcDir, dstDir string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil // katalog nie istnieje w tym pakiecie -- normalny przypadek
	}
	if err != nil {
		return 0, err
	}

	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Kopiuj tylko pliki z bitem wykonywalnym
		if info.Mode()&0o111 == 0 {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			return n, fmt.Errorf("odczyt %s: %w", src, err)
		}
		if err := os.WriteFile(dst, data, info.Mode()); err != nil {
			return n, fmt.Errorf("zapis %s: %w", dst, err)
		}
		n++
	}
	return n, nil
}
