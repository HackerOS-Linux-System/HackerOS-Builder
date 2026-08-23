package liveparse

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// Obejmuje hooks/normal/ + hooks/live/ (patrz parseHooks). NIE obejmuje
	// hooks/installer/ -- te sa osobno w InstallerHooks (patrz nizej),
	// dzialaja na INNYM rootfs (kopia ISO-only) i w INNYM momencie buildu.
	Hooks []HookScript

	// InstallerHooks to lista skryptow z config/hooks/installer/, wykonywana
	// WYLACZNIE przez "build iso" (isobuild), PO wstrzyknieciu instalatora
	// (Calamares) do kopii rootfs uzywanej do budowy ISO -- i TYLKO gdy
	// instalator jest wlaczony ([project] -> installer != none). Sluzy do
	// dostosowywania SAMEGO instalatora (np. wlasny branding.desc,
	// dodatkowe moduly Calamares, wlasny welcome.png/logo.png) -- w
	// odroznieniu od Hooks (ktore modyfikuja system DOCELOWY), te hooki
	// modyfikuja srodowisko INSTALATORA/live-medium. Ten sam format pliku
	// (*.hook.chroot, dowolny jezyk przez shebang) i ten sam mechanizm
	// wykonania (sandbox.ExecHook) co Hooks -- patrz isobuild.runInstallerHooks.
	InstallerHooks []HookScript

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

	// Interpreter to nazwa binarki interpretera wykryta z linii shebang
	// (pierwsza linia pliku, "#!/usr/bin/..."), np. "python3", "ruby",
	// "lua5.4", "sh". Pusty string jesli plik nie ma shebanga (wtedy
	// wykonanie polega na tym, ze plik jest binarka ELF z bitem +x, albo
	// zakonczy sie bledem "exec format error" -- oba te scenariusze sa
	// jednoznacznie zdiagnozowane w komunikacie bledu wywolujacego, patrz
	// rootfs.Builder.runHooks / isobuild.runInstallerHooks).
	Interpreter string
}

