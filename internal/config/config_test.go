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
