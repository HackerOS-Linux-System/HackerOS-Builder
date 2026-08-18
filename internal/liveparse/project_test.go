package liveparse

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFile to mala pomocnicza funkcja tworzaca plik z zadana trescia,
// razem z katalogami posrednimi.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParsePackageLists_SimpleList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "package-lists", "base.list.chroot"),
		"curl\nvim\n# komentarz\n\nwget\n")

	p := &Project{RootDir: root}
	if err := p.parsePackageLists(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parsePackageLists: %v", err)
	}

	want := []string{"curl", "vim", "wget"}
	if !reflect.DeepEqual(p.Packages, want) {
		t.Fatalf("Packages = %v, chcialem %v", p.Packages, want)
	}
}

// TestParsePackageLists_ExclusionPrefix odtwarza dokladnie sytuacje z buga:
// jeden plik dodaje "firefox-esr", drugi (np. remove.list.chroot) go
// wyklucza przez "-firefox-esr". Wynik NIE moze zawierac ani "firefox-esr"
// ani (co najwazniejsze -- to byl faktyczny blad) literalnego wpisu
// "-firefox-esr" przekazywanego pozniej do apt-get.
func TestParsePackageLists_ExclusionPrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "package-lists", "base.list.chroot"),
		"firefox-esr\ncurl\n")
	writeFile(t, filepath.Join(root, "config", "package-lists", "remove.list.chroot"),
		"-firefox-esr\n")

	p := &Project{RootDir: root}
	if err := p.parsePackageLists(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parsePackageLists: %v", err)
	}

	for _, pkg := range p.Packages {
		if pkg == "-firefox-esr" {
			t.Fatalf("Packages zawiera surowy wpis wykluczenia '-firefox-esr' -- "+
				"to dokladnie ten blad ktory ma byc naprawiony: %v", p.Packages)
		}
		if pkg == "firefox-esr" {
			t.Fatalf("Packages zawiera 'firefox-esr' mimo wykluczenia w innym pliku: %v", p.Packages)
		}
	}

	want := []string{"curl"}
	if !reflect.DeepEqual(p.Packages, want) {
		t.Fatalf("Packages = %v, chcialem %v", p.Packages, want)
	}
}

// TestParsePackageLists_ExclusionOrderIndependent sprawdza ze wykluczenie
// dziala niezaleznie od kolejnosci wczytywania plikow (alfabetycznej) --
// nawet jesli plik z wykluczeniem zostanie wczytany PRZED plikiem ktory
// dodaje dany pakiet, wynik koncowy nadal go nie zawiera (semantyka
// zbioru, nie kolejnosci wykonania).
func TestParsePackageLists_ExclusionOrderIndependent(t *testing.T) {
	root := t.TempDir()
	// "aaa" < "zzz" alfabetycznie -- wykluczenie wczytane jako pierwsze.
	writeFile(t, filepath.Join(root, "config", "package-lists", "aaa-exclude.list.chroot"),
		"-nginx\n")
	writeFile(t, filepath.Join(root, "config", "package-lists", "zzz-base.list.chroot"),
		"nginx\napache2\n")

	p := &Project{RootDir: root}
	if err := p.parsePackageLists(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parsePackageLists: %v", err)
	}

	want := []string{"apache2"}
	if !reflect.DeepEqual(p.Packages, want) {
		t.Fatalf("Packages = %v, chcialem %v", p.Packages, want)
	}
}

func TestParsePackageLists_MissingDirIsNotError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	p := &Project{RootDir: root}
	if err := p.parsePackageLists(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parsePackageLists: oczekiwano braku bledu gdy package-lists/ nie istnieje, otrzymano: %v", err)
	}
	if len(p.Packages) != 0 {
		t.Fatalf("Packages = %v, chcialem pusta liste", p.Packages)
	}
}

