package builder

import (
	"fmt"
	"hackeros-builder/src/config"
	"hackeros-builder/src/ui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Builder struct {
	ProjectDir string
	Cfg        *config.BuildConfig
	Release    bool
	workDir    string
	chroot     string
	binary     string
	output     string
}

func New(projectDir string, cfg *config.BuildConfig, release bool) *Builder {
	work := filepath.Join(projectDir, ".hb-work")
	return &Builder{
		ProjectDir: projectDir,
		Cfg:        cfg,
		Release:    release,
		workDir:    work,
		chroot:     filepath.Join(work, "chroot"),
		binary:     filepath.Join(work, "binary"),
		output:     filepath.Join(projectDir, "output"),
	}
}

func (b *Builder) Build() error {
	ui.Banner("HackerOS Builder — Tryb standalone")

	if os.Getuid() != 0 {
		return fmt.Errorf("tryb standalone wymaga uprawnień root (sudo)")
	}
	if err := checkDeps(); err != nil {
		return err
	}

	steps := []struct {
		label string
		fn    func() error
	}{
		{"Przygotowanie katalogów roboczych", b.prepareDirs},
		{"Bootstrap systemu (debootstrap)", b.bootstrap},
		{"Montowanie pseudo-filesystems", b.mountPseudo},
		{"Konfiguracja systemu", b.configureSystem},
		{"Aktualizacja repozytoriów (apt update)", b.updateApt},
		{"Instalacja pakietów bazowych", b.installBase},
		{"Instalacja pakietów użytkownika", b.installUserPackages},
		{"Tworzenie użytkownika live", b.createUser},
		{"Konfiguracja SDDM autologin", b.configureSddm},
		{"Uruchamianie hooków", b.runHooks},
		{"Kopiowanie plików includes", b.copyIncludes},
		{"Odmontowanie pseudo-filesystems", b.umountPseudo},
		{"Kompresja squashfs", b.buildSquashfs},
		{"Przygotowanie struktury ISO", b.prepareIso},
		{"Budowanie obrazu ISO (xorriso)", b.buildIso},
	}

	prog := ui.NewProgress(len(steps), "Inicjalizacja...")
	prog.Start()

	for _, step := range steps {
		prog.SetLabel(step.label)
		if err := step.fn(); err != nil {
			b.umountPseudo()
			prog.Fail(step.label)
			return fmt.Errorf("%s: %w", step.label, err)
		}
		prog.Update(step.label)
	}

	isoPath := filepath.Join(b.output, b.Cfg.IsoName()+".iso")
	prog.Finish("ISO zbudowane!")

	if stat, err := os.Stat(isoPath); err == nil {
		ui.Ok(fmt.Sprintf("ISO: %s%s%s (%.1f MB)",
			ui.Bold+ui.Cyan, isoPath, ui.Reset,
			float64(stat.Size())/1024/1024))
	}
	return nil
}

