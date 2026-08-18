package ociimage

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
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

// LayeredBuildParams to dane potrzebne do zbudowania i wypchniecia obrazu
// OCI z JUZ GOTOWYCH warstw przyrostowych (patrz internal/rootfs/layers.go,
// Builder.LayerTarballs()) -- w odroznieniu od BuildParams/BuildAndPush
// (ponizej), ktore nadal pakuja CALY rootfs w JEDNA warstwe od zera (uzywane
// przez "build container", gdzie nie ma sensu przyrostowosci wzgledem
// registry -- patrz internal/buildflow/container.go).
type LayeredBuildParams struct {
	LayerPaths []string // kolejne warstwy tar.gz, JUZ zbudowane przez rootfs.Builder (baza -> pakiety -> hooki -> runtime)
	Repository string
	Tag        string
	Token      string
	Insecure   bool
}

// BuildAndPushLayers buduje v1.Image z WIELU warstw (mutate.AppendLayers,
// w kolejnosci) i wypycha go do Repository:Tag, pokazujac REALNY pasek
// postepu liczony z bajtow faktycznie wyslanych do registry
// (remote.WithProgress -- API biblioteki go-containerregistry, karmione
// przez nia samej podczas kazdego PUT bloba/manifestu, nie przez nasze
// wlasne przyblizenie).
//
// Korzysc z warstw przyrostowych ujawnia sie PO STRONIE REGISTRY: jesli
// warstwa "base" jest bajt-w-bajt taka sama jak w poprzednim wypchnietym
// obrazie (ten sam SHA256 digest), zgodny z OCI registry rozpozna to po
// samym digescie i NIE poprosi o ponowne wgranie danych (blob juz istnieje)
// -- to jest wbudowany mechanizm kazdego rejestru kontenerow (content-
// addressed storage), nie cos co ten kod musi implementowac recznie.
func BuildAndPushLayers(p LayeredBuildParams) (string, error) {
	if len(p.LayerPaths) == 0 {
		return "", fmt.Errorf(
			"brak warstw OCI do wypchniecia -- rootfs.Builder.Build() nie zaraportowal " +
				"ani jednej zmiany, co nie powinno sie zdarzyc dla swiezo zbudowanego rootfs " +
				"(sprawdz czy debootstrap faktycznie utworzyl jakiekolwiek pliki w RootfsDir)")
	}

	util.Infof("Walidacja %d warstw(y) OCI przed wypchnieciem...", len(p.LayerPaths))
	var totalBytes int64
	for i, path := range p.LayerPaths {
		if err := validateLayerTarball(path); err != nil {
			return "", fmt.Errorf("warstwa %d/%d (%s) jest uszkodzona: %w -- "+
				"NIE wypychamy ZADNEJ warstwy do registry (obraz musi byc spojny w calosci)",
				i+1, len(p.LayerPaths), path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil {
			totalBytes += info.Size()
		}
	}

	util.Infof("Budowanie obrazu OCI z %d warstw (razem %s)...", len(p.LayerPaths), util.FormatBytes(totalBytes))
	img, err := buildImageFromLayers(p.LayerPaths)
	if err != nil {
		return "", fmt.Errorf("budowanie obrazu OCI: %w", err)
	}

	refStr := fmt.Sprintf("%s:%s", p.Repository, p.Tag)
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return "", fmt.Errorf("nieprawidlowa referencja obrazu %q: %w", refStr, err)
	}

	auth := &authn.Basic{
		Username: "hackeros-builder",
		Password: p.Token,
	}
	httpClient := httpclient.NewForRegistry(p.Insecure)

	util.Infof("Wypychanie obrazu do %s...", refStr)
	refStr2, err := pushWithProgress(ref, img, auth, httpClient, totalBytes)
	if err != nil {
		return "", err
	}

	util.Infof("Obraz wypchniety: %s (%d warstw)", refStr2, len(p.LayerPaths))
	return refStr2, nil
}

// pushWithProgress opakowuje remote.Write w pasek postepu karmiony
// v1.Update z remote.WithProgress -- wydzielone z BuildAndPushLayers zeby
// ta sama logika dala sie ponownie uzyc dla BuildAndPush (jednowarstwowa
// sciezka, patrz nizej) bez duplikacji.
func pushWithProgress(ref name.Reference, img v1.Image, auth authn.Authenticator, httpClient *http.Client, totalBytes int64) (string, error) {
	updates := make(chan v1.Update, 64)
	bar := util.NewProgressBar("push OCI", totalBytes, "bajtow")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for u := range updates {
			if u.Error != nil {
				continue // blad koncowy i tak wraca z remote.Write ponizej, z pelnym kontekstem
			}
			bar.Set(u.Complete)
		}
	}()

	writeErr := remote.Write(ref, img,
		remote.WithAuth(auth),
		remote.WithTransport(httpClient.Transport),
		remote.WithProgress(updates))
	<-done

	if writeErr != nil {
		bar.Fail("push do registry")
		return "", fmt.Errorf("push do %s nie powiodl sie: %w", ref.String(), writeErr)
	}
	bar.Finish()
	return ref.String(), nil
}

