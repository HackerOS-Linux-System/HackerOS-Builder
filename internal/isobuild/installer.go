package isobuild

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/sandbox"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/toolchain"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// installerNoninteractiveEnv -- patrz internal/rootfs/builder.go,
// noninteractiveEnv: ten sam powod (apt-get/dpkg NIE moze nigdy pokazac
// dialogu/whiptail podczas budowy, np. pytania o keyboard-configuration).
var installerNoninteractiveEnv = []string{
	"DEBIAN_FRONTEND=noninteractive",
	"DEBCONF_NONINTERACTIVE_SEEN=true",
	"DEBCONF_NOWARNINGS=yes",
	"LC_ALL=C",
	"LANG=C",
	"LANGUAGE=C",
}

// InstallerVariant okresla wariant/branding instalatora Calamares
// wstrzykiwanego do ISO -- odpowiednik config.InstallerType, ale
// zdefiniowany lokalnie w tym pakiecie zeby internal/isobuild nie musial
// importowac internal/config wylacznie dla jednego typu wyliczeniowego
// (buildflow.BuildIso wykonuje mapowanie config.InstallerType ->
// isobuild.InstallerVariant przy wywolaniu).
type InstallerVariant string

const (
	// InstallerVariantDefault: standardowy branding "HackerOS".
	InstallerVariantDefault InstallerVariant = "default"

	// InstallerVariantCybersecurity: branding "HackerOS Cybersecurity
	// Edition" (inna paleta kolorow -- czerwony akcent) plus dodatkowy,
	// mniejszy zestaw narzedzi diagnostycznych/sieciowych zainstalowany w
	// SAMYM srodowisku live/instalatora (installerCybersecurityExtraPackages
	// ponizej) -- odrebne od cybersecurityPackages() w internal/rootfs,
	// ktore trafiaja do SYSTEMU DOCELOWEGO, nie do live-medium.
	InstallerVariantCybersecurity InstallerVariant = "cybersecurity"
)

// installerPackages to minimalny zestaw potrzebny by Calamares mogl
// wystartowac w X bez pelnego srodowiska graficznego (bez GNOME/KDE/etc --
// sam Calamares ma wlasna szate Qt/QML, openbox jest tylko "oknem" pod spodem,
// bez paskow zadan/dekoracji, zeby ekran wygladal jak dedykowany instalator,
// a nie jak biurko). Instalowany zawsze, niezaleznie od InstallerVariant.
var installerPackages = []string{
	"calamares",
	"xserver-xorg",
	"xserver-xorg-core",
	// Bez --no-install-recommends Xorg nie ciagnie sterownikow. Bez nich X nie
	// widzi klawiatury/myszy ("no input devices") albo ekranu ("no screens
	// found") i zostaje sam tty. input-all = libinput+evdev, video-all =
	// modesetting/fbdev/vesa/qxl/vmware/... (QEMU, VirtualBox, goly sprzet).
	"xserver-xorg-input-all",
	"xserver-xorg-video-all",
	"xserver-xorg-video-fbdev",
	"xserver-xorg-video-vesa",
	"xinit",
	"x11-xserver-utils",
	"xkb-data",
	"libgl1-mesa-dri",
	"fonts-dejavu-core",
	"udisks2",
	"kmod",
	"openbox",
	"dbus-x11",
	// "policykit-1" byl transitional package (polkitd + pkexec) usuniety w
	// Debian trixie -- apt-get konczyl sie "E: Package 'policykit-1' has
	// no installation candidate". Instalujemy oba realne pakiety wprost.
	"polkitd",
	"pkexec",
	"network-manager",
	"parted",
	"gdisk",
	"dosfstools",
	"os-prober",
}

// installerOptionalPackages to pakiety "mile widziane", ktorych BRAK w
// danym wydaniu Debiana NIE moze przerwac budowy (xmessage zniknal z forky:
// "E: Package 'xmessage' has no installation candidate" przerwal caly build).
// Kazda grupa to lista alternatyw -- instalowana jest pierwsza dostepna;
// jesli zadna sie nie zainstaluje, tylko ostrzezenie.
//   - komunikat "uruchamianie instalatora..." (xmessage, a w nowszych
//     wydaniach binarka jest w x11-apps) + xwininfo (x11-utils),
//   - motyw kursora (dmz-cursor-theme / adwaita-icon-theme).
var installerOptionalPackages = [][]string{
	{"x11-utils"},
	{"xmessage", "x11-apps"},
	{"dmz-cursor-theme", "adwaita-icon-theme", "xcursor-themes"},
}

// bootloaderPackages zwraca pakiety GRUB potrzebne do "grub-install" w systemie
// docelowym: UEFI (grub-efi-<arch>-bin + efibootmgr) oraz, dla amd64, tez BIOS
// (grub-pc-bin) -- Calamares sam wybiera tryb wg /sys/firmware/efi.
func bootloaderPackages(arch string) []string {
	base := []string{"grub-common", "grub2-common", "efibootmgr", "mtools"}
	switch arch {
	case "", "amd64":
		return append(base, "grub-efi-amd64-bin", "grub-pc-bin")
	case "arm64":
		return append(base, "grub-efi-arm64-bin")
	case "i386":
		return append(base, "grub-efi-ia32-bin", "grub-pc-bin")
	default:
		return base
	}
}

