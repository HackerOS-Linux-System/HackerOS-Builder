package isobuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteXorrisoIsoLevelWrapper_ContentAndExecutable(t *testing.T) {
	dir := t.TempDir()

	wrapperPath, err := writeXorrisoIsoLevelWrapper(dir)
	if err != nil {
		t.Fatalf("writeXorrisoIsoLevelWrapper: %v", err)
	}

	fi, err := os.Stat(wrapperPath)
	if err != nil {
		t.Fatalf("Stat wrapper: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("wrapper %s nie jest wykonywalny (tryb %v)", wrapperPath, fi.Mode())
	}

	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("ReadFile wrapper: %v", err)
	}
	got := string(content)

	// Kluczowy wymog: "-compliance iso_9660_level=3" MUSI byc PIERWSZYM
	// argumentem przekazywanym do prawdziwego xorriso -- dokladnie to
	// zostalo zweryfikowane recznie (plik testowy > 4 GiB, prawdziwy
	// grub-mkrescue) jako jedyny sposob zeby ta opcja faktycznie zadzialala
	// (xorriso przetwarza polecenia sekwencyjnie; ustawiona PO tym jak
	// grub-mkrescue juz doda pliki przez wlasne "-map" nie ma zadnego
	// efektu, mimo poprawnej skladni).
	if !strings.Contains(got, "-compliance iso_9660_level=3 \"$@\"") {
		t.Fatalf("wrapper nie zawiera oczekiwanego wstrzykniecia '-compliance iso_9660_level=3' "+
			"jako pierwszego argumentu (tresc: %q)", got)
	}
	if !strings.HasPrefix(got, "#!/bin/sh\n") {
		t.Fatalf("wrapper nie zaczyna sie od poprawnego shebanga (tresc: %q)", got)
	}

	wantWrapperPath := filepath.Join(dir, "xorriso-iso-level-wrapper.sh")
	if wrapperPath != wantWrapperPath {
		t.Fatalf("wrapperPath = %q, chcialem %q", wrapperPath, wantWrapperPath)
	}
}

func TestShellQuote_HandlesEmbeddedSingleQuotes(t *testing.T) {
	got := shellQuote(`/path/with 'quote/xorriso`)
	want := `'/path/with '\''quote/xorriso'`
	if got != want {
		t.Fatalf("shellQuote = %q, chcialem %q", got, want)
	}
}

func TestShellQuote_NoSpecialChars(t *testing.T) {
	got := shellQuote("/usr/bin/xorriso")
	want := "'/usr/bin/xorriso'"
	if got != want {
		t.Fatalf("shellQuote = %q, chcialem %q", got, want)
	}
}
