package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// noninteractiveEnv to zmienne srodowiskowe wstrzykiwane do KAZDEGO procesu
// uruchamianego wewnatrz sandbox. Eliminuja wszelkie interaktywne dialogi
// debconf/dpkg podczas instalacji pakietow i wykonywania hookow.
var noninteractiveEnv = []string{
	"DEBIAN_FRONTEND=noninteractive",
	"DEBCONF_NONINTERACTIVE_SEEN=true",
	"DEBCONF_NOWARNINGS=yes",
	"LC_ALL=C",
	"LANG=C",
	"LANGUAGE=C",
	// needrestart (domyslnie wlaczony w wielu instalacjach Debiana od paczki
	// libpam-systemd/init-system-helpers) po instalacji dowolnego pakietu
	// z demonem pyta interaktywnie "ktore uslugi zrestartowac?" -- bez tty
	// ten dialog albo wisi w nieskonczonosc, albo (zaleznie od wersji) od
	// razu przerywa caly krok bledem. "a" = automatycznie restartuj wszystko,
	// bez pytania.
	"NEEDRESTART_MODE=a",
	"NEEDRESTART_SUSPEND=1",
	// ucf (uzywany przez postinst wielu pakietow do zarzadzania plikami
	// konfiguracyjnymi) ma WLASNY mechanizm pytania o konflikty, niezalezny
	// od Dpkg::Options::=--force-conf*. UCF_FORCE_CONFFOLD wymusza
	// zachowanie istniejacego pliku (spojne z --force-confold dla dpkg).
	"UCF_FORCE_CONFFOLD=1",
	// apt-listchanges (jesli zainstalowany hookiem/pakietem) domyslnie
	// otwiera pager z changelogiem i CZEKA na klawisz -- "none" wylacza
	// wyswietlanie w ogole.
	"APT_LISTCHANGES_FRONTEND=none",
	// PATH wewnatrz chroot musi zawierac /sbin i /usr/sbin -- bez tego
	// apt-get nie znajdzie dpkg, ldconfig, update-alternatives itp.
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
}

// Exec uruchamia <command> <args...> wewnatrz rootfsDir w izolowanym
// srodowisku (wlasny namespace mount+PID+UTS) przez unshare + chroot.
// stdout i stderr sa przekazywane na zywo do terminala (streaming).
func Exec(rootfsDir string, command string, args ...string) error {
	return execInternal(rootfsDir, nil, nil, 0, command, args...)
}

// ExecEnv jak Exec, ale dopisuje dodatkowe zmienne srodowiskowe do
// noninteractiveEnv (format "KLUCZ=WARTOSC").
func ExecEnv(rootfsDir string, extraEnv []string, command string, args ...string) error {
	return execInternal(rootfsDir, extraEnv, nil, 0, command, args...)
}

// ExecWithStdin jak Exec, ale podaje stdinData na stdin komendy wewnatrz
// sandbox (np. dla "debconf-set-selections", ktore czyta preseed z stdin).
func ExecWithStdin(rootfsDir string, stdinData []byte, command string, args ...string) error {
	return execInternal(rootfsDir, nil, stdinData, 0, command, args...)
}

// DefaultHookTimeout to maksymalny czas na wykonanie POJEDYNCZEGO hooka
// (config/hooks/normal/*.hook.chroot) zanim build zostanie przerwany z
// jasnym komunikatem. Nadpisywalny zmienna srodowiskowa
// HACKEROS_HOOK_TIMEOUT_SECONDS (np. dla hookow ktore legalnie potrzebuja
// duzo czasu -- kompilacja ze zrodel, duze pobieranie).
//
// Powod istnienia: stdin hooka jest zawsze /dev/null (execInternal nie
// podaje stdin dla ExecHook), wiec proste "read odpowiedz" w skrypcie od
// razu dostaje EOF zamiast wisiec -- ale niektore narzedzia (dialog/whiptail
// bez wykrytego terminala, proces czekajacy na polaczenie sieciowe/GUI ktore
// nigdy nie nadejdzie) moga zawiesic sie mimo to. Timeout jest ostatnia
// linia obrony: build ZAWSZE sie konczy, zamiast wisiec bez konca w CI/skrypcie.
const DefaultHookTimeout = 20 * time.Minute

