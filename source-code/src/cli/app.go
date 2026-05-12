package cli

import (
	"fmt"
	"hackeros-builder/src/builder"
	"hackeros-builder/src/config"
	"hackeros-builder/src/container"
	"hackeros-builder/src/livebuild"
	"hackeros-builder/src/ui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type App struct {
	Version string
}

func NewApp(version string) *App {
	return &App{Version: version}
}

func (a *App) Run(args []string) error {
	ui.PrintLogo(a.Version)

	if len(args) < 2 {
		a.printHelp()
		return nil
	}

	switch args[1] {
	case "container":
		return a.cmdContainer(args[2:])
	case "init":
		return a.cmdInit(args[2:])
	case "build":
		return a.cmdBuild(args[2:])
	case "clean":
		return a.cmdClean(args[2:])
	case "setup":
		return a.cmdSetup(args[2:])
	case "migration", "migrate":
		return a.cmdMigration(args[2:])
	case "info":
		return a.cmdInfo(args[2:])
	case "lb":
		return a.cmdLb(args[2:])
	case "help", "--help", "-h":
		a.printHelp()
	case "version", "--version", "-v":
		fmt.Printf("  hackeros-builder v%s\n", a.Version)
	default:
		ui.Err("Nieznana komenda: " + args[1])
		a.printHelp()
		return fmt.Errorf("nieznana komenda")
	}
	return nil
}

// ── container ─────────────────────────────────────────────────────────────────

func (a *App) cmdContainer(args []string) error {
	if len(args) == 0 {
		a.printContainerHelp()
		return nil
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "init":
		return a.cmdContainerInit(rest)
	case "build":
		return a.cmdContainerBuild(rest)
	case "clean":
		return a.cmdContainerClean(rest)
	case "info":
		return a.cmdContainerInfo(rest)
	default:
		ui.Err("Nieznana podkomenda: container " + sub)
		a.printContainerHelp()
		return fmt.Errorf("nieznana podkomenda")
	}
}

func (a *App) cmdContainerInit(args []string) error {
	target := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}

	ui.Step("Inicjalizacja projektu w trybie kontenerowym")

	// Struktura katalogów
	dirs := []string{
		filepath.Join(target, "config", "container"),
		filepath.Join(target, "config", "hooks", "container"),
		filepath.Join(target, "config", "includes.chroot", "etc"),
		filepath.Join(target, "output"),
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
		rel, _ := filepath.Rel(target, d)
		ui.Ok("Utworzono: " + rel)
	}

	// config.hk z trybem kontenerowym
	os.WriteFile(filepath.Join(target, "config", "config.hk"),
		[]byte(templateContainerConfigHk), 0644)
	ui.Ok("Utworzono: config/config.hk")

	// Containerfile
	cfPath := filepath.Join(target, "config", "container", "Containerfile")
	os.WriteFile(cfPath, []byte(a.generateContainerfile("trixie")), 0644)
	ui.Ok("Utworzono: config/container/Containerfile")

	// Przykładowy hook
	hookPath := filepath.Join(target, "config", "hooks", "container", "example.hook.chroot")
	os.WriteFile(hookPath, []byte(templateContainerHook), 0755)
	ui.Ok("Utworzono: config/hooks/container/example.hook.chroot")

	fmt.Println()
	ui.Ok(fmt.Sprintf("Projekt kontenerowy zainicjalizowany w: %s%s%s", ui.Bold, target, ui.Reset))
	ui.Info(fmt.Sprintf("Edytuj %sconfig/container/Containerfile%s", ui.Cyan, ui.Reset))
	ui.Info(fmt.Sprintf("Zbuduj: %ssudo hackeros-builder container build%s", ui.Cyan, ui.Reset))
	return nil
}

