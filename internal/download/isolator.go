package download

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// Ten plik obsluguje pobieranie i "wbudowywanie" Isolatora
// (https://github.com/HackerOS-Linux-System/Isolator) do rootfs -- uzywane
// przez [project] -> type = containerized (patrz internal/buildflow/container.go).
//
// Isolator to podman-owy menedzer pakietow HackerOS: instaluje pakiety z
// dowolnej wspieranej dystrybucji do izolowanych (albo wspoldzielonych)
// kontenerow, sam zarzadzajac GUI/GPU/audio/D-Bus itd. "Isolator Builder"
// (osobne narzedzie w tym samym repo) buduje MINIMALNE obrazy bazowe
// (jadro + podman + wbudowany Isolator -- reszta instaluje sie PoZNIEJ jako
// kontenery zarzadzane przez Isolator). [project] -> type = containerized w
// hackeros-builderze podąza za DOKLADNIE tym samym pomyslem: zwykly
// kontener roboczy (jak type=container) plus wbudowany Isolator, zeby od
// razu po starcie kontenera dzialalo `isolator install <pakiet>`.
//
// UWAGA NA PRZYSZLOSC: Isolator jest dzis napisany w Go, ale traktujemy go
// tu WYLACZNIE jako "archiwum binarne z GitHub Releases" -- ta sama logika
// (pobierz tar.gz, rozpakuj kazdy zwykly plik do /usr/bin/, chmod a+x)
// zadziala identycznie niezaleznie od jezyka w jakim Isolator zostanie
// kiedys przepisany, bo nie zaklada niczego o Go (brak `go build`, brak
// odwolan do zrodel Isolatora) -- jedyny kontrakt to "wydanie GitHub z
// archiwum isolator.tar.gz zawierajacym gotowe binarki".

const (
	// isolatorReleasesPageURL -- ta sama technika co dla hammer
	// (scraping strony HTML zamiast GitHub API) celowo, zeby uniknac
	// rate-limitu GitHub API dla zadan bez tokenu (60/h) -- patrz
	// LatestHammerVersion w hammer.go dla identycznego uzasadnienia.
	isolatorReleasesPageURL = "https://github.com/HackerOS-Linux-System/Isolator/releases"

	// isolatorAssetName to nazwa archiwum publikowanego przy kazdym
	// wydaniu Isolatora, zawierajacego gotowe binarki (isolator i,
	// docelowo, mozliwe towarzyszace narzedzia typu isolator-daemon) --
	// patrz DownloadAndEmbedIsolator, ktora WYPAKOWUJE KAZDY zwykly plik
	// z tego archiwum (nie zaklada z gory dokladnej listy nazw, zeby
	// dzialac tez gdy wydanie doda/usunie binarke).
	isolatorAssetName = "isolator.tar.gz"

	isolatorChecksumsAssetName = "checksums.txt"

	// isolatorFallbackVersion -- uzywana TYLKO gdy wykrycie najnowszej
	// wersji przez scraping zawiedzie (brak sieci, zmiana layoutu HTML).
	// AKTUALIZUJ przy kazdym nowym wydaniu Isolatora o ktorym wiadomo ze
	// dziala z hackeros-builderem -- w chwili pisania tego kodu repo
	// Isolatora nie mialo jeszcze opublikowanego wydania z ktorego dalo
	// by sie odczytac prawdziwy najnowszy tag, wiec ta wartosc jest
	// CELOWYM, udokumentowanym zgadywaniem placeholder -- realny build
	// i tak najpierw PROBUJE wykryc prawdziwa najnowsza wersje przez
	// scraping (dziala poprawnie gdy tylko repo bedzie miec jakiekolwiek
	// wydanie), fallback jest wylacznie siatka bezpieczenstwa gdy sieć
	// zawiedzie.
	isolatorFallbackVersion = "v0.1.0"
)

