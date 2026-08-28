package ociimage

import (
	"archive/tar"
	"compress/gzip"
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

	var totalBytes int64
	sizes := make([]int64, len(layers))
	for i, layer := range layers {
		size, sizeErr := layer.Size()
		if sizeErr == nil {
			sizes[i] = size
			totalBytes += size
		}
	}

	util.Infof("Rozpakowywanie %d warstw(y) (razem %s)...", len(layers), util.FormatBytes(totalBytes))
	bar := util.NewProgressBar("pull OCI", totalBytes, "bajtow")
	var doneBytes int64
	onRead := func(n int64) {
		doneBytes += n
		bar.Set(doneBytes)
	}
	for i, layer := range layers {
		if err := extractLayerWithProgress(layer, destDir, onRead); err != nil {
			bar.Fail(fmt.Sprintf("warstwa %d/%d", i+1, len(layers)))
			return fmt.Errorf("rozpakowywanie warstwy %d/%d: %w", i+1, len(layers), err)
		}
	}
	bar.Finish()
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

// tarModeToFileMode konwertuje surowe uniksowe bity trybu z naglowka tar
// (hdr.Mode, np. 0o1777 dla /tmp albo 0o4755 dla binarki setuid jak
// /usr/bin/sudo) na poprawny os.FileMode.
//
// TO NIE JEST to samo co os.FileMode(hdr.Mode)! os.FileMode koduje bity
// specjalne (setuid/setgid/sticky) jako ODREBNE, WYSOKIE bity (ModeSetuid =
// 1<<23 itd.), calkowicie inaczej niz tradycyjny uniksowy zapis osemkowy
// (gdzie sticky/setgid/setuid siedza w NISKICH bitach, 0o1000/0o2000/0o4000).
// Naiwna konwersja typu os.FileMode(hdr.Mode) NIE ustawia poprawnie tych
// flag Go -- a co gorsza, o.Perm() (uzywane wewnetrznie przez os.Chmod przy
// tlumaczeniu na wywolanie systemowe) maskuje wynik do samych 9 najnizszych
// bitow (0o777), wiec bit sticky (0o1000) po prostu ZNIKA, a syscallMode()
// nie doda S_ISVTX/S_ISUID/S_ISGID z powrotem, bo nie widzi ustawionej flagi
// Go. Efekt: katalogi typu /tmp traca sticky bit, a binarki setuid (sudo!)
// traca bit setuid -- w obu przypadkach CICHO, bez bledu, po prostu z
// nieprawidlowymi uprawnieniami w finalnym rootfs.
func tarModeToFileMode(rawMode int64) os.FileMode {
	fm := os.FileMode(rawMode & 0o777)
	if rawMode&0o4000 != 0 {
		fm |= os.ModeSetuid
	}
	if rawMode&0o2000 != 0 {
		fm |= os.ModeSetgid
	}
	if rawMode&0o1000 != 0 {
		fm |= os.ModeSticky
	}
	return fm
}

// extractLayerWithProgress rozpakowuje pojedyncza warstwe OCI, zliczajac
// REALNE bajty skompresowane odczytane ze strumienia (layer.Compressed(),
// nie Uncompressed()!) i przekazujac je do onRead -- total odpowiada
// DOKLADNIE layer.Size() uzytemu przez wywolujacego (PullAndUnpack) do
// zainicjowania paska postepu, wiec postep jest dokladny (nie
// przyblizenie z rozmiaru zdekompresowanego, ktorego nie da sie poznac z
// gory bez pobrania calej warstwy).
func extractLayerWithProgress(layer v1.Layer, destDir string, onRead func(int64)) error {
	rc, err := layer.Compressed()
	if err != nil {
		return fmt.Errorf("Compressed(): %w", err)
	}
	defer rc.Close()

	counting := &util.CountingReader{R: rc, OnRead: onRead}
	gzr, err := gzip.NewReader(counting)
	if err != nil {
		return fmt.Errorf("naglowek gzip warstwy: %w", err)
	}
	defer gzr.Close()

	return extractTarStream(tar.NewReader(gzr), destDir)
}

// extractTarStream zawiera cala logike rozpakowywania pojedynczego strumienia
// tar do destDir -- wydzielone z extractLayerWithProgress wylacznie po to, zeby dalo sie
// to przetestowac jednostkowo bez potrzeby prawdziwego v1.Layer/registry
// (testy budują tr z bytes.Buffer przez archive/tar bezposrednio).
func extractTarStream(tr *tar.Reader, destDir string) error {
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
		mode := tarModeToFileMode(hdr.Mode)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode); err != nil {
				return err
			}
			// os.MkdirAll ma DWA problemy ktore razem gubia bity specjalne
			// (najbardziej widoczne na /tmp, ktore w normalnym rootfs ma
			// tryb 1777 -- sticky bit + zapis dla wszystkich):
			//
			//  1. Jesli target JUZ ISTNIEJE (np. zostal utworzony wczesniej
			//     jako katalog nadrzedny jakiegos pliku przez ensureParentDir,
			//     ktore uzywa na sztywno 0o755 -- patrz nizej), MkdirAll w
			//     ogole nie zmienia jego trybu, tylko zwraca nil.
			//  2. Nawet jesli target jest tworzony od nowa w tym wywolaniu,
			//     jadro stosuje "mode & ~umask" -- przy typowym umask 0022
			//     procesu (np. root spod sudo) zadane 01777 wychodzi jako
			//     01755, czyli BEZ bitu zapisu dla "innych". To dokladnie
			//     tyle, ile trzeba, zeby apt-get (ktory dla bezpieczenstwa
			//     pobiera/weryfikuje repozytoria jako nieuprzywilejowany
			//     uzytkownik _apt) nie mogl juz utworzyc pliku tymczasowego
			//     w /tmp -- "Unable to mkstemp ... Permission denied".
			//
			// os.Chmod ustawia tryb DOKLADNIE taki jak zadany, pomijajac
			// umask i dzialajac tez na katalogach ktore juz istnialy --
			// wywolujemy go zawsze, bezwarunkowo, po MkdirAll.
			if err := os.Chmod(target, mode); err != nil {
				return fmt.Errorf("chmod katalogu %q na %o: %w", entryName, hdr.Mode, err)
			}
		case tar.TypeReg:
			if err := ensureParentDir(target); err != nil {
				return err
			}
			// Tak jak przy TypeSymlink/TypeLink nizej: target MOZE juz
			// istniec z poprzedniej warstwy jako SYMLINK (bardzo typowe
			// w motywach ikon typu breeze-dark, gdzie wiele plikow to
			// symlinki do innych ikon). os.OpenFile z O_CREATE NIE
			// podmienia istniejacego symlinku -- PODAZA za nim (resolve).
			// Jesli ten symlink jest czescia lancucha/petli przekraczajacej
			// limit jadra, OpenFile wywala sie z ELOOP ("too many levels
			// of symbolic links"), mimo ze intencja tej warstwy jest po
			// prostu polozyc tu zwykly plik. Bezwarunkowe os.Remove PRZED
			// OpenFile gwarantuje, ze zawsze piszemy do SWIEZEGO i-node,
			// nigdy nie podazajac za starym symlinkiem z poprzedniej
			// warstwy (blad "nie istnieje" z Remove jest tu nieistotny
			// i celowo ignorowany).
			os.Remove(target)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("kopiowanie zawartosci %q ze strumienia warstwy: %w", entryName, err)
			}
			f.Close()
			// Tak jak przy katalogach: O_CREATE respektuje umask procesu, a
			// jesli plik JUZ ISTNIAL (nadpisanie czegos z poprzedniej
			// warstwy), OpenFile w ogole nie zmienia trybu istniejacego
			// pliku. Bity setuid/setgid sa krytyczne dla poprawnosci binarek
			// typu /usr/bin/sudo (setuid root) -- bez tego chmod, sudo
			// wygladaloby na zainstalowane, ale faktycznie nie dzialaloby
			// dla zwyklych uzytkownikow w finalnie zainstalowanym systemie.
			if err := os.Chmod(target, mode); err != nil {
				return fmt.Errorf("chmod pliku %q na %o: %w", entryName, hdr.Mode, err)
			}
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