func (a *App) cmdContainerBuild(args []string) error {
	projectDir := "."
	release := false

	for _, arg := range args {
		switch arg {
		case "--release":
			release = true
		default:
			if !strings.HasPrefix(arg, "-") {
				projectDir = arg
			}
		}
	}

	absProject, _ := filepath.Abs(projectDir)
	configPath := filepath.Join(absProject, "config", "config.hk")
	if _, err := os.Stat(configPath); err != nil {
		ui.Die("Brak config/config.hk — uruchom najpierw: hackeros-builder container init")
	}

	ui.Step("Wczytywanie konfiguracji")
	cfg, err := config.Load(absProject)
	if err != nil {
		return fmt.Errorf("błąd konfiguracji: %w", err)
	}

	ui.PrintKV("Dystrybucja", cfg.Distro()+" "+cfg.Release())
	ui.PrintKV("Obraz bazowy", cfg.ContainerBaseImage())
	ui.PrintKV("Użytkownik", cfg.Username())
	ui.PrintKV("ISO", cfg.IsoName()+".iso")
	ui.PrintKV("Kompresja", cfg.Compression())
	ui.PrintKV("Tryb", "kontenerowy (bootc-style)")
	if release {
		ui.Info("Tryb: " + ui.C(ui.Green, "RELEASE"))
	}
	fmt.Println()

	b := container.New(absProject, cfg, release)
	return b.Build()
}

func (a *App) cmdContainerClean(args []string) error {
	purge := false
	for _, arg := range args {
		if arg == "--purge" {
			purge = true
		}
	}

	ui.Step("Czyszczenie trybu kontenerowego")

	work := ".hb-work-container"
	if _, err := os.Stat(work); err == nil {
		for _, pseudo := range []string{"dev/pts", "dev", "sys", "proc"} {
			exec.Command("umount", "-lf", filepath.Join(work, "chroot", pseudo)).Run()
		}
		os.RemoveAll(work)
		ui.Ok("Usunięto .hb-work-container/")
	} else {
		ui.Ok("Brak plików do wyczyszczenia")
	}

	// Usuń obrazy podman
	exec.Command("podman", "rmi", "-f", "hackeros-builder-hackeros-live").Run()

	if purge {
		os.RemoveAll("output")
		ui.Ok("Usunięto output/")
	}
	return nil
}

func (a *App) cmdContainerInfo(_ []string) error {
	cfg, err := config.Load(".")
	if err != nil {
		ui.Die("Brak config/config.hk")
	}

	ui.Banner("Informacje — tryb kontenerowy")
	ui.PrintKV("Dystrybucja", cfg.Distro()+" "+cfg.Release())
	ui.PrintKV("Obraz bazowy", cfg.ContainerBaseImage())
	ui.PrintKV("Użytkownik", cfg.Username())
	ui.PrintKV("ISO", cfg.IsoName()+".iso")
	ui.PrintKV("Kompresja", cfg.Compression())

	cf := filepath.Join("config", "container", "Containerfile")
	if _, err := os.Stat(cf); err == nil {
		ui.Ok("Containerfile: " + cf)
	} else {
		ui.Warn("Brak Containerfile: " + cf)
	}

	// Stan obrazów podman
	out, err := exec.Command("podman", "images", "--format",
		"{{.Repository}}:{{.Tag}} {{.Size}}", "hackeros-builder*").Output()
	if err == nil && len(out) > 0 {
		fmt.Printf("\n  %sObrazy Podman:%s\n", ui.Bold, ui.Reset)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fmt.Printf("    %s·%s %s\n", ui.Dim, ui.Reset, line)
		}
	}
	return nil
}

