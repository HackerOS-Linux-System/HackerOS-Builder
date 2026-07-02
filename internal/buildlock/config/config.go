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
	// ProjectTypeDefault / ProjectTypeCybersecurity / brak:
	// Pelny atomowy build (debootstrap + OCI push + deb-ostree).
	// To jest glowny tryb hackeros-builder, aktywnie rozwijany.
	ProjectTypeDefault       ProjectType = "default"
	ProjectTypeCybersecurity ProjectType = "cybersecurity"

	// ProjectTypeNormal / ProjectTypeOfficial:
	// Zwykla nakladka na live-build -- bez deb-ostree, bez OCI, bez atomowosci.
	// Wymaga zainstalowanego live-build na hoscie.
	// Builder deleguje caly build do "lb build" zamiast robic to samemu.
	ProjectTypeNormal   ProjectType = "normal"
	ProjectTypeOfficial ProjectType = "official"

	// ProjectTypeIndependent:
	// Alternatywa dla live-build dla projektow typu normal/official --
	// nie atomowe, ale bez zewnetrznej zaleznosci od live-build.
	// Uzywa wewnetrznego pipeline'u hackeros-builder (debootstrap + squashfs
	// + iso) ale BEZ push OCI i BEZ deb-ostree.
	ProjectTypeIndependent ProjectType = "independent"
)

// InstallerType opisuje jaki instalator jest dolaczony do obrazu ISO.
type InstallerType string

const (
	// InstallerDefault / InstallerCybersecurity / brak:
	// Wlasny instalator hackeros-builder (Calamares, uruchamiany od razu
	// przy starcie ISO -- bez pośredniego pulpitu live, inspirowany
	// trybem "Install" z Kubuntu: wybierasz "Install" i jesteś od razu
	// w instalatorze, nie w srodowisku live z ikona na pulpicie).
	InstallerDefault       InstallerType = "default"
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

	// Project to zawartosc sekcji [project] -- wszystkie pola opcjonalne,
	// brak calej sekcji nie jest bledem (stosowane sa wartosci domyslne).
	Project ProjectConfig
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
	// Wartosc domyslna (brak lub "default" lub "cybersecurity"):
	//   pelny atomowy build z OCI + deb-ostree.
	Type ProjectType

	// Installer okresla instalator dolaczany do obrazu ISO (patrz InstallerType*).
	// Wartosc domyslna (brak lub "default" lub "cybersecurity"):
	//   wlasny instalator Calamares uruchamiany od razu przy starcie ISO.
	Installer InstallerType

	// MAC to system kontroli dostepu obowiazkowego (AppArmor lub SELinux).
	// Wartosc domyslna (brak lub selinux=false): AppArmor.
	// selinux=true: SELinux.
	MAC MACSystem
}

// IsAtomicBuild zwraca true jesli projekt ma byc budowany jako pelny
// atomowy obraz OCI z deb-ostree (domyslne zachowanie). Zwraca false
// dla typow normal/official/independent.
func (p *ProjectConfig) IsAtomicBuild() bool {
	switch p.Type {
	case ProjectTypeNormal, ProjectTypeOfficial, ProjectTypeIndependent:
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
func Load(path string) (*Config, error) {
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

	if err := cfg.validate(); err != nil {
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
func parseProjectType(s string) (ProjectType, error) {
	switch strings.ToLower(s) {
	case "", "default", "cybersecurity":
		return ProjectTypeDefault, nil
	case "normal":
		return ProjectTypeNormal, nil
	case "official":
		return ProjectTypeOfficial, nil
	case "independent":
		return ProjectTypeIndependent, nil
	default:
		return "", fmt.Errorf(
			"nieznana wartosc %q -- dozwolone: default, cybersecurity, normal, official, independent",
			s)
	}
}

// parseInstallerType parsuje wartosc klucza "installer" z sekcji [project].
func parseInstallerType(s string) (InstallerType, error) {
	switch strings.ToLower(s) {
	case "", "default", "cybersecurity":
		return InstallerDefault, nil
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

func (c *Config) validate() error {
	switch c.AccountType {
	case AccountTypeUser, AccountTypeOrganisation:
	default:
		return fmt.Errorf(
			"config.hk: [account] -> type musi byc 'user' lub 'organisation', otrzymano %q",
			c.AccountType)
	}
	if c.AccountName == "" {
		return fmt.Errorf("config.hk: [account] -> name nie moze byc puste")
	}
	if c.Token == "" {
		return fmt.Errorf("config.hk: [auth] -> token nie moze byc puste")
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
