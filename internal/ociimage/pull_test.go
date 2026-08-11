package ociimage

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// buildTar to pomocnicza funkcja tworzaca w pamieci strumien tar z podanych
// wpisow, w podanej kolejnosci (kolejnosc ma znaczenie dla testu odtwarzajacego
// przypadek "plik w katalogu przetworzony przed wpisem samego katalogu").
type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	linkname string
	content  string
}

func buildTar(t *testing.T, entries []tarEntry) *tar.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Linkname: e.linkname,
			Size:     int64(len(e.content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", e.name, err)
		}
		if e.content != "" {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatalf("Write(%s): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar writer: %v", err)
	}
	return tar.NewReader(&buf)
}

// TestExtractTarStream_DirModeAppliedDespiteUmask odtwarza dokladnie sytuacje
// z buga: proces ma restrykcyjny umask (0022, typowy dla root spod sudo na
// runnerach CI), a warstwa OCI zawiera jawny wpis katalogu /tmp z trybem
// 01777 (sticky bit + zapis dla wszystkich, standard dla /tmp w kazdym
// rootfs Debiana). Po rozpakowaniu katalog docelowy MUSI miec dokladnie
// 01777, mimo umask -- inaczej apt-get (ktory jako nieuprzywilejowany
// uzytkownik _apt zapisuje pliki tymczasowe w /tmp) dostaje "Permission
// denied" przy proba mkstemp, dokladnie jak w logu CI.
func TestExtractTarStream_DirModeAppliedDespiteUmask(t *testing.T) {
	oldUmask := syscall.Umask(0o022)
	defer syscall.Umask(oldUmask)

	destDir := t.TempDir()
	tr := buildTar(t, []tarEntry{
		{name: "tmp/", typeflag: tar.TypeDir, mode: 0o1777},
	})

	if err := extractTarStream(tr, destDir); err != nil {
		t.Fatalf("extractTarStream: %v", err)
	}

	fi, err := os.Stat(filepath.Join(destDir, "tmp"))
	if err != nil {
		t.Fatalf("Stat tmp: %v", err)
	}
	want := tarModeToFileMode(0o1777)
	got := fi.Mode() & (os.ModePerm | os.ModeSticky | os.ModeSetuid | os.ModeSetgid)
	if got != want {
		t.Fatalf("tryb /tmp po rozpakowaniu = %v (%o), chcialem %v (umask ukradl bit zapisu dla 'innych' -- "+
			"dokladnie ten blad powodowal 'Unable to mkstemp ... Permission denied' w apt-get)", got, fi.Mode().Perm(), want)
	}
}

// TestExtractTarStream_DirModeAppliedEvenIfCreatedEarlierAsParent odtwarza
// druga czesc buga: jesli jakis plik WEWNATRZ /tmp zostalby przetworzony
// PRZED wlasnym wpisem katalogu /tmp w strumieniu tar, ensureParentDir
// utworzylby /tmp na sztywno z trybem 0o755 (bez sticky bitu i bez zapisu
// dla innych). Nastepnie wlasciwy wpis katalogu /tmp (z trybem 01777) MUSI
// i tak wymusic poprawny tryb, mimo ze katalog juz istnial (os.MkdirAll
// samo w sobie by tego nie zrobilo -- nie zmienia trybu istniejacych
// katalogow).
func TestExtractTarStream_DirModeAppliedEvenIfCreatedEarlierAsParent(t *testing.T) {
	destDir := t.TempDir()
	tr := buildTar(t, []tarEntry{
		// plik wewnatrz tmp/ PRZED wpisem samego katalogu tmp/
		{name: "tmp/apt.sig.XXXX", typeflag: tar.TypeReg, mode: 0o644, content: "x"},
		// dopiero teraz wlasciwy wpis katalogu, z docelowym trybem 01777
		{name: "tmp/", typeflag: tar.TypeDir, mode: 0o1777},
	})

	if err := extractTarStream(tr, destDir); err != nil {
		t.Fatalf("extractTarStream: %v", err)
	}

	fi, err := os.Stat(filepath.Join(destDir, "tmp"))
	if err != nil {
		t.Fatalf("Stat tmp: %v", err)
	}
	want := tarModeToFileMode(0o1777)
	got := fi.Mode() & (os.ModePerm | os.ModeSticky | os.ModeSetuid | os.ModeSetgid)
	if got != want {
		t.Fatalf("tryb /tmp po rozpakowaniu = %v (%o), chcialem %v (katalog zostal utworzony wczesniej "+
			"przez ensureParentDir z 0o755 i nigdy nie zostal skorygowany)", got, fi.Mode().Perm(), want)
	}
}