func (a *App) generateContainerfile(release string) string {
	baseImage := "debian:" + release
	return fmt.Sprintf(`# HackerOS Containerfile
# Generuje bootowalny obraz ISO z kontenera (bootc-style)
# Dokumentacja: https://hackeros-linux-system.github.io/HackerOS-Website/

FROM %s

# ── Metadane ──────────────────────────────────────────────────────────────────
LABEL maintainer="HackerOS Project"
LABEL description="HackerOS Live System"
LABEL version="1.0"

# ── Zmienne budowania ─────────────────────────────────────────────────────────
ARG DEBIAN_FRONTEND=noninteractive
ARG RELEASE=%s

# ── Repozytoria ───────────────────────────────────────────────────────────────
RUN echo "deb http://deb.debian.org/debian ${RELEASE} main contrib non-free non-free-firmware" \
    > /etc/apt/sources.list && \
    echo "deb http://deb.debian.org/debian-security ${RELEASE}-security main contrib non-free non-free-firmware" \
    >> /etc/apt/sources.list && \
    echo "deb http://deb.debian.org/debian ${RELEASE}-updates main contrib non-free non-free-firmware" \
    >> /etc/apt/sources.list

# ── Aktualizacja systemu ──────────────────────────────────────────────────────
RUN apt-get update -qq && \
    apt-get upgrade -y -qq

# ── Kernel i pakiety live ─────────────────────────────────────────────────────
RUN apt-get install -y -qq \
    linux-image-amd64 \
    live-boot \
    live-config \
    live-config-systemd \
    user-setup \
    systemd-sysv \
    sudo \
    network-manager \
    firmware-linux \
    firmware-misc-nonfree

# ── Desktop (KDE Plasma) ──────────────────────────────────────────────────────
RUN apt-get install -y -qq \
    plasma-desktop \
    sddm \
    konsole \
    dolphin \
    kate \
    ark \
    gwenview

# ── Dodatkowe pakiety ─────────────────────────────────────────────────────────
RUN apt-get install -y -qq \
    curl \
    wget \
    git \
    nano \
    htop \
    calamares \
    calamares-settings-debian

# ── Konfiguracja systemu ──────────────────────────────────────────────────────
RUN echo "hackeros" > /etc/hostname && \
    locale-gen pl_PL.UTF-8 en_US.UTF-8 || true

# ── Czyszczenie cache ─────────────────────────────────────────────────────────
RUN apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
`, baseImage, release)
}

func (a *App) printContainerHelp() {
	fmt.Printf(`%sUżycie:%s hackeros-builder container <podkomenda>

%sPodkomendy:%s
  %sinit%s [katalog]    Inicjalizuj projekt kontenerowy
  %sbuild%s [katalog]   Zbuduj ISO z kontenera
    %s--release%s        Tryb release
  %sclean%s            Wyczyść pliki robocze
    %s--purge%s          Usuń też output/
  %sinfo%s             Informacje o projekcie kontenerowym

%sFlow budowania:%s
  1. podman build (Containerfile) → obraz kontenera
  2. podman export → filesystem tar
  3. Rozpakowanie + dodanie live-boot/live-config
  4. squashfs + xorriso → bootowalny ISO

%sPrzykłady:%s
  hackeros-builder container init
  sudo hackeros-builder container build
  hackeros-builder container clean --purge
  hackeros-builder container info

`,
		ui.Bold, ui.Reset,
		ui.Bold, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Dim, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Dim, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Bold, ui.Reset,
		ui.Bold, ui.Reset,
	)
}

// ── init ──────────────────────────────────────────────────────────────────────

func (a *App) cmdInit(args []string) error {
	target := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}

	ui.Step("Inicjalizacja projektu HackerOS Builder")

	configDir := filepath.Join(target, "config")
	if _, err := os.Stat(configDir); err == nil {
		ui.Die("Katalog config/ już istnieje w " + target)
	}

	dirs := []string{
		filepath.Join(configDir, "hooks", "normal"),
		filepath.Join(configDir, "hooks", "live"),
		filepath.Join(configDir, "includes.chroot", "etc"),
		filepath.Join(configDir, "package-lists"),
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
		rel, _ := filepath.Rel(target, d)
		ui.Ok("Utworzono: " + rel)
	}

	os.WriteFile(filepath.Join(configDir, "config.hk"), []byte(templateConfigHk), 0644)
	ui.Ok("Utworzono: config/config.hk")

	os.WriteFile(filepath.Join(configDir, "hooks", "normal", "example.hook.chroot"),
		[]byte(templateHook), 0755)
	ui.Ok("Utworzono: config/hooks/normal/example.hook.chroot")

	fmt.Println()
	ui.Ok(fmt.Sprintf("Projekt zainicjalizowany w: %s%s%s", ui.Bold, target, ui.Reset))
	ui.Info(fmt.Sprintf("Edytuj %sconfig/config.hk%s", ui.Cyan, ui.Reset))
	ui.Info(fmt.Sprintf("Zbuduj: %shackeros-builder build%s", ui.Cyan, ui.Reset))
	return nil
}

