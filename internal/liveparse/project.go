package liveparse

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project to w pelni zinterpretowana struktura projektu live-build/hackeros-builder.
type Project struct {
	RootDir string // katalog glowny projektu (zawiera "config/")

	// Packages to spljaszczona, deduplikowana lista nazw pakietow apt
	// zebrana ze wszystkich plikow config/package-lists/*.list.chroot.
	Packages []string

	// Hooks to lista skryptow do wykonania wewnatrz chroot, w porzadku
	// alfabetycznym nazwy pliku (tak jak live-build sortuje hooks/normal/).
	Hooks []HookScript

	// IncludesChroot to sciezka do config/includes.chroot (lub "" jesli
	// katalog nie istnieje) -- cala jego zawartosc jest kopiowana 1:1
	// do korzenia rootfs PO instalacji pakietow, PRZED hooks.
	IncludesChroot string

	// ExtraSources to dodatkowe linie sources.list z config/archives/*.list.chroot.
	ExtraSources []string

	// ExtraKeys to sciezki do plikow kluczy GPG z config/archives/*.key.chroot,
	// ktore trzeba zaimportowac przed apt-get update jesli ExtraSources
	// odwoluje sie do repo spoza domyslnych kluczy Debiana.
	ExtraKeys []string
}

// HookScript to pojedynczy skrypt hook.chroot do wykonania wewnatrz chroot.
type HookScript struct {
	Name string // nazwa pliku, np. "0100-install-extra-tools.hook.chroot"
	Path string // pelna sciezka na dysku hosta
}

// Parse interpretuje projekt w danym katalogu glownym (root). Zwraca blad
// jesli "config/" nie istnieje -- to jest wymagany katalog kazdego projektu
// live-build/hackeros-builder.
func Parse(root string) (*Project, error) {
	configDir := filepath.Join(root, "config")
	if info, err := os.Stat(configDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf(
			"katalog %q nie istnieje -- to nie jest poprawny projekt "+
				"live-build/hackeros-builder (oczekiwano podkatalogu 'config/')",
			configDir)
	}

	p := &Project{RootDir: root}

	if err := p.parsePackageLists(configDir); err != nil {
		return nil, err
	}
	if err := p.parseHooks(configDir); err != nil {
		return nil, err
	}
	p.parseIncludesChroot(configDir)
	if err := p.parseArchives(configDir); err != nil {
		return nil, err
	}

	return p, nil
}

// parsePackageLists czyta wszystkie pliki config/package-lists/*.list.chroot
// i sklada deduplikowana, sortowana liste nazw pakietow.
//
// Format pliku .list.chroot (jak w live-build): jedna nazwa pakietu na
// linie, '#' zaczyna komentarz, puste linie ignorowane.
func (p *Project) parsePackageLists(configDir string) error {
	dir := filepath.Join(configDir, "package-lists")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil // brak package-lists jest dopuszczalne (rootfs minimalny)
	}
	if err != nil {
		return fmt.Errorf("nie mozna odczytac %s: %w", dir, err)
	}

	seen := make(map[string]bool)
	var packages []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".list.chroot") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		names, err := readPackageListFile(path)
		if err != nil {
			return fmt.Errorf("blad parsowania %s: %w", path, err)
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				packages = append(packages, n)
			}
		}
	}

	sort.Strings(packages)
	p.Packages = packages
	return nil
}

func readPackageListFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// live-build pozwala na wiele pakietow w jednej linii rozdzielonych
		// spacja -- wspieramy to dla zgodnosci, choc konwencja to 1/linie.
		for _, field := range strings.Fields(line) {
			names = append(names, field)
		}
	}
	return names, nil
}

// parseHooks zbiera skrypty z config/hooks/normal/*.hook.chroot, sortowane
// alfabetycznie po nazwie pliku -- live-build wykonuje je w tym porzadku,
// stad konwencja numerowania prefiksow (0100-..., 0200-...).
func (p *Project) parseHooks(configDir string) error {
	dir := filepath.Join(configDir, "hooks", "normal")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("nie mozna odczytac %s: %w", dir, err)
	}

	var hooks []HookScript
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".hook.chroot") {
			continue
		}
		hooks = append(hooks, HookScript{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
		})
	}

	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Name < hooks[j].Name })
	p.Hooks = hooks
	return nil
}

// parseIncludesChroot ustawia sciezke do config/includes.chroot jesli istnieje.
// Sama kopia plikow odbywa sie w pakiecie rootfs (BuildRootfs), nie tutaj --
// ten pakiet tylko interpretuje strukture, nie wykonuje I/O na docelowym rootfs.
func (p *Project) parseIncludesChroot(configDir string) {
	dir := filepath.Join(configDir, "includes.chroot")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		p.IncludesChroot = dir
	}
}

// parseArchives czyta config/archives/*.list.chroot (dodatkowe linie
// sources.list) i config/archives/*.key.chroot (klucze GPG do zaimportowania).
func (p *Project) parseArchives(configDir string) error {
	dir := filepath.Join(configDir, "archives")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("nie mozna odczytac %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		switch {
		case strings.HasSuffix(e.Name(), ".list.chroot"):
			lines, err := readLines(path)
			if err != nil {
				return fmt.Errorf("blad odczytu %s: %w", path, err)
			}
			p.ExtraSources = append(p.ExtraSources, lines...)
		case strings.HasSuffix(e.Name(), ".key.chroot"):
			p.ExtraKeys = append(p.ExtraKeys, path)
		}
	}
	return nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// Summary zwraca krotki, czytelny opis projektu do wyswietlenia w CLI
// przed rozpoczeciem budowania (transparentnosc -- uzytkownik widzi co
// zostanie wykonane zanim potrwa to dlugo).
func (p *Project) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pakietow do instalacji:  %d\n", len(p.Packages))
	fmt.Fprintf(&b, "Hookow do wykonania:     %d\n", len(p.Hooks))
	if p.IncludesChroot != "" {
		fmt.Fprintf(&b, "includes.chroot:         %s\n", p.IncludesChroot)
	} else {
		fmt.Fprintf(&b, "includes.chroot:         (brak)\n")
	}
	fmt.Fprintf(&b, "Dodatkowych zrodel apt:  %d\n", len(p.ExtraSources))
	fmt.Fprintf(&b, "Dodatkowych kluczy GPG:  %d\n", len(p.ExtraKeys))
	return b.String()
}