// TestExtractTarStream_PreservesSetuidBit odtwarza scenariusz krytyczny dla
// /usr/bin/sudo: plik z bitem setuid (0o4755, jak prawdziwy sudo na
// Debianie) MUSI zachowac ten bit po przejsciu przez pakowanie+rozpakowanie
// warstwy OCI. Naiwna konwersja os.FileMode(hdr.Mode) (stan sprzed poprawki)
// tego bitu nie ustawia w ogole -- sudo wygladaloby na obecne w systemie
// plikow, ale faktycznie nie dzialaloby dla zwyklych uzytkownikow.
func TestExtractTarStream_PreservesSetuidBit(t *testing.T) {
	destDir := t.TempDir()
	tr := buildTar(t, []tarEntry{
		{name: "usr/bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "usr/bin/sudo", typeflag: tar.TypeReg, mode: 0o4755, content: "fake-elf"},
	})

	if err := extractTarStream(tr, destDir); err != nil {
		t.Fatalf("extractTarStream: %v", err)
	}

	fi, err := os.Stat(filepath.Join(destDir, "usr", "bin", "sudo"))
	if err != nil {
		t.Fatalf("Stat sudo: %v", err)
	}
	if fi.Mode()&os.ModeSetuid == 0 {
		t.Fatalf("bit setuid zniknal po rozpakowaniu (tryb = %v, %o) -- sudo bylby zainstalowany "+
			"ale nie dzialalby dla zwyklych uzytkownikow", fi.Mode(), fi.Mode().Perm())
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("uprawnienia sudo = %o, chcialem 0755 (poza bitem setuid)", fi.Mode().Perm())
	}
}

func TestExtractTarStream_RegularFileAndSymlink(t *testing.T) {
	destDir := t.TempDir()
	tr := buildTar(t, []tarEntry{
		{name: "etc/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "etc/motd", typeflag: tar.TypeReg, mode: 0o644, content: "hello\n"},
		{name: "usr/bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "usr/bin/foo", typeflag: tar.TypeSymlink, linkname: "bar"},
	})

	if err := extractTarStream(tr, destDir); err != nil {
		t.Fatalf("extractTarStream: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "etc", "motd"))
	if err != nil {
		t.Fatalf("ReadFile motd: %v", err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("motd = %q, chcialem 'hello\\n'", string(content))
	}

	link, err := os.Readlink(filepath.Join(destDir, "usr", "bin", "foo"))
	if err != nil {
		t.Fatalf("Readlink foo: %v", err)
	}
	if link != "bar" {
		t.Fatalf("symlink foo -> %q, chcialem 'bar'", link)
	}
}

func TestExtractTarStream_Whiteout(t *testing.T) {
	destDir := t.TempDir()
	// symuluj plik z poprzedniej warstwy
	if err := os.MkdirAll(filepath.Join(destDir, "usr", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr", "bin", "stary"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tr := buildTar(t, []tarEntry{
		{name: "usr/bin/.wh.stary", typeflag: tar.TypeReg, mode: 0o644},
	})

	if err := extractTarStream(tr, destDir); err != nil {
		t.Fatalf("extractTarStream: %v", err)
	}

	if _, err := os.Stat(filepath.Join(destDir, "usr", "bin", "stary")); !os.IsNotExist(err) {
		t.Fatalf("plik 'stary' powinien zostac usuniety przez whiteout, stat err = %v", err)
	}
}
