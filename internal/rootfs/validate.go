package rootfs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// ValidateOptions steruje ktore sprawdzenia ValidateRootfs wykonuje.
type ValidateOptions struct {
	// RequireHammerConfig: sprawdz obecnosc /etc/hammer/oci.hk. Ma sens
	// TYLKO dla pelnego atomowego builda (Builder.ContainerMode == false)
	// -- kontener roboczy ([project] -> type=container) nigdy nie ma
	// hammer wstrzyknietego, wiec ta walidacja bylaby dla niego falszywym
	// alarmem.
	RequireHammerConfig bool

	// MinSizeBytes: minimalny akceptowalny calkowity rozmiar rootfs (suma
	// rozmiarow plikow regularnych). 0 = domyslne 50 MB -- najmniejszy
	// rozsadny wariant debootstrap (--variant=minbase dla trixie/sid) to
	// zazwyczaj 100-300 MB, wiec 50 MB to bezpieczny, konserwatywny prog
	// lapiacy PRZERWANE w polowie/uciete buildy bez ryzyka falszywych
	// alarmow na legalnie minimalnych obrazach.
	MinSizeBytes int64

	// MinPackages: minimalna akceptowalna liczba wpisow "Package: " w
	// /var/lib/dpkg/status. 0 = domyslne 10 -- nawet najbardziej okrojony
	// debootstrap instaluje kilkadziesiat pakietow bazowych, 10 to
	// swiadomie nisko ustawiony prog (zeby nie kolidowac z niestandardowymi
	// [release] -> variant), ktory i tak lapie przypadek "dpkg w ogole nie
	// zdazyl sie skonfigurowac".
	MinPackages int
}

// ValidateRootfs sprawdza podstawowa spojnosc/kompletnosc rootfsDir PRZED
// wypchnieciem go jako obraz OCI do registry -- ma wylapac PRZERWANY w
// polowie build (np. debootstrap zabity przez OOM killer, przerwany
// hook, brak miejsca na dysku w trakcie instalacji pakietow) ZANIM
// zmarnujemy czas/transfer na wypchniecie czegos, co i tak nie jest
// uzywalnym systemem. To NIE jest pelna weryfikacja integralnosci (nie
// liczy hashy tresci, nie uruchamia niczego wewnatrz rootfs) -- to szybkie,
// tanie sprawdzenia "czy to w ogole wyglada jak system Debian", analogiczne
// do tego co np. debootstrap sam robi wewnetrznie miedzy swoimi etapami.
func ValidateRootfs(rootfsDir string, opts ValidateOptions) error {
	info, err := os.Stat(rootfsDir)
	if err != nil {
		return fmt.Errorf("rootfs %s nie istnieje/nie mozna odczytac: %w", rootfsDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootfs %s nie jest katalogiem", rootfsDir)
	}

	minSize := opts.MinSizeBytes
	if minSize <= 0 {
		minSize = 50 * 1024 * 1024
	}
	minPkgs := opts.MinPackages
	if minPkgs <= 0 {
		minPkgs = 10
	}

	var totalSize int64
	walkErr := filepath.Walk(rootfsDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			totalSize += fi.Size()
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("liczenie rozmiaru rootfs: %w", walkErr)
	}
	if totalSize < minSize {
		return fmt.Errorf(
			"rootfs wyglada na niekompletny: %s calkowitego rozmiaru, oczekiwano co najmniej %s "+
				"(debootstrap/instalacja pakietow prawdopodobnie zostaly przerwane w polowie -- "+
				"brak miejsca na dysku, OOM killer, przerwane polaczenie z mirrorem) -- "+
				"NIE wypychamy tego do registry",
			util.FormatBytes(totalSize), util.FormatBytes(minSize))
	}

	for _, rel := range []string{"etc", "usr/bin", "var/lib/dpkg/status", "var/lib/dpkg"} {
		p := filepath.Join(rootfsDir, rel)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf(
				"rootfs wyglada na niekompletny: brak %s (%w) -- to nie wyglada na dzialajacy "+
					"system Debian -- NIE wypychamy tego do registry", rel, err)
		}
	}

	if opts.RequireHammerConfig {
		p := filepath.Join(rootfsDir, "etc", "hammer", "oci.hk")
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf(
				"rootfs nie zawiera /etc/hammer/oci.hk (%w) -- wstrzykniecie hammer "+
					"prawdopodobnie zawiodlo mimo ze Build() zglosil sukces -- "+
					"NIE wypychamy tego do registry", err)
		}
	}

	pkgCount, err := countDpkgPackages(filepath.Join(rootfsDir, "var", "lib", "dpkg", "status"))
	if err != nil {
		return fmt.Errorf("odczyt bazy dpkg (%s): %w", filepath.Join(rootfsDir, "var/lib/dpkg/status"), err)
	}
	if pkgCount < minPkgs {
		return fmt.Errorf(
			"baza dpkg zawiera tylko %d pakiet(ow), oczekiwano co najmniej %d -- rootfs wyglada "+
				"na niekompletny (debootstrap prawdopodobnie nie skonczyl instalacji bazowej) -- "+
				"NIE wypychamy tego do registry", pkgCount, minPkgs)
	}

	util.Infof("Walidacja rootfs OK: %s, %d pakietow dpkg, wszystkie wymagane sciezki obecne",
		util.FormatBytes(totalSize), pkgCount)
	return nil
}

// countDpkgPackages liczy wpisy "Package: " w pliku stanu dpkg -- prosty,
// tani (bez parsowania pelnej struktury pola-po-polu) sposob oszacowania
// ile pakietow dpkg faktycznie "widzi" jako zainstalowane/skonfigurowane w
// tym rootfs.
func countDpkgPackages(statusPath string) (int, error) {
	f, err := os.Open(statusPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "Package: ") {
			count++
		}
	}
	return count, scanner.Err()
}
