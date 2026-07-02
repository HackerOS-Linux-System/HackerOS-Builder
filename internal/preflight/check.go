package preflight

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Tool opisuje pojedyncze zewnetrzne narzedzie wymagane przez hackeros-builder,
// wraz z podpowiedzia jak je zainstalowac (nazwa pakietu apt) -- dla
// wygodnego komunikatu bledu, ktory mowi uzytkownikowi co dokladnie zrobic.
type Tool struct {
	Binary      string // nazwa binarki szukanej w $PATH, np. "debootstrap"
	AptPackage  string // nazwa pakietu apt instalujacego ta binarke
	UsedForStep string // krotki opis kroku builda, w ktorym ta binarka jest uzywana
}

// cloudTools to narzedzia ZAWSZE wymagane przez "build cloud".
// Narzedzia build-time (debootstrap, mksquashfs itp.) sa pobierane
// automatycznie przez toolchain.Manager jesli brakuje ich na hoscie --
// tutaj sprawdzamy tylko absolutne minimum, bez ktorego toolchain sam
// nie moze dzialac.
var cloudTools = []Tool{
	{
		Binary:      "unshare",
		AptPackage:  "util-linux",
		UsedForStep: "sandbox: izolacja namespace mount/PID/UTS dla operacji w chroot",
	},
	{
		Binary:      "dpkg-deb",
		AptPackage:  "dpkg",
		UsedForStep: "toolchain: rozpakowywanie .deb narzedzi build-time bez instalacji na hoscie",
	},
}

// isoTools to zestaw narzedzi wymaganych przez "build iso" (mksquashfs,
// budowa hybrydowego ISO BIOS+UEFI przez grub-mkrescue).
var isoTools = []Tool{
	{"mksquashfs", "squashfs-tools", "build iso: pakowanie rootfs do filesystem.squashfs"},
	{"grub-mkrescue", "grub-pc-bin lub grub-efi-amd64-bin", "build iso: budowa hybrydowego ISO BIOS+UEFI"},
	{"xorriso", "xorriso", "build iso: tworzenie obrazu ISO (uzywane wewnetrznie przez grub-mkrescue)"},
}

// CheckCloud weryfikuje narzedzia wymagane przez "build cloud". Zwraca
// blad zawierajacy WSZYSTKIE brakujace binarki na raz (nie tylko pierwsza),
// z podpowiedzia apt-get install dla kazdej z nich.
func CheckCloud() error {
	return checkTools(cloudTools)
}

// CheckIso weryfikuje narzedzia wymagane przez "build iso".
func CheckIso() error {
	return checkTools(isoTools)
}

// CheckAll weryfikuje narzedzia wymagane przez "build all" (suma
// CheckCloud + CheckIso) -- wywolywane raz na starcie "build all" zamiast
// dwukrotnie (raz przed cloud, raz przed iso), zeby ewentualny brak
// narzedzia ISO nie ujawnil sie po dlugim, kosztownym etapie cloud.
func CheckAll() error {
	all := append(append([]Tool{}, cloudTools...), isoTools...)
	return checkTools(all)
}

// checkTools sprawdza kazde Tool przez exec.LookPath, zbiera wszystkie
// brakujace i zwraca jeden, czytelny blad z lista + podpowiedzia instalacji.
func checkTools(tools []Tool) error {
	var missing []Tool
	seen := make(map[string]bool)

	for _, t := range tools {
		if seen[t.Binary] {
			continue // ten sam binarz wymagany przez wiecej niz jeden krok
		}
		seen[t.Binary] = true

		if _, err := exec.LookPath(t.Binary); err != nil {
			missing = append(missing, t)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	sort.Slice(missing, func(i, j int) bool { return missing[i].Binary < missing[j].Binary })

	var b strings.Builder
	fmt.Fprintf(&b, "brakuje %d wymaganych narzedzi w $PATH:\n", len(missing))
	for _, t := range missing {
		fmt.Fprintf(&b, "  - %-16s (uzywane przez: %s)\n", t.Binary, t.UsedForStep)
	}
	b.WriteString("\nZainstaluj brakujace pakiety:\n  sudo apt install")

	seenPkg := make(map[string]bool)
	for _, t := range missing {
		if !seenPkg[t.AptPackage] {
			seenPkg[t.AptPackage] = true
			b.WriteString(" " + t.AptPackage)
		}
	}

	// errors.New, nie fmt.Errorf -- tresc bledu jest juz w pelni zbudowana
	// w b.String() i nie powinna byc interpretowana jako format string
	// (uniknac przypadkowej interpretacji znaku '%' jesli pojawi sie w
	// nazwie narzedzia/pakietu w przyszlosci).
	return errors.New(b.String())
}
