package config

import (
	"fmt"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hk"
)

// AccountType okresla czy konto docelowe w registry jest kontem uzytkownika
// czy organizacji (wplywa na sciezke obrazu OCI).
type AccountType string

const (
	AccountTypeUser         AccountType = "user"
	AccountTypeOrganisation AccountType = "organisation"
)

// ProjectType opisuje tryb pracy buildera dla danego projektu.
type ProjectType string

const (
	// ProjectTypeDefault / brak:
	// Pelny atomowy build (debootstrap + OCI push + hammer).
	// To jest glowny tryb hackeros-builder, aktywnie rozwijany.
	ProjectTypeDefault ProjectType = "default"

	// ProjectTypeCybersecurity:
	// TO SAMO co ProjectTypeDefault (pelny atomowy build: debootstrap +
	// OCI push + hammer) plus DODATKOWY zestaw pakietow cybersecurity/
	// pentest (nmap, wireshark, aircrack-ng, hydra, john, hashcat,
	// sqlmap, radare2, binwalk, ... -- patrz internal/rootfs,
	// cybersecurityPackages) instalowany do rootfs PRZED hookami
	// uzytkownika, tak zeby wlasne hooki mogly juz na nich polegac lub je
	// nadpisac. Odpowiada edycji "Cybersecurity Edition" znanej z
	// glownego repozytorium HackerOS (nastawionej na Red Team).
	//
	// Do wersji 0.6.0 ta wartosc byla WYLACZNIE aliasem "default" --
	// parsowana poprawnie, ale nie wplywala na build w zaden sposob (ani
	// jeden dodatkowy pakiet, ani inny installer). Od tej wersji jest to
	// realny, odrebny tryb pracy.
	ProjectTypeCybersecurity ProjectType = "cybersecurity"

	// ProjectTypeNormal / ProjectTypeOfficial:
	// Zwykla nakladka na live-build -- bez hammer, bez OCI, bez atomowosci.
	// Wymaga zainstalowanego live-build na hoscie.
	// Builder deleguje caly build do "lb build" zamiast robic to samemu.
	ProjectTypeNormal   ProjectType = "normal"
	ProjectTypeOfficial ProjectType = "official"

	// ProjectTypeIndependent:
	// Alternatywa dla live-build dla projektow typu normal/official --
	// nie atomowe, ale bez zewnetrznej zaleznosci od live-build.
	// Uzywa wewnetrznego pipeline'u hackeros-builder (debootstrap + squashfs
	// + iso) ale BEZ push OCI i BEZ hammer.
	ProjectTypeIndependent ProjectType = "independent"

	// ProjectTypeContainer:
	// Buduje ZWYKLY kontener roboczy (OCI, kompatybilny z podman/docker)
	// do codziennej pracy -- NIE obraz atomowy/bootc. Rootfs jest budowany
	// identycznie jak dla "default" (debootstrap + package-lists + hooks +
	// includes.chroot), ale BEZ wstrzykiwania hammer/`/etc/hammer/oci.hk`
	// (kontener roboczy nie jest zarzadzany atomowo) i BEZ usuwania
	// apt/apt-get (przeciwnie -- w zwyklym kontenerze do pracy apt ma
	// zostac, tak jak w kazdym innym obrazie bazowym Debiana). Wynik to
	// gotowe archiwum wczytywalne przez `podman load`/`docker load` (patrz
	// "hackeros-builder build container" oraz internal/ociimage/local.go).
	ProjectTypeContainer ProjectType = "container"

	// ProjectTypeContainerized:
	// Jak ProjectTypeContainer (zwykly kontener roboczy, bez hammer/
	// atomowosci -- ta sama budowa rootfs, ten sam "hackeros-builder build
	// container"), ale DODATKOWO wbudowuje Isolator
	// (https://github.com/HackerOS-Linux-System/Isolator) do /usr/bin/ --
	// podman-owy menedzer pakietow HackerOS, ktory instaluje pakiety z
	// dowolnej wspieranej dystrybucji do izolowanych/wspoldzielonych
	// kontenerow, sam zarzadzajac GUI/GPU/audio/D-Bus. Kontener startuje
	// gotowy do `isolator install <pakiet>` bez zadnej dodatkowej
	// konfiguracji -- ten sam pomysl co "Isolator Builder" (osobne
	// narzedzie w repo Isolatora: minimalny obraz bazowy + wbudowany
	// Isolator, reszta instaluje sie PoZNIEJ jako kontenery zarzadzane
	// przez Isolator), zaimplementowany tutaj bez zaleznosci od
	// posiadania samego binarnego Isolatora na hoscie budujacym --
	// najnowsza wersja jest pobierana z GitHub Releases Isolatora
	// (archiwum "isolator.tar.gz") i wypakowywana bezposrednio do
	// rootfs, dokladnie tak samo jak hammer jest juz dzis pobierany dla
	// ProjectTypeDefault (patrz internal/download).
	//
	// Isolator jest dzis napisany w Go, ale ten kod NIE zaklada niczego o
	// Go -- traktuje wydanie Isolatora WYLACZNIE jako "archiwum z
	// gotowymi binarkami do rozpakowania", wiec bedzie dzialac identycznie
	// nawet gdy Isolator zostanie kiedys przepisany w innym jezyku, o ile
	// logika wydawania (GitHub Releases, isolator.tar.gz, gotowe binarki
	// w archiwum) pozostanie taka sama. Patrz
	// internal/download/isolator.go, DownloadAndEmbedIsolator.
	ProjectTypeContainerized ProjectType = "containerized"
)