// installerCybersecurityExtraPackages to DODATKOWE pakiety instalowane w
// srodowisku live/instalatora WYLACZNIE dla InstallerVariantCybersecurity --
// przydatne do szybkiej diagnostyki sieci/sprzetu PRZED (lub zamiast)
// instalacja na dysk, bez czekania na pelny system docelowy. Celowo
// niewielki zestaw (nie duplikuje calego cybersecurityPackages() z
// internal/rootfs) -- to jest live-medium, nie ma sensu obciazac go
// dziesiatkami narzedzi ktore i tak beda dostepne w zainstalowanym
// systemie gdy [project] -> type=cybersecurity.
var installerCybersecurityExtraPackages = []string{
	"nmap",
	"net-tools",
	"tcpdump",
	"dnsutils",
}

// InjectInstaller wykonuje caly krok wstrzykniecia instalatora GUI do
// rootfsDir (kopia ISO-only). workDir jest uzywany przez toolchain.Manager
// do pobierania brakujacych narzedzi (wspoldzielony z reszta buildu --
// narzedzia pobrane w kroku "build cloud" sa tu ponownie uzywane z cache).
// variant okresla branding/dodatkowe narzedzia -- patrz InstallerVariant.
func InjectInstaller(rootfsDir, workDir string, variant InstallerVariant, projectDir, arch string) error {
	// Toolchain: upewnij sie ze apt-get i dpkg-deb sa dostepne (sa zawsze,
	// ale Manager.Env() daje nam sciezke z toolchain/bin/ na czele PATH
	// co jest potrzebne jesli debootstrap byl pobrany tymczasowo).
	tc := toolchain.New(workDir)
	tcEnv := tc.Env()

	pkgs := append([]string{}, installerPackages...)
	// GRUB + efibootmgr MUSZA byc w rootfs: ten sam rootfs (squashfs) jest
	// kopiowany na dysk, a Calamares robi w nim chroot i wola grub-install.
	// Bez tych pakietow bootloader.conf konczy sie kodem 1.
	pkgs = append(pkgs, bootloaderPackages(arch)...)
	if variant == InstallerVariantCybersecurity {
		pkgs = append(pkgs, installerCybersecurityExtraPackages...)
	}

	util.Infof("  instalator GUI (%s): instalacja Calamares + Xorg (%d pakietow)...", variantLabel(variant), len(pkgs))
	if err := sandbox.ExecEnv(rootfsDir, tcEnv, "apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update: %w", err)
	}
	installArgs := append([]string{
		"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgs...)
	if err := sandbox.ExecEnv(rootfsDir, tcEnv, "apt-get", installArgs...); err != nil {
		return fmt.Errorf("apt-get install (instalator): %w", err)
	}

	for _, group := range installerOptionalPackages {
		installed := false
		for _, pkg := range group {
			args := []string{"install", "-y", "--no-install-recommends",
				"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", pkg}
			if err := sandbox.ExecEnv(rootfsDir, tcEnv, "apt-get", args...); err == nil {
				installed = true
				break
			}
			util.Warnf("opcjonalny pakiet instalatora %q niedostepny -- probuje alternatywe", pkg)
		}
		if !installed {
			util.Warnf("zaden z pakietow %v nie zainstalowal sie -- kontynuuje bez nich (funkcja opcjonalna)", group)
		}
	}

	util.Infof("  instalator GUI: zapis konfiguracji Calamares...")
	if err := writeCalamaresConfig(rootfsDir, variant, projectDir); err != nil {
		return fmt.Errorf("konfiguracja calamares: %w", err)
	}

	util.Infof("  instalator GUI: konfiguracja autostartu (usluga systemd, tty1)...")
	if err := writeInstallerAutostart(rootfsDir); err != nil {
		return fmt.Errorf("autostart instalatora: %w", err)
	}
	if err := verifyInstaller(rootfsDir, arch); err != nil {
		return fmt.Errorf("weryfikacja instalatora: %w", err)
	}

	return nil
}

// variantLabel zwraca czytelna etykiete wariantu do logow.
func variantLabel(variant InstallerVariant) string {
	if variant == InstallerVariantCybersecurity {
		return "Cybersecurity Edition"
	}
	return "default"
}

// writeCalamaresConfig zapisuje pelny zestaw plikow konfiguracyjnych
// Calamares wewnatrz rootfs (trafiaja do ISO, NIE do obrazu wypchnietego
// "build cloud"). Sekwencja modulow odpowiada standardowemu, sprawdzonemu
// przeplywowi distro-niezaleznemu (uzywanemu m.in. przez oficjalne obrazy
// Debian Live z Calamares): welcome -> locale -> keyboard -> partition ->
// users -> summary -> unpackfs (kopiowanie z /live/filesystem.squashfs) ->
// machineid -> fstab -> localecfg -> grubcfg -> bootloader -> umountcfg ->
// finished.
func writeCalamaresConfig(rootfsDir string, variant InstallerVariant, projectDir string) error {
	base := filepath.Join(rootfsDir, "etc", "calamares")
	modulesDir := filepath.Join(base, "modules")
	brandingDir := filepath.Join(base, "branding", "hackeros")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(brandingDir, 0o755); err != nil {
		return err
	}

	files := map[string]string{
		filepath.Join(base, "settings.conf"): calamaresSettingsConf,

		filepath.Join(modulesDir, "welcome.conf"):      calamaresWelcomeConf,
		filepath.Join(modulesDir, "locale.conf"):       calamaresLocaleConf,
		filepath.Join(modulesDir, "keyboard.conf"):     calamaresKeyboardConf,
		filepath.Join(modulesDir, "partition.conf"):    calamaresPartitionConf,
		filepath.Join(modulesDir, "users.conf"):        calamaresUsersConf,
		filepath.Join(modulesDir, "unpackfs.conf"):     calamaresUnpackfsConf,
		filepath.Join(modulesDir, "mount.conf"):        calamaresMountConf,
		filepath.Join(modulesDir, "machineid.conf"):    calamaresMachineidConf,
		filepath.Join(modulesDir, "fstab.conf"):        calamaresFstabConf,
		filepath.Join(modulesDir, "bootloader.conf"):   calamaresBootloaderConf,
		filepath.Join(modulesDir, "umount.conf"):       calamaresUmountConf,
		filepath.Join(modulesDir, "shellprocess.conf"): calamaresShellprocessConf,

		filepath.Join(brandingDir, "branding.desc"): brandingDescFor(variant),
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("zapis %s: %w", path, err)
		}
	}

	// Zasoby brandingu wskazane w branding.desc (logo.png, welcome.png,
	// show.qml). WCZESNIEJ NIE BYLY GENEROWANE -- Calamares nie potrafi
	// zaladowac brandingu z nieistniejacymi obrazami i konczy dzialanie, a
	// uzytkownik zostaje bez instalatora.
	logo, welcome, err := brandingPNGs(variant)
	if err != nil {
		return fmt.Errorf("generowanie obrazow brandingu: %w", err)
	}
	// Obrazy z projektu (debian-logo.png = logo HackerOS, welcome.png = banner
	// HackerOS) maja pierwszenstwo przed generowanymi.
	if project := loadProjectBranding(projectDir); len(project) > 0 {
		if b, ok := project["logo.png"]; ok {
			logo = b
		}
		if b, ok := project["welcome.png"]; ok {
			welcome = b
		}
	}
	assets := map[string][]byte{
		filepath.Join(brandingDir, "logo.png"):    logo,
		filepath.Join(brandingDir, "welcome.png"): welcome,
		filepath.Join(brandingDir, "show.qml"):    []byte(calamaresShowQML),
	}
	for path, content := range assets {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("zapis %s: %w", path, err)
		}
	}
	return nil
}