// ExecHook uruchamia skrypt hooka wewnatrz rootfsDir z limitem czasu
// (DefaultHookTimeout, chyba ze nadpisany HACKEROS_HOOK_TIMEOUT_SECONDS).
// W razie przekroczenia limitu zwraca czytelny blad ExecTimeoutError zamiast
// pozwolic calemu buildowi wisiec w nieskonczonosc.
func ExecHook(rootfsDir string, hookPath string) error {
	timeout := DefaultHookTimeout
	if v := os.Getenv("HACKEROS_HOOK_TIMEOUT_SECONDS"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
	}
	return execInternal(rootfsDir, nil, nil, timeout, hookPath)
}

// ExecTimeoutError sygnalizuje ze komenda zostala zabita po przekroczeniu
// limitu czasu -- odrozniane od zwyklego bledu (kod wyjscia != 0), zeby
// wolajacy mogl wyswietlic wskazowke ("hook prawdopodobnie czeka na
// interakcje uzytkownika, ktorej nienadzorowany build nie moze dostarczyc").
type ExecTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *ExecTimeoutError) Error() string {
	return fmt.Sprintf("przekroczono limit czasu (%s) na wykonanie %q", e.Timeout, e.Command)
}

// execInternal to wspolna implementacja Exec/ExecEnv/ExecWithStdin/ExecHook.
// timeout=0 oznacza brak limitu (Exec/ExecEnv/ExecWithStdin -- apt-get na
// wolnym mirrorze moze legalnie trwac dlugo, wiec te sciezki go nie maja).
func execInternal(rootfsDir string, extraEnv []string, stdin []byte, timeout time.Duration, command string, args ...string) error {
	// Zapewnij istnienie punktow montowania wewnatrz rootfs przed wejsciem
	// do namespace -- chroot nie tworzy ich automatycznie, a mount -t proc
	// wysypie sie jesli katalog docelowy nie istnieje.
	for _, sub := range []string{"proc", "sys", "dev", "dev/pts"} {
		if err := os.MkdirAll(filepath.Join(rootfsDir, sub), 0o755); err != nil {
			return fmt.Errorf("sandbox: mkdir %s w rootfs: %w", sub, err)
		}
	}

	script := buildMountAndChrootScript(rootfsDir, command, args)

	env := append(os.Environ(), noninteractiveEnv...)
	env = append(env, extraEnv...)

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// unshare --kill-child: gdy hackeros-builder dostanie SIGTERM/SIGKILL
	// (albo gdy context wygasnie po timeout -> CommandContext wysyla
	// SIGKILL do "unshare"), kernel wysyla SIGKILL do calej grupy procesow
	// wewnatrz namespace -- gwarantuje ze zadne "osierocone" procesy budowy
	// nie zostaja w tle, nawet jesli to byl hook ktory sam odpalil cos w tle.
	cmd := exec.CommandContext(ctx, "unshare",
		"--mount",      // prywatny namespace mount
		"--pid",        // prywatny namespace PID
		"--fork",       // wymagane przez --pid: unshare forkuje przed exec
		"--uts",        // prywatny UTS namespace (izolacja hostname)
		"--kill-child", // SIGKILL do child grupy gdy unshare umiera
		"sh", "-e", "-c", script,
	)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return &ExecTimeoutError{Command: command, Timeout: timeout}
	}
	if err != nil {
		return fmt.Errorf("sandbox: exec %q w %s nie powiodl sie: %w", command, rootfsDir, err)
	}
	return nil
}

