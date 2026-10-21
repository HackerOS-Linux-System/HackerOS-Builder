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
	for _, f := range []string{"usr/local/sbin/hackeros-installer-session", "usr/local/sbin/hackeros-installer-xinit", "usr/local/sbin/hackeros-installer-diag"} {
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
	err := verifyInstaller(t.TempDir(), "amd64")
	if err == nil || !strings.Contains(err.Error(), "/usr/bin/calamares") {
		t.Fatalf("oczekiwano bledu z lista brakow, jest: %v", err)
	}
}

// Regresja: branding.desc wskazuje logo.png/welcome.png/show.qml -- musza
// istniec, inaczej Calamares nie zaladuje brandingu i instalator nie wstanie.
func TestWriteCalamaresConfig_GeneratesBrandingAssets(t *testing.T) {
	for _, v := range []InstallerVariant{InstallerVariantDefault, InstallerVariantCybersecurity} {
		root := t.TempDir()
		if err := writeCalamaresConfig(root, v, ""); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "etc/calamares/branding/hackeros")
		desc, _ := os.ReadFile(filepath.Join(dir, "branding.desc"))
		for _, f := range []string{"logo.png", "welcome.png", "show.qml"} {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil || len(b) == 0 {
				t.Fatalf("%s (%s): brak lub pusty: %v", f, v, err)
			}
			if strings.HasSuffix(f, ".png") && string(b[1:4]) != "PNG" {
				t.Fatalf("%s nie jest PNG", f)
			}
			if !strings.Contains(string(desc), f) {
				t.Errorf("branding.desc nie wspomina %s", f)
			}
		}
	}
}

func TestInstallerScripts_SoftwareRenderingAndLogging(t *testing.T) {
	if !strings.Contains(installerXinitScript, "QT_QUICK_BACKEND=software") ||
		!strings.Contains(installerXinitScript, "hackeros-installer-calamares.log") {
		t.Fatal("xinit script bez renderowania programowego/logu")
	}
	if !strings.Contains(installerServiceUnit, "plymouth quit") {
		t.Fatal("unit nie zamyka plymouth")
	}
}

func TestWriteCalamaresConfig_UsesProjectBranding(t *testing.T) {
	proj := t.TempDir()
	src := filepath.Join(proj, "config/includes.chroot_after_packages/etc/calamares/branding/debian")
	os.MkdirAll(src, 0o755)
	logo, welcome, err := brandingPNGs(InstallerVariantDefault)
	if err != nil {
		t.Fatal(err)
	}
	// rozpoznawalne zawartosci: PNG + doklejony znacznik (DecodeConfig ignoruje ogon)
	os.WriteFile(filepath.Join(src, "debian-logo.png"), append(append([]byte{}, logo...), []byte("LOGO-PROJEKTU")...), 0o644)
	os.WriteFile(filepath.Join(src, "welcome.png"), append(append([]byte{}, welcome...), []byte("BANER-PROJEKTU")...), 0o644)

	root := t.TempDir()
	if err := writeCalamaresConfig(root, InstallerVariantDefault, proj); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "etc/calamares/branding/hackeros")
	l, _ := os.ReadFile(filepath.Join(dir, "logo.png"))
	w, _ := os.ReadFile(filepath.Join(dir, "welcome.png"))
	if !strings.HasSuffix(string(l), "LOGO-PROJEKTU") || !strings.HasSuffix(string(w), "BANER-PROJEKTU") {
		t.Fatal("builder nie uzyl obrazow z projektu")
	}

	// kopia zapasowa config/installer-branding (po usunieciu etc/calamares)
	os.RemoveAll(filepath.Join(proj, "config/includes.chroot_after_packages"))
	bk := filepath.Join(proj, "config/installer-branding")
	os.MkdirAll(bk, 0o755)
	os.WriteFile(filepath.Join(bk, "debian-logo.png"), append(append([]byte{}, logo...), []byte("ZAPAS")...), 0o644)
	root2 := t.TempDir()
	if err := writeCalamaresConfig(root2, InstallerVariantDefault, proj); err != nil {
		t.Fatal(err)
	}
	l2, _ := os.ReadFile(filepath.Join(root2, "etc/calamares/branding/hackeros/logo.png"))
	if !strings.HasSuffix(string(l2), "ZAPAS") {
		t.Fatal("brak fallbacku do config/installer-branding")
	}
}

