package rootfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/download"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hkgen"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/sandbox"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/toolchain"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// defaultMirror to domyslny mirror Debiana uzywany przez debootstrap gdy
// uzytkownik nie poda innego (na razie sztywno -- ROADMAP: konfigurowalny
// mirror per-projekt w config.hk).
const defaultMirror = "http://deb.debian.org/debian"

// Builder buduje rootfs na podstawie sparsowanego projektu i konfiguracji.
type Builder struct {
	Project   *liveparse.Project
	Config    *config.Config
	RootfsDir string // katalog docelowy rootfs
	WorkDir   string // katalog roboczy buildu (na toolchain-bin/ i inne temp)

	// OriginRefspec to refspec obrazu OCI wpisywany do /etc/deb-ostree/deb-ostree.hk
	OriginRefspec string
}

// New tworzy nowy Builder.
func New(project *liveparse.Project, cfg *config.Config, rootfsDir, workDir string) *Builder {
	return &Builder{Project: project, Config: cfg, RootfsDir: rootfsDir, WorkDir: workDir}
}

// macPackages zwraca liste pakietow apt wymaganych przez wybrany system
// kontroli dostepu obowiazkowego (MAC) z [project] -> selinux.
//
// AppArmor (domyslny dla Debiana):
//   - apparmor            -- glowna implementacja AppArmor w kernelu + narzedzia
//   - apparmor-profiles   -- predefiniowane profile (przydatne dla Firefox, Cups itp.)
//   - apparmor-utils      -- aa-status, aa-enforce, aa-complain (diagnostyka)
//
// SELinux (kiedy [project] -> selinux = true):
//   - selinux-basics      -- bazowa konfiguracja SELinux dla Debiana (selinux-activate itp.)
//   - selinux-policy-default -- polityki SELinux (targeted -- najbardziej praktyczna)
//   - policycoreutils     -- setenforce, getenforce, restorecon, sestatus
//   - policycoreutils-python-utils -- semanage (zarzadzanie politykami przez CLI)
//   - auditd              -- daemon audytu (SELinux loguje AVC denials przez audit)
//
// Oba systemy sa wzajemnie wylaczne -- instalacja obu jest mozliwa technicznie
// ale nie ma sensu. hackeros-builder instaluje JEDEN z nich w zaleznosci od
// konfiguracji, nigdy oba.
func (b *Builder) macPackages() []string {
	if b.Config.Project.MAC == config.MACSELinux {
		return []string{
			"selinux-basics",
			"selinux-policy-default",
			"policycoreutils",
			"policycoreutils-python-utils",
			"auditd",
		}
	}
	// AppArmor (domyslne)
	return []string{
		"apparmor",
		"apparmor-profiles",
		"apparmor-utils",
	}
}

