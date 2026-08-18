package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.hk")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("nie mozna zapisac testowego config.hk: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load zwrocilo blad: %v", err)
	}
	if cfg.AccountType != AccountTypeUser {
		t.Errorf("oczekiwano AccountType=user, otrzymano %q", cfg.AccountType)
	}
	if cfg.AccountName != "michal" {
		t.Errorf("oczekiwano AccountName=michal, otrzymano %q", cfg.AccountName)
	}
	if cfg.Token != "ghp_test123" {
		t.Errorf("oczekiwano Token=ghp_test123, otrzymano %q", cfg.Token)
	}
	if cfg.Release != "trixie" {
		t.Errorf("oczekiwano Release=trixie, otrzymano %q", cfg.Release)
	}
}

func TestLoad_MissingRequiredSection(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("oczekiwano bledu przy brakujacej sekcji [auth]/[release]")
	}
}

func TestLoad_InvalidAccountType(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => cokolwiek_innego
-> name => michal

[auth]
-> token => x

[release]
-> name => trixie
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("oczekiwano bledu dla niepoprawnego account.type")
	}
}

func TestIsKnownRelease(t *testing.T) {
	cfg := &Config{Release: "trixie"}
	if !cfg.IsKnownRelease() {
		t.Error("trixie powinno byc znana wersja")
	}
	cfg.Release = "nieznana-wersja-xyz"
	if cfg.IsKnownRelease() {
		t.Error("nieznana-wersja-xyz nie powinna byc znana wersja")
	}
}

func TestImageRepository_LowercasesAccountName(t *testing.T) {
	cfg := &Config{AccountName: "MichaL"}
	repo := cfg.ImageRepository("ghcr.io", "moj-obraz")
	want := "ghcr.io/michal/moj-obraz"
	if repo != want {
		t.Errorf("oczekiwano %q, otrzymano %q", want, repo)
	}
}

// TestParseProjectType_CybersecurityIsNoLongerAliasOfDefault odtwarza
// dokladnie zgloszony problem: "cybersecurity" byl parsowany poprawnie ale
// zwracal ProjectTypeDefault -- czyli byl NIEODROZNIALNY od "default" w
// calej reszcie kodu. Od v0.7.0 musi zwracac WLASNA wartosc.
func TestParseProjectType_CybersecurityIsNoLongerAliasOfDefault(t *testing.T) {
	pt, err := parseProjectType("cybersecurity")
	if err != nil {
		t.Fatalf("parseProjectType(cybersecurity): %v", err)
	}
	if pt != ProjectTypeCybersecurity {
		t.Fatalf("parseProjectType(cybersecurity) = %q, chcialem %q (nie powinno byc juz aliasem default)",
			pt, ProjectTypeCybersecurity)
	}
	if pt == ProjectTypeDefault {
		t.Fatal("ProjectTypeCybersecurity nie powinno byc rowne ProjectTypeDefault")
	}
}

// TestParseInstallerType_CybersecurityIsNoLongerAliasOfDefault -- jak
// powyzej, dla [project] -> installer.
func TestParseInstallerType_CybersecurityIsNoLongerAliasOfDefault(t *testing.T) {
	it, err := parseInstallerType("cybersecurity")
	if err != nil {
		t.Fatalf("parseInstallerType(cybersecurity): %v", err)
	}
	if it != InstallerCybersecurity {
		t.Fatalf("parseInstallerType(cybersecurity) = %q, chcialem %q", it, InstallerCybersecurity)
	}
	if it == InstallerDefault {
		t.Fatal("InstallerCybersecurity nie powinno byc rowne InstallerDefault")
	}
}