// InstallerType opisuje jaki instalator jest dolaczony do obrazu ISO.
type InstallerType string

const (
	// InstallerDefault / brak:
	// Wlasny instalator hackeros-builder (Calamares, uruchamiany od razu
	// przy starcie ISO -- bez pośredniego pulpitu live, inspirowany
	// trybem "Install" z Kubuntu: wybierasz "Install" i jesteś od razu
	// w instalatorze, nie w srodowisku live z ikona na pulpicie).
	InstallerDefault InstallerType = "default"

	// InstallerCybersecurity:
	// TO SAMO co InstallerDefault (Calamares uruchamiany od razu przy
	// starcie ISO) plus: (1) branding Calamares zmieniony na "HackerOS
	// Cybersecurity Edition" (inna paleta -- czerwony akcent zamiast
	// niebieskiego, patrz internal/isobuild/installer.go), (2) dodatkowy,
	// mniejszy zestaw narzedzi sieciowych/diagnostycznych zainstalowany
	// juz w SAMYM srodowisku live/instalatora (nie w systemie docelowym --
	// to robi [project] -> type=cybersecurity, patrz ProjectTypeCybersecurity),
	// przydatny np. do sprawdzenia sieci przed instalacja.
	//
	// Do wersji 0.6.0 ta wartosc byla WYLACZNIE aliasem "default" --
	// parsowana poprawnie, ale wizualnie i funkcjonalnie nieodrozniona od
	// zwyklego instalatora. Od tej wersji jest to realny, odrebny wariant.
	InstallerCybersecurity InstallerType = "cybersecurity"

	// InstallerNone:
	// Brak instalatora -- builder nie wstrzykuje niczego. Uzytkownik
	// (deweloper) sam dba o instalator przez hooks/includes.chroot.
	InstallerNone InstallerType = "none"
)

// MACSystem opisuje system kontroli dostepu obowiazkowego (MAC) uzyty
// w budowanym obrazie.
type MACSystem string

const (
	// MACAppArmor: domyslne dla Debiana -- AppArmor.
	MACAppArmor MACSystem = "apparmor"

	// MACSELinux: SELinux zamiast AppArmor (wymaga dodatkowych pakietow
	// i polityk -- builder automatycznie dobiera wlasciwe pakiety).
	MACSELinux MACSystem = "selinux"
)