// ── build ─────────────────────────────────────────────────────────────────────

func (a *App) cmdBuild(args []string) error {
	projectDir := "."
	release := false
	standalone := false

	for _, arg := range args {
		switch arg {
		case "--release":
			release = true
		case "--standalone":
			standalone = true
		default:
			if !strings.HasPrefix(arg, "-") {
				projectDir = arg
			}
		}
	}

	absProject, _ := filepath.Abs(projectDir)
	configPath := filepath.Join(absProject, "config", "config.hk")
	if _, err := os.Stat(configPath); err != nil {
		ui.Die("Brak config/config.hk — uruchom najpierw: hackeros-builder init")
	}

	ui.Step("Wczytywanie konfiguracji")
	cfg, err := config.Load(absProject)
	if err != nil {
		return fmt.Errorf("błąd konfiguracji: %w", err)
	}

	// Override trybu jeśli podano flagę
	if standalone {
		cfg.Mode = config.ModeStandalone
	}

	// Wyświetl info
	ui.PrintKV("Dystrybucja", cfg.Distro()+" "+cfg.Release())
	ui.PrintKV("Architektura", cfg.Arch())
	ui.PrintKV("Hostname", cfg.Hostname())
	ui.PrintKV("Użytkownik", cfg.Username())
	ui.PrintKV("Kernel", cfg.Kernel())
	ui.PrintKV("ISO", cfg.IsoName()+".iso")
	ui.PrintKV("Kompresja", cfg.Compression())

	mode := "nakładka na live-build"
	if cfg.Mode == config.ModeStandalone {
		mode = "standalone (niezależny)"
	}
	ui.PrintKV("Tryb budowania", mode)

	if release {
		ui.Info("Tryb: " + ui.C(ui.Green, "RELEASE"))
	}

	fmt.Println()

	switch cfg.Mode {
	case config.ModeStandalone:
		b := builder.New(absProject, cfg, release)
		return b.Build()
	case config.ModeContainer:
		b := container.New(absProject, cfg, release)
		return b.Build()
	default:
		w := livebuild.New(absProject, cfg)
		return w.Build(release)
	}
}

// ── clean ─────────────────────────────────────────────────────────────────────

func (a *App) cmdClean(args []string) error {
	purge := false
	standalone := false
	for _, arg := range args {
		switch arg {
		case "--purge":
			purge = true
		case "--standalone":
			standalone = true
		}
	}

	ui.Step("Czyszczenie projektu")

	if standalone {
		// Tryb standalone - usuń .hb-work
		work := ".hb-work"
		if _, err := os.Stat(work); err != nil {
			ui.Ok("Brak plików do wyczyszczenia")
			return nil
		}
		for _, pseudo := range []string{"dev/pts", "dev", "sys", "proc"} {
			exec.Command("umount", "-lf", filepath.Join(work, "chroot", pseudo)).Run()
		}
		os.RemoveAll(work)
		ui.Ok("Usunięto .hb-work/")
	} else {
		// Tryb live-build
		cfg, err := config.Load(".")
		if err == nil {
			w := livebuild.New(".", cfg)
			w.Clean(purge)
		} else {
			// Fallback - lb clean bezpośrednio
			exec.Command("lb", "clean", "--purge").Run()
		}
		ui.Ok("Wyczyszczono projekt live-build")
	}

	if purge {
		os.RemoveAll("output")
		ui.Ok("Usunięto output/")
	}
	return nil
}

// ── setup ─────────────────────────────────────────────────────────────────────

