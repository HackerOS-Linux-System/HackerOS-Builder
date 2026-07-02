package ociimage

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/httpclient"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

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
func PullAndUnpack(p PullParams) error {
	refStr := fmt.Sprintf("%s:%s", p.Repository, p.Tag)
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return fmt.Errorf("nieprawidlowa referencja obrazu %q: %w", refStr, err)
	}

	util.Infof("Sciagam obraz OCI: %s", refStr)
	auth := &authn.Basic{Username: "hackeros-builder", Password: p.Token}
	httpClient := httpclient.NewForRegistry(p.Insecure)

	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithTransport(httpClient.Transport))
	if err != nil {
		return fmt.Errorf("pobieranie %s nie powiodlo sie: %w", refStr, err)
	}

	if err := os.RemoveAll(p.DestDir); err != nil {
		return fmt.Errorf("czyszczenie %s: %w", p.DestDir, err)
	}
	if err := os.MkdirAll(p.DestDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie %s: %w", p.DestDir, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("odczyt warstw obrazu: %w", err)
	}

	util.Infof("Rozpakowywanie %d warstw(y)...", len(layers))
	for i, layer := range layers {
		if err := extractLayer(layer, p.DestDir); err != nil {
			return fmt.Errorf("rozpakowywanie warstwy %d/%d: %w", i+1, len(layers), err)
		}
	}

	util.Infof("Obraz rozpakowany do %s", p.DestDir)
	return nil
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
				return err
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