func TestParseHooks_ReadsBothNormalAndLiveSubdirs(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	writeFile(t, filepath.Join(configDir, "hooks", "normal", "b-user-setup.hook.chroot"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(configDir, "hooks", "normal", "a-preseed.hook.chroot"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(configDir, "hooks", "live", "build-red-team-tools.hook.chroot"), "#!/bin/sh\n")

	p := &Project{RootDir: root}
	if err := p.parseHooks(configDir); err != nil {
		t.Fatalf("parseHooks: %v", err)
	}

	var names []string
	for _, h := range p.Hooks {
		names = append(names, h.Name)
	}

	// hooks/normal/ posortowane alfabetycznie, PRZED hooks/live/ (ktore
	// tez jest -- i, co najwazniejsze, JEST OBECNE, bo to byl caly punkt
	// tego buga: hooks/live/ bylo wczesniej calkowicie pomijane).
	want := []string{"a-preseed.hook.chroot", "b-user-setup.hook.chroot", "build-red-team-tools.hook.chroot"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Hooks nazwy = %v, chcialem %v (hooks/live/ musi byc odczytane, "+
			"nie tylko hooks/normal/)", names, want)
	}
}

func TestParseHooks_MissingDirsIsNotError(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	p := &Project{RootDir: root}
	if err := p.parseHooks(configDir); err != nil {
		t.Fatalf("parseHooks: oczekiwano braku bledu gdy hooks/ nie istnieje, otrzymano: %v", err)
	}
	if len(p.Hooks) != 0 {
		t.Fatalf("Hooks = %v, chcialem pusta liste", p.Hooks)
	}
}

func TestParseHooks_OnlyNormalDirPresent(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	writeFile(t, filepath.Join(configDir, "hooks", "normal", "only.hook.chroot"), "#!/bin/sh\n")

	p := &Project{RootDir: root}
	if err := p.parseHooks(configDir); err != nil {
		t.Fatalf("parseHooks: %v", err)
	}
	if len(p.Hooks) != 1 || p.Hooks[0].Name != "only.hook.chroot" {
		t.Fatalf("Hooks = %v, chcialem dokladnie jeden hook 'only.hook.chroot'", p.Hooks)
	}
}

func TestParsePackageLists_DeduplicatesAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "package-lists", "a.list.chroot"), "curl\n")
	writeFile(t, filepath.Join(root, "config", "package-lists", "b.list.chroot"), "curl\nwget\n")

	p := &Project{RootDir: root}
	if err := p.parsePackageLists(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parsePackageLists: %v", err)
	}

	want := []string{"curl", "wget"}
	if !reflect.DeepEqual(p.Packages, want) {
		t.Fatalf("Packages = %v, chcialem %v", p.Packages, want)
	}
}

// TestDetectHookInterpreter_VariousShebangs sprawdza wykrywanie jezyka
// hooka z shebang -- podstawa dla wsparcia hookow w Pythonie/Ruby/Lua/...
// (nie tylko w powloce), patrz HookScript.Interpreter i internal/hooklang.
func TestDetectHookInterpreter_VariousShebangs(t *testing.T) {
	cases := map[string]string{
		"#!/bin/sh\necho hi\n":                  "sh",
		"#!/bin/bash\necho hi\n":                "bash",
		"#!/usr/bin/env python3\nprint('hi')\n": "python3",
		"#!/usr/bin/python3\nprint('hi')\n":     "python3",
		"#!/usr/bin/env ruby\nputs 'hi'\n":      "ruby",
		"#!/usr/bin/env lua5.4\nprint('hi')\n":  "lua5.4",
		"#!/usr/bin/perl\nprint \"hi\";\n":      "perl",
		"no shebang here\njust text\n":          "",
	}
	for content, want := range cases {
		root := t.TempDir()
		path := filepath.Join(root, "hook.hook.chroot")
		writeFile(t, path, content)
		got := detectHookInterpreter(path)
		if got != want {
			t.Errorf("detectHookInterpreter(%q) = %q, chcialem %q", content, got, want)
		}
	}
}

