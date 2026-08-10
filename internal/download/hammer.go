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

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/httpclient"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

const (
	// releasesPageURL to strona HTML z lista wydan hammer -- scrapujemy ja
	// zamiast uzywac GitHub API zeby uniknac rate-limitow (60 req/h bez tokenu).
	releasesPageURL = "https://github.com/HackerOS-Linux-System/hammer/releases"

	// ociModeAssetName to nazwa archiwum publikowanego przy kazdym wydaniu
	// hammer, zawierajacego POJEDYNCZA binarke "hammer" zbudowana w trybie
	// OCI (tryb uzywany przez systemy atomowe/immutable, w tym HackerOS
	// budowany przez hackeros-builder). Archiwum rozpakowuje sie do
	// dokladnie jednego pliku o nazwie "hammer" w korzeniu archiwum.
	ociModeAssetName = "oci-mode.tar.gz"

	// checksumsAssetName to nazwa pliku z suma kontrolna SHA256 w wydaniu
	// (jesli wydanie go publikuje -- patrz verifyChecksum).
	checksumsAssetName = "checksums.txt"

	// fallbackVersion to hardkodowana wersja uzywana jesli scraping strony
	// releases zawiedzie (np. brak sieci, zmiana layoutu HTML przez GitHub).
	// Aktualizuj przy kazdym nowym wydaniu hammer.
	fallbackVersion = "v0.6.0"
)

// reReleaseTag to wyrazenie regularne szukajace sciezki do tagu wydania
// w HTML strony /releases. GitHub renderuje je jako:
//
//	href="/HackerOS-Linux-System/hammer/releases/tag/v0.6.0"
//
// Bierzemy PIERWSZY taki tag (najnowszy = na gorze strony).
var reReleaseTag = regexp.MustCompile(
	`/HackerOS-Linux-System/hammer/releases/tag/(v[0-9][^"'\s]*)`)

var httpClient = httpclient.New()

// LatestHammerVersion wykrywa najnowszy tag wydania hammer przez scraping
// strony HTML github.com/HackerOS-Linux-System/hammer/releases.
//
// W przypadku bledu (brak sieci, timeout, zmiana layoutu HTML) zwraca
// fallbackVersion z ostrzezeniem -- build moze kontynuowac ze znana wersja
// zamiast twardo padac na etapie wykrywania wersji.
func LatestHammerVersion() (string, error) {
	tag, err := scrapLatestTag()
	if err != nil {
		util.Warnf(
			"Nie udalo sie automatycznie wykryc najnowszej wersji hammer "+
				"(%v) -- uzywam wersji fallback %s. Jesli ta wersja jest nieaktualna, "+
				"zaktualizuj pole 'hammer_version' w config/config.hk projektu.",
			err, fallbackVersion)
		return fallbackVersion, nil
	}
	return tag, nil
}

// scrapLatestTag pobiera strone HTML /releases i wyciaga najnowszy tag.
func scrapLatestTag() (string, error) {
	req, err := http.NewRequest(http.MethodGet, releasesPageURL, nil)
	if err != nil {
		return "", fmt.Errorf("budowanie zadania HTTP: %w", err)
	}
	// User-Agent zeby GitHub nie zablokowal jako bota bez agenta
	req.Header.Set("User-Agent", "hackeros-builder/0.3 (+https://github.com/HackerOS-Linux-System/hackeros-builder)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pobieranie %s: %w", releasesPageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub zwrocilo status %d dla %s", resp.StatusCode, releasesPageURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("odczyt odpowiedzi HTTP: %w", err)
	}

	matches := reReleaseTag.FindSubmatch(body)
	if matches == nil {
		return "", fmt.Errorf(
			"nie znaleziono zadnego tagu wydania na stronie %s -- "+
				"sprawdz czy repo ma co najmniej jedno wydanie", releasesPageURL)
	}

	tag := string(matches[1])
	util.Infof("Wykryto najnowsza wersje hammer: %s", tag)
	return tag, nil
}