var reIsolatorReleaseTag = regexp.MustCompile(
	`/HackerOS-Linux-System/Isolator/releases/tag/(v[0-9][^"'\s]*)`)

// LatestIsolatorVersion wykrywa najnowszy tag wydania Isolatora przez
// scraping strony HTML github.com/HackerOS-Linux-System/Isolator/releases
// -- identyczna technika i uzasadnienie co LatestHammerVersion (hammer.go).
func LatestIsolatorVersion() (string, error) {
	tag, err := scrapLatestIsolatorTag()
	if err != nil {
		util.Warnf(
			"Nie udalo sie automatycznie wykryc najnowszej wersji Isolatora "+
				"(%v) -- uzywam wersji fallback %s. Jesli ta wersja nie istnieje "+
				"albo jest nieaktualna, ustaw [project] -> isolator_version w config.hk.",
			err, isolatorFallbackVersion)
		return isolatorFallbackVersion, nil
	}
	return tag, nil
}

func scrapLatestIsolatorTag() (string, error) {
	req, err := http.NewRequest(http.MethodGet, isolatorReleasesPageURL, nil)
	if err != nil {
		return "", fmt.Errorf("budowanie zadania HTTP: %w", err)
	}
	req.Header.Set("User-Agent", "hackeros-builder/0.9 (+https://github.com/HackerOS-Linux-System/hackeros-builder)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pobieranie %s: %w", isolatorReleasesPageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub zwrocilo status %d dla %s", resp.StatusCode, isolatorReleasesPageURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("odczyt odpowiedzi HTTP: %w", err)
	}

	matches := reIsolatorReleaseTag.FindSubmatch(body)
	if matches == nil {
		return "", fmt.Errorf(
			"nie znaleziono zadnego tagu wydania na stronie %s -- "+
				"sprawdz czy repo Isolator ma co najmniej jedno wydanie", isolatorReleasesPageURL)
	}

	tag := string(matches[1])
	util.Infof("Wykryto najnowsza wersje Isolatora: %s", tag)
	return tag, nil
}

// DownloadAndEmbedIsolator sciaga isolator.tar.gz dla danej wersji (np.
// "v0.3.0") z GitHub Releases Isolatora, weryfikuje sume kontrolna SHA256
// calego archiwum (jesli wydanie publikuje checksums.txt -- jak w
// verifyChecksum dla hammer, brak pliku to ostrzezenie a nie blad), i
// WYPAKOWUJE KAZDY zwykly plik z archiwum bezposrednio do
// <rootfsDir>/usr/bin/<nazwa-bazowa> (splaszczajac ewentualne katalogi
// posrednie z archiwum -- np. "bin/isolator" w archiwum ladowany jest jako
// /usr/bin/isolator), z uprawnieniami 0755 (a+x) na kazdym wypakowanym
// pliku -- dokladnie jak opisano: "rozpakuj zawartosc do /usr/bin/ i daj
// chmod a+x".
//
// Zwraca liste nazw wypakowanych binarek (np. ["isolator"]) -- pusta lista
// i blad jesli archiwum nie zawiera ani jednego zwyklego pliku.
func DownloadAndEmbedIsolator(version, rootfsDir string) ([]string, error) {
	archiveURL := isolatorReleaseAssetURL(version, isolatorAssetName)
	util.Infof("Pobieranie Isolator %s z %s ...", version, archiveURL)

	archiveData, err := fetchBytes(archiveURL)
	if err != nil {
		return nil, fmt.Errorf("pobieranie Isolator z %s: %w", archiveURL, err)
	}

	if err := verifyIsolatorChecksum(version, archiveData); err != nil {
		return nil, fmt.Errorf("weryfikacja integralnosci Isolator %s: %w", version, err)
	}

	destDir := filepath.Join(rootfsDir, "usr", "bin")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("tworzenie %s: %w", destDir, err)
	}

	names, err := extractAllToDir(archiveData, destDir)
	if err != nil {
		return nil, fmt.Errorf("rozpakowanie %s do %s: %w", isolatorAssetName, destDir, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf(
			"archiwum %s nie zawiera zadnego zwyklego pliku -- sprawdz czy layout "+
				"wydania Isolatora sie nie zmienil", isolatorAssetName)
	}

	util.Infof("Isolator %s wbudowany do %s: %s", version, destDir, strings.Join(names, ", "))
	return names, nil
}

