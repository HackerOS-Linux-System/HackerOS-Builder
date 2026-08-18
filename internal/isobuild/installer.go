package isobuild

import (
	"fmt"
	"os"
	"path/filepath"

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
	"xinit",
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
func InjectInstaller(rootfsDir, workDir string, variant InstallerVariant) error {
	// Toolchain: upewnij sie ze apt-get i dpkg-deb sa dostepne (sa zawsze,
	// ale Manager.Env() daje nam sciezke z toolchain/bin/ na czele PATH
	// co jest potrzebne jesli debootstrap byl pobrany tymczasowo).
	tc := toolchain.New(workDir)
	tcEnv := tc.Env()

	pkgs := installerPackages
	if variant == InstallerVariantCybersecurity {
		pkgs = append(append([]string{}, installerPackages...), installerCybersecurityExtraPackages...)
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

	util.Infof("  instalator GUI: zapis konfiguracji Calamares...")
	if err := writeCalamaresConfig(rootfsDir, variant); err != nil {
		return fmt.Errorf("konfiguracja calamares: %w", err)
	}

	util.Infof("  instalator GUI: konfiguracja autostartu na tty1...")
	if err := writeInstallerAutostart(rootfsDir); err != nil {
		return fmt.Errorf("autostart instalatora: %w", err)
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
func writeCalamaresConfig(rootfsDir string, variant InstallerVariant) error {
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
	return nil
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

const calamaresMountConf = `---
extraMounts: []
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
    - command: "rm -f /etc/systemd/system/getty@tty1.service.d/autologin.conf"
    - command: "rmdir --ignore-fail-on-non-empty /etc/systemd/system/getty@tty1.service.d"
    - command: "sh -c 'grep -q \"autostart instalatora GUI na tty1\" /root/.bash_profile 2>/dev/null && rm -f /root/.bash_profile; exit 0'"
    - command: "rm -f /usr/local/sbin/hackeros-installer-xinit"
    - command: "rm -rf /etc/hackeros-installer"
    - command: "rm -f /run/hackeros-installer-started"
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

// writeInstallerAutostart sprawia, ze po starcie nosnika live PIERWSZY ekran
// jakiego uzytkownik dotyka to pelnoekranowy Calamares -- DOKLADNIE jak
// boot-time instalator (np. trybu tekstowego Anacondy/Fedora Silverblue),
// a NIE ikona/skrot wewnatrz pelnego srodowiska graficznego live.
//
// Mechanizm: systemd override dla getty@tty1 podmienia "agetty" (zwykly
// login) na autologin roota + natychmiastowy "startx", ktorego .xinitrc
// odpala WYLACZNIE openbox (bez paska, bez dekoracji okien) i na nim
// Calamares w --rootMountPoint=/ -- bez menedzera logowania, bez
// dodatkowego pulpitu w tle. default.target jest ustawiony na
// multi-user.target (tryb tekstowy) wlasnie po to, by nie startowal zaden
// pelny desktop environment przed instalatorem.
func writeInstallerAutostart(rootfsDir string) error {
	// 1) autologin roota na tty1 (wymagane zeby cokolwiek mogl wystartowac
	//    bez interakcji z klawiatura/myszka -- live media nie ma hasla).
	overrideDir := filepath.Join(rootfsDir, "etc", "systemd", "system", "getty@tty1.service.d")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		return err
	}
	overrideConf := `[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
`
	if err := os.WriteFile(filepath.Join(overrideDir, "autologin.conf"), []byte(overrideConf), 0o644); err != nil {
		return err
	}

	// 2) profil roota: zaraz po zalogowaniu na tty1 odpal X (raz, nie w
	//    petli przy kazdym logowaniu) z samym instalatorem -- jesli ktos
	//    poprosi o terminal w trakcie (np. F2 na realnym sprzecie), system
	//    nie wymusza X ponownie po wyjsciu z konsoli.
	profileDir := filepath.Join(rootfsDir, "root")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return err
	}
	bashProfile := `# Wygenerowane przez hackeros-builder: autostart instalatora GUI na tty1.
if [ -z "$DISPLAY" ] && [ "$(tty)" = "/dev/tty1" ] && [ ! -f /run/hackeros-installer-started ]; then
    touch /run/hackeros-installer-started
    exec startx /usr/local/sbin/hackeros-installer-xinit -- -nocursor
fi
`
	if err := os.WriteFile(filepath.Join(profileDir, ".bash_profile"), []byte(bashProfile), 0o644); err != nil {
		return err
	}

	// 3) .xinitrc dedykowany instalatorowi: openbox jako goly window
	//    manager (zeby Calamares mial dekoracje okna/mozliwosc przesuwania
	//    na multi-monitor), bez paneli/tapety/aplikacji startowych, i
	//    natychmiast calamares na pelnym ekranie. Gdy Calamares sie zamknie
	//    (koniec instalacji albo "Anuluj"), sesja X konczy sie tym samym --
	//    wraca tekstowy tty1, tak jak w trybie instalatora Anaconda/Fedora.
	binDir := filepath.Join(rootfsDir, "usr", "local", "sbin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	xinitScript := `#!/bin/sh
# Wygenerowane przez hackeros-builder.
openbox --config-file /etc/hackeros-installer/openbox-rc.xml &
exec calamares
`
	xinitPath := filepath.Join(binDir, "hackeros-installer-xinit")
	if err := os.WriteFile(xinitPath, []byte(xinitScript), 0o755); err != nil {
		return err
	}

	openboxDir := filepath.Join(rootfsDir, "etc", "hackeros-installer")
	if err := os.MkdirAll(openboxDir, 0o755); err != nil {
		return err
	}
	openboxRC := `<?xml version="1.0" encoding="UTF-8"?>
<openbox_config xmlns="http://openbox.org/3.4/rc">
  <theme><name>Clearlooks</name></theme>
  <applications>
    <application class="*"><decor>no</decor><maximized>true</maximized><fullscreen>yes</fullscreen></application>
  </applications>
</openbox_config>
`
	if err := os.WriteFile(filepath.Join(openboxDir, "openbox-rc.xml"), []byte(openboxRC), 0o644); err != nil {
		return err
	}

	// 4) default.target = multi-user (tekstowy) -- to NIE jest skrot na
	//    pulpicie: nie ma zadnego pulpitu live do wystartowania w pierwszej
	//    kolejnosci. Pierwszy graficzny ekran jakikolwiek uzytkownik widzi
	//    to instalator.
	systemdDir := filepath.Join(rootfsDir, "etc", "systemd", "system")
	if err := os.MkdirAll(systemdDir, 0o755); err != nil {
		return err
	}
	defaultTargetLink := filepath.Join(systemdDir, "default.target")
	os.Remove(defaultTargetLink) // moze juz istniec jako symlink z bazowego rootfs
	if err := os.Symlink("/lib/systemd/system/multi-user.target", defaultTargetLink); err != nil {
		return fmt.Errorf("ustawienie default.target: %w", err)
	}

	return nil
}
