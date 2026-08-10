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
