package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// PATH wewnatrz chroot musi zawierac /sbin i /usr/sbin -- bez tego
	// apt-get nie znajdzie dpkg, ldconfig, update-alternatives itp.
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
}

// Exec uruchamia <command> <args...> wewnatrz rootfsDir w izolowanym
// srodowisku (wlasny namespace mount+PID+UTS) przez unshare + chroot.
// stdout i stderr sa przekazywane na zywo do terminala (streaming).
func Exec(rootfsDir string, command string, args ...string) error {
	return execInternal(rootfsDir, nil, nil, command, args...)
}

// ExecEnv jak Exec, ale dopisuje dodatkowe zmienne srodowiskowe do
// noninteractiveEnv (format "KLUCZ=WARTOSC").
func ExecEnv(rootfsDir string, extraEnv []string, command string, args ...string) error {
	return execInternal(rootfsDir, extraEnv, nil, command, args...)
}

// ExecWithStdin jak Exec, ale podaje stdinData na stdin komendy wewnatrz
// sandbox (np. dla "debconf-set-selections", ktore czyta preseed z stdin).
func ExecWithStdin(rootfsDir string, stdinData []byte, command string, args ...string) error {
	return execInternal(rootfsDir, nil, stdinData, command, args...)
}

// execInternal to wspolna implementacja Exec/ExecEnv/ExecWithStdin.
func execInternal(rootfsDir string, extraEnv []string, stdin []byte, command string, args ...string) error {
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

	// unshare --kill-child: gdy hackeros-builder dostanie SIGTERM/SIGKILL,
	// kernel wysyla SIGKILL do calej grupy procesow wewnatrz namespace --
	// gwarantuje ze zadne "osierozone" procesy budowy nie zostaja w tle.
	cmd := exec.Command("unshare",
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

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sandbox: exec %q w %s nie powiodl sie: %w", command, rootfsDir, err)
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