// TestParseProjectType_ContainerAndContainerized sprawdza nowe wartosci
// wprowadzone w v0.7.0 dla trybu pracy buildera.
func TestParseProjectType_ContainerAndContainerized(t *testing.T) {
	cases := map[string]ProjectType{
		"container":     ProjectTypeContainer,
		"CONTAINER":     ProjectTypeContainer, // case-insensitive
		"containerized": ProjectTypeContainerized,
	}
	for input, want := range cases {
		got, err := parseProjectType(input)
		if err != nil {
			t.Fatalf("parseProjectType(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("parseProjectType(%q) = %q, chcialem %q", input, got, want)
		}
	}
}

// TestProjectConfig_IsAtomicBuild_ContainerTypesAreNotAtomic sprawdza ze
// nowe typy container/containerized sa (podobnie jak normal/official/
// independent) traktowane jako NIEATOMOWE -- nie maja przechodzic przez
// sciezke "build cloud" wstrzykujaca hammer.
func TestProjectConfig_IsAtomicBuild_ContainerTypesAreNotAtomic(t *testing.T) {
	for _, pt := range []ProjectType{ProjectTypeContainer, ProjectTypeContainerized} {
		p := &ProjectConfig{Type: pt}
		if p.IsAtomicBuild() {
			t.Errorf("IsAtomicBuild() dla Type=%q powinno byc false", pt)
		}
	}
	for _, pt := range []ProjectType{ProjectTypeDefault, ProjectTypeCybersecurity, ""} {
		p := &ProjectConfig{Type: pt}
		if !p.IsAtomicBuild() {
			t.Errorf("IsAtomicBuild() dla Type=%q powinno byc true", pt)
		}
	}
}

// TestProjectConfig_Helpers sprawdza nowe metody pomocnicze wprowadzone
// razem z rozbudowa [project] -> type/installer.
func TestProjectConfig_Helpers(t *testing.T) {
	cyberType := &ProjectConfig{Type: ProjectTypeCybersecurity}
	if !cyberType.IsCybersecurity() {
		t.Error("IsCybersecurity() powinno byc true dla Type=cybersecurity")
	}

	containerType := &ProjectConfig{Type: ProjectTypeContainer}
	if !containerType.IsContainerBuild() {
		t.Error("IsContainerBuild() powinno byc true dla Type=container")
	}
	if containerType.IsContainerizedIsolator() {
		t.Error("IsContainerizedIsolator() powinno byc false dla Type=container")
	}

	containerizedType := &ProjectConfig{Type: ProjectTypeContainerized}
	if !containerizedType.IsContainerizedIsolator() {
		t.Error("IsContainerizedIsolator() powinno byc true dla Type=containerized")
	}
	if containerizedType.IsContainerBuild() {
		t.Error("IsContainerBuild() powinno byc false dla Type=containerized")
	}

	cyberInstaller := &ProjectConfig{Installer: InstallerCybersecurity}
	if !cyberInstaller.UsesCybersecurityInstaller() {
		t.Error("UsesCybersecurityInstaller() powinno byc true dla Installer=cybersecurity")
	}
	defaultInstaller := &ProjectConfig{Installer: InstallerDefault}
	if defaultInstaller.UsesCybersecurityInstaller() {
		t.Error("UsesCybersecurityInstaller() powinno byc false dla Installer=default")
	}
}

// TestParseProjectType_UnknownValueListsAllOptions sprawdza ze komunikat
// bledu dla nieznanej wartosci wymienia WSZYSTKIE dozwolone opcje, w tym
// nowe container/containerized -- to jest tekst ktory faktycznie widzi
// uzytkownik przy literowce w config.hk.
func TestParseProjectType_UnknownValueListsAllOptions(t *testing.T) {
	_, err := parseProjectType("cokolwiek-nieistniejacego")
	if err == nil {
		t.Fatal("oczekiwano bledu dla nieznanej wartosci [project] -> type")
	}
	for _, want := range []string{"default", "cybersecurity", "normal", "official", "independent", "container", "containerized"} {
		if !contains(err.Error(), want) {
			t.Errorf("komunikat bledu powinien wymieniac %q, otrzymano: %v", want, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestLoad_MirrorAndArch sprawdza nowe opcjonalne klucze
// [release] -> mirror / arch wraz z ich wartosciami domyslnymi.
func TestLoad_MirrorAndArch(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
-> mirror => http://mirror.local/debian
-> arch => arm64
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EffectiveMirror() != "http://mirror.local/debian" {
		t.Errorf("EffectiveMirror() = %q, chcialem http://mirror.local/debian", cfg.EffectiveMirror())
	}
	if cfg.EffectiveArch() != "arm64" {
		t.Errorf("EffectiveArch() = %q, chcialem arm64", cfg.EffectiveArch())
	}
	if !cfg.IsKnownArch() {
		t.Error("IsKnownArch() powinno byc true dla arm64")
	}
}

func TestLoad_MirrorAndArch_Defaults(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EffectiveMirror() != DefaultMirror {
		t.Errorf("EffectiveMirror() = %q, chcialem %q", cfg.EffectiveMirror(), DefaultMirror)
	}
	if cfg.EffectiveArch() != DefaultArch {
		t.Errorf("EffectiveArch() = %q, chcialem %q", cfg.EffectiveArch(), DefaultArch)
	}
}

func TestLoad_UnknownArchWarnsButParses(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
-> arch => amd46
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load nie powinno zwrocic bledu dla nierozpoznanej architektury: %v", err)
	}
	if cfg.IsKnownArch() {
		t.Error("IsKnownArch() powinno byc false dla \"amd46\"")
	}
}

// TestLoadProjectSection_CybersecurityPackages sprawdza parsowanie tablicy
// [project] -> cybersecurity_packages.
func TestLoadProjectSection_CybersecurityPackages(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie

[project]
-> type => cybersecurity
-> cybersecurity_packages => [nmap, wireshark, custom-tool]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"nmap", "wireshark", "custom-tool"}
	if len(cfg.Project.CybersecurityPackages) != len(want) {
		t.Fatalf("CybersecurityPackages = %v, chcialem %v", cfg.Project.CybersecurityPackages, want)
	}
	for i, w := range want {
		if cfg.Project.CybersecurityPackages[i] != w {
			t.Errorf("CybersecurityPackages[%d] = %q, chcialem %q", i, cfg.Project.CybersecurityPackages[i], w)
		}
	}
}

func TestLoadProjectSection_CybersecurityPackages_EmptyMeansDefault(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Project.CybersecurityPackages) != 0 {
		t.Errorf("CybersecurityPackages = %v, chcialem puste (brak klucza w config.hk)", cfg.Project.CybersecurityPackages)
	}
}

// TestLoadProjectSection_SignAndVerifySignature sprawdza [project] -> sign,
// verify_signature, cosign_key.
func TestLoadProjectSection_SignAndVerifySignature(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie

[project]
-> sign => true
-> verify_signature => true
-> cosign_key => /etc/hackeros-builder/cosign.key
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Project.Sign {
		t.Error("Sign powinno byc true")
	}
	if !cfg.Project.VerifySignature {
		t.Error("VerifySignature powinno byc true")
	}
	if cfg.Project.CosignKey != "/etc/hackeros-builder/cosign.key" {
		t.Errorf("CosignKey = %q, chcialem /etc/hackeros-builder/cosign.key", cfg.Project.CosignKey)
	}
}

func TestLoadProjectSection_SignDefaultsFalse(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Sign {
		t.Error("Sign domyslnie powinno byc false")
	}
	if cfg.Project.VerifySignature {
		t.Error("VerifySignature domyslnie powinno byc false")
	}
}

// TestParseProjectType_ContainerizedIsRealNotPlaceholder sprawdza ze
// containerized jest realnym trybem (patrz IsContainerizedIsolator) --
// nie powinien byc mylony z blednym typem.
func TestParseProjectType_ContainerizedIsRealNotPlaceholder(t *testing.T) {
	p := &ProjectConfig{Type: ProjectTypeContainerized}
	if !p.IsContainerizedIsolator() {
		t.Error("IsContainerizedIsolator() powinno byc true dla Type=containerized")
	}
	if p.IsAtomicBuild() {
		t.Error("IsAtomicBuild() powinno byc false dla Type=containerized (kontener, nie atomowy build)")
	}
}

// TestLoadProjectSection_IsolatorVersion sprawdza [project] -> isolator_version.
func TestLoadProjectSection_IsolatorVersion(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie

[project]
-> type => containerized
-> isolator_version => v0.3.0
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.IsolatorVersion != "v0.3.0" {
		t.Errorf("IsolatorVersion = %q, chcialem v0.3.0", cfg.Project.IsolatorVersion)
	}
}

func TestLoadProjectSection_IsolatorVersion_EmptyMeansAutoDetect(t *testing.T) {
	path := writeTestConfig(t, `[account]
-> type => user
-> name => michal

[auth]
-> token => ghp_test123

[release]
-> name => trixie
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.IsolatorVersion != "" {
		t.Errorf("IsolatorVersion = %q, chcialem puste (auto-wykrywanie)", cfg.Project.IsolatorVersion)
	}
}
