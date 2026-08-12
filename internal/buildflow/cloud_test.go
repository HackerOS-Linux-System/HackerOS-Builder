package buildflow

import (
	"testing"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
)

// TestResolveImageNameAndTag_UsesProjectNameAndTag odtwarza dokladnie bug
// z logow CI: standalone "build iso" (bez wczesniejszego "build cloud" w
// tym samym procesie) probowalo sciagnac obraz spod
// "ghcr.io/hackeros-linux-system/HackerOS:forky" zamiast poprawnego
// "ghcr.io/hackeros-linux-system/hackeros-atomic:stable" -- bo uzywalo
// nazwy katalogu projektu na dysku (nie [project] -> name) i nazwy wydania
// Debiana (nie [project] -> tag).
func TestResolveImageNameAndTag_UsesProjectNameAndTag(t *testing.T) {
	cfg := &config.Config{
		Release: "forky", // [release] -> name -- NIE powinno wplywac na tag obrazu OCI
		Project: config.ProjectConfig{
			Name: "hackeros-atomic", // [project] -> name
			Tag:  "stable",          // [project] -> tag
		},
	}

	imageName, tag := resolveImageNameAndTag(cfg, "/jakis/katalog/HackerOS")

	if imageName != "hackeros-atomic" {
		t.Fatalf("imageName = %q, chcialem %q ([project] -> name musi miec pierwszenstwo "+
			"nad nazwa katalogu projektu)", imageName, "hackeros-atomic")
	}
	if tag != "stable" {
		t.Fatalf("tag = %q, chcialem %q ([project] -> tag musi miec pierwszenstwo nad "+
			"[release] -> name -- to byl dokladnie zaobserwowany blad: tag='forky' zamiast 'stable')",
			tag, "stable")
	}
}

// TestResolveImageNameAndTag_FallbacksWhenProjectSectionEmpty sprawdza
// zachowanie domyslne gdy [project] -> name/tag nie sa ustawione w
// config.hk (dopuszczalne -- cala sekcja [project] jest opcjonalna).
func TestResolveImageNameAndTag_FallbacksWhenProjectSectionEmpty(t *testing.T) {
	cfg := &config.Config{
		Release: "trixie",
		Project: config.ProjectConfig{
			// Name i Tag celowo puste
		},
	}

	imageName, tag := resolveImageNameAndTag(cfg, "/home/runner/work/HackerOS/HackerOS")

	if imageName != "HackerOS" {
		t.Fatalf("imageName = %q, chcialem %q (fallback na nazwe katalogu projektu)", imageName, "HackerOS")
	}
	if tag != "latest" {
		t.Fatalf("tag = %q, chcialem %q (fallback na 'latest', NIE na [release] -> name)", tag, "latest")
	}
}

// TestResolveImageNameAndTag_NeverUsesReleaseAsTag to bezposrednia,
// wyrazna asercja przeciwko regresji tego konkretnego buga: cfg.Release
// NIGDY nie powinno wplynac na zwracany tag, niezaleznie od tego czy
// [project] -> tag jest ustawiony czy nie.
func TestResolveImageNameAndTag_NeverUsesReleaseAsTag(t *testing.T) {
	for _, release := range []string{"forky", "trixie", "bookworm", ""} {
		cfg := &config.Config{
			Release: release,
			Project: config.ProjectConfig{Tag: "v1.2.3"},
		}
		_, tag := resolveImageNameAndTag(cfg, "/x")
		if tag != "v1.2.3" {
			t.Fatalf("dla Release=%q: tag = %q, chcialem 'v1.2.3' (Release nie powinno miec wplywu)", release, tag)
		}
	}
}