// installMACPackages instaluje pakiety systemu kontroli dostepu (AppArmor
// lub SELinux) wewnatrz rootfs przez sandbox. Wykonywane PRZED hookami
// uzytkownika zeby hooki mogly juz zakladac ze MAC jest dostepny.
func (b *Builder) installMACPackages() error {
	pkgs := b.macPackages()
	macName := "AppArmor"
	if b.Config.Project.MAC == config.MACSELinux {
		macName = "SELinux"
	}
	util.Infof("  MAC (%s): instalacja %d pakietow...", macName, len(pkgs))

	args := append([]string{
		"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgs...)
	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("instalacja pakietow %s (%v): %w", macName, pkgs, err)
	}
	return nil
}

// Build wykonuje caly przeplyw budowy rootfs.
// Narzedzia (debootstrap, mksquashfs itp.) sa pobierane tymczasowo jesli
// brakuje ich na hoscie -- bez instalacji, bez konfliktow zaleznosci.
func (b *Builder) Build() error {
	if err := b.prepareDir(); err != nil {
		return err
	}

	// --- toolchain: przygotuj narzedzia build-time ---
	util.Infof("Krok 0/7: sprawdzanie/pobieranie narzedzi build-time...")
	tc := toolchain.New(b.WorkDir)
	if err := tc.PrepareAll(); err != nil {
		return fmt.Errorf("toolchain: %w", err)
	}
	// Ustaw PATH tak by toolchain-bin/ byl pierwszy -- procesy potomne
	// (debootstrap, apt-get w sandbox) automatycznie znajda tymczasowe binarki.
	if err := os.Setenv("PATH", tc.Env()[0][len("PATH="):]); err != nil {
		return fmt.Errorf("ustawienie PATH toolchain: %w", err)
	}

	util.Infof("Krok 1/7: debootstrap (%s)...", b.Config.Release)
	if err := b.runDebootstrap(); err != nil {
		return fmt.Errorf("debootstrap: %w", err)
	}

	util.Infof("Krok 2/7: preseed debconf + sudo-stub...")
	if err := b.seedDebconf(); err != nil {
		return fmt.Errorf("preseed debconf: %w", err)
	}
	if err := b.installSudoStub(); err != nil {
		return fmt.Errorf("sudo stub: %w", err)
	}

	if len(b.Project.ExtraSources) > 0 {
		util.Infof("Krok 3/8: dodatkowe zrodla apt (%d)...", len(b.Project.ExtraSources))
		if err := b.applyExtraSources(); err != nil {
			return fmt.Errorf("extra sources: %w", err)
		}
	} else {
		util.Infof("Krok 3/8: brak dodatkowych zrodel apt -- pominieto")
	}

	util.Infof("Krok 4/8: instalacja systemu MAC ([project] -> selinux=%v)...",
		b.Config.Project.MAC == config.MACSELinux)
	if err := b.installMACPackages(); err != nil {
		return fmt.Errorf("instalacja MAC: %w", err)
	}

	util.Infof("Krok 5/8: instalacja %d pakiet(ow)...", len(b.Project.Packages))
	if err := b.installPackages(); err != nil {
		return fmt.Errorf("instalacja pakietow: %w", err)
	}

	if b.Project.IncludesChroot != "" {
		util.Infof("Krok 6/8: kopiowanie includes.chroot...")
		if err := b.copyIncludesChroot(); err != nil {
			return fmt.Errorf("includes.chroot: %w", err)
		}
	} else {
		util.Infof("Krok 6/8: brak includes.chroot -- pominieto")
	}

	util.Infof("Krok 7/8: wykonywanie %d hook(ow)...", len(b.Project.Hooks))
	if err := b.runHooks(); err != nil {
		return fmt.Errorf("hooks: %w", err)
	}

	util.Infof("Krok 8/8: wstrzykiwanie deb-ostree + generowanie deb-ostree.hk...")
	if err := b.injectDebOstree(); err != nil {
		return fmt.Errorf("deb-ostree injection: %w", err)
	}
	if err := b.installDebOstreeDeps(); err != nil {
		return fmt.Errorf("deb-ostree biblioteki dynamiczne: %w", err)
	}
	if err := b.generateDebOstreeConfig(); err != nil {
		return fmt.Errorf("generowanie deb-ostree.hk: %w", err)
	}

	util.Infof("Rootfs zbudowany: %s", b.RootfsDir)
	return nil
}

func (b *Builder) prepareDir() error {
	if err := os.RemoveAll(b.RootfsDir); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", b.RootfsDir, err)
	}
	if err := os.MkdirAll(b.RootfsDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie %s: %w", b.RootfsDir, err)
	}
	return nil
}

// runDebootstrap wywoluje "debootstrap <suite> <target> <mirror>". To jest
// JEDYNA czesc procesu ktora delegujemy do istniejacego narzedzia Debiana --
// reimplementacja debootstrap (rozwiazywanie zaleznosci bazowego systemu od
// zera) wykraczalaby daleko poza zakres hackeros-builder.
func (b *Builder) runDebootstrap() error {
	return util.RunStreaming("", "debootstrap",
		"--arch=amd64",
		b.Config.Release,
		b.RootfsDir,
		defaultMirror,
	)
}