// calamaresShowQML to minimalna prezentacja wyswietlana podczas kopiowania
// systemu (slideshow z branding.desc). Jeden slajd z nazwa systemu.
const calamaresShowQML = `import QtQuick 2.0
import calamares.slideshow 1.0

Presentation {
    id: presentation

    Slide {
        Text {
            anchors.centerIn: parent
            text: "Instalowanie systemu..."
            color: "#ffffff"
            font.pixelSize: 28
        }
    }
}
`

// brandingPNGs rysuje (stdlib image/png, bez zewnetrznych plikow) logo
// (128x128) i obraz powitalny (640x240) w kolorach wariantu.
func brandingPNGs(variant InstallerVariant) (logo, welcome []byte, err error) {
	accent := color.RGBA{0x3d, 0xae, 0xe9, 0xff}
	if variant == InstallerVariantCybersecurity {
		accent = color.RGBA{0xff, 0x3b, 0x30, 0xff}
	}
	bg := color.RGBA{0x1d, 0x20, 0x23, 0xff}

	drawH := func(img *image.RGBA, x0, y0, w, h int, c color.Color) {
		t := w / 5
		fill := func(x, y, ww, hh int) {
			draw.Draw(img, image.Rect(x, y, x+ww, y+hh), &image.Uniform{c}, image.Point{}, draw.Src)
		}
		fill(x0, y0, t, h)
		fill(x0+w-t, y0, t, h)
		fill(x0, y0+h/2-t/2, w, t)
	}
	encode := func(img image.Image) ([]byte, error) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	l := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(l, l.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	draw.Draw(l, image.Rect(8, 8, 120, 120), &image.Uniform{accent}, image.Point{}, draw.Src)
	draw.Draw(l, image.Rect(14, 14, 114, 114), &image.Uniform{bg}, image.Point{}, draw.Src)
	drawH(l, 36, 34, 56, 60, accent)
	if logo, err = encode(l); err != nil {
		return nil, nil, err
	}

	w := image.NewRGBA(image.Rect(0, 0, 640, 240))
	for y := 0; y < 240; y++ {
		f := float64(y) / 239
		c := color.RGBA{
			uint8(float64(bg.R)*(1-f) + float64(accent.R)*f*0.35),
			uint8(float64(bg.G)*(1-f) + float64(accent.G)*f*0.35),
			uint8(float64(bg.B)*(1-f) + float64(accent.B)*f*0.35),
			0xff,
		}
		draw.Draw(w, image.Rect(0, y, 640, y+1), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	drawH(w, 270, 70, 100, 100, accent)
	if welcome, err = encode(w); err != nil {
		return nil, nil, err
	}
	return logo, welcome, nil
}

// brandingDescFor zwraca zawartosc branding.desc dopasowana do wariantu
// instalatora -- InstallerVariantCybersecurity dostaje inna nazwe produktu
// i czerwona palete akcentu (zamiast domyslnej niebieskiej), zeby instalator
// byl wizualnie odrozniony od zwyklej edycji juz na pierwszym ekranie.
func brandingDescFor(variant InstallerVariant) string {
	if variant == InstallerVariantCybersecurity {
		return calamaresBrandingDescCybersecurity
	}
	return calamaresBrandingDesc
}

const calamaresSettingsConf = `# Wygenerowane przez hackeros-builder -- NIE edytuj recznie w obrazie,
# zmiany rob w config/hooks/normal/*.hook.chroot wlasnego projektu.
modules-search: [ local ]
instances: []
sequence:
  - show:
      - welcome
      - locale
      - keyboard
      - partition
      - users
      - summary
  - exec:
      - partition
      - mount
      - unpackfs
      - machineid
      - fstab
      - locale
      - localecfg
      - keyboard
      - users
      - bootloader
      - shellprocess
      - umount
  - show:
      - finished
branding: hackeros
prompt-install: false
dont-chroot: false
oem-setup: false
disable-cancel: false
disable-cancel-during-exec: true
quit-at-end: false
`

const calamaresWelcomeConf = `---
showSupportUrl:       false
showKnownIssuesUrl:   false
showReleaseNotesUrl:  false
requirements:
  requiredStorage:    4.0
  requiredRam:        1.0
  internetCheckUrl:   "https://deb.debian.org"
  check:
    - storage
    - ram
    - power
  required:
    - storage
    - ram
`

const calamaresLocaleConf = `---
region: "Etc"
zone: "UTC"
`

const calamaresKeyboardConf = `---
defaultLayout: us
defaultVariant: ""
`

const calamaresPartitionConf = `---
efiSystemPartition: "/boot/efi"
userSwapChoices:
    - none
    - small
    - suspend
defaultFileSystemType: "ext4"
availableFileSystemTypes:  [ "ext4", "btrfs", "xfs" ]
enableLuksAutomatedPartitioning: true
drawNestedPartitions: false
alwaysShowPartitionLabels: true
allowManualPartitioning: true
`

const calamaresUsersConf = `---
defaultGroups:
    - users
    - sudo
    - audio
    - video
    - network
    - storage
autologinGroup:  autologin
sudoersGroup:    sudo
setRootPassword: true
doAutologin:     false
passwordRequirements:
    nonempty: true
`

// calamaresUnpackfsConf -- sourcefs "squashfs" oznacza ze Calamares sam
// loop-mountuje wskazany plik .squashfs jako zrodlo kopiowania (nie trzeba
// recznie mountowac /live/filesystem.squashfs przed buildem instalatora).
// "/run/live/medium" to standardowa sciezka montowania nosnika live przez
// live-boot (uzywany w tym projekcie, patrz internal/isobuild/builder.go
// generujace grub.cfg z "boot=live").
const calamaresUnpackfsConf = `---
unpack:
    - source: "/run/live/medium/live/filesystem.squashfs"
      sourcefs: "squashfs"
      destination: ""
`

// UWAGA: "extraMounts: []" (poprzednia wersja) NADPISYWALO domyslna liste
// Calamares -- do chroota systemu docelowego nie montowano /proc, /sys, /dev
// ani /run, wiec grub-install (i grub-probe, update-initramfs, ...) padal z
// kodem 1 ("Nie mozna zainstalowac bootloadera ... zwrocilo kod bledu 1").
const calamaresMountConf = `---
extraMounts:
    - device: proc
      fs: proc
      mountPoint: /proc
    - device: sys
      fs: sysfs
      mountPoint: /sys
    - device: /dev
      mountPoint: /dev
      options: [ bind ]
    - device: tmpfs
      fs: tmpfs
      mountPoint: /run
    - device: /run/udev
      mountPoint: /run/udev
      options: [ bind ]
extraMountsEfi:
    - device: "efivarfs"
      fs: "efivarfs"
      mountPoint: "/sys/firmware/efi/efivars"
`

const calamaresMachineidConf = `---
systemd: true
dbus: true
symlink: false
`

const calamaresFstabConf = `---
efiMountPoint: "/boot/efi"
crypttabOptions: [ "luks", "keyscript=/bin/cat" ]
`

const calamaresBootloaderConf = `---
efiBootLoader:        "grub"
kernel: "/boot/vmlinuz"
img:    "/boot/initrd.img"
grubInstall:          "grub-install"
grubMkconfig:         "grub-mkconfig"
grubCfg:              "/boot/grub/grub.cfg"
grubProbe:            "grub-probe"
efiBootMgr:           "efibootmgr"
installEFIFallback:   true
timeout: "10"
`

const calamaresUmountConf = `---
`

// calamaresShellprocessConf sprzata artefakty SAMEGO INSTALATORA z systemu
// DOCELOWEGO tuz po unpackfs+bootloader, PRZED umount -- bez tego kroku
// zainstalowany system dziedziczylby (bo unpackfs kopiuje 1:1 z tego samego
// live-medium ktore ma writeInstallerAutostart wypalone w /etc, /root,
// /usr/local/sbin) autologin roota na tty1 + natychmiastowy `startx` w
// Calamares -- czyli PIERWSZY reboot po instalacji odpalilby instalator
// PONOWNIE zamiast normalnie zalogowac do zainstalowanego systemu. To byl
// realny brakujacy krok: shellprocess wczesniej robil WYLACZNIE
// "mkdir -p /etc/hammer" (siatka bezpieczenstwa dla hammer), bez zadnego
// sprzatania po instalatorze.
//
// dontChroot: false => kazda komenda ponizej jest wykonywana W CHROOCIE do
// systemu docelowego (nie na live-medium) -- to wlasnie ten mechanizm robi
// sprzatanie bezpiecznym: usuwamy pliki na PARTYCJI DOCELOWEJ, nie na ISO.
const calamaresShellprocessConf = `---
dontChroot: false
timeout: 30
script:
    - command: "mkdir -p /etc/hammer"
    - command: "rm -f /etc/systemd/system/multi-user.target.wants/hackeros-installer.service"
    - command: "rm -f /etc/systemd/system/hackeros-installer.service"
    - command: "rm -f /usr/local/sbin/hackeros-installer-session /usr/local/sbin/hackeros-installer-xinit /usr/local/sbin/hackeros-installer-diag"
    - command: "rm -f /etc/X11/xorg.conf.d/40-hackeros-input.conf"
    - command: "rm -rf /etc/hackeros-installer"
    - command: "rm -f /var/log/hackeros-installer.log /var/log/hackeros-installer-calamares.log"
    - command: "rm -rf /etc/calamares"
`

const calamaresBrandingDesc = `---
componentName:  hackeros

welcomeStyleCalamares: true
welcomeExpandingLogo:  true

strings:
    productName:         "HackerOS"
    shortProductName:    "HackerOS"
    version:              ""
    shortVersion:         ""
    versionedName:        "HackerOS"
    shortVersionedName:   "HackerOS"
    bootloaderEntryName:  "HackerOS"
    productUrl:           "https://github.com/HackerOS-Linux-System"
    supportUrl:           "https://github.com/HackerOS-Linux-System"
    releaseNotesUrl:      "https://github.com/HackerOS-Linux-System"
    knownIssuesUrl:       "https://github.com/HackerOS-Linux-System"
    donateUrl:            "https://github.com/HackerOS-Linux-System"

images:
    productLogo:         "logo.png"
    productIcon:         "logo.png"
    productWelcome:      "welcome.png"

slideshow: "show.qml"

style:
   sidebarBackground:       "#1d2023"
   sidebarText:             "#ffffff"
   sidebarTextSelect:       "#3daee9"
   sidebarTextHighlight:    "#3daee9"
`

// calamaresBrandingDescCybersecurity -- InstallerVariantCybersecurity, patrz
// brandingDescFor. Identyczna struktura co calamaresBrandingDesc, roznica
// to nazwa produktu ("HackerOS Cybersecurity Edition") i paleta stylu
// (czerwony akcent "#ff3b30" zamiast niebieskiego "#3daee9") -- wizualny
// sygnal juz na pierwszym ekranie instalatora, ze to edycja Cybersecurity
// (Red Team), nie zwykla edycja HackerOS.
const calamaresBrandingDescCybersecurity = `---
componentName:  hackeros

welcomeStyleCalamares: true
welcomeExpandingLogo:  true

strings:
    productName:         "HackerOS Cybersecurity Edition"
    shortProductName:    "HackerOS CyberSec"
    version:              ""
    shortVersion:         ""
    versionedName:        "HackerOS Cybersecurity Edition"
    shortVersionedName:   "HackerOS CyberSec"
    bootloaderEntryName:  "HackerOS Cybersecurity Edition"
    productUrl:           "https://github.com/HackerOS-Linux-System"
    supportUrl:           "https://github.com/HackerOS-Linux-System"
    releaseNotesUrl:      "https://github.com/HackerOS-Linux-System"
    knownIssuesUrl:       "https://github.com/HackerOS-Linux-System"
    donateUrl:            "https://github.com/HackerOS-Linux-System"

images:
    productLogo:         "logo.png"
    productIcon:         "logo.png"
    productWelcome:      "welcome.png"

slideshow: "show.qml"

style:
   sidebarBackground:       "#170a0a"
   sidebarText:             "#ffffff"
   sidebarTextSelect:       "#ff3b30"
   sidebarTextHighlight:    "#ff3b30"
`

// installerServiceName to nazwa jednostki systemd uruchamiajacej instalator.
const installerServiceName = "hackeros-installer.service"

// installerServiceUnit -- jednostka systemd odpalajaca sesje instalatora na
// tty1 (zamiast poprzedniego autologinu roota + ~/.bash_profile).
//
// DLACZEGO NIE .bash_profile (przyczyna "po starcie jest tylko tty"):
// poprzedni mechanizm wymagal, zeby (1) getty@tty1 zalogowal roota przez
// autologin, (2) bash wczytal /root/.bash_profile jako login shell, (3)
// `startx` wystartowal Xorg. Kazde ogniwo moglo cicho zawiesc (np. Xorg bez
// sterownikow wejscia/wideo, bo apt szedl z --no-install-recommends), a
// flaga /run/hackeros-installer-started sprawiala, ze po nieudanym starcie X
// uzytkownik zostawal na golym tty1 BEZ zadnej diagnostyki.
//
// Teraz: osobna usluga z logiem (/var/log/hackeros-installer.log), kilkoma
// probami X (autodetekcja -> fbdev -> vesa) i awaryjnym powrotem do loginu
// na tty1 TYLKO gdy wszystkie zawioda.
//
// ConditionKernelCommandLine=boot=live -- usluga dziala WYLACZNIE na
// medium live. Zainstalowany system nigdy jej nie uruchomi, nawet gdyby
// sprzatanie w Calamares (calamaresShellprocessConf) czegos nie usunelo.
const installerServiceUnit = `[Unit]
Description=HackerOS installer (Openbox + Calamares)
ConditionKernelCommandLine=boot=live
# udev MUSI skonczyc koldplug przed startem X: Xorg dodaje klawiature/mysz/
# touchpad z udev (tag ID_INPUT_*). Gdy X wystartuje za wczesnie, urzadzenia
# wejscia nie sa jeszcze otagowane -> kursor jest, ale nie reaguje na ruch.
After=systemd-user-sessions.service systemd-logind.service dbus.service plymouth-quit-wait.service getty@tty1.service systemd-udevd.service systemd-udev-trigger.service
Conflicts=getty@tty1.service
Wants=dbus.service systemd-logind.service systemd-udevd.service systemd-udev-trigger.service

[Service]
Type=simple
User=root
Environment=HOME=/root
WorkingDirectory=/root
TTYPath=/dev/tty1
StandardInput=tty
StandardOutput=journal
StandardError=journal
TTYReset=yes
TTYVHangup=yes
TTYVTDisallocate=yes
# Plymouth (parametr "splash") trzyma tty1/DRM i maluje niebieski ekran
# startowy -- X nie przejmie wtedy ekranu (obiaw: "niebieski gradient i
# nic wiecej, a na tty2 wszystko dziala"). Zamykamy go jawnie przed startem.
ExecStartPre=-/usr/bin/plymouth quit
ExecStartPre=-/usr/bin/chvt 1
ExecStartPre=-/usr/bin/udevadm settle --timeout=30
ExecStart=/usr/local/sbin/hackeros-installer-session
Restart=no

[Install]
WantedBy=multi-user.target
`

// installerSessionScript -- uruchamiany przez usluge. Probuje startu X z
// kolejnymi konfiguracjami; znacznik /run/hackeros-installer-x-ok (tworzony
// przez hackeros-installer-xinit tuz przed calamares) odroznia "X nie
// wstal" od "X wstal, a uzytkownik zamknal/anulowal instalator".
const installerSessionScript = `#!/bin/sh
# Wygenerowane przez hackeros-builder.
LOG=/var/log/hackeros-installer.log
exec >>"$LOG" 2>&1
echo "=== hackeros-installer-session: $(date) ==="
rm -f /run/hackeros-installer-x-ok
export XDG_RUNTIME_DIR=/run/user/0
mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
plymouth quit 2>/dev/null || true

# Urzadzenia wejscia (kursor, ktory sie nie rusza = X wystartowal, zanim
# udev otagowal mysz/touchpad): doladuj sterowniki, ponow zdarzenia "add" dla
# podsystemu input i poczekaj az udev skonczy.
modprobe -a evdev usbhid hid_generic psmouse i2c_hid_acpi hid_multitouch 2>/dev/null || true
udevadm trigger --subsystem-match=input --action=add 2>/dev/null || true
udevadm settle --timeout=30 2>/dev/null || true
echo "--- urzadzenia wejscia (/proc/bus/input/devices):"
grep -E '^N: Name|^H: Handlers' /proc/bus/input/devices

XRC=1
try_x() {
    label="$1"; shift
    echo "--- proba X: $label"
    xinit /usr/local/sbin/hackeros-installer-xinit -- :0 vt1 "$@"
    XRC=$?
    echo "--- xinit ($label) zakonczyl sie kodem $XRC"
    [ -e /run/hackeros-installer-x-ok ]
}

fallback_console() {
    printf '\n[hackeros] %s\n[hackeros] Logi: /var/log/hackeros-installer.log, /var/log/hackeros-installer-calamares.log, /var/log/Xorg.0.log\n[hackeros] Diagnostyka: hackeros-installer-diag\n[hackeros] Reczny start: xinit /usr/local/sbin/hackeros-installer-xinit -- :0 vt1\n\n' "$1" >/dev/tty1 2>/dev/null
    exec /sbin/agetty --autologin root --noclear tty1 linux
}

for attempt in "autodetekcja:" "fbdev:-config /etc/hackeros-installer/xorg-fbdev.conf" "vesa:-config /etc/hackeros-installer/xorg-vesa.conf"; do
    label="${attempt%%:*}"; args="${attempt#*:}"
    # shellcheck disable=SC2086
    if try_x "$label" $args; then
        # X wstal i uruchomil Calamares. Kod 0 = normalne zakonczenie
        # (instalacja/anulowanie); inny = Calamares sie wywalil.
        if [ "$XRC" -eq 0 ]; then exit 0; fi
        fallback_console "Instalator (Calamares) zakonczyl sie bledem (kod $XRC)."
    fi
done

echo "!!! Nie udalo sie uruchomic X"
fallback_console "Nie udalo sie uruchomic graficznego instalatora (X)."
`

const installerXinitScript = `#!/bin/sh
# Wygenerowane przez hackeros-builder.
# Instalator nie potrzebuje akceleracji GL -- renderowanie programowe omija
# problemy ze sterownikami (puste/niebieskie okno Qt Quick na starszym sprzecie).
export QT_QUICK_BACKEND=software
export QT_XCB_GL_INTEGRATION=none
export LIBGL_ALWAYS_SOFTWARE=1
export QT_QPA_PLATFORM=xcb
export XDG_RUNTIME_DIR=/run/user/0
export NO_AT_BRIDGE=1
# Kursor: bez "-nocursor" i z motywem (inaczej Qt/X nie pokazuja wskaznika).
[ -d /usr/share/icons/DMZ-White ] && export XCURSOR_THEME=DMZ-White
export XCURSOR_SIZE=24
xsetroot -solid '#1d2023' -cursor_name left_ptr 2>/dev/null || true
openbox --config-file /etc/hackeros-installer/openbox-rc.xml &
sleep 1
touch /run/hackeros-installer-x-ok
# Calamares na starszym sprzecie (HDD, skan partycji/os-prober) potrzebuje
# do ~1 min zanim pokaze okno -- w tym czasie ekran bylby pusty. Pokazujemy
# komunikat i zamykamy go, gdy tylko pojawi sie okno Calamares.
if command -v xmessage >/dev/null 2>&1; then
    xmessage -center -buttons "" "HackerOS - uruchamianie instalatora, prosze czekac (do ok. minuty)..." &
    SPLASH_PID=$!
    (
        i=0
        while [ "$i" -lt 240 ]; do
            if xwininfo -root -tree 2>/dev/null | grep -qi 'calamares'; then break; fi
            i=$((i+1)); sleep 1
        done
        kill "$SPLASH_PID" 2>/dev/null
    ) &
fi
CALA_LOG=/var/log/hackeros-installer-calamares.log
echo "=== calamares: $(date) ===" >>"$CALA_LOG"
if command -v dbus-launch >/dev/null 2>&1; then
    exec dbus-launch --exit-with-session calamares -d >>"$CALA_LOG" 2>&1
fi
exec calamares -d >>"$CALA_LOG" 2>&1
`

// installerDiagScript -- jedno polecenie ("hackeros-installer-diag" na tty2,
// jako root) zbierajace DOKLADNIE te linie, ktore wskazuja dlaczego
// instalator sie nie pokazal: stan procesow, koniec logu Calamares, bledy
// Calamares/Xorg (bez halasu xkbcomp), status uslugi i dziennik. Wynik
// idzie na ekran I do /root/installer-diag.txt.
const installerDiagScript = `#!/bin/sh
OUT=/root/installer-diag.txt
{
echo "### czas: $(date)  cmdline: $(cat /proc/cmdline)"
echo
echo "### procesy (Xorg/openbox/calamares/xinit/dbus)"
ps -eo pid,etime,stat,args | grep -E 'Xorg|openbox|calamares|xinit|dbus-launch|kpmcore' | grep -v grep
echo
echo "### stan uslugi"
systemctl is-active hackeros-installer 2>&1
systemctl status hackeros-installer --no-pager -l 2>&1 | tail -n 15
echo
echo "### log sesji (bez halasu xkbcomp)"
grep -v -E 'xkbcomp|Warning:|^> |^>$|Using F23|Using last definition|AIGLX' /var/log/hackeros-installer.log 2>&1 | tail -n 40
echo
echo "### Calamares: bledy/ostrzezenia (ostatnie 40)"
grep -n -i -E 'error|fatal|fail|cannot|could not|missing|not found|bad config|abort|crash|segfault|warning' /var/log/hackeros-installer-calamares.log 2>&1 | tail -n 40
echo
echo "### Calamares: ostatnie 40 linii logu"
tail -n 40 /var/log/hackeros-installer-calamares.log 2>&1
echo
echo "### urzadzenia wejscia: jadro i X"
grep -E '^N: Name|^H: Handlers' /proc/bus/input/devices 2>&1
grep -E 'Using input driver|input device|libinput|evdev|XINPUT|No input driver' /var/log/Xorg.0.log 2>&1 | tail -n 25
udevadm info --export-db 2>/dev/null | grep -c 'ID_INPUT_MOUSE=1\|ID_INPUT_TOUCHPAD=1' | sed 's/^/urzadzen mysz\/touchpad w udev: /'
echo
echo "### Xorg: (EE) i istotne (WW)"
grep -E '\(EE\)|\(WW\)' /var/log/Xorg.0.log 2>&1 | grep -v -i -E 'xkb|firmware|fbdev|vesa' | tail -n 25
echo
echo "### branding / konfiguracja"
ls -la /etc/calamares/branding/hackeros/ /etc/calamares/modules/ 2>&1
echo
echo "### dziennik uslugi (ostatnie 25)"
journalctl -b -u hackeros-installer --no-pager 2>&1 | tail -n 25
} 2>&1 | tee "$OUT"
echo
echo "[diag] zapisano w $OUT"
`

// installerXorgInput -- jawne przypisanie sterownika libinput do urzadzen
// wejscia (klawiatura, mysz, touchpad) -- siatka bezpieczenstwa na wypadek
// gdyby systemowy 40-libinput.conf nie zostal zainstalowany/zaladowany.
const installerXorgInput = `Section "InputClass"
    Identifier "hackeros libinput pointer"
    MatchIsPointer "on"
    MatchDevicePath "/dev/input/event*"
    Driver "libinput"
EndSection

Section "InputClass"
    Identifier "hackeros libinput keyboard"
    MatchIsKeyboard "on"
    MatchDevicePath "/dev/input/event*"
    Driver "libinput"
EndSection

Section "InputClass"
    Identifier "hackeros libinput touchpad"
    MatchIsTouchpad "on"
    MatchDevicePath "/dev/input/event*"
    Driver "libinput"
    Option "Tapping" "on"
EndSection
`

const installerXorgFbdev = `Section "Device"
    Identifier "hackeros-fbdev"
    Driver "fbdev"
EndSection
`

const installerXorgVesa = `Section "Device"
    Identifier "hackeros-vesa"
    Driver "vesa"
EndSection
`

const installerOpenboxRC = `<?xml version="1.0" encoding="UTF-8"?>
<openbox_config xmlns="http://openbox.org/3.4/rc">
  <theme><name>Clearlooks</name></theme>
  <applications>
    <application class="*"><decor>no</decor><maximized>true</maximized><fullscreen>yes</fullscreen></application>
  </applications>
</openbox_config>
`

// writeInstallerAutostart sprawia, ze po starcie nosnika live PIERWSZY ekran
// to pelnoekranowy Calamares na Openboxie (bez menedzera logowania i bez
// pulpitu). Mechanizm: usluga systemd hackeros-installer.service (patrz
// installerServiceUnit). Rootfs NIE dostaje juz nadpisanego default.target --
// przejscie do multi-user.target na medium live wymusza parametr jadra
// "systemd.unit=multi-user.target" w grub.cfg (writeGrubConfig), dzieki
// czemu zainstalowany system zachowuje wlasny, domyslny target (np.
// graphical.target z menedzerem logowania) i nie laduje na golym tty.
func writeInstallerAutostart(rootfsDir string) error {
	write := func(rel string, content string, mode os.FileMode) error {
		path := filepath.Join(rootfsDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), mode)
	}

	if err := write("usr/local/sbin/hackeros-installer-session", installerSessionScript, 0o755); err != nil {
		return err
	}
	if err := write("usr/local/sbin/hackeros-installer-xinit", installerXinitScript, 0o755); err != nil {
		return err
	}
	if err := write("usr/local/sbin/hackeros-installer-diag", installerDiagScript, 0o755); err != nil {
		return err
	}
	if err := write("etc/hackeros-installer/openbox-rc.xml", installerOpenboxRC, 0o644); err != nil {
		return err
	}
	if err := write("etc/X11/xorg.conf.d/40-hackeros-input.conf", installerXorgInput, 0o644); err != nil {
		return err
	}
	if err := write("etc/hackeros-installer/xorg-fbdev.conf", installerXorgFbdev, 0o644); err != nil {
		return err
	}
	if err := write("etc/hackeros-installer/xorg-vesa.conf", installerXorgVesa, 0o644); err != nil {
		return err
	}
	if err := write("etc/systemd/system/"+installerServiceName, installerServiceUnit, 0o644); err != nil {
		return err
	}

	// systemctl enable -- reczny symlink (w chroot bez dzialajacego systemd).
	wantsDir := filepath.Join(rootfsDir, "etc", "systemd", "system", "multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, installerServiceName)
	os.Remove(link)
	if err := os.Symlink("/etc/systemd/system/"+installerServiceName, link); err != nil {
		return fmt.Errorf("enable %s: %w", installerServiceName, err)
	}

	// Sprzatanie po poprzednim mechanizmie (rootfs z OCI mogl go miec).
	os.Remove(filepath.Join(rootfsDir, "root", ".bash_profile"))
	os.RemoveAll(filepath.Join(rootfsDir, "etc", "systemd", "system", "getty@tty1.service.d"))
	// Dawniej builder nadpisywal default.target na multi-user -- usuwamy ten
	// override, jesli przyszedl z wczesniejszego obrazu.
	if fi, err := os.Lstat(filepath.Join(rootfsDir, "etc", "systemd", "system", "default.target")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if t, _ := os.Readlink(filepath.Join(rootfsDir, "etc", "systemd", "system", "default.target")); strings.HasSuffix(t, "multi-user.target") {
			os.Remove(filepath.Join(rootfsDir, "etc", "systemd", "system", "default.target"))
		}
	}
	return nil
}

// installerRequiredFiles to pliki, ktore MUSZA istniec w rootfs po
// InjectInstaller -- inaczej instalator nie wystartuje i zostanie sam tty.
var installerRequiredFiles = []string{
	"usr/bin/calamares",
	"usr/bin/openbox",
	"usr/bin/xinit",
	"usr/bin/Xorg",
	"usr/lib/xorg/modules/input/libinput_drv.so",
	"etc/calamares/settings.conf",
	"etc/calamares/branding/hackeros/branding.desc",
	"etc/calamares/branding/hackeros/logo.png",
	"etc/calamares/branding/hackeros/welcome.png",
	"etc/calamares/branding/hackeros/show.qml",
}

// verifyInstaller przerywa build z czytelnym komunikatem, gdy brakuje
// binarek/sterownikow potrzebnych do startu graficznego instalatora.
func verifyInstaller(rootfsDir, arch string) error {
	var missing []string
	required := append([]string{}, installerRequiredFiles...)
	// Lokalizacja zalezy od wydania (/usr/bin vs /usr/sbin) -- "a|b" = ktorakolwiek.
	required = append(required, "usr/sbin/grub-install|usr/bin/grub-install", "usr/sbin/grub-mkconfig|usr/bin/grub-mkconfig", "usr/sbin/efibootmgr|usr/bin/efibootmgr")
	if arch == "" || arch == "amd64" {
		required = append(required, "usr/lib/grub/x86_64-efi/modinfo.sh", "usr/lib/grub/i386-pc/modinfo.sh")
	}
	for _, spec := range required {
		found := false
		for _, rel := range strings.Split(spec, "|") {
			if _, err := os.Stat(filepath.Join(rootfsDir, rel)); err == nil {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, "/"+strings.ReplaceAll(spec, "|", " lub /"))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("w rootfs brakuje: %s -- graficzny instalator nie wystartuje (zostanie tty)",
			strings.Join(missing, ", "))
	}
	return nil
}