func TestInstaller_InputDevicesHandled(t *testing.T) {
	if !strings.Contains(installerServiceUnit, "systemd-udev-trigger.service") ||
		!strings.Contains(installerServiceUnit, "udevadm settle") {
		t.Fatal("usluga musi czekac na udev (inaczej kursor sie nie rusza)")
	}
	if !strings.Contains(installerSessionScript, "udevadm trigger --subsystem-match=input") {
		t.Fatal("sesja musi ponowic zdarzenia input przed startem X")
	}
	root := t.TempDir()
	if err := writeInstallerAutostart(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/X11/xorg.conf.d/40-hackeros-input.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestCalamaresMountConf_MountsProcSysDevRun(t *testing.T) {
	for _, m := range []string{"mountPoint: /proc", "mountPoint: /sys", "mountPoint: /dev", "mountPoint: /run"} {
		if !strings.Contains(calamaresMountConf, m) {
			t.Errorf("mount.conf bez %q -- grub-install w chroocie nie zadziala", m)
		}
	}
	if strings.Contains(calamaresMountConf, "extraMounts: []") {
		t.Error("extraMounts: [] wylacza montowanie /proc,/sys,/dev,/run")
	}
}

func TestBootloaderPackages(t *testing.T) {
	got := strings.Join(bootloaderPackages("amd64"), " ")
	for _, want := range []string{"grub-efi-amd64-bin", "grub-pc-bin", "efibootmgr", "grub2-common"} {
		if !strings.Contains(got, want) {
			t.Errorf("brak %s w %s", want, got)
		}
	}
}

func TestVerifyInstaller_AcceptsEitherBinDir(t *testing.T) {
	root := t.TempDir()
	for _, f := range append(append([]string{}, installerRequiredFiles...),
		"usr/bin/grub-install", "usr/bin/grub-mkconfig", "usr/bin/efibootmgr",
		"usr/lib/grub/x86_64-efi/modinfo.sh", "usr/lib/grub/i386-pc/modinfo.sh") {
		p := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o755)
	}
	if err := verifyInstaller(root, "amd64"); err != nil {
		t.Fatalf("binarki w /usr/bin powinny wystarczyc: %v", err)
	}
}

func TestInstaller_BootfixFinishedAndWindowRules(t *testing.T) {
	root := t.TempDir()
	if err := writeCalamaresConfig(root, InstallerVariantDefault, ""); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("brak %s: %v", rel, err)
		}
		return string(b)
	}
	if !strings.Contains(read("etc/calamares/modules/finished.conf"), "restartNowChecked: true") {
		t.Error("finished.conf bez restartu po zakonczeniu")
	}
	bf := read("usr/local/sbin/hackeros-bootfix")
	for _, want := range []string{"grub-mkconfig -o", "configfile", "exit 1"} {
		if !strings.Contains(bf, want) {
			t.Errorf("bootfix bez %q", want)
		}
	}
	if !strings.Contains(read("etc/calamares/modules/shellprocess.conf"), "hackeros-bootfix") {
		t.Error("shellprocess nie wywoluje bootfix")
	}
	if strings.Contains(installerOpenboxRC, `class="*"`) {
		t.Error("regula fullscreen nie moze dotyczyc wszystkich okien")
	}
	if strings.Contains(installerSessionScript, `if [ "$XRC" -eq 0 ]; then exit 0; fi`) {
		t.Error("po zamknieciu Calamares nie wolno konczyc uslugi (czarny ekran)")
	}
}

func TestInstaller_AutologinDoneMarkerAndLiveUserRemoval(t *testing.T) {
	root := t.TempDir()
	// bez menedzera logowania: brak modulu displaymanager w sekwencji
	if err := writeCalamaresConfig(root, InstallerVariantDefault, ""); err != nil {
		t.Fatal(err)
	}
	st, _ := os.ReadFile(filepath.Join(root, "etc/calamares/settings.conf"))
	if strings.Contains(string(st), "- displaymanager") {
		t.Error("displaymanager bez zainstalowanego DM")
	}
	for _, want := range []string{"shellprocess@done", "shellprocess_done.conf"} {
		if !strings.Contains(string(st), want) {
			t.Errorf("settings.conf bez %q", want)
		}
	}
	// z SDDM: modul jest w sekwencji po users i ma config
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, "usr/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root2, "usr/bin/sddm"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeCalamaresConfig(root2, InstallerVariantDefault, ""); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.ReadFile(filepath.Join(root2, "etc/calamares/settings.conf"))
	if !strings.Contains(string(st2), "- users\n      - displaymanager") {
		t.Errorf("brak displaymanager po users:\n%s", st2)
	}
	dmc, err := os.ReadFile(filepath.Join(root2, "etc/calamares/modules/displaymanager.conf"))
	if err != nil || !strings.Contains(string(dmc), "- sddm") {
		t.Errorf("displaymanager.conf: %v %s", err, dmc)
	}
	// skrypt usuwania konta live + wywolanie w shellprocess
	if !strings.Contains(removeLiveUserScript, "userdel") {
		t.Error("osadzony remove-live-user.sh pusty")
	}
	sp, _ := os.ReadFile(filepath.Join(root, "etc/calamares/modules/shellprocess.conf"))
	if !strings.Contains(string(sp), "remove-live-user") {
		t.Error("shellprocess nie usuwa konta live")
	}
	// odliczanie i wylaczenie po instalacji bez restartu
	for _, want := range []string{"hackeros-install-done", "Komputer wylaczy sie za", "systemctl poweroff"} {
		if !strings.Contains(installerSessionScript, want) {
			t.Errorf("sesja bez %q", want)
		}
	}
}

func TestWriteImmutability_HammerFlags(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr/bin/hammer"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeImmutability(root); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hammer/db/.immutable-enabled", "hammer/db/.immutable-backend"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Error(err)
		}
	}
}