// installSudoStub instaluje /usr/local/sbin/sudo wewnatrz rootfs jako
// prosty wrapper "exec $@" (uruchamia komende bezposrednio, bez faktycznych
// uprawnien sudo). Ma to jeden cel: hooki uzytkownikow czesto zaczynaja linie
// od "sudo apt-get install ..." / "sudo curl ..." bo sa pisane z myśla o
// uruchomieniu na normalnej maszynie z userem. Wewnatrz kontenera nspawn
// build ZAWSZE biegnie jako root (uid=0), wiec sudo jest zbedne -- ale jego
// BRAK powoduje "sudo: not found" i natychmiastowe wyjscie ze statusem != 0,
// co konczy caly build bledem (dokladnie ten komunikat widac na zrzucie
// ekranu: "/tmp-hackeros-hook-install-mullvad.hook.chroot: 2: sudo: not found").
//
// Stub jest instalowany w /usr/local/sbin/sudo (ma priorytet nad ewentualnym
// pakietowym /usr/bin/sudo jezeli ten zostanie doinstalowany przez hooks --
// nie. /usr/local/sbin jest pierwsze w $PATH wewnatrz nspawn, wiec stub
// zawsze wygrywa na czas builda). Zostaje usuniety w ostatnim kroku by nie
// trafic do finalnego obrazu.
func (b *Builder) installSudoStub() error {
	stubDir := filepath.Join(b.RootfsDir, "usr", "local", "sbin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		return err
	}
	stub := "#!/bin/sh\n# sudo-stub wygenerowany przez hackeros-builder.\n# Hooki pisane z myslą o normalnej maszynie uzywaja 'sudo <cmd>' -- wewnatrz\n# kontenera nspawn build jest rootem, wiec sudo jest zbedne. Ten stub\n# po prostu odpala komende bezposrednio, eliminujac \"sudo: not found\".\nexec \"$@\"\n"
	stubPath := filepath.Join(stubDir, "sudo")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		return fmt.Errorf("zapis sudo-stub: %w", err)
	}
	util.Infof("  sudo-stub zainstalowany: %s", stubPath)
	return nil
}

// removeSudoStub usuwa stub sudo z rootfs po wykonaniu hookow -- nie powinien
// trafic do finalnego obrazu (w zainstalowanym systemie sudo jest normalnym
// pakietem z SUID, nie wrapperem).
func (b *Builder) removeSudoStub() {
	stubPath := filepath.Join(b.RootfsDir, "usr", "local", "sbin", "sudo")
	if err := os.Remove(stubPath); err != nil && !os.IsNotExist(err) {
		util.Warnf("Nie mozna usunac sudo-stub %s: %v", stubPath, err)
	}
}

// installPackages wykonuje apt-get update + apt-get install wewnatrz
// izolowanego kontenera nspawn (nie plain chroot -- patrz Build() i
// util.RunNspawnStreaming dla uzasadnienia).
func (b *Builder) installPackages() error {
	if err := b.sandboxExec("apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update: %w", err)
	}

	if len(b.Project.Packages) == 0 {
		return nil
	}

	args := append([]string{
		"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
		"-o", "APT::Get::Assume-Yes=true",
	}, b.Project.Packages...)
	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("apt-get install: %w", err)
	}
	return nil
}

// applyExtraSources dopisuje config/archives/*.list.chroot do
// rootfs/etc/apt/sources.list.d/hackeros-extra.list i importuje klucze GPG.
func (b *Builder) applyExtraSources() error {
	sourcesDir := filepath.Join(b.RootfsDir, "etc", "apt", "sources.list.d")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return err
	}
	listPath := filepath.Join(sourcesDir, "hackeros-extra.list")
	f, err := os.Create(listPath)
	if err != nil {
		return fmt.Errorf("tworzenie %s: %w", listPath, err)
	}
	defer f.Close()
	for _, line := range b.Project.ExtraSources {
		if _, err := fmt.Fprintln(f, line); err != nil {
			return err
		}
	}
	for _, keyPath := range b.Project.ExtraKeys {
		destName := filepath.Base(keyPath)
		destPath := filepath.Join(b.RootfsDir, "etc", "apt", "trusted.gpg.d", destName)
		if err := copyFile(keyPath, destPath, 0o644); err != nil {
			return fmt.Errorf("kopiowanie klucza GPG %s: %w", keyPath, err)
		}
	}
	return nil
}