// hookInterpreterRe wyciaga nazwe binarki interpretera z linii shebang.
// Obsluguje oba popularne warianty:
//
//	#!/usr/bin/env python3        -> "python3"
//	#!/usr/bin/python3            -> "python3"
//	#!/bin/bash                   -> "bash"
//
// Zamierzenie: dowolny jezyk dziala "za darmo" przez shebang -- ta funkcja
// tylko WYKRYWA co zostalo zadeklarowane, zeby CLI mogl (a) ladnie
// zalogowac w jakim jezyku jest hook, (b) doinstalowac brakujacy
// interpreter PRZED probą wykonania (patrz internal/rootfs, mapa
// hookInterpreterPackages), zamiast dawac kryptyczny "exec format error"
// albo "no such file or directory" z samego chroot/exec.
var hookInterpreterRe = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+)?(\S*/)?([A-Za-z0-9_.+-]+)`)

// detectHookInterpreter czyta pierwsza linie pliku i zwraca nazwe binarki
// interpretera (np. "python3", "ruby", "bash"), albo "" jesli plik nie ma
// linii shebang. Blad odczytu jest CELOWO ignorowany (zwraca "") -- brak
// mozliwosci wykrycia jezyka nie powinien wywrocic calego parsowania
// projektu, tylko pozbawic ten JEDEN hook ladnej etykiety jezyka w logach;
// faktyczny blad (plik nieczytelny/nieistniejacy) i tak wyjdzie pozniej,
// przy faktycznej probie skopiowania/wykonania hooka.
func detectHookInterpreter(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4096)
	if !scanner.Scan() {
		return ""
	}
	firstLine := scanner.Text()
	if !strings.HasPrefix(firstLine, "#!") {
		return ""
	}
	m := hookInterpreterRe.FindStringSubmatch(firstLine)
	if m == nil {
		return ""
	}
	return m[2]
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
	if err := p.parseInstallerHooks(configDir); err != nil {
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

	// DOPISANIE BACKENDU "live-boot-initramfs-tools", GDY PROJEKT PROSI O
	// "live-boot" -- PRZED filtrowaniem wykluczen (zeby projekt nadal mogl
	// jawnie wpisac "-live-boot-initramfs-tools" i swiadomie to wylaczyc).
	//
	// KLUCZOWA ROZNICA wzgledem live-build: live-build, gdy skonfigurowane
	// z LB_INITRAMFS="live-boot" (patrz np. helpers/gaming/common w repo
	// HackerOS), SAMO doinstalowuje wlasciwy pakiet "backendu" generowania
	// initrd (live-boot-initramfs-tools dla initramfs-tools, live-boot-dracut
	// dla dracut) -- to wewnetrzna, automatyczna logika lb_binary_* i wiele
	// istniejacych list pakietow (przygotowanych z mysla o live-build) polega
	// na tym MILCZACO, wpisujac tylko "live-boot" bez backendu.
	//
	// hackeros-builder NIE ma takiej magii -- instaluje DOSLOWNIE to, co jest
	// na liscie pakietow, nic wiecej (patrz komentarze w internal/rootfs/
	// builder.go przy kroku "instalacja jadra" w build-hackeros-atomic).
	// Samo "live-boot" NIE zawiera hooka dla initramfs-tools (pakiet
	// "live-boot" dostarcza TYLKO skrypty /lib/live/boot i konfiguracje --
	// wlasciwy hook /usr/share/initramfs-tools/hooks/live, ktory update-
	// initramfs faktycznie wbudowuje do initrd.img, jest w OSOBNYM pakiecie
	// "live-boot-initramfs-tools"; patrz oficjalny opis pakietu "live-boot"
	// na packages.debian.org: "In addition to live-boot, a backend for the
	// initrd generation is required, such as live-boot-initramfs-tools.").
	//
	// Bez tego pakietu, update-initramfs generuje CALKOWICIE ZWYKLY initrd
	// (bez zadnej wiedzy o "boot=live"/squashfs) -- initrd.img trafia na
	// ISO, grub przekazuje "boot=live" (patrz internal/isobuild/builder.go,
	// writeGrubConfig) ale initramfs-tools nie ma jak tego zinterpretowac,
	// nie znajduje ZADNEGO korzenia (nie ma tez "root=" w cmdline -- to
	// live-boot mialo je wyznaczyc dynamicznie), wyczerpuje wszystkie
	// fallbacki i konczy dzialanie -- kernel widzi PID 1 (skrypt /init z
	// initramfs) po prostu KONCZACY SIE (exit 1), co objawia sie DOKLADNIE
	// jako "Kernel panic - not syncing: Attempted to kill init!
	// exitcode=0x00000100" -- panika NIE przy montowaniu (to bylby inny,
	// bardziej oczywisty komunikat "VFS: Unable to mount root fs"), tylko
	// przy "smierci" PID 1, bo /init w initramfs technicznie "dziala
	// poprawnie", po prostu nie ma co dalej zrobic bez skryptow live-boot.
	//
	// Wykrywamy TYLKO "live-boot" (nie live-config -- live-config samo w
	// sobie nie wplywa na initrd, dziala juz PO przelaczeniu na docelowy
	// root) i dopisujemy backend TYLKO jesli srodowisko builda faktycznie
	// generuje initrd przez initramfs-tools (co jest jedynym wspieranym
	// backendem w hackeros-builder -- brak jakiejkolwiek sciezki dracut w
	// tym repo), wiec zawsze "live-boot-initramfs-tools", nigdy
	// "live-boot-dracut".
	const liveBootBackend = "live-boot-initramfs-tools"
	if seen["live-boot"] && !seen[liveBootBackend] {
		seen[liveBootBackend] = true
		packages = append(packages, liveBootBackend)
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
	hooks, err := readHookDirs(configDir, "normal", "live")
	if err != nil {
		return err
	}
	p.Hooks = hooks
	return nil
}

// parseInstallerHooks zbiera skrypty z config/hooks/installer/ -- osobna
// kategoria od Hooks (normal/live), patrz komentarz przy polu
// Project.InstallerHooks. Nie jest wolane przez Parse() (build cloud nie
// potrzebuje tych hookow -- dzialaja wylacznie w build iso), tylko
// osobno przez ParseInstallerHooks ponizej.
func (p *Project) parseInstallerHooks(configDir string) error {
	hooks, err := readHookDirs(configDir, "installer")
	if err != nil {
		return err
	}
	p.InstallerHooks = hooks
	return nil
}

// readHookDirs czyta config/hooks/<subdir1>/*.hook.chroot dla kazdego
// podanego subdir (w podanej kolejnosci), sortujac PLIKI W OBRebie
// KAZDEGO katalogu alfabetycznie (konwencja live-build: prefiksy
// numeryczne typu "0100-...", "0200-..." decyduja o kolejnosci wykonania
// wewnatrz jednego katalogu). Kazdy znaleziony plik ma wykrywany
// interpreter z shebang (patrz detectHookInterpreter) -- niezaleznie od
// kategorii, dowolny jezyk (shell, python, ruby, lua, perl, ...) jest
// wspierany jednakowo we wszystkich trzech katalogach (normal/live/installer).
func readHookDirs(configDir string, subdirs ...string) ([]HookScript, error) {
	var hooks []HookScript
	for _, subdir := range subdirs {
		dir := filepath.Join(configDir, "hooks", subdir)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("nie mozna odczytac %s: %w", dir, err)
		}

		var subHooks []HookScript
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".hook.chroot") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			subHooks = append(subHooks, HookScript{
				Name:        e.Name(),
				Path:        path,
				Interpreter: detectHookInterpreter(path),
			})
		}
		sort.Slice(subHooks, func(i, j int) bool { return subHooks[i].Name < subHooks[j].Name })
		hooks = append(hooks, subHooks...)
	}
	return hooks, nil
}

// ParseInstallerHooks czyta config/hooks/installer/*.hook.chroot
// NIEZALEZNIE od reszty struktury projektu (Parse) -- uzywane przez
// "build iso", ktore MOZE dzialac bez pelnej struktury package-lists/
// hooks-normal/includes.chroot na dysku lokalnym (np. gdy obraz OCI zostal
// juz zbudowany gdzie indziej i "build iso" tylko go sciaga z registry).
// Zwraca pusta liste (bez bledu) jesli root, config/ lub
// config/hooks/installer/ nie istnieje -- brak tych hookow jest zupelnie
// normalny (wiekszosc projektow ich nie uzywa), nie jest to blad.
func ParseInstallerHooks(root string) ([]HookScript, error) {
	configDir := filepath.Join(root, "config")
	if info, err := os.Stat(configDir); err != nil || !info.IsDir() {
		return nil, nil
	}
	return readHookDirs(configDir, "installer")
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
	fmt.Fprintf(&b, "Hookow do wykonania:     %d%s\n", len(p.Hooks), languageBreakdown(p.Hooks))
	if len(p.InstallerHooks) > 0 {
		fmt.Fprintf(&b, "Hookow instalatora:      %d%s (config/hooks/installer/)\n",
			len(p.InstallerHooks), languageBreakdown(p.InstallerHooks))
	}
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

// languageBreakdown zwraca krotkie podsumowanie jezykow wykrytych w liscie
// hookow w nawiasach, np. " (sh: 2, python3: 1)" -- albo pusty string gdy
// lista jest pusta. Uzywane przez Summary() zeby uzytkownik od razu widzial
// w jakich jezykach sa jego hooki, bez zagladania do kazdego pliku.
func languageBreakdown(hooks []HookScript) string {
	if len(hooks) == 0 {
		return ""
	}
	counts := make(map[string]int)
	var order []string
	for _, h := range hooks {
		lang := h.Interpreter
		if lang == "" {
			lang = "brak shebang"
		}
		if counts[lang] == 0 {
			order = append(order, lang)
		}
		counts[lang]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, lang := range order {
		parts = append(parts, fmt.Sprintf("%s: %d", lang, counts[lang]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
