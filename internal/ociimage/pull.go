package ociimage

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/httpclient"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// maxPullAttempts to liczba prob sciagniecia+rozpakowania obrazu zanim
// PullAndUnpack odda blad uzytkownikowi. Rejestrujaco: sam registry (ghcr.io)
// oraz posrednie proxy potrafia ucinac polaczenie w polowie strumienia danych
// pod obciazeniem/przy dluzszych transferach -- to sie objawia jako
// "unexpected EOF" w SRODKU kopiowania konkretnego pliku warstwy (a nie na
// granicy naglowka tar), czyli klasyczny objaw zerwanego polaczenia
// sieciowego, NIE uszkodzonych danych zbudowanych lokalnie (to osobny
// przypadek, zabezpieczony juz w push.go przez validateLayerTarball).
const maxPullAttempts = 4

// pullRetryBackoff to czas oczekiwania miedzy kolejnymi probami.
var pullRetryBackoff = 3 * time.Second

// PullParams to dane potrzebne do sciagniecia obrazu OCI z registry.
type PullParams struct {
	Repository string // np. "ghcr.io/michal/hackeros-debian"
	Tag        string // np. "trixie"
	Token      string // token autoryzacyjny
	DestDir    string // katalog docelowy na rozpakowany rootfs

	// Insecure wylacza weryfikacje certyfikatu TLS -- patrz BuildParams.Insecure
	// w push.go (te sama semantyka, ten sam powod istnienia).
	Insecure bool
}

// PullAndUnpack sciaga obraz Repository:Tag z registry (uzywajac
// go-containerregistry, bez zewnetrznych binarek) i rozpakowuje wszystkie
// jego warstwy (w poprawnej kolejnosci, z obsluga whiteoutow OCI) do DestDir.
//
// Uzywane przez "hackeros-builder build iso", ktore buduje ISO z obrazu
// OCI juz znajdujacego sie w registry -- gwarantuje to, ze ISO jest
// dokladnym odzwierciedleniem tego co zostalo opublikowane przez "build cloud".
//
// Cala operacja (fetch obrazu + rozpakowanie WSZYSTKICH warstw) jest ponawiana
// az do maxPullAttempts razy, jesli blad wyglada na przejsciowy problem
// sieciowy (zerwane polaczenie w trakcie strumieniowania). Kazda proba
// zaczyna od ZERA -- swiezy uchwyt v1.Image/v1.Layer z registry, wyczyszczony
// DestDir -- bo ponawianie odczytu na tym samym, juz zerwanym strumieniu
// HTTP nie ma sensu (polaczenie jest martwe, trzeba nowego).
func PullAndUnpack(p PullParams) error {
	refStr := fmt.Sprintf("%s:%s", p.Repository, p.Tag)
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return fmt.Errorf("nieprawidlowa referencja obrazu %q: %w", refStr, err)
	}

	auth := &authn.Basic{Username: "hackeros-builder", Password: p.Token}
	httpClient := httpclient.NewForRegistry(p.Insecure)

	var lastErr error
	for attempt := 1; attempt <= maxPullAttempts; attempt++ {
		if attempt > 1 {
			util.Warnf("Sciaganie obrazu nie powiodlo sie (proba %d/%d): %v -- ponawiam za %s...",
				attempt-1, maxPullAttempts, lastErr, pullRetryBackoff)
			time.Sleep(pullRetryBackoff)
		}
		util.Infof("Sciagam obraz OCI: %s (proba %d/%d)", refStr, attempt, maxPullAttempts)

		err := pullAndUnpackOnce(ref, auth, httpClient, p.DestDir)
		if err == nil {
			util.Infof("Obraz rozpakowany do %s", p.DestDir)
			return nil
		}
		lastErr = err
		if !isRetryableTransferErr(err) {
			return err
		}
	}
	return fmt.Errorf("sciaganie obrazu %s nie powiodlo sie po %d probach (ostatni blad): %w",
		refStr, maxPullAttempts, lastErr)
}