// buildImageFromLayers tworzy v1.Image z WIELU warstw tar.gz (w podanej
// kolejnosci), startujac od empty.Image i dodajac je wszystkie naraz przez
// mutate.AppendLayers -- ta sama funkcja co buildImageFromLayer (pojedyncza
// warstwa) nizej, tylko dla N warstw.
func buildImageFromLayers(layerPaths []string) (v1.Image, error) {
	layers := make([]v1.Layer, 0, len(layerPaths))
	for _, path := range layerPaths {
		layer, err := tarball.LayerFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("tarball.LayerFromFile(%s): %w", path, err)
		}
		layers = append(layers, layer)
	}
	img, err := mutate.AppendLayers(empty.Image, layers...)
	if err != nil {
		return nil, fmt.Errorf("mutate.AppendLayers: %w", err)
	}
	return img, nil
}

// BuildAndPush pakuje RootfsDir do jednowarstwowego obrazu OCI i wypycha go
// do Repository:Tag. Zwraca pelny refspec wypchnietego obrazu
// (np. "ghcr.io/michal/hackeros-debian:trixie") gotowy do wstawienia w
// [origin] -> refspec configu hammer (/etc/hammer/oci.hk).
func BuildAndPush(p BuildParams) (string, error) {
	util.Infof("Pakowanie rootfs do warstwy OCI...")
	layerTarPath := filepath.Join(p.WorkDir, "layer.tar.gz")
	if err := createLayerTarball(p.RootfsDir, layerTarPath); err != nil {
		return "", fmt.Errorf("tworzenie warstwy tar: %w", err)
	}
	defer os.Remove(layerTarPath)

	// Walidacja LOKALNA zaraz po zbudowaniu warstwy, PRZED jakimkolwiek
	// wypchnieciem do registry. tarball.LayerFromFile (nizej) czyta ten
	// plik z dysku WIELOKROTNIE podczas remote.Write (raz zeby policzyc
	// SHA256, raz zeby faktycznie wgrac dane) -- jesli layer.tar.gz jest
	// obciety/uszkodzony, bez tej walidacji dowiedzielibysmy sie o tym
	// dopiero przy "build iso" (pull z registry), jako niejasne
	// "unexpected EOF" oderwane od miejsca faktycznej przyczyny. Tutaj
	// blad wychodzi natychmiast, lokalnie, z jasnym komunikatem.
	if err := validateLayerTarball(layerTarPath); err != nil {
		return "", fmt.Errorf("zbudowana warstwa OCI jest uszkodzona (%s): %w -- "+
			"NIE wypychamy jej do registry; sprawdz miejsce na dysku w %s i uruchom build ponownie",
			layerTarPath, err, p.WorkDir)
	}

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

	var totalBytes int64
	if info, statErr := os.Stat(layerTarPath); statErr == nil {
		totalBytes = info.Size()
	}

	refStr2, err := pushWithProgress(ref, img, auth, httpClient, totalBytes)
	if err != nil {
		return "", err
	}
	return refStr2, nil
}

