package container

import (
	"fmt"
	"hackeros-builder/src/config"
	"hackeros-builder/src/ui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Builder buduje ISO z kontenera (inspirowane bootc)
// Flow:
//   1. podman build → obraz kontenera
//   2. podman export → tar systemu plików
//   3. Rozpakowanie do chroot
//   4. Dodanie live-boot/live-config
//   5. Squashfs + ISO

type Builder struct {
	ProjectDir  string
	Cfg         *config.BuildConfig
	Release     bool
	ImageName   string
	output      string
	workDir     string
	chroot      string
	binary      string
}

func New(projectDir string, cfg *config.BuildConfig, release bool) *Builder {
	work := filepath.Join(projectDir, ".hb-work-container")
	return &Builder{
		ProjectDir: projectDir,
		Cfg:        cfg,
		Release:    release,
		ImageName:  "hackeros-builder-" + cfg.IsoName(),
		output:     filepath.Join(projectDir, "output"),
		workDir:    work,
		chroot:     filepath.Join(work, "chroot"),
		binary:     filepath.Join(work, "binary"),
	}
}

func (b *Builder) Build() error {
	ui.Banner("HackerOS Builder — Tryb kontenerowy (bootc-style)")

	if err := b.checkDeps(); err != nil {
		return err
	}

	steps := []struct {
		label string
		fn    func() error
	}{
		{"Przygotowanie katalogów roboczych", b.prepareDirs},
		{"Walidacja Containerfile", b.validateContainerfile},
		{"Budowanie obrazu kontenera (podman build)", b.buildContainer},
		{"Eksport systemu plików z kontenera", b.exportContainer},
		{"Rozpakowanie systemu plików", b.extractRootfs},
		{"Montowanie pseudo-filesystems", b.mountPseudo},
		{"Instalacja live-boot i live-config", b.installLivePackages},
		{"Tworzenie użytkownika live", b.createUser},
		{"Konfiguracja SDDM autologin", b.configureSddm},
		{"Uruchamianie hooków post-container", b.runHooks},
		{"Odmontowanie pseudo-filesystems", b.umountPseudo},
		{"Kompresja squashfs", b.buildSquashfs},
		{"Przygotowanie struktury ISO", b.prepareIso},
		{"Budowanie obrazu ISO (xorriso)", b.buildIso},
		{"Czyszczenie obrazu kontenera", b.cleanupContainer},
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

// ── Kroki budowania ───────────────────────────────────────────────────────────

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

func (b *Builder) validateContainerfile() error {
	cf := b.containerfilePath()
	if _, err := os.Stat(cf); err != nil {
		return fmt.Errorf("brak Containerfile w %s\nUruchom: hackeros-builder container init", filepath.Dir(cf))
	}

	data, err := os.ReadFile(cf)
	if err != nil {
		return err
	}

	content := string(data)

	// Sprawdź czy ma FROM
	if !strings.Contains(content, "FROM") {
		return fmt.Errorf("Containerfile nie zawiera instrukcji FROM")
	}

	// Ostrzeżenie jeśli nie ma live-boot
	if !strings.Contains(content, "live-boot") {
		ui.Warn("Containerfile nie instaluje live-boot — zostanie dodany automatycznie")
	}

	return nil
}

func (b *Builder) buildContainer() error {
	cf := b.containerfilePath()
	contextDir := filepath.Dir(cf)

	args := []string{
		"build",
		"--tag", b.ImageName,
		"--file", cf,
		"--no-cache",
	}

	// Przekaż build args z config.hk
	for k, v := range b.Cfg.ContainerBuildArgs() {
		args = append(args, "--build-arg", k+"="+v)
	}

	args = append(args, contextDir)

	cmd := exec.Command("podman", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (b *Builder) exportContainer() error {
	tarPath := filepath.Join(b.workDir, "rootfs.tar")

	// Utwórz tymczasowy kontener
	out, err := exec.Command("podman", "create", "--name", b.ImageName+"-export", b.ImageName).Output()
	if err != nil {
		return fmt.Errorf("podman create: %w", err)
	}
	containerID := strings.TrimSpace(string(out))
	defer exec.Command("podman", "rm", containerID).Run()

	// Eksportuj filesystem
	cmd := exec.Command("podman", "export", "--output", tarPath, containerID)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (b *Builder) extractRootfs() error {
	tarPath := filepath.Join(b.workDir, "rootfs.tar")

	// Wyczyść chroot
	os.RemoveAll(b.chroot)
	os.MkdirAll(b.chroot, 0755)

	cmd := exec.Command("tar", "-xf", tarPath, "-C", b.chroot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rozpakowywanie rootfs: %w", err)
	}

	// Usuń tar po rozpakowaniu
	os.Remove(tarPath)
	return nil
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
			exec.Command("mount", "--bind", m.src, target).Run()
		} else {
			exec.Command("mount", "-t", m.fs, m.src, target).Run()
		}
	}
	return nil
}

func (b *Builder) umountPseudo() error {
	for _, dst := range []string{"dev/pts", "dev", "sys", "proc"} {
		exec.Command("umount", "-lf", filepath.Join(b.chroot, dst)).Run()
	}
	return nil
}

func (b *Builder) installLivePackages() error {
	// Sprawdź czy live-boot jest już zainstalowany
	result := exec.Command("chroot", b.chroot, "dpkg", "-l", "live-boot")
	if result.Run() == nil {
		return nil // już zainstalowany przez Containerfile
	}

	// Dodaj sources.list
	repos := strings.Join(b.Cfg.Repos(), " ")
	mirror := b.Cfg.Mirror()
	release := b.Cfg.Release()
	sources := fmt.Sprintf(
		"deb %s %s %s\ndeb %s-security %s-security %s\n",
		mirror, release, repos,
		mirror, release, repos,
	)
	writeFile(filepath.Join(b.chroot, "etc", "apt", "sources.list"), sources)

	return b.chrootRun(
		"apt-get", "install", "-y", "-qq",
		"live-boot", "live-config", "live-config-systemd",
		"user-setup", "systemd-sysv",
	)
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
	// Hooki specyficzne dla trybu kontenerowego
	hooksDir := filepath.Join(b.ProjectDir, "config", "hooks", "container")
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".hook.chroot") {
			hookPath := filepath.Join(hooksDir, e.Name())
			name := e.Name()
			dest := filepath.Join(b.chroot, "tmp", name)
			data, _ := os.ReadFile(hookPath)
			writeFile(dest, string(data))
			os.Chmod(dest, 0755)
			b.chrootRun("/tmp/" + name)
			os.Remove(dest)
		}
	}
	return nil
}

func (b *Builder) buildSquashfs() error {
	squashfs := filepath.Join(b.binary, "live", "filesystem.squashfs")
	os.Remove(squashfs)
	return runCmd("mksquashfs", b.chroot, squashfs,
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
	err := runCmd("xorriso", "-as", "mkisofs",
		"-iso-level", "3",
		"-full-iso9660-filenames",
		"-volid", "HACKEROS_LIVE",
		"--efi-boot", "EFI/boot/bootx64.efi",
		"-efi-boot-part", "--efi-boot-image",
		"--protective-msdos-label",
		"-output", isoPath, b.binary)
	if err != nil {
		return runCmd("xorriso", "-as", "mkisofs",
			"-iso-level", "3",
			"-full-iso9660-filenames",
			"-volid", "HACKEROS_LIVE",
			"-output", isoPath, b.binary)
	}
	return nil
}

func (b *Builder) cleanupContainer() error {
	exec.Command("podman", "rmi", b.ImageName).Run()
	exec.Command("podman", "rm", b.ImageName+"-export").Run()
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (b *Builder) containerfilePath() string {
	// Szukaj Containerfile w config/container/
	paths := []string{
		filepath.Join(b.ProjectDir, "config", "container", "Containerfile"),
		filepath.Join(b.ProjectDir, "config", "container", "Dockerfile"),
		filepath.Join(b.ProjectDir, "Containerfile"),
		filepath.Join(b.ProjectDir, "Dockerfile"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(b.ProjectDir, "config", "container", "Containerfile")
}

func (b *Builder) checkDeps() error {
	deps := []string{"podman", "mksquashfs", "xorriso", "tar"}
	var missing []string
	for _, dep := range deps {
		if _, err := exec.LookPath(dep); err != nil {
			missing = append(missing, dep)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("brak zależności: %s\nZainstaluj: sudo apt install %s podman",
			strings.Join(missing, ", "), strings.Join(missing, " "))
	}
	if os.Getuid() != 0 {
		return fmt.Errorf("tryb kontenerowy wymaga root (sudo)")
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

func runCmd(args ...string) error {
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