// Config to w pelni zwalidowana zawartosc config/config.hk.
type Config struct {
	AccountType AccountType
	AccountName string
	Token       string
	Release     string

	// Mirror to adres mirrora Debiana przekazywany do debootstrap.
	// Domyslnie (puste w config.hk) "http://deb.debian.org/debian" --
	// patrz DefaultMirror() ponizej. Konfigurowalne przez
	// [release] -> mirror, np. dla mirrorow lokalnych/firmowych (szybszy
	// build w sieci firmowej) albo mirrorow regionalnych.
	Mirror string

	// Arch to architektura docelowa przekazywana do debootstrap jako
	// --arch=<Arch> (i pozniej do grub-mkrescue/hooków ktore moga
	// potrzebowac architektury). Domyslnie (puste w config.hk) "amd64" --
	// patrz DefaultArch() ponizej. Konfigurowalne przez [release] -> arch,
	// np. "arm64" dla Raspberry Pi / serwerow ARM.
	Arch string

	// Project to zawartosc sekcji [project] -- wszystkie pola opcjonalne,
	// brak calej sekcji nie jest bledem (stosowane sa wartosci domyslne).
	Project ProjectConfig
}

// DefaultMirror to domyslny mirror Debiana uzywany gdy [release] -> mirror
// jest puste/nieustawione w config.hk.
const DefaultMirror = "http://deb.debian.org/debian"

// DefaultArch to domyslna architektura uzywana gdy [release] -> arch jest
// puste/nieustawione w config.hk.
const DefaultArch = "amd64"

// knownArches to architektury oficjalnie wspierane przez Debiana (patrz
// https://www.debian.org/ports/) dla ktorych debootstrap ma gotowe
// definicje -- lista uzywana WYLACZNIE do ostrzezenia (nie twardej
// walidacji, patrz IsKnownArch), zeby literowka w config.hk ("amd46")
// zostala zauwazona PRZED wielominutowym debootstrap zamiast dopiero gdy
// on sam zwroci kryptyczny blad.
var knownArches = map[string]bool{
	"amd64": true, "arm64": true, "armhf": true, "armel": true,
	"i386": true, "mips64el": true, "mipsel": true, "ppc64el": true,
	"riscv64": true, "s390x": true,
}

// IsKnownArch zwraca false gdy Arch (po zastosowaniu domyslnej wartosci)
// nie jest na liscie architektur oficjalnie wspieranych przez Debiana.
func (c *Config) IsKnownArch() bool {
	return knownArches[c.EffectiveArch()]
}

// EffectiveMirror zwraca Mirror, a jesli jest puste -- DefaultMirror.
func (c *Config) EffectiveMirror() string {
	if c.Mirror == "" {
		return DefaultMirror
	}
	return c.Mirror
}

// EffectiveArch zwraca Arch, a jesli jest puste -- DefaultArch.
func (c *Config) EffectiveArch() string {
	if c.Arch == "" {
		return DefaultArch
	}
	return c.Arch
}

// ProjectConfig to zawartosc sekcji [project] w config/config.hk.
type ProjectConfig struct {
	// Name to nazwa projektu uzywana jako nazwa obrazu OCI w registry:
	//   ghcr.io/<account.name>/<project.name>:<project.tag>
	// Jesli puste -- builder uzywa nazwy katalogu projektu.
	Name string

	// Tag to wersja/tag obrazu OCI (np. "latest", "1.0.0", "nightly").
	// Jesli puste -- builder uzywa "latest".
	Tag string

	// Type okresla tryb pracy buildera (patrz ProjectType*).
	// Wartosc domyslna (brak lub "default"):
	//   pelny atomowy build z OCI + hammer.
	Type ProjectType

	// Installer okresla instalator dolaczany do obrazu ISO (patrz InstallerType*).
	// Wartosc domyslna (brak lub "default"):
	//   wlasny instalator Calamares uruchamiany od razu przy starcie ISO.
	Installer InstallerType

	// MAC to system kontroli dostepu obowiazkowego (AppArmor lub SELinux).
	// Wartosc domyslna (brak lub selinux=false): AppArmor.
	// selinux=true: SELinux.
	MAC MACSystem

	// CybersecurityPackages nadpisuje WBUDOWANA liste pakietow cybersecurity
	// (patrz internal/rootfs, cybersecurityPackages) gdy niepuste. Ustawiane
	// przez [project] -> cybersecurity_packages (tablica stringow w
	// config.hk). Puste (nil) => uzyj wbudowanej listy domyslnej. Dziala
	// TYLKO gdy Type == ProjectTypeCybersecurity (tak jak wbudowana lista).
	CybersecurityPackages []string

	// Sign: gdy true, "build cloud" podpisuje wypchniety obraz OCI przez
	// cosign PO udanym push, uzywajac CosignKey jako klucza prywatnego.
	// Ustawiane przez [project] -> sign (bool).
	Sign bool

	// VerifySignature: gdy true, "build iso" weryfikuje podpis cosign
	// obrazu OCI PRZED pociagnieciem go z registry, uzywajac CosignKey jako
	// klucza publicznego. Ustawiane przez [project] -> verify_signature (bool).
	VerifySignature bool

	// CosignKey to sciezka do klucza cosign -- prywatnego gdy Sign=true
	// (build cloud), publicznego gdy VerifySignature=true (build iso).
	// Ustawiane przez [project] -> cosign_key.
	CosignKey string

	// IsolatorVersion to wersja Isolatora (np. "v0.3.0") wbudowywana do
	// rootfs gdy Type == ProjectTypeContainerized. Puste -- automatyczne
	// wykrycie najnowszej wersji (patrz download.LatestIsolatorVersion).
	// Ustawiane przez [project] -> isolator_version.
	IsolatorVersion string
}