// createLayerTarball pakuje cala zawartosc rootfsDir do pojedynczego pliku
// tar.gz, zachowujac uprawnienia i symlinki -- kluczowe dla poprawnosci
// systemowych binarek (setuid root, etc.) po stronie hammer/libostree, ktore te
// metadane odczytuje przy checkout z OSTree.
func createLayerTarball(rootfsDir, destTarGz string) error {
	out, err := os.Create(destTarGz)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	walkErr := createLayerTarballWalk(rootfsDir, tw)

	// UWAGA: zamykamy w poprawnej kolejnosci (tar -> gzip -> plik) i
	// SPRAWDZAMY KAZDY blad Close(). Wczesniej te trzy Close() byly
	// wywolywane wylacznie przez `defer` z pominietym bledem zwrotnym --
	// jesli tw.Close() lub gz.Close() zawiodlo (np. brak miejsca na
	// dysku, przerwany zapis buforowanych danych gzip), archiwum konczylo
	// sie BEZ koncowych blokow tar / trailer'a gzip, ale funkcja i tak
	// zwracala sukces (bo filepath.Walk sam w sobie sie powiodl). Taki
	// obciety layer.tar.gz byl nastepnie wypychany do registry jako
	// poprawny obraz, a dopiero przy pozniejszym "build iso" (pull +
	// rozpakowanie warstwy) ujawnial sie jako "unexpected EOF" -- czyli
	// dokladnie objaw z tego zgloszenia. Teraz kazdy blad Close()
	// przerywa build od razu, w miejscu gdzie powstaje, zamiast
	// wyplywac pozniej przy pullu z registry.
	if closeErr := tw.Close(); closeErr != nil && walkErr == nil {
		walkErr = fmt.Errorf("zamykanie tar writer warstwy: %w", closeErr)
	}
	if closeErr := gz.Close(); closeErr != nil && walkErr == nil {
		walkErr = fmt.Errorf("zamykanie gzip writer warstwy: %w", closeErr)
	}
	if closeErr := out.Close(); closeErr != nil && walkErr == nil {
		walkErr = fmt.Errorf("zamykanie pliku warstwy %s: %w", destTarGz, closeErr)
	}

	if walkErr != nil {
		// Nie zostawiamy czesciowo zapisanego/uszkodzonego pliku warstwy --
		// kolejna proba builda ma zaczynac od zera, a nie przypadkiem
		// podniesc obciety tar.gz z poprzedniej, nieudanej proby.
		os.Remove(destTarGz)
		return walkErr
	}
	return nil
}

// createLayerTarballWalk przechodzi po rootfsDir i zapisuje kazdy wpis do tw.
// Wydzielone z createLayerTarball, zeby Close() warstwy dalo sie wywolac
// jawnie (z obsluga bledu) niezaleznie od tego, czy sam walk sie powiodl.
func createLayerTarballWalk(rootfsDir string, tw *tar.Writer) error {
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

// validateLayerTarball otwiera layer.tar.gz od nowa (dokladnie tak jak
// pozniej zrobi to tarball.LayerFromFile / remote.Write) i czyta go w calosci
// -- gzip do konca strumienia oraz kazdy wpis tar az do ostatniego naglowka
// EOF. To wykrywa dokladnie ten sam rodzaj uszkodzenia (obciety plik, brak
// koncowych blokow tar / trailera gzip) jaki inaczej ujawnilby sie dopiero
// przy pullu z registry jako "unexpected EOF", ale robi to LOKALNIE, zaraz
// po zbudowaniu warstwy, zanim cokolwiek zostanie wyslane w swiat.
func validateLayerTarball(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("otwarcie: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("naglowek gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("naglowek tar wpisu #%d: %w", entries+1, err)
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fmt.Errorf("zawartosc wpisu tar %q: %w", hdr.Name, err)
		}
		entries++
	}
	if entries == 0 {
		return fmt.Errorf("warstwa jest pusta (0 wpisow) -- prawdopodobnie rootfs nie zostal poprawnie zbudowany")
	}

	// gzr.Close() (deferred) sprawdza CRC32/rozmiar zapisane w trailerze
	// gzip wzgledem faktycznie odczytanych danych -- wywolujemy jawnie tutaj
	// (przed defer) zeby jego blad tez trafil do walidacji, a nie zostal
	// po cichu zignorowany jak w oryginalnym bledzie tego zgloszenia.
	if err := gzr.Close(); err != nil {
		return fmt.Errorf("weryfikacja trailera gzip: %w", err)
	}
	return nil
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
