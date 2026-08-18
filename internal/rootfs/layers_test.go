package rootfs

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
}

func TestSnapshotTree_CapturesFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "etc", "hostname"), "hackeros\n")
	writeTestFile(t, filepath.Join(root, "usr", "bin", "true"), "binary")

	snap, err := snapshotTree(root)
	if err != nil {
		t.Fatalf("snapshotTree: %v", err)
	}
	for _, want := range []string{"etc", "etc/hostname", "usr", "usr/bin", "usr/bin/true"} {
		if _, ok := snap[want]; !ok {
			t.Errorf("snapshot brakuje sciezki %q", want)
		}
	}
}

func TestDiffSnapshots_DetectsAddedChangedRemoved(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "keep.txt"), "niezmieniony")
	writeTestFile(t, filepath.Join(root, "remove.txt"), "zniknie")
	before, err := snapshotTree(root)
	if err != nil {
		t.Fatalf("snapshotTree before: %v", err)
	}

	if err := os.Remove(filepath.Join(root, "remove.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Zmieniamy tresc pliku "keep.txt" (rozmiar sie zmienia, wiec wykryte
	// jako "changed" nawet bez kontrolowania mtime co do sekundy).
	writeTestFile(t, filepath.Join(root, "keep.txt"), "zmieniony, dluzszy tekst")
	writeTestFile(t, filepath.Join(root, "new.txt"), "nowy plik")

	after, err := snapshotTree(root)
	if err != nil {
		t.Fatalf("snapshotTree after: %v", err)
	}

	changed, removed := diffSnapshots(before, after)

	mustContain := func(list []string, want string) {
		t.Helper()
		for _, v := range list {
			if v == want {
				return
			}
		}
		t.Errorf("lista %v nie zawiera %q", list, want)
	}
	mustContain(changed, "new.txt")
	mustContain(changed, "keep.txt")
	mustContain(removed, "remove.txt")

	for _, v := range changed {
		if v == "remove.txt" {
			t.Error("remove.txt nie powinno byc w changed")
		}
	}
}

func TestPruneRedundantRemovals_KeepsOnlyParent(t *testing.T) {
	removed := []string{
		"usr/share/doc",
		"usr/share/doc/pkg/README",
		"usr/share/doc/pkg/changelog.gz",
		"var/log/apt.log",
	}
	pruned := pruneRedundantRemovals(removed)

	want := map[string]bool{"usr/share/doc": true, "var/log/apt.log": true}
	if len(pruned) != len(want) {
		t.Fatalf("pruned = %v, chcialem dokladnie %v", pruned, want)
	}
	for _, p := range pruned {
		if !want[p] {
			t.Errorf("nieoczekiwana sciezka %q w pruned", p)
		}
	}
}

func TestWriteIncrementalLayer_EmptyDiffWritesNothing(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(t.TempDir(), "layer.tar.gz")

	wrote, err := writeIncrementalLayer(root, nil, nil, dest)
	if err != nil {
		t.Fatalf("writeIncrementalLayer: %v", err)
	}
	if wrote {
		t.Error("wrote powinno byc false dla pustego diff")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("plik warstwy nie powinien powstac dla pustego diff")
	}
}

func TestWriteIncrementalLayer_ChangedAndRemoved(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "etc", "hostname"), "hackeros\n")

	dest := filepath.Join(t.TempDir(), "layer.tar.gz")
	wrote, err := writeIncrementalLayer(root, []string{"etc", "etc/hostname"}, []string{"var/removed.txt"}, dest)
	if err != nil {
		t.Fatalf("writeIncrementalLayer: %v", err)
	}
	if !wrote {
		t.Fatal("wrote powinno byc true")
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)

	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names = append(names, hdr.Name)
	}

	foundContent := false
	foundWhiteout := false
	for _, n := range names {
		if n == "etc/hostname" {
			foundContent = true
		}
		if n == "var/.wh.removed.txt" {
			foundWhiteout = true
		}
	}
	if !foundContent {
		t.Errorf("warstwa nie zawiera etc/hostname, zawiera: %v", names)
	}
	if !foundWhiteout {
		t.Errorf("warstwa nie zawiera whiteout var/.wh.removed.txt, zawiera: %v", names)
	}
}

func TestFsEntry_ModTimeComparisonDetectsChange(t *testing.T) {
	// Sanity check swiadomej heurystyki diffSnapshots: dwa wpisy o tym
	// samym rozmiarze/mode ale INNYM mtime maja byc wykryte jako zmiana.
	a := fsEntry{mode: 0o644, size: 10, modTime: time.Unix(1000, 0)}
	b := fsEntry{mode: 0o644, size: 10, modTime: time.Unix(2000, 0)}
	before := map[string]fsEntry{"f": a}
	after := map[string]fsEntry{"f": b}
	changed, removed := diffSnapshots(before, after)
	if len(removed) != 0 {
		t.Errorf("removed = %v, chcialem pusta", removed)
	}
	if len(changed) != 1 || changed[0] != "f" {
		t.Errorf("changed = %v, chcialem [\"f\"]", changed)
	}
}
