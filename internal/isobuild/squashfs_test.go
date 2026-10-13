package isobuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regresja: filesystem.squashfs MUSI zawierac /dev (z /dev/console), /proc,
// /sys, /run i /tmp -- ale puste. Wczesniej "-e dev proc sys run tmp"
// wycinalo je w calosci, co konczylo sie panika "Attempted to kill init!".
func TestBuildSquashfs_KeepsEmptyRuntimeDirsAndDevConsole(t *testing.T) {
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("brak mksquashfs")
	}
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("brak unsquashfs")
	}
	if os.Geteuid() != 0 {
		t.Skip("potrzebny root (mknod /dev/console)")
	}

	root := t.TempDir()
	for _, d := range []string{"etc", "usr", "proc", "sys", "run", "tmp", "dev"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"etc/os-release", "proc/zostanie-wykluczony", "tmp/x", "run/x", "sys/x"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	iso := t.TempDir()
	if err := buildSquashfs(root, iso); err != nil {
		t.Fatalf("buildSquashfs: %v", err)
	}

	out, err := exec.Command("unsquashfs", "-l", filepath.Join(iso, "live", "filesystem.squashfs")).CombinedOutput()
	if err != nil {
		t.Fatalf("unsquashfs: %v\n%s", err, out)
	}
	list := string(out)
	for _, want := range []string{"squashfs-root/dev/console", "squashfs-root/proc", "squashfs-root/sys",
		"squashfs-root/run", "squashfs-root/tmp", "squashfs-root/etc/os-release"} {
		if !strings.Contains(list, want+"\n") {
			t.Errorf("w squashfs brakuje %q:\n%s", want, list)
		}
	}
	for _, bad := range []string{"proc/zostanie-wykluczony", "tmp/x", "run/x", "sys/x"} {
		if strings.Contains(list, bad+"\n") {
			t.Errorf("w squashfs nie powinno byc %q", bad)
		}
	}
}

func TestWriteGrubConfig_HasDebugEntry(t *testing.T) {
	dir := t.TempDir()
	if err := writeGrubConfig(dir, "HackerOS", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "boot", "grub", "grub.cfg"))
	cfg := string(b)
	if strings.Count(cfg, "menuentry") != 3 || !strings.Contains(cfg, "boot=live") || !strings.Contains(cfg, "debug") {
		t.Fatalf("niepoprawny grub.cfg:\n%s", cfg)
	}
	if !strings.Contains(cfg, `menuentry "HackerOS" {`) || strings.Contains(cfg, "HACKEROS") {
		t.Fatalf("tytul menu powinien byc \"HackerOS\":\n%s", cfg)
	}
	if strings.Count(cfg, "systemd.unit=multi-user.target") != 3 {
		t.Fatalf("z instalatorem kazdy wpis ma wymuszac multi-user.target:\n%s", cfg)
	}
	if strings.Contains(cfg, " splash") || strings.Count(cfg, "plymouth.enable=0") != 3 {
		t.Fatalf("z instalatorem plymouth ma byc wylaczony:\n%s", cfg)
	}
	dir2 := t.TempDir()
	if err := writeGrubConfig(dir2, "HackerOS", false); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(dir2, "boot", "grub", "grub.cfg"))
	if strings.Contains(string(b2), "systemd.unit") {
		t.Fatalf("bez instalatora nie wolno zmieniac targetu")
	}
}