// copyIncludesChroot kopiuje rekurencyjnie config/includes.chroot/* do
// korzenia rootfs, zachowujac uprawnienia plikow (1:1 jak live-build).
func (b *Builder) copyIncludesChroot() error {
	return filepath.Walk(b.Project.IncludesChroot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(b.Project.IncludesChroot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dest := filepath.Join(b.RootfsDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dest)
		}
		return copyFile(path, dest, info.Mode())
	})
}

// runHooks wykonuje kazdy skrypt hooks wewnatrz izolowanego kontenera nspawn.
// Sudo-stub instalowany w kroku 2 jest USUWANY po wykonaniu WSZYSTKICH hookow
// (patrz installSudoStub / removeSudoStub).
func (b *Builder) runHooks() error {
	defer b.removeSudoStub()
	for _, h := range b.Project.Hooks {
		util.Infof("  hook: %s", h.Name)
		tmpName := "/tmp-hackeros-hook-" + h.Name
		destOnHost := filepath.Join(b.RootfsDir, tmpName)
		if err := copyFile(h.Path, destOnHost, 0o755); err != nil {
			return fmt.Errorf("kopiowanie hooka %s: %w", h.Name, err)
		}
		if err := b.sandboxExec(tmpName); err != nil {
			os.Remove(destOnHost)
			return fmt.Errorf("wykonanie hooka %s: %w", h.Name, err)
		}
		if err := os.Remove(destOnHost); err != nil {
			util.Warnf("Nie mozna usunac tymczasowego hooka %s: %v", destOnHost, err)
		}
	}
	return nil
}

// injectDebOstree sciaga najnowsza wersje deb-ostree z GitHub Releases
// (lub wersje wskazana przez DEBOSTREE_VERSION jesli ustawiona -- przydatne
// do pinowania konkretnej wersji / testow offline) i umieszcza w
// rootfs/usr/bin/deb-ostree z uprawnieniami a+x.
func (b *Builder) injectDebOstree() error {
	version := os.Getenv("DEBOSTREE_VERSION")
	if version == "" {
		v, err := download.LatestDebOstreeVersion()
		if err != nil {
			return fmt.Errorf("wykrywanie najnowszej wersji deb-ostree: %w", err)
		}
		version = v
	}

	destPath := filepath.Join(b.RootfsDir, "usr", "bin", "deb-ostree")
	util.Infof("  deb-ostree %s -> %s", version, destPath)

	if err := download.DownloadDebOstree(version, destPath); err != nil {
		return err
	}
	return nil
}

// generateDebOstreeConfig wywoluje hkgen, by wygenerowac kompletny plik
// /etc/deb-ostree/deb-ostree.hk wewnatrz rootfs, gotowy do uzycia przez
// deb-ostree natychmiast po pierwszym boocie zbudowanego systemu.
//
// Wartosci sciezek (sysroot/ostree/overlay/apt) sa pozostawione jako
// domyslne deb-ostree (przekazujemy puste stringi -> hkgen wypelni je
// wartosciami domyslnymi zgodnymi z cmd/types.h w repo deb-ostree).
// OriginRefspec jest wypelniany przez b.OriginRefspec, jesli zostal ustawiony
// przez wolajacego (typowo PO komendzie "build cloud", patrz cmd/build_cloud.go).
func (b *Builder) generateDebOstreeConfig() error {
	destPath := filepath.Join(b.RootfsDir, "etc", "deb-ostree", "deb-ostree.hk")

	// Katalog /etc/deb-ostree/ moze nie istniec w bazowym rootfs debootstrap --
	// tworzy go tylko pakiet deb-ostree, a my wstrzykujemy binark deb-ostree
	// recznie (nie przez apt), wiec musimy sami zadbac o katalog.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu %s: %w", filepath.Dir(destPath), err)
	}

	params := hkgen.DebOstreeConfigParams{
		OSName:        "debian",
		OriginRefspec: b.OriginRefspec,
	}

	if err := hkgen.WriteDebOstreeConfig(destPath, params); err != nil {
		return fmt.Errorf("zapis %s: %w", destPath, err)
	}
	util.Infof("  wygenerowano: %s", destPath)
	return nil
}

