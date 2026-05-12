package livebuild

import (
	"fmt"
	"hackeros-builder/src/config"
	"hackeros-builder/src/ui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Wrapper zarządza projektem live-build przez hackeros-builder
type Wrapper struct {
	ProjectDir string
	Cfg        *config.BuildConfig
}

func New(projectDir string, cfg *config.BuildConfig) *Wrapper {
	return &Wrapper{ProjectDir: projectDir, Cfg: cfg}
}

// Build - przetłumacz config.hk → live-build config i zbuduj
func (w *Wrapper) Build(release bool) error {
	ui.Banner("HackerOS Builder — Tryb live-build")

	steps := []struct {
		label string
		fn    func() error
	}{
		{"Sprawdzanie zależności (live-build)", w.checkDeps},
		{"Generowanie config/bootstrap", w.genBootstrap},
		{"Generowanie config/binary", w.genBinary},
		{"Generowanie config/chroot", w.genChroot},
		{"Generowanie config/common", w.genCommon},
		{"Generowanie package-lists", w.genPackageLists},
		{"Tworzenie hooka użytkownika live", w.genUserHook},
		{"Tworzenie hooka SDDM autologin", w.genSddmHook},
		{"lb clean --purge", w.lbClean},
		{"lb config", w.lbConfig},
		{"lb build", w.lbBuild},
		{"Przenoszenie ISO do output/", w.moveIso},
	}

	prog := ui.NewProgress(len(steps), "Start...")
	prog.Start()

	for _, step := range steps {
		prog.SetLabel(step.label)
		if err := step.fn(); err != nil {
			prog.Fail(step.label)
			return fmt.Errorf("%s: %w", step.label, err)
		}
		prog.Update(step.label)
	}

	prog.Finish("ISO zbudowane!")

	isoPath := filepath.Join(w.ProjectDir, "output", w.Cfg.IsoName()+".iso")
	if stat, err := os.Stat(isoPath); err == nil {
		ui.Ok(fmt.Sprintf("ISO: %s%s%s (%.1f MB)",
			ui.Bold+ui.Cyan, isoPath, ui.Reset,
			float64(stat.Size())/1024/1024))
	}

	return nil
}

// Clean - lb clean
func (w *Wrapper) Clean(purge bool) error {
	ui.Step("Czyszczenie projektu live-build")
	if !w.hasLiveBuild() {
		ui.Warn("live-build nie jest zainstalowany")
		return nil
	}
	args := []string{"clean"}
	if purge {
		args = append(args, "--purge")
	}
	return w.lb(args...)
}

// ── Generatory konfiguracji live-build ───────────────────────────────────────

func (w *Wrapper) genBootstrap() error {
	repos := strings.Join(w.Cfg.Repos(), " ")
	mirror := w.Cfg.Mirror()
	release := w.Cfg.Release()

	content := fmt.Sprintf(`# Wygenerowane przez hackeros-builder
# NIE EDYTUJ — edytuj config/config.hk

LB_ARCHITECTURE="%s"
LB_DISTRIBUTION="%s"
LB_DISTRIBUTION_CHROOT="%s"
LB_DISTRIBUTION_BINARY="%s"
LB_PARENT_DISTRIBUTION="%s"
LB_PARENT_DISTRIBUTION_CHROOT="%s"
LB_PARENT_DISTRIBUTION_BINARY="%s"
LB_ARCHIVE_AREAS="%s"
LB_PARENT_ARCHIVE_AREAS="%s"
LB_MIRROR_BOOTSTRAP="%s"
LB_MIRROR_CHROOT="%s"
LB_MIRROR_CHROOT_SECURITY="%s-security"
LB_MIRROR_BINARY="%s"
LB_MIRROR_BINARY_SECURITY="%s-security"
LB_PARENT_MIRROR_BOOTSTRAP="%s"
LB_PARENT_MIRROR_CHROOT="%s"
LB_PARENT_MIRROR_CHROOT_SECURITY="%s-security"
LB_PARENT_MIRROR_BINARY="%s"
LB_PARENT_MIRROR_BINARY_SECURITY="%s-security"
`,
		w.Cfg.Arch(),
		release, release, release, release, release, release,
		repos, repos,
		mirror, mirror, mirror, mirror, mirror,
		mirror, mirror, mirror, mirror, mirror,
	)

	return w.writeConfig("bootstrap", content)
}

func (w *Wrapper) genBinary() error {
	bootappend := w.Cfg.LBBootappend()
	content := fmt.Sprintf(`# Wygenerowane przez hackeros-builder

LB_IMAGE_TYPE="iso-hybrid"
LB_BINARY_FILESYSTEM="fat32"
LB_APT_INDICES="true"
LB_BOOTAPPEND_LIVE="%s"
LB_BOOTAPPEND_INSTALL=""
LB_BOOTAPPEND_LIVE_FAILSAFE="boot=live components memtest noapic noapm nodma nomce nosmp nosplash vga=788"
LB_BOOTLOADER_BIOS="syslinux"
LB_BOOTLOADER_EFI="grub-efi"
LB_CHECKSUMS="sha256"
LB_COMPRESSION="none"
LB_ZSYNC="false"
LB_BUILD_WITH_CHROOT="true"
LB_DEBIAN_INSTALLER="%s"
LB_DEBIAN_INSTALLER_GUI="true"
LB_HDD_LABEL="HACKEROS_LIVE"
LB_HDD_SIZE="auto"
LB_ISO_APPLICATION="HackerOS Live"
LB_ISO_VOLUME="HackerOS Live @ISOVOLUME_TS@"
LB_MEMTEST="none"
LB_LOADLIN="false"
LB_WIN32_LOADER="false"
LB_NET_TARBALL="true"
LB_ONIE="false"
LB_FIRMWARE_BINARY="true"
LB_FIRMWARE_CHROOT="true"
LB_SWAP_FILE_SIZE="512"
LB_UEFI_SECURE_BOOT="%s"
`,
		bootappend,
		w.Cfg.LBDebian_installer(),
		w.Cfg.LBSecureboot(),
	)
	return w.writeConfig("binary", content)
}

func (w *Wrapper) genChroot() error {
	content := `# Wygenerowane przez hackeros-builder

LB_CHROOT_FILESYSTEM="squashfs"
LB_CHROOT_SQUASHFS_COMPRESSION_TYPE="xz"
LB_UNION_FILESYSTEM="overlay"
LB_INTERACTIVE="false"
LB_KEYRING_PACKAGES="debian-archive-keyring"
LB_LINUX_FLAVOURS_WITH_ARCH="amd64"
LB_LINUX_PACKAGES="linux-image"
LB_SECURITY="true"
LB_UPDATES="true"
LB_BACKPORTS="false"
LB_PROPOSED_UPDATES="false"
`
	return w.writeConfig("chroot", content)
}

func (w *Wrapper) genCommon() error {
	aptRecommends := "true"
	if !w.Cfg.AptRecommends() {
		aptRecommends = "false"
	}
	content := fmt.Sprintf(`# Wygenerowane przez hackeros-builder

LB_CONFIGURATION_VERSION="20250505"
LB_APT="apt"
LB_APT_HTTP_PROXY=""
LB_APT_PIPELINE=""
LB_APT_RECOMMENDS="%s"
LB_APT_SECURE="true"
LB_APT_SOURCE_ARCHIVES="true"
LB_CACHE="true"
LB_CACHE_INDICES="false"
LB_CACHE_PACKAGES="true"
LB_CACHE_STAGES="bootstrap"
LB_DEBCONF_FRONTEND="noninteractive"
LB_DEBCONF_PRIORITY="critical"
LB_INITRAMFS="live-boot"
LB_INITRAMFS_COMPRESSION="gzip"
LB_INITSYSTEM="systemd"
LB_MODE="debian"
LB_SYSTEM="live"
LB_IMAGE_NAME="live-image"
APT_OPTIONS="--allow-unauthenticated --yes"
APTITUDE_OPTIONS="--assume-yes -o Acquire::Retries=5"
LB_UTC_TIME="false"
`, aptRecommends)
	return w.writeConfig("common", content)
}

func (w *Wrapper) genPackageLists() error {
	pkgs := w.Cfg.Packages()

	// Zawsze dodaj user-setup żeby live-config mógł tworzyć usera
	baseRequired := []string{
		"live-boot",
		"live-config",
		"live-config-systemd",
		"systemd-sysv",
		"user-setup",
	}

	// Deduplikacja
	seen := map[string]bool{}
	var all []string
	for _, p := range append(baseRequired, pkgs...) {
		if !seen[p] {
			seen[p] = true
			all = append(all, p)
		}
	}

	content := "# Wygenerowane przez hackeros-builder\n# NIE EDYTUJ — edytuj config/config.hk\n\n"
	for _, p := range all {
		content += p + "\n"
	}

	dir := filepath.Join(w.ProjectDir, "config", "package-lists")
	os.MkdirAll(dir, 0755)
	return os.WriteFile(filepath.Join(dir, "hackeros.list.chroot"), []byte(content), 0644)
}

func (w *Wrapper) genUserHook() error {
	username := w.Cfg.Username()
	password := w.Cfg.Password()

	content := fmt.Sprintf(`#!/bin/bash
# Wygenerowane przez hackeros-builder
# Tworzy użytkownika live i ustawia hasło

set -e

USERNAME="%s"
PASSWORD="%s"

if ! id "$USERNAME" &>/dev/null; then
    useradd -m -s /bin/bash \
        -G sudo,audio,video,cdrom,plugdev,netdev,bluetooth \
        "$USERNAME"
fi

echo "$USERNAME:$PASSWORD" | chpasswd
passwd -u "$USERNAME"

echo "$USERNAME ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/live-user
chmod 440 /etc/sudoers.d/live-user
`, username, password)

	dir := filepath.Join(w.ProjectDir, "config", "hooks", "live")
	os.MkdirAll(dir, 0755)
	hookPath := filepath.Join(dir, "0001-hackeros-user.hook.chroot")
	if err := os.WriteFile(hookPath, []byte(content), 0755); err != nil {
		return err
	}
	return nil
}

func (w *Wrapper) genSddmHook() error {
	username := w.Cfg.Username()

	content := fmt.Sprintf(`#!/bin/bash
# Wygenerowane przez hackeros-builder
# Konfiguruje autologin SDDM

set -e

mkdir -p /etc/sddm.conf.d
cat > /etc/sddm.conf.d/autologin.conf << EOF
[Autologin]
User=%s
Session=plasmawayland
Relogin=false
EOF
`, username)

	dir := filepath.Join(w.ProjectDir, "config", "hooks", "live")
	os.MkdirAll(dir, 0755)
	hookPath := filepath.Join(dir, "0002-hackeros-sddm.hook.chroot")
	return os.WriteFile(hookPath, []byte(content), 0755)
}

func (w *Wrapper) lbClean() error {
	if !w.hasLiveBuild() {
		return fmt.Errorf("live-build nie jest zainstalowany. Uruchom: sudo hackeros-builder setup")
	}
	return w.lb("clean", "--purge")
}

func (w *Wrapper) lbConfig() error {
	return w.lb("config")
}

func (w *Wrapper) lbBuild() error {
	return w.lb("build")
}

func (w *Wrapper) moveIso() error {
	outDir := filepath.Join(w.ProjectDir, "output")
	os.MkdirAll(outDir, 0755)

	// Szukaj ISO w bieżącym katalogu
	entries, err := os.ReadDir(w.ProjectDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".iso") || strings.HasSuffix(e.Name(), ".hybrid.iso") {
			src := filepath.Join(w.ProjectDir, e.Name())
			dst := filepath.Join(outDir, w.Cfg.IsoName()+".iso")
			if err := os.Rename(src, dst); err != nil {
				// Jeśli rename nie działa (różne filesystemy) - kopiuj
				data, err := os.ReadFile(src)
				if err != nil {
					return err
				}
				if err := os.WriteFile(dst, data, 0644); err != nil {
					return err
				}
				os.Remove(src)
			}
			return nil
		}
	}
	// ISO może już być w output/ lub live-image-amd64.hybrid.iso
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (w *Wrapper) lb(args ...string) error {
	cmd := exec.Command("lb", args...)
	cmd.Dir = w.ProjectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (w *Wrapper) hasLiveBuild() bool {
	_, err := exec.LookPath("lb")
	return err == nil
}

func (w *Wrapper) writeConfig(name, content string) error {
	path := filepath.Join(w.ProjectDir, "config", name)
	return os.WriteFile(path, []byte(content), 0644)
}

func (w *Wrapper) checkDeps() error {
	if !w.hasLiveBuild() {
		return fmt.Errorf("live-build nie jest zainstalowany\nZainstaluj: sudo apt install live-build")
	}
	return nil
}