func (a *App) cmdSetup(args []string) error {
	release := false
	for _, arg := range args {
		if arg == "--release" {
			release = true
		}
	}

	ui.Step("Instalacja zależności systemowych")

	if os.Getuid() != 0 {
		ui.Die("Setup wymaga root (sudo hackeros-builder setup)")
	}

	exec.Command("apt-get", "update", "-qq").Run()

	deps := []string{
		"live-build",
		"debootstrap", "squashfs-tools", "xorriso",
		"grub-pc-bin", "grub-efi-amd64-bin", "grub-common",
		"isolinux", "syslinux-common", "dosfstools", "mtools",
	}

	ui.Info("Instalowanie: " + strings.Join(deps, ", "))
	cmd := exec.Command("apt-get", append([]string{"install", "-y", "-qq"}, deps...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	ui.Ok("Zainstalowano zależności")

	if release {
		fmt.Println()
		ui.Info("Weryfikacja środowiska release:")
		for _, bin := range []string{"lb", "debootstrap", "mksquashfs", "xorriso"} {
			if _, err := exec.LookPath(bin); err != nil {
				ui.Err("Brak: " + bin)
			} else {
				ui.Ok("OK: " + bin)
			}
		}
	}

	ui.Ok("Środowisko gotowe!")
	return nil
}

// ── migration ─────────────────────────────────────────────────────────────────

func (a *App) cmdMigration(_ []string) error {
	ui.Step("Migracja projektu live-build → HackerOS Builder")

	if _, err := os.Stat("config"); err != nil {
		ui.Die("Brak config/ — uruchom z katalogu projektu live-build")
	}

	// Odczytaj parametry
	release, arch, mirror := "trixie", "amd64", "http://deb.debian.org/debian"
	aptRecommends := "true"

	if data, err := os.ReadFile("config/bootstrap"); err == nil {
		content := string(data)
		if m := regexp.MustCompile(`LB_DISTRIBUTION="(\w+)"`).FindStringSubmatch(content); len(m) > 1 {
			release = m[1]
		}
		if m := regexp.MustCompile(`LB_ARCHITECTURE="(\w+)"`).FindStringSubmatch(content); len(m) > 1 {
			arch = m[1]
		}
		if m := regexp.MustCompile(`LB_MIRROR_BOOTSTRAP="([^"]+)"`).FindStringSubmatch(content); len(m) > 1 {
			mirror = strings.TrimRight(m[1], "/")
		}
		ui.Ok("Odczytano: config/bootstrap")
	}

	if data, err := os.ReadFile("config/common"); err == nil {
		if m := regexp.MustCompile(`LB_APT_RECOMMENDS="(\w+)"`).FindStringSubmatch(string(data)); len(m) > 1 {
			aptRecommends = m[1]
		}
		ui.Ok("Odczytano: config/common")
	}

	// Pakiety
	var pkgs []string
	seen := map[string]bool{}
	entries, _ := os.ReadDir("config/package-lists")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".chroot") || strings.Contains(name, "remove") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join("config", "package-lists", name))
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "-") && !seen[line] {
				pkgs = append(pkgs, line)
				seen[line] = true
			}
		}
		ui.Ok("Odczytano pakiety z: " + name)
	}

	// Grupuj pakiety
	var pkgLines []string
	for i := 0; i < len(pkgs); i += 8 {
		end := i + 8
		if end > len(pkgs) {
			end = len(pkgs)
		}
		group := pkgs[i:end]
		pkgLines = append(pkgLines,
			fmt.Sprintf("-> group%d  => [%s]", i/8+1, strings.Join(group, ", ")))
	}

	// Generuj config.hk
	content := fmt.Sprintf(`! Wygenerowane przez hackeros-builder migration
! z projektu live-build — sprawdź i uzupełnij
! Dokumentacja: https://hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html

[system]
-> distro      => debian
-> release     => %s
-> arch        => %s
-> hostname    => hackeros
-> mirror      => %s
-> repos       => [main, contrib, non-free, non-free-firmware]
-> pkg_manager => apt
-> apt_recommends => %s
-> kernel      => linux-image-amd64

[user]
-> username  => user
-> password  => live

[packages]
! Pakiety przeniesione z live-build
%s

[build]
-> mode        => livebuild
-> iso_name    => hackeros-live
-> compression => xz

[livebuild]
! Opcje specyficzne dla live-build
-> bootappend  => "boot=live components quiet splash username=user"
-> secure_boot => auto

[hooks]
! Hooki z config/hooks/ są automatycznie uruchamiane
`, release, arch, mirror, aptRecommends, strings.Join(pkgLines, "\n"))

	os.WriteFile("config/config.hk", []byte(content), 0644)
	ui.Ok(fmt.Sprintf("Wygenerowano config/config.hk (%d pakietów)", len(pkgs)))
	ui.Info("Hooki są kompatybilne — nie trzeba migrować")
	ui.Warn("Sprawdź config/config.hk przed budowaniem")
	ui.Info(fmt.Sprintf("Zbuduj: %shackeros-builder build%s", ui.Cyan, ui.Reset))
	return nil
}

