package download

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// buildTestIsolatorArchive tworzy w pamieci archiwum .tar.gz z podanymi
// wpisami (nazwa -> zawartosc), niektore ewentualnie pod katalogiem
// posrednim (np. "bin/isolator") -- zeby przetestowac ze
// extractAllToDir splaszcza sciezki.
func buildTestIsolatorArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("zapis naglowka tar dla %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("zapis danych tar dla %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("zamkniecie tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("zamkniecie gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractAllToDir_FlattensAndExtractsRegularFiles(t *testing.T) {
	archive := buildTestIsolatorArchive(t, map[string][]byte{
		"bin/isolator":        []byte("\x7fELF-fake-isolator-binary"),
		"bin/isolator-daemon": []byte("\x7fELF-fake-daemon-binary"),
	})

	destDir := t.TempDir()
	names, err := extractAllToDir(archive, destDir)
	if err != nil {
		t.Fatalf("extractAllToDir: %v", err)
	}

	wantNames := map[string]bool{"isolator": true, "isolator-daemon": true}
	if len(names) != len(wantNames) {
		t.Fatalf("names = %v, chcialem dokladnie %v", names, wantNames)
	}
	for _, n := range names {
		if !wantNames[n] {
			t.Errorf("nieoczekiwana nazwa %q w wyniku", n)
		}
		info, err := os.Stat(filepath.Join(destDir, n))
		if err != nil {
			t.Fatalf("stat %s: %v", n, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s nie ma bitu wykonywalnosci (chmod a+x)", n)
		}
	}
}

func TestExtractAllToDir_EmptyArchiveReturnsEmptyNames(t *testing.T) {
	archive := buildTestIsolatorArchive(t, map[string][]byte{})
	destDir := t.TempDir()
	names, err := extractAllToDir(archive, destDir)
	if err != nil {
		t.Fatalf("extractAllToDir: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names = %v, chcialem pusta liste", names)
	}
}

func TestIsolatorReleaseAssetURL(t *testing.T) {
	got := isolatorReleaseAssetURL("v0.3.0", isolatorAssetName)
	want := "https://github.com/HackerOS-Linux-System/Isolator/releases/download/v0.3.0/isolator.tar.gz"
	if got != want {
		t.Errorf("isolatorReleaseAssetURL = %q, chcialem %q", got, want)
	}
}

func TestReIsolatorReleaseTag_MatchesGitHubReleasesHTML(t *testing.T) {
	html := []byte(`<a href="/HackerOS-Linux-System/Isolator/releases/tag/v0.3.0">v0.3.0</a>`)
	m := reIsolatorReleaseTag.FindSubmatch(html)
	if m == nil {
		t.Fatal("oczekiwano dopasowania tagu wydania")
	}
	if string(m[1]) != "v0.3.0" {
		t.Errorf("tag = %q, chcialem v0.3.0", string(m[1]))
	}
}
