package isobuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteImmutability(t *testing.T) {
	root := t.TempDir()
	if err := writeImmutability(root); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(root, "usr/sbin/hackeros-immutable"))
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("skrypt niewykonywalny: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "etc/systemd/system/sysinit.target.wants/hackeros-immutable.service")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc/systemd/system/hackeros-immutable.service"))
	if !strings.Contains(string(b), "ConditionKernelCommandLine=!hackeros.mutable") {
		t.Error("brak wylaczenia awaryjnego")
	}
}

func TestInstallerXinit_NoXDGAutostart(t *testing.T) {
	for _, want := range []string{"XDG_CONFIG_DIRS=/etc/hackeros-installer/xdg", "plasma-welcome"} {
		if !strings.Contains(installerXinitScript, want) {
			t.Errorf("xinit bez %q", want)
		}
	}
	root := t.TempDir()
	if err := writeInstallerAutostart(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/hackeros-installer/xdg/openbox/autostart")); err != nil {
		t.Error(err)
	}
}
