package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyDirToRootfs_OverwritesExistingSymlink odtwarza dokladnie sytuacje
// z buga: pakiet (np. adwaita-icon-theme) juz utworzyl symlink
// usr/share/icons/Adwaita/cursors/arrow -> default w rootfs, a
// includes.chroot_after_packages ma wlasny plik/symlink pod ta sama
// sciezka, ktory ma go NADPISAC (jak w live-build). Przed poprawka
// os.Symlink konczylo sie bledem "file exists".
func TestCopyDirToRootfs_OverwritesExistingSymlink(t *testing.T) {
	rootfs := t.TempDir()
	cursorsDir := filepath.Join(rootfs, "usr", "share", "icons", "Adwaita", "cursors")
	if err := os.MkdirAll(cursorsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := filepath.Join(cursorsDir, "arrow")
	if err := os.Symlink("default", existing); err != nil {
		t.Fatalf("utworzenie istniejacego symlinku (symulacja pakietu): %v", err)
	}

	// includes.chroot_after_packages/usr/share/icons/Adwaita/cursors/arrow
	// -> left_ptr (inny cel, zeby latwo sprawdzic ze nadpisanie zadzialalo)
	src := t.TempDir()
	srcCursorsDir := filepath.Join(src, "usr", "share", "icons", "Adwaita", "cursors")
	if err := os.MkdirAll(srcCursorsDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.Symlink("left_ptr", filepath.Join(srcCursorsDir, "arrow")); err != nil {
		t.Fatalf("utworzenie symlinku zrodlowego: %v", err)
	}

	b := &Builder{RootfsDir: rootfs}
	if err := b.copyDirToRootfs(src); err != nil {
		t.Fatalf("copyDirToRootfs: %v (dokladnie ten blad wystapil w CI: "+
			"'symlink default .../cursors/arrow: file exists')", err)
	}

	got, err := os.Readlink(existing)
	if err != nil {
		t.Fatalf("Readlink po kopiowaniu: %v", err)
	}
	if got != "left_ptr" {
		t.Fatalf("symlink arrow wskazuje na %q, chcialem 'left_ptr' (nadpisanie sie nie udalo)", got)
	}
}

// TestCopyDirToRootfs_OverwritesExistingRegularFile sprawdza analogiczny
// przypadek dla zwyklego pliku (np. pakiet zainstalowal domyslny config,
// a includes.chroot_after_packages ma go nadpisac wlasna wersja).
func TestCopyDirToRootfs_OverwritesExistingRegularFile(t *testing.T) {
	rootfs := t.TempDir()
	destDir := filepath.Join(rootfs, "etc")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	destFile := filepath.Join(destDir, "motd")
	if err := os.WriteFile(destFile, []byte("domyslny motd z pakietu\n"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "etc", "motd"), nil, 0o644); err != nil {
		// upewnij sie ze katalog etc/ istnieje w src
		os.MkdirAll(filepath.Join(src, "etc"), 0o755)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "motd"), []byte("wlasny motd HackerOS\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &Builder{RootfsDir: rootfs}
	if err := b.copyDirToRootfs(src); err != nil {
		t.Fatalf("copyDirToRootfs: %v", err)
	}

	got, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("ReadFile po kopiowaniu: %v", err)
	}
	if string(got) != "wlasny motd HackerOS\n" {
		t.Fatalf("motd = %q, chcialem 'wlasny motd HackerOS\\n' (nadpisanie sie nie udalo)", string(got))
	}
}

// TestCopyDirToRootfs_ConflictWithExistingDirIsError sprawdza ze proba
// nadpisania ISTNIEJACEGO KATALOGU plikiem/symlinkiem zwraca czytelny
// blad, zamiast po cichu rekurencyjnie kasowac caly katalog docelowy.
func TestCopyDirToRootfs_ConflictWithExistingDirIsError(t *testing.T) {
	rootfs := t.TempDir()
	conflictDir := filepath.Join(rootfs, "usr", "share", "weird")
	if err := os.MkdirAll(conflictDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// dodaj plik wewnatrz, zeby ewentualne cichuteńkie os.RemoveAll bylo
	// widoczne jako realna utrata danych, gdyby fix tego nie pilnowal
	if err := os.WriteFile(filepath.Join(conflictDir, "dontdelete.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "usr", "share"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "usr", "share", "weird"), []byte("plik zamiast katalogu"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &Builder{RootfsDir: rootfs}
	err := b.copyDirToRootfs(src)
	if err == nil {
		t.Fatal("oczekiwano bledu przy probie nadpisania katalogu plikiem, otrzymano nil")
	}

	// katalog konfliktowy i jego zawartosc MUSZA nadal istniec -- fix nie
	// moze cichaczem skasowac calego poddrzewa.
	if _, statErr := os.Stat(filepath.Join(conflictDir, "dontdelete.txt")); statErr != nil {
		t.Fatalf("plik wewnatrz konfliktowego katalogu zniknal (nie powinien): %v", statErr)
	}
}

func TestCopyFile_OverwritesExistingSymlinkTarget(t *testing.T) {
	dir := t.TempDir()

	// "gdzies-indziej" symuluje plik NIEZWIAZANY z dst, na ktory dst
	// (jako symlink) wskazuje przed nadpisaniem. Po naprawie nie powinien
	// zostac tkniety -- dst ma stac sie NOWYM plikiem regularnym, a nie
	// zapisem "przez" stary symlink.
	elsewhere := filepath.Join(dir, "gdzies-indziej")
	if err := os.WriteFile(elsewhere, []byte("nie ruszac"), 0o644); err != nil {
		t.Fatalf("write elsewhere: %v", err)
	}

	dst := filepath.Join(dir, "dst")
	if err := os.Symlink(elsewhere, dst); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("nowa tresc"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := copyFile(src, dst, 0o644); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	// elsewhere MUSI pozostac nietkniety
	elsewhereContent, err := os.ReadFile(elsewhere)
	if err != nil {
		t.Fatalf("ReadFile elsewhere: %v", err)
	}
	if string(elsewhereContent) != "nie ruszac" {
		t.Fatalf("elsewhere = %q, oczekiwano 'nie ruszac' (copyFile napisal przez stary symlink!)", string(elsewhereContent))
	}

	// dst musi byc teraz PLIKIEM REGULARNYM z nowa trescia
	fi, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("Lstat dst: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("dst nadal jest symlinkiem po copyFile -- oczekiwano zwyklego pliku")
	}
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(dstContent) != "nowa tresc" {
		t.Fatalf("dst = %q, oczekiwano 'nowa tresc'", string(dstContent))
	}
}