// ── info ──────────────────────────────────────────────────────────────────────

func (a *App) cmdInfo(_ []string) error {
	cfg, err := config.Load(".")
	if err != nil {
		ui.Die("Brak config/config.hk — uruchom z katalogu projektu")
	}

	ui.Banner("Informacje o projekcie")
	ui.PrintKV("Dystrybucja", cfg.Distro()+" "+cfg.Release())
	ui.PrintKV("Architektura", cfg.Arch())
	ui.PrintKV("Hostname", cfg.Hostname())
	ui.PrintKV("Użytkownik", cfg.Username())
	ui.PrintKV("Desktop", cfg.Desktop())
	ui.PrintKV("Kernel", cfg.Kernel())
	ui.PrintKV("Locale", cfg.Locale())
	ui.PrintKV("Timezone", cfg.Timezone())
	ui.PrintKV("ISO", cfg.IsoName()+".iso")
	ui.PrintKV("Kompresja", cfg.Compression())
	ui.PrintKV("Tryb budowania", cfg.ModeString())
	ui.PrintKVDim("Mirror", cfg.Mirror())
	ui.PrintKVDim("Repozytoria", strings.Join(cfg.Repos(), ", "))

	pkgs := cfg.Packages()
	if len(pkgs) > 0 {
		fmt.Printf("\n  %sPakiety%s (%d):\n", ui.Bold, ui.Reset, len(pkgs))
		limit := 12
		if len(pkgs) < limit {
			limit = len(pkgs)
		}
		for _, p := range pkgs[:limit] {
			fmt.Printf("    %s·%s %s\n", ui.Dim, ui.Reset, p)
		}
		if len(pkgs) > limit {
			fmt.Printf("    %s... i %d więcej%s\n", ui.Dim, len(pkgs)-limit, ui.Reset)
		}
	}

	// Stan plików roboczych
	for _, dir := range []string{".hb-work", "chroot", "binary"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			var size int64
			filepath.Walk(dir, func(_ string, i os.FileInfo, _ error) error {
				if i != nil && !i.IsDir() {
					size += i.Size()
				}
				return nil
			})
			fmt.Printf("\n  %s%s/%s: %.1f MB\n", ui.Bold, dir, ui.Reset, float64(size)/1024/1024)
		}
	}
	return nil
}

// ── lb ────────────────────────────────────────────────────────────────────────

