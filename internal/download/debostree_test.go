package download

import "testing"

func TestParseChecksumsFile_FindsMatchingEntry(t *testing.T) {
	content := "abc123  deb-ostree\ndef456  checksums.txt\n"
	hash, found := parseChecksumsFile(content, "deb-ostree")
	if !found {
		t.Fatal("oczekiwano znalezienia wpisu dla deb-ostree")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}

func TestParseChecksumsFile_BinaryModePrefix(t *testing.T) {
	// Format "sha256sum" w trybie binarnym dodaje prefiks '*' do nazwy pliku.
	content := "abc123 *deb-ostree\n"
	hash, found := parseChecksumsFile(content, "deb-ostree")
	if !found {
		t.Fatal("oczekiwano znalezienia wpisu z prefiksem '*'")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}

func TestParseChecksumsFile_NotFound(t *testing.T) {
	content := "abc123  inny-plik\n"
	_, found := parseChecksumsFile(content, "deb-ostree")
	if found {
		t.Fatal("nie oczekiwano znalezienia wpisu dla nieistniejacej nazwy")
	}
}

func TestParseChecksumsFile_EmptyContent(t *testing.T) {
	_, found := parseChecksumsFile("", "deb-ostree")
	if found {
		t.Fatal("nie oczekiwano znalezienia wpisu w pustej tresci")
	}
}

func TestParseChecksumsFile_IgnoresMalformedLines(t *testing.T) {
	content := "to jest zla linia z trzema slowami\nabc123  deb-ostree\n"
	hash, found := parseChecksumsFile(content, "deb-ostree")
	if !found {
		t.Fatal("oczekiwano znalezienia poprawnego wpisu pomimo zlej linii wczesniej")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}