// IsAtomicBuild zwraca true jesli projekt ma byc budowany jako pelny
// atomowy obraz OCI z hammer (domyslne zachowanie). Zwraca false
// dla typow normal/official/independent/container/containerized.
func (p *ProjectConfig) IsAtomicBuild() bool {
	switch p.Type {
	case ProjectTypeNormal, ProjectTypeOfficial, ProjectTypeIndependent,
		ProjectTypeContainer, ProjectTypeContainerized:
		return false
	default:
		// default, cybersecurity, "" -- atomowy
		return true
	}
}

// RequiresLiveBuild zwraca true jesli projekt wymaga zainstalowanego live-build
// na hoscie (typy: normal, official).
func (p *ProjectConfig) RequiresLiveBuild() bool {
	return p.Type == ProjectTypeNormal || p.Type == ProjectTypeOfficial
}

// UseBuiltinInstaller zwraca true jesli builder ma wstrzyknac wlasny
// instalator (Calamares) do ISO. Zwraca false dla InstallerNone.
func (p *ProjectConfig) UseBuiltinInstaller() bool {
	return p.Installer != InstallerNone
}

// IsCybersecurity zwraca true jesli [project] -> type = cybersecurity --
// rootfs otrzymuje wtedy dodatkowy zestaw pakietow cybersecurity/pentest
// (patrz internal/rootfs, cybersecurityPackages) OPROCZ zwyklego atomowego
// builda (Type nadal jest traktowany jak "default" przez IsAtomicBuild).
func (p *ProjectConfig) IsCybersecurity() bool {
	return p.Type == ProjectTypeCybersecurity
}

// IsContainerBuild zwraca true jesli [project] -> type = container --
// projekt ma byc zbudowany jako zwykly kontener roboczy (podman/docker),
// bez hammer/atomowosci. Patrz ProjectTypeContainer i buildflow.BuildContainer.
func (p *ProjectConfig) IsContainerBuild() bool {
	return p.Type == ProjectTypeContainer
}

// IsContainerizedIsolator zwraca true jesli [project] -> type = containerized
// -- kontener roboczy z wbudowanym Isolatorem (podman + isolator w
// /usr/bin/), patrz ProjectTypeContainerized.
func (p *ProjectConfig) IsContainerizedIsolator() bool {
	return p.Type == ProjectTypeContainerized
}

// UsesCybersecurityInstaller zwraca true jesli [project] -> installer = cybersecurity
// -- Calamares dostaje branding/motyw "Cybersecurity Edition" i dodatkowe
// narzedzia diagnostyczne w SAMYM srodowisku live/instalatora (patrz
// internal/isobuild/installer.go). Niezalezne od [project] -> type.
func (p *ProjectConfig) UsesCybersecurityInstaller() bool {
	return p.Installer == InstallerCybersecurity
}

// ImageTag zwraca tag obrazu OCI -- "latest" jesli nie ustawiony.
func (p *ProjectConfig) ImageTag() string {
	if p.Tag == "" {
		return "latest"
	}
	return p.Tag
}