func (a *App) cmdLb(args []string) error {
	ui.Step("Przekazywanie do live-build: lb " + strings.Join(args, " "))
	cmd := exec.Command("lb", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ── Help ──────────────────────────────────────────────────────────────────────

func (a *App) printHelp() {
	fmt.Printf(`%sUżycie:%s hackeros-builder <komenda> [opcje]

%sKomendy:%s
  %sinit%s [katalog]        Inicjalizuj nowy projekt
  %sbuild%s [katalog]       Zbuduj obraz ISO
    %s--release%s            Tryb release
    %s--standalone%s         Tryb niezależny (bez live-build)
  %sclean%s                Wyczyść pliki robocze
    %s--purge%s              Usuń też output/
    %s--standalone%s         Wyczyść .hb-work/ (tryb standalone)
  %ssetup%s                Zainstaluj zależności systemowe
    %s--release%s            Weryfikacja dla release
  %smigration%s            Migruj projekt live-build → hackeros-builder
  %sinfo%s                 Informacje o projekcie
  %slb%s <args>            Przekaż komendę bezpośrednio do live-build
  %shelp%s                 Pokaż tę pomoc
  %sversion%s              Pokaż wersję

%sTryby budowania (config.hk):%s
  %s-> mode => livebuild%s    nakładka na live-build (domyślny)
  %s-> mode => standalone%s   całkowicie niezależny od live-build

%sPrzykłady:%s
  hackeros-builder init
  hackeros-builder build
  hackeros-builder build --release
  hackeros-builder build --standalone
  hackeros-builder clean --purge
  sudo hackeros-builder setup
  hackeros-builder migration
  hackeros-builder lb clean --purge
  hackeros-builder lb build

%sFormat konfiguracji .hk:%s
  https://hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html

`,
		ui.Bold, ui.Reset,
		ui.Bold, ui.Reset,
		ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
		ui.Dim, ui.Reset, ui.Dim, ui.Reset,
		ui.Cyan, ui.Reset, ui.Dim, ui.Reset, ui.Dim, ui.Reset,
		ui.Cyan, ui.Reset, ui.Dim, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Cyan, ui.Reset,
		ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
		ui.Bold, ui.Reset,
		ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
		ui.Bold, ui.Reset,
		ui.Bold, ui.Reset,
	)
}

// ── Szablony ──────────────────────────────────────────────────────────────────

const templateContainerConfigHk = `! HackerOS Builder — konfiguracja trybu kontenerowego (bootc-style)
! Format .hk — dokumentacja: https://hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html

[system]
-> distro      => debian
-> release     => trixie
! trixie (Debian 13 stable) | forky (Debian 14 testing)
-> arch        => amd64
-> hostname    => hackeros
-> mirror      => http://deb.debian.org/debian
-> repos       => [main, contrib, non-free, non-free-firmware]
-> locale      => pl_PL.UTF-8
-> timezone    => Europe/Warsaw

[user]
-> username  => user
-> password  => live

[container]
! Konfiguracja trybu kontenerowego
-> containerfile => config/container/Containerfile
-> base_image    => debian:trixie
! Dla forky: debian:forky
! Argumenty przekazywane do podman build --build-arg
-> build_args
--> RELEASE     => trixie
--> DEBIAN_FRONTEND => noninteractive

[build]
-> mode        => container
-> iso_name    => hackeros-live
-> compression => xz
`

const templateContainerHook = `#!/bin/bash
# Hook uruchamiany po ekstrakcji kontenera (w chroot)
# Katalog: config/hooks/container/

set -e

echo "[container-hook] Konfiguracja post-container..."

# Przykład: włącz usługi
# systemctl enable NetworkManager
# systemctl enable sddm
`

const templateConfigHk = `! HackerOS Builder — plik konfiguracyjny projektu
! Format .hk — dokumentacja: https://hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html

[system]
-> distro      => debian
-> release     => trixie
! bookworm (Debian 12) | trixie (Debian 13 stable) | forky (Debian 14 testing)
-> arch        => amd64
-> hostname    => hackeros
-> mirror      => http://deb.debian.org/debian
-> repos       => [main, contrib, non-free, non-free-firmware]
-> pkg_manager => apt
-> apt_recommends => true
-> desktop     => kde
-> kernel      => linux-image-amd64
-> locale      => pl_PL.UTF-8
-> timezone    => Europe/Warsaw

[user]
-> username  => user
-> password  => live

[packages]
-> base    => [sudo, curl, wget, git, nano]
! -> kde   => [plasma-desktop, sddm, konsole, dolphin]
! -> tools => [htop, vim, neofetch]

[build]
-> mode        => livebuild
! livebuild (nakładka na lb) | standalone | container (bootc-style)
-> iso_name    => hackeros-live
-> compression => xz

[livebuild]
-> bootappend  => "boot=live components quiet splash username=user"
-> secure_boot => auto

[hooks]
! Hooki z config/hooks/ są uruchamiane automatycznie
`

const templateHook = `#!/bin/bash
# Przykładowy hook — uruchamiany podczas budowania w chroot
set -e
echo "[hook] Konfiguracja systemu..."
`