// ExecWithLines jak Exec, ale zamiast przekazywac stdout surowo na zywo do
// terminala, woła onStdoutLine dla kazdej linii stdout -- uzywane do
// zasilania paskow postepu (internal/util.ProgressBar) REALNYMI zdarzeniami
// sparsowanymi z wyjscia polecenia (np. "Unpacking"/"Setting up" z
// apt-get), zamiast zalewac terminal surowym, gadatliwym logiem apt.
// stderr nadal idzie NA ZYWO do terminala -- bledy maja byc widoczne
// natychmiast, niezaleznie od tego czy stdout jest w tej chwili
// przechwytywany.
func ExecWithLines(rootfsDir string, extraEnv []string, onStdoutLine func(string), command string, args ...string) error {
	for _, sub := range []string{"proc", "sys", "dev", "dev/pts"} {
		if err := os.MkdirAll(filepath.Join(rootfsDir, sub), 0o755); err != nil {
			return fmt.Errorf("sandbox: mkdir %s w rootfs: %w", sub, err)
		}
	}

	script := buildMountAndChrootScript(rootfsDir, command, args)

	env := append(os.Environ(), noninteractiveEnv...)
	env = append(env, extraEnv...)

	cmd := exec.Command("unshare",
		"--mount", "--pid", "--fork", "--uts", "--kill-child",
		"sh", "-e", "-c", script,
	)
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("sandbox: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sandbox: start %q w %s: %w", command, rootfsDir, err)
	}

	scanner := bufio.NewScanner(stdout)
	// apt-get potrafi wypisywac bardzo dlugie linie (np. przy koliduj\u0105cych
	// plikach konfiguracyjnych) -- domyslny limit bufio.Scanner (64KB) w
	// rzadkich przypadkach mogl to obcinac; podnosimy limit do 1MB, tanie
	// zabezpieczenie kosztem pomijalnej ilosci pamieci.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		onStdoutLine(scanner.Text())
	}
	scanErr := scanner.Err()

	waitErr := cmd.Wait()
	if waitErr != nil {
		return fmt.Errorf("sandbox: exec %q w %s nie powiodl sie: %w", command, rootfsDir, waitErr)
	}
	if scanErr != nil {
		return fmt.Errorf("sandbox: odczyt stdout %q w %s: %w", command, rootfsDir, scanErr)
	}
	return nil
}

// buildMountAndChrootScript buduje skrypt sh wykonywany wewnatrz nowego
// namespace mount (po unshare). Skrypt:
//  1. Montuje /proc,/sys,/dev,/dev/pts wewnatrz rootfsDir (prywatnie).
//  2. Rejestruje trap EXIT ktory odmontowuje je przy kazdym wyjsciu
//     (normalnym, bledzie, przerwaniu) -- defensywnie, bo namespace
//     i tak by to sprzatnal, ale trap eliminuje rzadkie edge-case'y
//     ze starszymi wersjami kernela gdzie --kill-child nie dzialal.
//  3. Wykonuje chroot rootfsDir command args...
//
// Argumenty sa shell-escapowane apostrofami (apostrofy w tresci sa
// zamieniane na '\”) -- wystarczajace dla sciezek i nazw pakietow Debiana.
func buildMountAndChrootScript(rootfsDir, command string, args []string) string {
	quot := func(s string) string {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}

	qRootfs := quot(rootfsDir)
	qCmd := quot(command)

	var qArgs strings.Builder
	for i, a := range args {
		if i > 0 {
			qArgs.WriteByte(' ')
		}
		qArgs.WriteString(quot(a))
	}

	return fmt.Sprintf(`set -e
ROOTFS=%s
mount -t proc    proc       "$ROOTFS/proc"
mount -t sysfs   sysfs      "$ROOTFS/sys"
mount --bind     /dev       "$ROOTFS/dev"
mount --bind     /dev/pts   "$ROOTFS/dev/pts"
_cleanup() {
    umount -l "$ROOTFS/dev/pts" 2>/dev/null || true
    umount -l "$ROOTFS/dev"     2>/dev/null || true
    umount -l "$ROOTFS/sys"     2>/dev/null || true
    umount -l "$ROOTFS/proc"    2>/dev/null || true
}
trap _cleanup EXIT
exec chroot "$ROOTFS" %s %s
`, qRootfs, qCmd, qArgs.String())
}