// noninteractiveEnv to zmienne srodowiskowe wstrzykiwane do KAZDEGO procesu
// uruchamianego wewnatrz chroot (apt-get, dpkg przez postinst, hooki).
//
// Bez DEBIAN_FRONTEND=noninteractive debconf wybiera dialog/whiptail jako
// frontend gdy wykryje terminal (TUI tak jak na zrzucie ekranu z pytaniem
// o "keyboard-configuration") i ZATRZYMUJE caly build czekajac na reczna
// odpowiedz uzytkownika -- w nienadzorowanym buildzie ISO to jest blad, nie
// funkcja. DEBCONF_NONINTERACTIVE_SEEN=true dodatkowo wycisza pytania o
// priorytecie "high", ktore normalnie pokazuja sie nawet w trybie
// noninteractive przy pierwszym uruchomieniu danego pakietu.
var noninteractiveEnv = []string{
	"DEBIAN_FRONTEND=noninteractive",
	"DEBCONF_NONINTERACTIVE_SEEN=true",
	"DEBCONF_NOWARNINGS=yes",
	"LC_ALL=C",
	"LANG=C",
	"LANGUAGE=C",
}

// sandboxExec uruchamia komende wewnatrz rootfs w izolowanym srodowisku
// (unshare + chroot) -- patrz internal/sandbox/sandbox.go.
func (b *Builder) sandboxExec(command string, args ...string) error {
	return sandbox.Exec(b.RootfsDir, command, args...)
}

// sandboxExecWithStdin jak sandboxExec ale z danymi na stdin.
func (b *Builder) sandboxExecWithStdin(data []byte, command string, args ...string) error {
	return sandbox.ExecWithStdin(b.RootfsDir, data, command, args...)
}

// seedDebconf preseeduje debconf rozsadnymi wartosciami domyslnymi dla
// najczesciej "gadatliwych" pakietow (keyboard-configuration, tzdata,
// locales) PRZED instalacja pakietow -- DEBIAN_FRONTEND=noninteractive samo
// w sobie wystarcza zeby nie pokazac okienka, ale bez zadnej odpowiedzi w
// bazie debconf niektore postinst i tak potrafia "utknac" na braku wartosci.
// Preseed jest wykonywany przez "debconf-set-selections" wewnatrz chroot.
func (b *Builder) seedDebconf() error {
	preseed := "keyboard-configuration\tkeyboard-configuration/layout\tselect\tEnglish (US)\n" +
		"keyboard-configuration\tkeyboard-configuration/layoutcode\tstring\tus\n" +
		"keyboard-configuration\tkeyboard-configuration/variant\tselect\tEnglish (US)\n" +
		"keyboard-configuration\tkeyboard-configuration/modelcode\tstring\tpc105\n" +
		"keyboard-configuration\tkeyboard-configuration/model\tselect\tGeneric 105-key PC (intl.)\n" +
		"keyboard-configuration\tkeyboard-configuration/altgr\tselect\tThe default for the keyboard layout\n" +
		"keyboard-configuration\tkeyboard-configuration/unsupported_layout\tboolean\ttrue\n" +
		"keyboard-configuration\tkeyboard-configuration/unsupported_options\tboolean\ttrue\n" +
		"tzdata\ttzdata/Areas\tselect\tEtc\n" +
		"tzdata\ttzdata/Zones/Etc\tselect\tUTC\n" +
		"locales\tlocales/default_environment_locale\tselect\tC.UTF-8\n" +
		"locales\tlocales/locales_to_be_generated\tmultiselect\ten_US.UTF-8 UTF-8\n" +
		"debconf\tdebconf/frontend\tselect\tNoninteractive\n" +
		"debconf\tdebconf/priority\tselect\tcritical\n" +
		"man-db\tman-db/auto-update\tboolean\tfalse\n"

	return b.sandboxExecWithStdin([]byte(preseed), "debconf-set-selections")
}

// copyFile kopiuje plik src do dst, ustawiajac podane uprawnienia (mode)
// i tworzac katalogi nadrzedne jesli potrzebne.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
}