// extractAllToDir rozpakowuje KAZDY wpis typu "zwykly plik" z archiwum
// tar.gz do destDir, uzywajac WYLACZNIE nazwy bazowej sciezki z archiwum
// (splaszczenie -- katalogi posrednie w archiwum sa ignorowane), z
// uprawnieniami 0755. Celowo splaszczone: nie zakladamy z gory ZADNEJ
// konkretnej struktury wewnatrz archiwum (patrz komentarz o przyszlej
// zmianie jezyka Isolatora na poczatku pliku) -- jedyny kontrakt to "kazdy
// zwykly plik w archiwum to binarka ktora ma wyladowac w /usr/bin/".
func extractAllToDir(archiveData []byte, destDir string) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, fmt.Errorf("otwieranie gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("czytanie tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("odczyt zawartosci %s: %w", hdr.Name, err)
		}
		destPath := filepath.Join(destDir, name)
		if err := os.WriteFile(destPath, data, 0o755); err != nil {
			return nil, fmt.Errorf("zapis %s: %w", destPath, err)
		}
		if err := os.Chmod(destPath, 0o755); err != nil {
			return nil, fmt.Errorf("chmod a+x na %s: %w", destPath, err)
		}
		names = append(names, name)
	}
	return names, nil
}

func isolatorReleaseAssetURL(version, assetName string) string {
	return fmt.Sprintf(
		"https://github.com/HackerOS-Linux-System/Isolator/releases/download/%s/%s",
		version, assetName)
}

// verifyIsolatorChecksum -- ta sama logika co verifyChecksum dla hammer
// (hammer.go), tylko wskazujaca na repo/asset Isolatora. Duplikacja jest
// celowa: hammer.go jest dobrze przetestowanym, dzialajacym kodem --
// zmiana go na "generyczny" pod dwa rozne repo zwiekszalaby ryzyko
// regresji dla zerowej realnej korzysci (obie funkcje sa krotkie i
// niezalezne).
func verifyIsolatorChecksum(version string, data []byte) error {
	checksumsURL := isolatorReleaseAssetURL(version, isolatorChecksumsAssetName)

	checksumsData, err := fetchBytes(checksumsURL)
	if err != nil {
		util.Warnf(
			"Brak pliku %s dla wydania Isolator %s -- KONTYNUUJE BEZ WERYFIKACJI SHA256. "+
				"To jest normalne dla wydania %s ktore nie publikuje sum kontrolnych.",
			isolatorChecksumsAssetName, version, version)
		return nil
	}

	expectedHex, found := parseChecksumsFile(string(checksumsData), isolatorAssetName)
	if !found {
		util.Warnf(
			"Plik %s dla wydania Isolator %s nie zawiera wpisu dla %q -- KONTYNUUJE BEZ WERYFIKACJI.",
			isolatorChecksumsAssetName, version, isolatorAssetName)
		return nil
	}

	actualHash := sha256.Sum256(data)
	actualHex := hex.EncodeToString(actualHash[:])

	if !strings.EqualFold(actualHex, expectedHex) {
		return fmt.Errorf(
			"SUMA KONTROLNA SIE NIE ZGADZA dla %s %s:\n"+
				"  oczekiwano: %s\n"+
				"  otrzymano:  %s\n"+
				"Moze to byc uszkodzony transfer lub podmieniony artefakt -- "+
				"NIE uzywamy tego archiwum.",
			isolatorAssetName, version, expectedHex, actualHex)
	}
	return nil
}