// pullAndUnpackOnce to pojedyncza proba PullAndUnpack -- wydzielona zeby
// PullAndUnpack mogl ja ponawiac ze swiezym stanem przy kazdej probie.
func pullAndUnpackOnce(ref name.Reference, auth authn.Authenticator, httpClient *http.Client, destDir string) error {
	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithTransport(httpClient.Transport))
	if err != nil {
		return fmt.Errorf("pobieranie %s nie powiodlo sie: %w", ref.String(), err)
	}

	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", destDir, err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie %s: %w", destDir, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("odczyt warstw obrazu: %w", err)
	}

	util.Infof("Rozpakowywanie %d warstw(y)...", len(layers))
	for i, layer := range layers {
		if err := extractLayer(layer, destDir); err != nil {
			return fmt.Errorf("rozpakowywanie warstwy %d/%d: %w", i+1, len(layers), err)
		}
	}
	return nil
}

// isRetryableTransferErr rozpoznaje bledy typowe dla zerwanego/przejsciowo
// niestabilnego polaczenia siecowego z registry -- odrozniamy je od bledow
// trwalych (401/403 autoryzacji, 404/MANIFEST_UNKNOWN zlej nazwy/tagu,
// bledny URL) ktore ponawianie i tak by nie naprawilo, wiec od razu oddajemy
// je uzytkownikowi zamiast marnowac czas na maxPullAttempts prob z gory
// skazanych na ten sam wynik.
func isRetryableTransferErr(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unexpected eof",
		"connection reset",
		"broken pipe",
		"timeout",
		"tls handshake",
		"i/o timeout",
		"eof",
		"temporary failure",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// extractLayer rozpakowuje pojedyncza warstwe OCI (tar, zdekompresowany
// automatycznie przez Uncompressed()) do destDir, obslugujac whiteouts OCI:
//   - plik o nazwie ".wh.<nazwa>" oznacza usuniecie <nazwa> z warstw nizszych
//   - katalog z plikiem ".wh..wh..opq" oznacza "opaque whiteout" -- usuniecie
//     CALEJ zawartosci tego katalogu pochodzacej z warstw nizszych przed
//     wstawieniem nowej zawartosci tej warstwy
func extractLayer(layer v1.Layer, destDir string) error {
	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("Uncompressed(): %w", err)
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("czytanie tar: %w", err)
		}

		entryName := hdr.Name
		base := filepath.Base(entryName)
		dir := filepath.Dir(entryName)

		// Opaque whiteout: ".wh..wh..opq" w katalogu -- czyscimy caly
		// odpowiadajacy katalog docelowy z poprzednich warstw.
		if base == ".wh..wh..opq" {
			target := filepath.Join(destDir, dir)
			entries, _ := os.ReadDir(target)
			for _, e := range entries {
				os.RemoveAll(filepath.Join(target, e.Name()))
			}
			continue
		}

		// Standardowy whiteout: ".wh.<nazwa>" -- usuwamy <nazwa> z destDir.
		if strings.HasPrefix(base, ".wh.") {
			realName := strings.TrimPrefix(base, ".wh.")
			target := filepath.Join(destDir, dir, realName)
			os.RemoveAll(target)
			continue
		}

		target := filepath.Join(destDir, entryName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := ensureParentDir(target); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("kopiowanie zawartosci %q ze strumienia warstwy: %w", entryName, err)
			}
			f.Close()
		case tar.TypeSymlink:
			if err := ensureParentDir(target); err != nil {
				return err
			}
			os.Remove(target) // symlink moze juz istniec z poprzedniej warstwy
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := ensureParentDir(target); err != nil {
				return err
			}
			linkTarget := filepath.Join(destDir, hdr.Linkname)
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		default:
			// Inne typy (FIFO, device files itp.) sa rzadkie w warstwach
			// rootfs Debiana i pomijamy je z ostrzezeniem -- nie powinny
			// blokowac calego rozpakowania obrazu.
			util.Warnf("Pominieto nieobslugiwany typ wpisu tar: %s (typeflag=%c)", entryName, hdr.Typeflag)
		}
	}
	return nil
}

// ensureParentDir tworzy katalogi nadrzedne dla danej sciezki pliku.
func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
