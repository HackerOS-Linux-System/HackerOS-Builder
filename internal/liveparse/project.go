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
	// katalog nie istnieje) -- zachowane dla zgodnosci wstecz, semantyka
	// TAKA SAMA jak IncludesChrootAfterPackages (kopiowane PO instalacji
	// pakietow, PRZED hooks). Jesli projekt ma OBA katalogi (legacy
	// "includes.chroot" i nowy "includes.chroot_after_packages"), oba sa
	// kopiowane -- najpierw legacy, potem after_packages.
	IncludesChroot string

	// IncludesChrootBeforePackages to sciezka do
	// config/includes.chroot_before_packages (lub "" jesli katalog nie
	// istnieje) -- jej zawartosc jest kopiowana do korzenia rootfs zaraz PO
	// debootstrap/preseed, ale PRZED dodatkowymi zrodlami apt i instalacja
	// jakichkolwiek pakietow. Uzyteczne np. gdy trzeba podmienic
	// /etc/apt/apt.conf.d/* albo /etc/hosts zanim apt-get w ogole ruszy.
	IncludesChrootBeforePackages string

	// IncludesChrootAfterPackages to sciezka do
	// config/includes.chroot_after_packages -- kopiowana PO instalacji
	// pakietow (rowniez po MAC/AppArmor/SELinux), PRZED hooks -- dokladnie
	// tam gdzie do tej pory dzialalo samo "includes.chroot" (patrz wyzej).
	IncludesChrootAfterPackages string

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
//
// WYKLUCZENIA (konwencja live-build): linia zaczynajaca sie od '-' (np.
// "-firefox-esr") NIE jest nazwa pakietu do zainstalowania -- to znacznik
// "ten pakiet NIE ma trafic na finalna liste, nawet jesli zostal dodany
// przez inny plik .list.chroot". Sluzy do wylaczania pakietow dodanych
// przez wspoldzielone/dziedziczone listy (np. helpers/cybersecurity ma
// package-lists z "firefox-esr", a config/package-lists/remove.list.chroot
// nadpisuje to wpisem "-firefox-esr"). Live-build filtruje takie wpisy
// PRZED wywolaniem apt-get -- apt-get NIGDY nie widzi surowego "-nazwa"
// jako argumentu (potraktowalby to jako nieznana opcje wiersza polecen i
// zakonczylby sie bledem, np. "Command line option 'i' [from -firefox-esr]
// is not understood").
//
// Implementacja: dwuprzebiegowa. Najpierw zbieramy WSZYSTKIE wpisy ze
// wszystkich plikow (pozytywne nazwy ORAZ wykluczenia z prefiksem '-'),
// zachowujac kolejnosc napotkania dla nazw pozytywnych. Na koniec z listy
// pozytywnych nazw usuwamy kazda, ktora pojawila sie w ktorymkolwiek pliku
// jako wykluczenie -- niezaleznie od tego, czy wykluczenie wystapilo w tym
// samym pliku, wczesniejszym czy pozniejszym (semantyka zbioru, nie
// kolejnosci wykonania jak w apt-get install/remove w jednym poleceniu).
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
	excluded := make(map[string]bool)
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
			if strings.HasPrefix(n, "-") {
				excludeName := strings.TrimPrefix(n, "-")
				if excludeName != "" {
					excluded[excludeName] = true
				}
				continue
			}
			if !seen[n] {
				seen[n] = true
				packages = append(packages, n)
			}
		}
	}

	// Usuwamy z finalnej listy wszystko, co zostalo oznaczone jako
	// wykluczone w KTORYMKOLWIEK pliku -- patrz komentarz funkcji.
	if len(excluded) > 0 {
		filtered := packages[:0]
		for _, n := range packages {
			if !excluded[n] {
				filtered = append(filtered, n)
			}
		}
		packages = filtered
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
// parseHooks czyta hooki z DWOCH katalogow, zgodnie z konwencja live-build:
//   - hooks/normal/  -- uruchamiane zawsze (odpowiednik "lb chroot hooks")
//   - hooks/live/    -- uruchamiane tylko dla systemow "live" (odpowiednik
//     hookow live-build wykonywanych na etapie live-image);
//     HackerOS jest zawsze systemem live (live-boot/live-config
//     zainstalowane w kazdej edycji, dostarczany jako bootowalne ISO), wiec
//     hooks/live/ ma tu takie samo prawo bytu jak hooks/normal/.
//
// WCZESNIEJ ta funkcja czytala WYLACZNIE hooks/normal/ -- hooki polozone w
// hooks/live/ (np. helpers/cybersecurity-default/hooks/live/build-red-team-tools.hook.chroot,
// budujacy dziesiatki narzedzi red-teamowych ze zrodel) byly CICHO
// pomijane: config.hk wygladalo na poprawne, build konczyl sie sukcesem,
// ale znaczna czesc zamierzonej zawartosci obrazu nigdy nie powstawala,
// bez zadnego bledu czy ostrzezenia.
func (p *Project) parseHooks(configDir string) error {
	var hooks []HookScript
	for _, subdir := range []string{"normal", "live"} {
		dir := filepath.Join(configDir, "hooks", subdir)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("nie mozna odczytac %s: %w", dir, err)
		}

		var subHooks []HookScript
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".hook.chroot") {
				continue
			}
			subHooks = append(subHooks, HookScript{
				Name: e.Name(),
				Path: filepath.Join(dir, e.Name()),
			})
		}
		sort.Slice(subHooks, func(i, j int) bool { return subHooks[i].Name < subHooks[j].Name })
		hooks = append(hooks, subHooks...)
	}

	p.Hooks = hooks
	return nil
}

// parseIncludesChroot ustawia sciezki do config/includes.chroot,
// config/includes.chroot_before_packages i config/includes.chroot_after_packages
// jesli istnieja. Sama kopia plikow odbywa sie w pakiecie rootfs (BuildRootfs),
// nie tutaj -- ten pakiet tylko interpretuje strukture, nie wykonuje I/O na
// docelowym rootfs.
func (p *Project) parseIncludesChroot(configDir string) {
	setIfDir := func(name string) string {
		dir := filepath.Join(configDir, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		return ""
	}
	p.IncludesChroot = setIfDir("includes.chroot")
	p.IncludesChrootBeforePackages = setIfDir("includes.chroot_before_packages")
	p.IncludesChrootAfterPackages = setIfDir("includes.chroot_after_packages")
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
	if p.IncludesChrootBeforePackages != "" {
		fmt.Fprintf(&b, "includes.chroot_before_packages: %s\n", p.IncludesChrootBeforePackages)
	}
	if p.IncludesChroot != "" {
		fmt.Fprintf(&b, "includes.chroot:         %s\n", p.IncludesChroot)
	}
	if p.IncludesChrootAfterPackages != "" {
		fmt.Fprintf(&b, "includes.chroot_after_packages: %s\n", p.IncludesChrootAfterPackages)
	}
	if p.IncludesChroot == "" && p.IncludesChrootBeforePackages == "" && p.IncludesChrootAfterPackages == "" {
		fmt.Fprintf(&b, "includes.chroot:         (brak)\n")
	}
	fmt.Fprintf(&b, "Dodatkowych zrodel apt:  %d\n", len(p.ExtraSources))
	fmt.Fprintf(&b, "Dodatkowych kluczy GPG:  %d\n", len(p.ExtraKeys))
	return b.String()
}
