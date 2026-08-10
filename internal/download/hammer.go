package download

import (
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
	// releasesPageURL to strona HTML z listą wydań -- scrapujemy ja zamiast
	// uzywac GitHub API zeby uniknac rate-limitow (60 req/h bez tokenu).
	releasesPageURL = "https://github.com/HackerOS-Linux-System/deb-ostree/releases"

	// releaseAssetName to nazwa pliku binarnego w wydaniu GitHub.
	releaseAssetName = "deb-ostree"

	// checksumsAssetName to nazwa pliku z suma kontrolna SHA256 w wydaniu.
	checksumsAssetName = "checksums.txt"

	// fallbackVersion to hardkodowana wersja uzywana jesli scraping strony
	// releases zawiedzie (np. brak sieci, zmiana layoutu HTML przez GitHub).
	// Aktualizuj przy kazdym nowym wydaniu deb-ostree.
	fallbackVersion = "v0.0.1"
)

// reReleaseTag to wyrazenie regularne szukajace sciezki do tagu wydania
// w HTML strony /releases. GitHub renderuje je jako:
//
//	href="/HackerOS-Linux-System/deb-ostree/releases/tag/v0.0.1"
//
// Bierzemy PIERWSZY taki tag (najnowszy = na gorze strony).
var reReleaseTag = regexp.MustCompile(
	`/HackerOS-Linux-System/deb-ostree/releases/tag/(v[0-9][^"'\s]*)`)

var httpClient = httpclient.New()

// LatestDebOstreeVersion wykrywa najnowszy tag wydania deb-ostree przez
// scraping strony HTML github.com/HackerOS-Linux-System/deb-ostree/releases.
//
// W przypadku bledu (brak sieci, timeout, zmiana layoutu HTML) zwraca
// fallbackVersion z ostrzezeniem -- build moze kontynuowac ze znana wersja
// zamiast twardo padac na etapie wykrywania wersji.
func LatestDebOstreeVersion() (string, error) {
	tag, err := scrapLatestTag()
	if err != nil {
		util.Warnf(
			"Nie udalo sie automatycznie wykryc najnowszej wersji deb-ostree "+
				"(%v) -- uzywam wersji fallback %s. Jesli ta wersja jest nieaktualna, "+
				"zaktualizuj pole 'deb_ostree_version' w config/config.hk projektu.",
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
	util.Infof("Wykryto najnowsza wersje deb-ostree: %s", tag)
	return tag, nil
}

// DownloadDebOstree sciaga binarke deb-ostree dla danej wersji (np. "v0.0.1")
// z GitHub Releases, weryfikuje sume kontrolna SHA256 (jesli dostepna)
// i zapisuje ja w destPath z uprawnieniami 0755.
func DownloadDebOstree(version, destPath string) error {
	binURL := releaseAssetURL(version, releaseAssetName)
	util.Infof("Pobieranie deb-ostree %s z %s ...", version, binURL)

	data, err := fetchBytes(binURL)
	if err != nil {
		return fmt.Errorf("pobieranie deb-ostree z %s: %w", binURL, err)
	}

	if err := verifyChecksum(version, data); err != nil {
		return fmt.Errorf("weryfikacja integralnosci deb-ostree %s: %w", version, err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu docelowego: %w", err)
	}

	if err := os.WriteFile(destPath, data, 0o755); err != nil {
		return fmt.Errorf("zapis pobranego pliku do %s: %w", destPath, err)
	}

	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("chmod a+x na %s: %w", destPath, err)
	}

	util.Infof("deb-ostree %s pobrano i zapisano do %s", version, destPath)
	return nil
}

func releaseAssetURL(version, assetName string) string {
	return fmt.Sprintf(
		"https://github.com/HackerOS-Linux-System/deb-ostree/releases/download/%s/%s",
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
// porownuje SHA256 pobranych danych z oczekiwana wartoscia.
// Brak checksums.txt = ostrzezenie (nie blad) -- starsze wydania nie maja pliku.
func verifyChecksum(version string, binaryData []byte) error {
	checksumsURL := releaseAssetURL(version, checksumsAssetName)

	checksumsData, err := fetchBytes(checksumsURL)
	if err != nil {
		util.Warnf(
			"Brak pliku %s dla wydania deb-ostree %s -- KONTYNUUJE BEZ WERYFIKACJI SHA256. "+
				"To jest normalne dla wydania %s ktore nie publikuje sum kontrolnych.",
			checksumsAssetName, version, version)
		return nil
	}

	expectedHex, found := parseChecksumsFile(string(checksumsData), releaseAssetName)
	if !found {
		util.Warnf(
			"Plik %s dla wydania %s nie zawiera wpisu dla %q -- KONTYNUUJE BEZ WERYFIKACJI.",
			checksumsAssetName, version, releaseAssetName)
		return nil
	}

	actualHash := sha256.Sum256(binaryData)
	actualHex := hex.EncodeToString(actualHash[:])

	if !strings.EqualFold(actualHex, expectedHex) {
		return fmt.Errorf(
			"SUMA KONTROLNA SIE NIE ZGADZA dla %s %s:\n"+
				"  oczekiwano: %s\n"+
				"  otrzymano:  %s\n"+
				"Moze to byc uszkodzony transfer lub podmieniony artefakt -- "+
				"build przerwany. Sprobuj ponownie; jesli problem sie powtarza, "+
				"zglos go w repozytorium deb-ostree.",
			releaseAssetName, version, expectedHex, actualHex)
	}

	util.Infof("SHA256 dla deb-ostree %s: OK", version)
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