// knownReleases to lista znanych wersji Debiana.
var knownReleases = map[string]bool{
	"bookworm": true,
	"trixie":   true,
	"forky":    true,
	"sid":      true,
	"unstable": true,
}

// Load wczytuje i parsuje config.hk z podanej sciezki.
//
// requireAuth: gdy true, [account] -> name oraz [auth] -> token musza miec
// NIEPUSTE wartosci (wymagane dla "build cloud"/"build iso"/"build all",
// ktore zawsze pushuja do registry). Gdy false, puste wartosci sa
// dozwolone -- uzywane przez "build container", gdzie konto w registry
// jest opcjonalne (patrz internal/buildflow/container.go).
func Load(path string, requireAuth bool) (*Config, error) {
	parsed, err := hk.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config.hk: %w", err)
	}

	if err := hk.ResolveInterpolations(parsed); err != nil {
		return nil, fmt.Errorf("config.hk: interpolacja: %w", err)
	}

	cfg := &Config{}

	accountType, err := getRequiredString(parsed, "account", "type")
	if err != nil {
		return nil, err
	}
	cfg.AccountType = AccountType(accountType)

	cfg.AccountName, err = getRequiredString(parsed, "account", "name")
	if err != nil {
		return nil, err
	}

	cfg.Token, err = getRequiredString(parsed, "auth", "token")
	if err != nil {
		return nil, err
	}

	cfg.Release, err = getRequiredString(parsed, "release", "name")
	if err != nil {
		return nil, err
	}

	// mirror/arch sa OPCJONALNE -- brak klucza nie jest bledem, po prostu
	// EffectiveMirror()/EffectiveArch() zwroca wartosc domyslna.
	if sec, secErr := parsed.Section("release"); secErr == nil {
		if val, ok := sec.Get("mirror"); ok {
			if s, err := val.AsString(); err == nil {
				cfg.Mirror = strings.TrimSpace(s)
			}
		}
		if val, ok := sec.Get("arch"); ok {
			if s, err := val.AsString(); err == nil {
				cfg.Arch = strings.TrimSpace(s)
			}
		}
	}

	if err := cfg.validate(requireAuth); err != nil {
		return nil, err
	}

	proj, err := loadProjectSection(parsed)
	if err != nil {
		return nil, err
	}
	cfg.Project = proj

	return cfg, nil
}