// TestParseHooks_DetectsInterpreterField sprawdza ze parseHooks (uzywana
// dla hooks/normal i hooks/live) wypelnia HookScript.Interpreter, nie tylko
// Name/Path.
func TestParseHooks_DetectsInterpreterField(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "hooks", "normal", "0100-py.hook.chroot"),
		"#!/usr/bin/env python3\nprint('siema')\n")

	p := &Project{RootDir: root}
	if err := p.parseHooks(filepath.Join(root, "config")); err != nil {
		t.Fatalf("parseHooks: %v", err)
	}
	if len(p.Hooks) != 1 {
		t.Fatalf("Hooks = %v, chcialem dokladnie 1", p.Hooks)
	}
	if p.Hooks[0].Interpreter != "python3" {
		t.Errorf("Interpreter = %q, chcialem \"python3\"", p.Hooks[0].Interpreter)
	}
}

// TestParseInstallerHooks_SeparateFromNormalAndLive sprawdza ze
// config/hooks/installer/ jest osobna kategoria (InstallerHooks), NIE
// wpada do zwyklych Hooks (normal/live) i odwrotnie.
func TestParseInstallerHooks_SeparateFromNormalAndLive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "hooks", "normal", "0100-a.hook.chroot"), "#!/bin/sh\ntrue\n")
	writeFile(t, filepath.Join(root, "config", "hooks", "installer", "0100-branding.hook.chroot"),
		"#!/usr/bin/env python3\nprint('branding')\n")

	p := &Project{RootDir: root}
	configDir := filepath.Join(root, "config")
	if err := p.parseHooks(configDir); err != nil {
		t.Fatalf("parseHooks: %v", err)
	}
	if err := p.parseInstallerHooks(configDir); err != nil {
		t.Fatalf("parseInstallerHooks: %v", err)
	}

	if len(p.Hooks) != 1 || p.Hooks[0].Name != "0100-a.hook.chroot" {
		t.Fatalf("Hooks = %v, chcialem dokladnie [0100-a.hook.chroot]", p.Hooks)
	}
	if len(p.InstallerHooks) != 1 || p.InstallerHooks[0].Name != "0100-branding.hook.chroot" {
		t.Fatalf("InstallerHooks = %v, chcialem dokladnie [0100-branding.hook.chroot]", p.InstallerHooks)
	}
	if p.InstallerHooks[0].Interpreter != "python3" {
		t.Errorf("InstallerHooks[0].Interpreter = %q, chcialem \"python3\"", p.InstallerHooks[0].Interpreter)
	}
}

// TestParseInstallerHooks_MissingDirReturnsEmptyNoError sprawdza ze brak
// config/hooks/installer/ (typowy przypadek -- wiekszosc projektow go nie
// uzywa) to NIE jest blad, tylko pusta lista.
func TestParseInstallerHooks_MissingDirReturnsEmptyNoError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "config.hk"), "[release]\n-> name => trixie\n")

	hooks, err := ParseInstallerHooks(root)
	if err != nil {
		t.Fatalf("ParseInstallerHooks: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("hooks = %v, chcialem pusta liste", hooks)
	}
}

// TestParseInstallerHooks_MissingProjectRootReturnsEmptyNoError sprawdza
// ze "build iso" moze wywolac ParseInstallerHooks nawet gdy caly katalog
// projektu (albo sam config/) nie istnieje lokalnie -- nie powinno to
// wywrocic budowy ISO ktora i tak tylko sciaga OCI z registry.
func TestParseInstallerHooks_MissingProjectRootReturnsEmptyNoError(t *testing.T) {
	hooks, err := ParseInstallerHooks(filepath.Join(t.TempDir(), "nie-istnieje"))
	if err != nil {
		t.Fatalf("ParseInstallerHooks: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("hooks = %v, chcialem pusta liste", hooks)
	}
}
