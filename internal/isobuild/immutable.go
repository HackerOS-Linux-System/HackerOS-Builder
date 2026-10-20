package isobuild

import (
	"fmt"
	"os"
	"path/filepath"
)

// Niezmienność systemu (styl ostree): partycja root zostaje zapisywalna
// (hammer/ostree jej potrzebuje), ale katalogi z systemem -- /usr (a przez
// usrmerge tez /bin /sbin /lib), /boot i /opt -- sa montowane jako
// OSOBNE montowania "bind" tylko do odczytu. Dzieki temu nawet root nie
// moze edytowac/usuwac plikow systemowych; /etc, /var, /home, /root, /tmp
// i /usr/local pozostaja zapisywalne. Zmiany systemu robi sie wylacznie
// przez hammer albo jawnie: `hackeros-immutable run -- <polecenie>`.
//
// Wylaczenie awaryjne: parametr jadra `hackeros.mutable=1` albo plik
// /etc/hackeros/mutable.

const immutableScript = `#!/bin/sh
# Wygenerowane przez hackeros-builder.
# hackeros-immutable status | lock | unlock | run -- polecenie...
PROTECTED="${HACKEROS_PROTECTED:-/usr /boot /opt}"

usage() {
    echo "uzycie: hackeros-immutable status|lock|unlock|run -- polecenie..." >&2
    exit 2
}

[ "$(id -u)" -eq 0 ] || [ "$1" = "status" ] || { echo "wymagany root" >&2; exit 1; }

lock_one() {
    d="$1"
    [ -d "$d" ] && [ ! -L "$d" ] || return 0
    mountpoint -q "$d" || mount --rbind "$d" "$d" || return 1
    mount -o remount,bind,ro "$d"
}

unlock_one() {
    d="$1"
    mountpoint -q "$d" || return 0
    mount -o remount,bind,rw "$d"
}

# /usr/local ma zostac zapisywalny (wlasne programy uzytkownika).
local_rw() {
    [ -d /usr/local ] && [ ! -L /usr/local ] || return 0
    mountpoint -q /usr/local || mount --bind /usr/local /usr/local || return 0
    mount -o remount,bind,rw /usr/local
}

is_ro() {
    mountpoint -q "$1" && findmnt -no OPTIONS "$1" 2>/dev/null | tr ',' '\n' | grep -qx ro
}

do_lock() {
    for d in $PROTECTED; do lock_one "$d" || echo "hackeros-immutable: nie udalo sie zablokowac $d" >&2; done
    local_rw
}

do_unlock() {
    for d in $PROTECTED; do unlock_one "$d"; done
}

case "$1" in
    lock) do_lock ;;
    unlock) do_unlock ;;
    status)
        rc=0
        for d in $PROTECTED; do
            if is_ro "$d"; then echo "$d: tylko do odczytu"; else echo "$d: ZAPISYWALNY"; rc=1; fi
        done
        exit $rc ;;
    run)
        shift
        [ "$1" = "--" ] && shift
        [ $# -gt 0 ] || usage
        was_locked=0
        for d in $PROTECTED; do is_ro "$d" && was_locked=1; done
        do_unlock
        "$@"; rc=$?
        [ "$was_locked" -eq 1 ] && do_lock
        exit $rc ;;
    *) usage ;;
esac
`

const immutableServiceUnit = `[Unit]
Description=HackerOS: system plikow tylko do odczytu (/usr, /boot, /opt)
DefaultDependencies=no
After=local-fs.target systemd-remount-fs.service
Before=sysinit.target shutdown.target
Conflicts=shutdown.target
ConditionVirtualization=!container
ConditionKernelCommandLine=!hackeros.mutable
ConditionPathExists=!/etc/hackeros/mutable

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/hackeros-immutable lock

[Install]
WantedBy=sysinit.target
`

// writeImmutability instaluje skrypt i usluge blokujaca zapis do /usr, /boot,
// /opt oraz wlacza ja w sysinit.target.
func writeImmutability(rootfsDir string) error {
	write := func(rel, content string, mode os.FileMode) error {
		p := filepath.Join(rootfsDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), mode)
	}
	if err := write("usr/sbin/hackeros-immutable", immutableScript, 0o755); err != nil {
		return err
	}
	if err := write("etc/systemd/system/hackeros-immutable.service", immutableServiceUnit, 0o644); err != nil {
		return err
	}
	wants := filepath.Join(rootfsDir, "etc/systemd/system/sysinit.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wants, "hackeros-immutable.service")
	_ = os.Remove(link)
	if err := os.Symlink("/etc/systemd/system/hackeros-immutable.service", link); err != nil {
		return fmt.Errorf("wlaczanie hackeros-immutable.service: %w", err)
	}
	return os.MkdirAll(filepath.Join(rootfsDir, "usr/local"), 0o755)
}