// loadProjectSection wczytuje opcjonalna sekcje [project].
// Brak sekcji lub poszczegolnych kluczy -> wartosci domyslne, brak bledu.
func loadProjectSection(parsed *hk.HkConfig) (ProjectConfig, error) {
	sec, err := parsed.Section("project")
	if err != nil {
		// Sekcja [project] nie istnieje -- w pelni dozwolone, stosuj domyslne.
		return defaultProjectConfig(), nil
	}

	p := defaultProjectConfig()

	if val, ok := sec.Get("name"); ok {
		if s, err := val.AsString(); err == nil {
			p.Name = strings.TrimSpace(s)
		}
	}

	if val, ok := sec.Get("tag"); ok {
		if s, err := val.AsString(); err == nil {
			p.Tag = strings.TrimSpace(s)
		}
	}

	if val, ok := sec.Get("type"); ok {
		if s, err := val.AsString(); err == nil {
			pt, err := parseProjectType(strings.TrimSpace(s))
			if err != nil {
				return ProjectConfig{}, fmt.Errorf("config.hk: [project] -> type: %w", err)
			}
			p.Type = pt
		}
	}

	if val, ok := sec.Get("installer"); ok {
		if s, err := val.AsString(); err == nil {
			it, err := parseInstallerType(strings.TrimSpace(s))
			if err != nil {
				return ProjectConfig{}, fmt.Errorf("config.hk: [project] -> installer: %w", err)
			}
			p.Installer = it
		}
	}

	// selinux => true/false (lub yes/no, 1/0) -- wszystko inne to AppArmor
	if val, ok := sec.Get("selinux"); ok {
		if s, err := val.AsString(); err == nil {
			if isTruthy(strings.TrimSpace(s)) {
				p.MAC = MACSELinux
			} else {
				p.MAC = MACAppArmor
			}
		}
	}

	// cybersecurity_packages => nadpisuje WBUDOWANA liste pakietow
	// cybersecurity/pentest (patrz internal/rootfs, cybersecurityPackages)
	// TYLKO gdy [project] -> type=cybersecurity. Format tablicowy .hk:
	//   -> cybersecurity_packages => [nmap, wireshark, hydra, ...]
	// Puste/brak klucza => uzyj wbudowanej listy domyslnej (zachowanie
	// sprzed tej opcji, bez zadnej zmiany).
	if val, ok := sec.Get("cybersecurity_packages"); ok {
		items, err := val.AsArray()
		if err != nil {
			return ProjectConfig{}, fmt.Errorf(
				"config.hk: [project] -> cybersecurity_packages musi byc tablica, np. "+
					"[nmap, wireshark, hydra]: %w", err)
		}
		pkgs := make([]string, 0, len(items))
		for _, item := range items {
			s, err := item.AsString()
			if err != nil {
				return ProjectConfig{}, fmt.Errorf(
					"config.hk: [project] -> cybersecurity_packages: kazdy element musi byc "+
						"stringiem (nazwa pakietu apt): %w", err)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				pkgs = append(pkgs, s)
			}
		}
		p.CybersecurityPackages = pkgs
	}

	// sign => true/false -- czy "build cloud" ma podpisac wypchniety obraz
	// OCI przez cosign PO udanym push (patrz internal/buildflow/cloud.go).
	if val, ok := sec.Get("sign"); ok {
		if s, err := val.AsString(); err == nil {
			p.Sign = isTruthy(strings.TrimSpace(s))
		}
	}

	// verify_signature => true/false -- czy "build iso" ma zweryfikowac
	// podpis cosign obrazu OCI PRZED pociagnieciem go z registry.
	if val, ok := sec.Get("verify_signature"); ok {
		if s, err := val.AsString(); err == nil {
			p.VerifySignature = isTruthy(strings.TrimSpace(s))
		}
	}

	// cosign_key => sciezka do klucza cosign. Uzywana JAKO KLUCZ PRYWATNY
	// przy sign=true ("cosign sign --key <cosign_key>") i JAKO KLUCZ
	// PUBLICZNY przy verify_signature=true ("cosign verify --key
	// <cosign_key>"). W typowym uzyciu to DWIE ROZNE sciezki (klucz
	// prywatny na maszynie ktora robi "build cloud", klucz publiczny na
	// maszynie ktora robi "build iso") -- ten sam klucz konfiguracyjny w
	// dwoch projektach/uruchomieniach, kazde ze swoja wlasciwa polowka pary.
	if val, ok := sec.Get("cosign_key"); ok {
		if s, err := val.AsString(); err == nil {
			p.CosignKey = strings.TrimSpace(s)
		}
	}

	// isolator_version => nadpisuje automatycznie wykrywana najnowsza
	// wersje Isolatora wbudowywana do rootfs dla [project] -> type =
	// containerized (patrz download.LatestIsolatorVersion). Puste/brak
	// klucza = automatyczne wykrycie.
	if val, ok := sec.Get("isolator_version"); ok {
		if s, err := val.AsString(); err == nil {
			p.IsolatorVersion = strings.TrimSpace(s)
		}
	}

	return p, nil
}

// defaultProjectConfig zwraca ProjectConfig z sensownymi wartosciami domyslnymi.
func defaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		Type:      ProjectTypeDefault,
		Installer: InstallerDefault,
		MAC:       MACAppArmor,
	}
}

// parseProjectType parsuje wartosc klucza "type" z sekcji [project].
//
// UWAGA (v0.7.0): "cybersecurity" NIE jest juz aliasem "default" -- zwraca
// wlasna, odrebna wartosc ProjectTypeCybersecurity (patrz jej dokumentacja
// dla pelnego opisu roznicy). "container"/"containerized" sa nowe w tej
// wersji, patrz ProjectTypeContainer / ProjectTypeContainerized.
func parseProjectType(s string) (ProjectType, error) {
	switch strings.ToLower(s) {
	case "", "default":
		return ProjectTypeDefault, nil
	case "cybersecurity":
		return ProjectTypeCybersecurity, nil
	case "normal":
		return ProjectTypeNormal, nil
	case "official":
		return ProjectTypeOfficial, nil
	case "independent":
		return ProjectTypeIndependent, nil
	case "container":
		return ProjectTypeContainer, nil
	case "containerized":
		return ProjectTypeContainerized, nil
	default:
		return "", fmt.Errorf(
			"nieznana wartosc %q -- dozwolone: default, cybersecurity, normal, official, "+
				"independent, container, containerized",
			s)
	}
}

