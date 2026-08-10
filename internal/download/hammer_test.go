package download

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseChecksumsFile_FindsMatchingEntry(t *testing.T) {
	content := "abc123  oci-mode.tar.gz\ndef456  checksums.txt\n"
	hash, found := parseChecksumsFile(content, "oci-mode.tar.gz")
	if !found {
		t.Fatal("oczekiwano znalezienia wpisu dla oci-mode.tar.gz")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}

func TestParseChecksumsFile_BinaryModePrefix(t *testing.T) {
	// Format "sha256sum" w trybie binarnym dodaje prefiks '*' do nazwy pliku.
	content := "abc123 *oci-mode.tar.gz\n"
	hash, found := parseChecksumsFile(content, "oci-mode.tar.gz")
	if !found {
		t.Fatal("oczekiwano znalezienia wpisu z prefiksem '*'")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}

func TestParseChecksumsFile_NotFound(t *testing.T) {
	content := "abc123  inny-plik\n"
	_, found := parseChecksumsFile(content, "oci-mode.tar.gz")
	if found {
		t.Fatal("nie oczekiwano znalezienia wpisu dla nieistniejacej nazwy")
	}
}

func TestParseChecksumsFile_EmptyContent(t *testing.T) {
	_, found := parseChecksumsFile("", "oci-mode.tar.gz")
	if found {
		t.Fatal("nie oczekiwano znalezienia wpisu w pustej tresci")
	}
}

func TestParseChecksumsFile_IgnoresMalformedLines(t *testing.T) {
	content := "to jest zla linia z trzema slowami\nabc123  oci-mode.tar.gz\n"
	hash, found := parseChecksumsFile(content, "oci-mode.tar.gz")
	if !found {
		t.Fatal("oczekiwano znalezienia poprawnego wpisu pomimo zlej linii wczesniej")
	}
	if hash != "abc123" {
		t.Fatalf("oczekiwano hash=abc123, otrzymano %q", hash)
	}
}

// buildTestOciModeArchive tworzy w pamieci minimalne archiwum .tar.gz
// odpowiadajace layoutowi prawdziwego wydania hammer (oci-mode.tar.gz):
// dokladnie jeden plik o nazwie "hammer" w korzeniu archiwum.
func buildTestOciModeArchive(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: "hammer",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("zapis naglowka tar: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("zapis danych tar: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("zamkniecie tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("zamkniecie gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractHammerBinary_FindsSingleBinaryInRoot(t *testing.T) {
	want := []byte("\x7fELF-fake-hammer-binary-content")
	archive := buildTestOciModeArchive(t, want)

	got, err := extractHammerBinary(archive)
	if err != nil {
		t.Fatalf("extractHammerBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("zawartosc binarki nie zgadza sie: chcialem %q, otrzymalem %q", want, got)
	}
}

func TestExtractHammerBinary_MissingHammerFile(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "cos-innego", Mode: 0o644, Size: 3}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("zapis naglowka tar: %v", err)
	}
	if _, err := tw.Write([]byte("abc")); err != nil {
		t.Fatalf("zapis danych tar: %v", err)
	}
	tw.Close()
	gz.Close()

	_, err := extractHammerBinary(buf.Bytes())
	if err == nil {
		t.Fatal("oczekiwano bledu gdy archiwum nie zawiera pliku 'hammer'")
	}
}
