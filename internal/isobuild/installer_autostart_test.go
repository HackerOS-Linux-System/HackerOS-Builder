package isobuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteInstallerAutostart_ServiceBased(t *testing.T) {
	root := t.TempDir()
	// pozostalosci po starym mechanizmie
	os.MkdirAll(filepath.Join(root, "root"), 0o755)
	os.WriteFile(filepath.Join(root, "root", ".bash_profile"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "etc/systemd/system"), 0o755)
	os.Symlink("/lib/systemd/system/multi-user.target", filepath.Join(root, "etc/systemd/system/default.target"))

	if err := writeInstallerAutostart(root); err != nil {
		t.Fatal(err)
	}

	unit, err := os.ReadFile(filepath.Join(root, "etc/systemd/system/hackeros-installer.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ConditionKernelCommandLine=boot=live", "Conflicts=getty@tty1.service", "hackeros-installer-session"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit bez %q", want)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "etc/systemd/system/multi-user.target.wants/hackeros-installer.service")); err != nil {
		t.Errorf("usluga nie jest enabled: %v", err)
	}
	for _, f := range []string{"usr/local/sbin/hackeros-installer-session", "usr/local/sbin/hackeros-installer-xinit"} {
		fi, err := os.Stat(filepath.Join(root, f))
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("%s brak lub niewykonywalny: %v", f, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "root/.bash_profile")); err == nil {
		t.Errorf("stary .bash_profile powinien zniknac")
	}
	if _, err := os.Lstat(filepath.Join(root, "etc/systemd/system/default.target")); err == nil {
		t.Errorf("stary override default.target powinien zniknac")
	}
}

func TestVerifyInstaller_ReportsMissing(t *testing.T) {
	err := verifyInstaller(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "/usr/bin/calamares") {
		t.Fatalf("oczekiwano bledu z lista brakow, jest: %v", err)
	}
}
