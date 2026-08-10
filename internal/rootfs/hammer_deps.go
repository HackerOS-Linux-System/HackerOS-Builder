package rootfs

import (
	"fmt"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// hammerDeps to lista pakietow apt instalowanych wewnatrz rootfs po
// wstrzyknieciu binarki hammer -- zawiera biblioteki dynamiczne (shared
// libraries) wymagane przez hammer do uruchomienia.
//
// Skad pochodzi ta lista: NIE jest to zgadywanie -- wydanie hammer v0.6.0
// (oci-mode.tar.gz) zostalo pobrane i sprawdzone bezposrednio narzedziem
// `readelf -d hammer | grep NEEDED` podczas rozwoju hackeros-builder.
// Faktyczna lista NEEDED z binarki:
//
//	libostree-1.so.1     -> libostree-1-1      (rdzen atomowosci/deploymentow hammer)
//	libgio-2.0.so.0      -> libglib2.0-0t64    (GLib/GObject/GIO, wymagane przez libostree)
//	libgobject-2.0.so.0  -> libglib2.0-0t64
//	libglib-2.0.so.0     -> libglib2.0-0t64
//	liblzma.so.5         -> liblzma5           (dekompresja warstw ostree/OCI)
//	libbz2.so.1.0        -> libbz2-1.0
//	libgcc_s.so.1        -> libgcc-s1
//	libm.so.6            -> libc6 (baza -- zawsze obecna po debootstrap)
//	libc.so.6            -> libc6 (baza -- zawsze obecna po debootstrap)
//	ld-linux-x86-64.so.2 -> libc6 (baza -- zawsze obecna po debootstrap)
//
// Hammer jest napisany w Rust i statycznie linkuje wiekszosc swoich
// zaleznosci (HTTP/TLS/GPG-Ed25519 sa wbudowane w binarke -- w
// przeciwienstwie do deb-ostree, hammer NIE wymaga libcurl, libgpgme ani
// libselinux1 jako bibliotek dynamicznych), wiec ta lista jest znaczaco
// krotsza niz analogiczna lista dla deb-ostree.
//
// WAZNE: jesli przyszle wydania hammer dodadza nowe zaleznosci dynamiczne,
// ta lista bedzie wymagac aktualizacji. Zeby ulatwic diagnostyke, builder
// po zainstalowaniu binarki uruchamia "ldd /usr/bin/hammer" wewnatrz
// sandbox i wypisuje wynik -- brakujace biblioteki (linia "not found")
// beda widoczne w logach builda.
var hammerDeps = []string{
	"libostree-1-1",
	"libglib2.0-0t64",
	"liblzma5",
	"libbz2-1.0",
	"libgcc-s1",
}

// installHammerDeps instaluje biblioteki dynamiczne wymagane przez
// wstrzyknieta binarke hammer wewnatrz rootfs przez sandbox (unshare+chroot).
//
// Musi byc wywolane PO injectHammer (zeby binarka byla juz w rootfs) i PO
// tym jak rootfs ma skonfigurowane zrodla apt (debootstrap juz to
// zapewnia -- /etc/apt/sources.list jest gotowy po kroku 1). Musi rowniez
// byc wywolane PRZED removeAptTooling -- to jest ostatnie miejsce w calym
// przeplywie budowy w ktorym apt-get jest jeszcze potrzebny.
func (b *Builder) installHammerDeps() error {
	util.Infof("  hammer: instalacja %d bibliotek dynamicznych...", len(hammerDeps))

	if err := b.sandboxExec("apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update przed instalacja zaleznosci hammer: %w", err)
	}

	args := append([]string{
		"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, hammerDeps...)

	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("instalacja bibliotek hammer (%v): %w", hammerDeps, err)
	}

	// Weryfikacja: uruchom "ldd /usr/bin/hammer" wewnatrz rootfs i wypisz
	// wynik -- brakujace biblioteki (linia "=> not found") beda widoczne
	// w logach builda co ulatwia diagnostyke w przyszlosci.
	util.Infof("  hammer: weryfikacja bibliotek dynamicznych (ldd)...")
	if err := b.sandboxExec("ldd", "/usr/bin/hammer"); err != nil {
		// ldd moze zwrocic status != 0 jesli jakas biblioteka nie zostala
		// znaleziona -- to jest OSTRZEZENIE, nie blad krytyczny (builder
		// kontynuuje, uzytkownik widzi ktore .so brakuje w logach wyzej).
		util.Warnf("ldd /usr/bin/hammer zwrocilo blad -- sprawdz logi powyzej pod katem 'not found'. " +
			"Brakujace biblioteki mozna dodac do hammerDeps w internal/rootfs/hammer_deps.go")
	}

	return nil
}