// DownloadHammer sciaga archiwum oci-mode.tar.gz dla danej wersji hammer
// (np. "v0.6.0") z GitHub Releases, weryfikuje sume kontrolna SHA256 calego
// archiwum (jesli dostepna), rozpakowuje z niego POJEDYNCZA binarke
// "hammer" i zapisuje ja w destPath z uprawnieniami 0755 (a+x).
//
// hackeros-builder jest CALKOWICIE NIEZALEZNY od deb-ostree -- jedynym
// narzedziem zarzadzania pakietami/atomowoscia wstrzykiwanym do budowanego
// obrazu jest hammer, w trybie OCI (oci-mode.tar.gz).
func DownloadHammer(version, destPath string) error {
	archiveURL := releaseAssetURL(version, ociModeAssetName)
	util.Infof("Pobieranie hammer %s (oci-mode) z %s ...", version, archiveURL)

	archiveData, err := fetchBytes(archiveURL)
	if err != nil {
		return fmt.Errorf("pobieranie hammer (oci-mode) z %s: %w", archiveURL, err)
	}

	if err := verifyChecksum(version, ociModeAssetName, archiveData); err != nil {
		return fmt.Errorf("weryfikacja integralnosci hammer %s: %w", version, err)
	}

	binaryData, err := extractHammerBinary(archiveData)
	if err != nil {
		return fmt.Errorf("rozpakowanie binarki hammer z %s: %w", ociModeAssetName, err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu docelowego: %w", err)
	}

	if err := os.WriteFile(destPath, binaryData, 0o755); err != nil {
		return fmt.Errorf("zapis pobranego pliku do %s: %w", destPath, err)
	}

	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("chmod a+x na %s: %w", destPath, err)
	}

	util.Infof("hammer %s pobrano, rozpakowano i zapisano do %s", version, destPath)
	return nil
}

// extractHammerBinary rozpakowuje archiwum oci-mode.tar.gz (gzip + tar) i
// zwraca bajty POJEDYNCZEGO pliku "hammer" znajdujacego sie w jego korzeniu.
// Archiwum wydania hammer w trybie OCI zawiera dokladnie jeden wpis --
// samodzielna, statycznie/dynamicznie zlinkowana binarke -- bez katalogow
// posrednich, wiec szukamy pliku o nazwie (base) rownej "hammer".
func extractHammerBinary(archiveData []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, fmt.Errorf("otwieranie gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
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
		if name != "hammer" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("odczyt zawartosci %s: %w", hdr.Name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf(
		"archiwum %s nie zawiera pliku 'hammer' -- sprawdz czy layout wydania sie nie zmienil",
		ociModeAssetName)
}

func releaseAssetURL(version, assetName string) string {
	return fmt.Sprintf(
		"https://github.com/HackerOS-Linux-System/hammer/releases/download/%s/%s",
		version, assetName)
}

func fetchBytes(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("budowanie zadania HTTP: %w", err)
	}
	req.Header.Set("User-Agent", "hackeros-builder/0.3 (+https://github.com/HackerOS-Linux-System/hackeros-builder)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zadanie HTTP nie powiodlo sie (timeout %s): %w",
			httpclient.DefaultTimeout, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status HTTP %d (URL: %s)", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

// verifyChecksum sciaga checksums.txt dla danej wersji (jesli istnieje),
// porownuje SHA256 pobranego ARCHIWUM (oci-mode.tar.gz, nie rozpakowanej
// binarki -- tak publikuje je release pipeline hammer) z oczekiwana
// wartoscia. Brak checksums.txt = ostrzezenie (nie blad) -- niektore
// wydania moga nie publikowac sum kontrolnych.
func verifyChecksum(version, assetName string, data []byte) error {
	checksumsURL := releaseAssetURL(version, checksumsAssetName)

	checksumsData, err := fetchBytes(checksumsURL)
	if err != nil {
		util.Warnf(
			"Brak pliku %s dla wydania hammer %s -- KONTYNUUJE BEZ WERYFIKACJI SHA256. "+
				"To jest normalne dla wydania %s ktore nie publikuje sum kontrolnych.",
			checksumsAssetName, version, version)
		return nil
	}

	expectedHex, found := parseChecksumsFile(string(checksumsData), assetName)
	if !found {
		util.Warnf(
			"Plik %s dla wydania %s nie zawiera wpisu dla %q -- KONTYNUUJE BEZ WERYFIKACJI.",
			checksumsAssetName, version, assetName)
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
				"build przerwany. Sprobuj ponownie; jesli problem sie powtarza, "+
				"zglos go w repozytorium hammer.",
			assetName, version, expectedHex, actualHex)
	}

	util.Infof("SHA256 dla hammer %s (%s): OK", version, assetName)
	return nil
}

func parseChecksumsFile(content, wantedFileName string) (hexHash string, found bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		hash := fields[0]
		name := strings.TrimPrefix(fields[1], "*")
		if name == wantedFileName {
			return hash, true
		}
	}
	return "", false
}
