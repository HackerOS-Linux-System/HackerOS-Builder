package rootfs

import (
	"fmt"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// debOstreeDeps to lista pakietow apt instalowanych wewnatrz rootfs po
// wstrzyknieciu binarki deb-ostree -- zawiera biblioteki dynamiczne
// (shared libraries) wymagane przez deb-ostree do uruchomienia.
//
// Skad pochodzi ta lista:
//
//   - libsolv1        -- resolver zaleznosci (komunikat bledu uzytkownika:
//     "libsolv.so.1: cannot open shared object file")
//   - libostree-1-1   -- biblioteka ostree (glowna zależnosc deb-ostree;
//     dostarcza libostree-1.so.1)
//   - libarchive13t64 -- archiwizacja (wymagana przez libostree)
//   - libcurl3t64-gnutls -- HTTP dla pobierania aktualizacji ostree
//   - libglib2.0-0t64 -- GLib (wymagana przez libostree: GObject, GVariant)
//   - libgpgme11t64   -- GPG do weryfikacji podpisow commitow ostree
//   - libselinux1     -- SELinux labeling (wymagana przez libostree nawet
//     gdy SELinux nie jest aktywny -- libostree linkuje
//     dynamicznie do libselinux, nawet na systemach bez
//     SELinux linker musi znalezc biblioteke)
//   - libsystemd0     -- integracja z journald / sd-bus (logowanie deb-ostree)
//
// WAZNE: ta lista bedzie rozszerzana w miare odkrywania kolejnych brakujacych
// bibliotek. Zeby ulatwic diagnostyke, builder po zainstalowaniu binarki
// uruchamia "ldd /usr/bin/deb-ostree" wewnatrz sandbox i wypisuje wynik --
// brakujace biblioteki (linia "not found") beda widoczne w logach builda.
var debOstreeDeps = []string{
	// Bezposrednia zaleznosc zglaszana przez uzytkownika:
	"libsolv1",
	// Pozostale typowe zaleznosci deb-ostree (narzedzia ostree-style dla Debiana):
	"libostree-1-1",
	"libarchive13t64",
	"libcurl3t64-gnutls",
	"libglib2.0-0t64",
	"libgpgme11t64",
	"libselinux1",
	"libsystemd0",
	"liblzma5",
	"zlib1g",
}

// installDebOstreeDeps instaluje biblioteki dynamiczne wymagane przez
// wstryknieta binarka deb-ostree wewnatrz rootfs przez sandbox (unshare+chroot).
//
// Musi byc wywolane PO injectDebOstree (zeby binarka byla juz w rootfs)
// i PO tym jak rootfs ma skonfigurowane zrodla apt (debootstrap juz to
// zapewnia -- /etc/apt/sources.list jest gotowy po kroku 1).
func (b *Builder) installDebOstreeDeps() error {
	util.Infof("  deb-ostree: instalacja %d bibliotek dynamicznych...", len(debOstreeDeps))

	if err := b.sandboxExec("apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update przed instalacja deb-ostree deps: %w", err)
	}

	args := append([]string{
		"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, debOstreeDeps...)

	if err := b.sandboxExec("apt-get", args...); err != nil {
		return fmt.Errorf("instalacja bibliotek deb-ostree (%v): %w", debOstreeDeps, err)
	}

	// Weryfikacja: uruchom "ldd /usr/bin/deb-ostree" wewnatrz rootfs i
	// wypisz wynik -- brakujace biblioteki (linia "=> not found") beda
	// widoczne w logach builda co ulatwia diagnostyke w przyszlosci.
	util.Infof("  deb-ostree: weryfikacja bibliotek dynamicznych (ldd)...")
	if err := b.sandboxExec("ldd", "/usr/bin/deb-ostree"); err != nil {
		// ldd moze zwrocic status != 0 jesli jakas biblioteka nie zostala
		// znaleziona -- to jest OSTRZEZENIE, nie blad krytyczny (builder
		// kontynuuje, uzytkownik widzi ktore .so brakuje w logach wyzej).
		util.Warnf("ldd /usr/bin/deb-ostree zwrocilo blad -- sprawdz logi powyzej pod katem 'not found'. " +
			"Brakujace biblioteki mozna dodac do debOstreeDeps w internal/rootfs/debostree_deps.go")
	}

	return nil
}
