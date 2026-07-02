package ociimage

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/httpclient"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// BuildParams to dane potrzebne do zbudowania i wypchniecia obrazu OCI.
type BuildParams struct {
	RootfsDir  string // katalog z gotowym rootfs (po rootfs.Builder.Build())
	Repository string // pelna sciezka repo, np. "ghcr.io/michal/hackeros-debian"
	Tag        string // tag obrazu, np. "trixie" lub "latest"
	Token      string // token autoryzacyjny (z config.hk -> [auth] -> token)
	WorkDir    string // katalog tymczasowy na warstwe tar (np. /tmp/hackeros-build)

	// Insecure wylacza weryfikacje certyfikatu TLS przy polaczeniu z
	// registry -- przeznaczone WYLACZNIE dla self-signed/wewnetrznych
	// registry testowych (np. Harbor bez poprawnego certyfikatu w sieci
	// lokalnej). Nigdy nie wlaczane domyslnie; ustawiane explicite przez
	// uzytkownika (np. flaga --insecure-registry w CLI).
	Insecure bool
}

// BuildAndPush pakuje RootfsDir do jednowarstwowego obrazu OCI i wypycha go
// do Repository:Tag. Zwraca pelny refspec wypchnietego obrazu
// (np. "ghcr.io/michal/hackeros-debian:trixie") gotowy do wstawienia w
// [origin] -> refspec configu deb-ostree.
func BuildAndPush(p BuildParams) (string, error) {
	util.Infof("Pakowanie rootfs do warstwy OCI...")
	layerTarPath := filepath.Join(p.WorkDir, "layer.tar.gz")
	if err := createLayerTarball(p.RootfsDir, layerTarPath); err != nil {
		return "", fmt.Errorf("tworzenie warstwy tar: %w", err)
	}
	defer os.Remove(layerTarPath)

	util.Infof("Budowanie obrazu OCI (v1.Image)...")
	img, err := buildImageFromLayer(layerTarPath)
	if err != nil {
		return "", fmt.Errorf("budowanie obrazu OCI: %w", err)
	}

	refStr := fmt.Sprintf("%s:%s", p.Repository, p.Tag)
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return "", fmt.Errorf("nieprawidlowa referencja obrazu %q: %w", refStr, err)
	}

	util.Infof("Wypychanie obrazu do %s...", refStr)
	auth := &authn.Basic{
		// Wiele registry (w tym ghcr.io) akceptuje token jako haslo z
		// dowolna niepusta nazwa uzytkownika przy autoryzacji Basic dla push.
		Username: "hackeros-builder",
		Password: p.Token,
	}

	httpClient := httpclient.NewForRegistry(p.Insecure)

	if err := remote.Write(ref, img, remote.WithAuth(auth), remote.WithTransport(httpClient.Transport)); err != nil {
		return "", fmt.Errorf("push do %s nie powiodl sie: %w", refStr, err)
	}

	util.Infof("Obraz wypchniety: %s", refStr)
	return refStr, nil
}

// createLayerTarball pakuje cala zawartosc rootfsDir do pojedynczego pliku
// tar.gz, zachowujac uprawnienia i symlinki -- kluczowe dla poprawnosci
// systemowych binarek (setuid root, etc.) po stronie deb-ostree, ktore te
// metadane odczytuje przy checkout z OSTree.
func createLayerTarball(rootfsDir, destTarGz string) error {
	out, err := os.Create(destTarGz)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(rootfsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootfsDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
}

// buildImageFromLayer tworzy v1.Image z pojedynczej warstwy tar.gz, startujac
// od empty.Image (pusty obraz OCI bez warstw) i dodajac nasza warstwe poprzez
// mutate.AppendLayers.
func buildImageFromLayer(layerTarGzPath string) (v1.Image, error) {
	layer, err := tarball.LayerFromFile(layerTarGzPath)
	if err != nil {
		return nil, fmt.Errorf("tarball.LayerFromFile: %w", err)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return nil, fmt.Errorf("mutate.AppendLayers: %w", err)
	}
	return img, nil
}