func (b *Builder) prepareDirs() error {
	for _, d := range []string{
		b.workDir, b.chroot, b.binary, b.output,
		filepath.Join(b.binary, "live"),
		filepath.Join(b.binary, "boot", "grub"),
		filepath.Join(b.binary, "EFI", "boot"),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) bootstrap() error {
	if _, err := os.Stat(filepath.Join(b.chroot, "bin")); err == nil {
		return nil
	}
	repos := strings.Join(b.Cfg.Repos(), ",")
	return run("debootstrap",
		"--arch="+b.Cfg.Arch(),
		"--components="+repos,
		"--include=live-boot,live-config,live-config-systemd,systemd-sysv,user-setup,sudo",
		b.Cfg.Release(), b.chroot, b.Cfg.Mirror(),
	)
}

func (b *Builder) mountPseudo() error {
	type mnt struct{ fs, src, dst string }
	for _, m := range []mnt{
		{"proc", "proc", "proc"},
		{"sysfs", "sysfs", "sys"},
		{"devtmpfs", "/dev", "dev"},
		{"devpts", "devpts", "dev/pts"},
	} {
		target := filepath.Join(b.chroot, m.dst)
		os.MkdirAll(target, 0755)
		if m.fs == "devtmpfs" {
			run("mount", "--bind", m.src, target)
		} else {
			run("mount", "-t", m.fs, m.src, target)
		}
	}
	return nil
}

func (b *Builder) umountPseudo() error {
	for _, dst := range []string{"dev/pts", "dev", "sys", "proc"} {
		run("umount", "-lf", filepath.Join(b.chroot, dst))
	}
	return nil
}

func (b *Builder) configureSystem() error {
	writeFile(filepath.Join(b.chroot, "etc", "hostname"), b.Cfg.Hostname()+"\n")
	writeFile(filepath.Join(b.chroot, "etc", "hosts"),
		fmt.Sprintf("127.0.0.1 localhost\n127.0.1.1 %s\n::1 localhost\n", b.Cfg.Hostname()))

	repos := strings.Join(b.Cfg.Repos(), " ")
	mirror := b.Cfg.Mirror()
	release := b.Cfg.Release()
	writeFile(filepath.Join(b.chroot, "etc", "apt", "sources.list"),
		fmt.Sprintf("deb %s %s %s\ndeb %s-security %s-security %s\ndeb %s %s-updates %s\n",
			mirror, release, repos,
			mirror, release, repos,
			mirror, release, repos))

	writeFile(filepath.Join(b.chroot, "etc", "locale.gen"),
		b.Cfg.Locale()+" UTF-8\nen_US.UTF-8 UTF-8\n")
	return nil
}

func (b *Builder) updateApt() error {
	return b.chrootRun("apt-get", "update", "-qq")
}

func (b *Builder) installBase() error {
	return b.chrootRun(
		"apt-get", "install", "-y", "-qq",
		"locales", "tzdata", b.Cfg.Kernel(),
		"live-boot", "live-config", "live-config-systemd",
		"user-setup", "systemd-sysv", "sudo",
		"network-manager", "curl", "wget",
	)
}

func (b *Builder) installUserPackages() error {
	pkgs := b.Cfg.Packages()
	if len(pkgs) == 0 {
		return nil
	}
	return b.chrootRun(append([]string{"apt-get", "install", "-y", "-qq"}, pkgs...)...)
}

func (b *Builder) createUser() error {
	username := b.Cfg.Username()
	password := b.Cfg.Password()

	result := exec.Command("chroot", b.chroot, "id", username)
	if result.Run() != nil {
		if err := b.chrootRun("useradd", "-m", "-s", "/bin/bash",
			"-G", "sudo,audio,video,cdrom,plugdev,netdev,bluetooth",
			username); err != nil {
			return err
		}
	}

	cmd := exec.Command("chroot", b.chroot, "chpasswd")
	cmd.Stdin = strings.NewReader(username + ":" + password + "\n")
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chpasswd: %w\n%s", err, out)
	}

	sudoersDir := filepath.Join(b.chroot, "etc", "sudoers.d")
	os.MkdirAll(sudoersDir, 0755)
	sudoersFile := filepath.Join(sudoersDir, "live-user")
	writeFile(sudoersFile, username+" ALL=(ALL) NOPASSWD:ALL\n")
	return os.Chmod(sudoersFile, 0440)
}

func (b *Builder) configureSddm() error {
	dir := filepath.Join(b.chroot, "etc", "sddm.conf.d")
	os.MkdirAll(dir, 0755)
	return writeFile(filepath.Join(dir, "autologin.conf"),
		fmt.Sprintf("[Autologin]\nUser=%s\nSession=plasmawayland\nRelogin=false\n",
			b.Cfg.Username()))
}

func (b *Builder) runHooks() error {
	hooksDir := filepath.Join(b.ProjectDir, "config", "hooks")
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		subDir := filepath.Join(hooksDir, entry.Name())
		if entry.IsDir() {
			subs, _ := os.ReadDir(subDir)
			for _, sub := range subs {
				if strings.HasSuffix(sub.Name(), ".hook.chroot") {
					b.runHook(filepath.Join(subDir, sub.Name()))
				}
			}
		} else if strings.HasSuffix(entry.Name(), ".hook.chroot") {
			b.runHook(filepath.Join(hooksDir, entry.Name()))
		}
	}
	return nil
}