// parseInstallerType parsuje wartosc klucza "installer" z sekcji [project].
//
// UWAGA (v0.7.0): "cybersecurity" NIE jest juz aliasem "default" -- zwraca
// wlasna, odrebna wartosc InstallerCybersecurity (patrz jej dokumentacja).
func parseInstallerType(s string) (InstallerType, error) {
	switch strings.ToLower(s) {
	case "", "default":
		return InstallerDefault, nil
	case "cybersecurity":
		return InstallerCybersecurity, nil
	case "none":
		return InstallerNone, nil
	default:
		return "", fmt.Errorf(
			"nieznana wartosc %q -- dozwolone: default, cybersecurity, none",
			s)
	}
}

// isTruthy zwraca true dla "true", "yes", "1", "on" (case-insensitive).
func isTruthy(s string) bool {
	switch strings.ToLower(s) {
	case "true", "yes", "1", "on":
		return true
	}
	return false
}

func getRequiredString(cfg *hk.HkConfig, section, key string) (string, error) {
	sec, err := cfg.Section(section)
	if err != nil {
		return "", fmt.Errorf(
			"brak wymaganej sekcji [%s] w config.hk (oczekiwano klucza '%s')",
			section, key)
	}
	val, ok := sec.Get(key)
	if !ok {
		return "", fmt.Errorf(
			"brak wymaganego klucza '%s' w sekcji [%s] config.hk", key, section)
	}
	str, err := val.AsString()
	if err != nil {
		return "", fmt.Errorf(
			"klucz '%s' w sekcji [%s] musi byc tekstem: %w", key, section, err)
	}
	return str, nil
}

func (c *Config) validate(requireAuth bool) error {
	switch c.AccountType {
	case AccountTypeUser, AccountTypeOrganisation:
	default:
		return fmt.Errorf(
			"config.hk: [account] -> type musi byc 'user' lub 'organisation', otrzymano %q",
			c.AccountType)
	}
	// UWAGA: [account] -> name oraz [auth] -> token MOGA byc pustymi
	// stringami w kontekstach, gdzie push do registry jest OPCJONALNY
	// (patrz "build container" w internal/buildflow/container.go: klucze
	// musza byc OBECNE w config.hk -- to sprawdza juz getRequiredString()
	// w Load() ponizej -- ale ich WARTOSC moze byc pusta, bo push jest
	// wykonywany DODATKOWO tylko gdy oba pola sa faktycznie wypelnione).
	// Dla "build cloud"/"build iso"/"build all" push jest OBOWIAZKOWY,
	// wiec tam requireAuth=true i te pola nadal musza byc niepuste --
	// lepiej dostac czytelny blad TUTAJ, niz niejasny blad autoryzacji
	// registry pozniej w trakcie pushowania warstw.
	if requireAuth {
		if c.AccountName == "" {
			return fmt.Errorf("config.hk: [account] -> name nie moze byc puste")
		}
		if c.Token == "" {
			return fmt.Errorf("config.hk: [auth] -> token nie moze byc puste")
		}
	}
	if c.Release == "" {
		return fmt.Errorf("config.hk: [release] -> name nie moze byc puste")
	}
	return nil
}

// IsKnownRelease zwraca false gdy Release nie jest na liscie znanych wersji.
func (c *Config) IsKnownRelease() bool {
	return knownReleases[c.Release]
}

// ImageRepository buduje pelna sciezke repozytorium OCI.
func (c *Config) ImageRepository(registryHost, imageName string) string {
	return fmt.Sprintf("%s/%s/%s", registryHost, toLower(c.AccountName), imageName)
}

func toLower(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'A' && ch <= 'Z' {
			b[i] = ch + ('a' - 'A')
		}
	}
	return string(b)
}