func (b *Builder) runHook(hookPath string) error {
	name := filepath.Base(hookPath)
	dest := filepath.Join(b.chroot, "tmp", name)
	data, _ := os.ReadFile(hookPath)
	writeFile(dest, string(data))
	os.Chmod(dest, 0755)
	defer os.Remove(dest)
	return b.chrootRun("/tmp/" + name)
}

func (b *Builder) copyIncludes() error {
	for _, dir := range []string{
		filepath.Join(b.ProjectDir, "config", "includes.chroot"),
		filepath.Join(b.ProjectDir, "config", "includes.chroot_after_packages"),
	} {
		if _, err := os.Stat(dir); err == nil {
			copyDir(dir, b.chroot)
		}
	}
	return nil
}

func (b *Builder) buildSquashfs() error {
	squashfs := filepath.Join(b.binary, "live", "filesystem.squashfs")
	os.Remove(squashfs)
	return run("mksquashfs", b.chroot, squashfs,
		"-comp", b.Cfg.Compression(),
		"-e", filepath.Join(b.chroot, "proc"),
		"-e", filepath.Join(b.chroot, "sys"),
		"-e", filepath.Join(b.chroot, "dev"),
		"-noappend")
}

func (b *Builder) prepareIso() error {
	liveDir := filepath.Join(b.binary, "live")
	grubDir := filepath.Join(b.binary, "boot", "grub")

	entries, _ := os.ReadDir(filepath.Join(b.chroot, "boot"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "vmlinuz-") {
			copyFile(filepath.Join(b.chroot, "boot", e.Name()), filepath.Join(liveDir, "vmlinuz"))
		}
		if strings.HasPrefix(e.Name(), "initrd.img-") {
			copyFile(filepath.Join(b.chroot, "boot", e.Name()), filepath.Join(liveDir, "initrd.img"))
		}
	}

	grubCfg := fmt.Sprintf(`set default=0
set timeout=5

menuentry "HackerOS Live" {
    linux  /live/vmlinuz boot=live components quiet splash username=%s
    initrd /live/initrd.img
}
menuentry "HackerOS Live (tryb awaryjny)" {
    linux  /live/vmlinuz boot=live components nomodeset username=%s
    initrd /live/initrd.img
}
`, b.Cfg.Username(), b.Cfg.Username())

	return writeFile(filepath.Join(grubDir, "grub.cfg"), grubCfg)
}

func (b *Builder) buildIso() error {
	isoPath := filepath.Join(b.output, b.Cfg.IsoName()+".iso")
	err := run("xorriso", "-as", "mkisofs",
		"-iso-level", "3",
		"-full-iso9660-filenames",
		"-volid", "HACKEROS_LIVE",
		"--efi-boot", "EFI/boot/bootx64.efi",
		"-efi-boot-part", "--efi-boot-image",
		"--protective-msdos-label",
		"-output", isoPath,
		b.binary)
	if err != nil {
		return run("xorriso", "-as", "mkisofs",
			"-iso-level", "3",
			"-full-iso9660-filenames",
			"-volid", "HACKEROS_LIVE",
			"-output", isoPath,
			b.binary)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func checkDeps() error {
	var missing []string
	for _, bin := range []string{"debootstrap", "mksquashfs", "xorriso"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("brak: %s\nUruchom: sudo hackeros-builder setup", strings.Join(missing, ", "))
	}
	return nil
}

func (b *Builder) chrootRun(args ...string) error {
	cmd := exec.Command("chroot", append([]string{b.chroot}, args...)...)
	cmd.Env = []string{
		"DEBIAN_FRONTEND=noninteractive",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root", "LANG=C.UTF-8",
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeFile(path, content string) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	return os.WriteFile(path, []byte(content), 0644)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, _ := os.ReadFile(path)
		return os.WriteFile(target, data, info.Mode())
	})
}
