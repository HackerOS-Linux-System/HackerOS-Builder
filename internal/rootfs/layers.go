package rootfs

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Ten plik implementuje warstwy OCI PRZYROSTOWE: zamiast pakowac caly
// rootfs do JEDNEJ warstwy tar.gz (jak robil BuildAndPush do wersji
// 0.8.0), Builder.Build() robi migawki (snapshotTree) stanu rootfsDir w
// kilku punktach kontrolnych budowy i pakuje RoZNICE miedzy kolejnymi
// migawkami jako osobne warstwy OCI:
//
//  1. "base"     -- debootstrap + preseed + includes_before + extra
//                   sources + MAC + (opcjonalnie) pakiety cybersecurity
//  2. "packages" -- pakiety z config/package-lists/*.list.chroot
//  3. "hooks"    -- includes.chroot / includes.chroot_after_packages +
//                   hooks/normal + hooks/live
//  4. "runtime"  -- wstrzykniety hammer + /etc/hammer/oci.hk (pomijana w
//                   ContainerMode, patrz Builder.ContainerMode)
//
// KOMPROMIS SWIADOMY: wykrywanie zmian oparte jest o (rozmiar, tryb,
// mtime) kazdej sciezki, NIE o hash tresci pliku -- czytanie i haszowanie
// KAZDEGO pliku w rootfs przy KAZDYM punkcie kontrolnym zaprzeczaloby
// samemu celowi warstw przyrostowych (mialoby to podobny koszt I/O co
// spakowanie calego rootfs za kazdym razem). W tym konkretnym pipeline
// jest to bezpieczne: kazdy plik ktory faktycznie sie zmienia miedzy
// dwoma punktami kontrolnymi dostaje NOWY mtime (od apt/dpkg/hooks/cp),
// wiec falszywy negatyw (zmiana tresci przy IDENTYCZNYM rozmiarze I
// mtime co do sekundy) jest praktycznie niemozliwy w normalnym przebiegu
// budowy. To ta sama heurystyka co np. "make"/"ninja" (bez trybu hash) czy
// rsync bez --checksum -- ugruntowana praktyka dla tego typu narzedzi.
//
// Korzysc: przy przebudowie projektu gdzie zmienily sie TYLKO wlasne hooki
// (np. iteracja nad "installer/" albo "normal/" podczas developmentu),
// warstwy "base" i "packages" sa BAJT W BAJT identyczne jak poprzednio --
// registry z deduplikacja warstw (content-addressed storage, tak dziala
// kazdy zgodny z OCI registry) nie musi ich ponownie przechowywac, a klient
// pullujacy obraz (hammer / "build iso") sciaga tylko zmienione warstwy.

// fsEntry to minimalny odcisk pojedynczej sciezki w drzewie rootfs,
// wystarczajacy do wykrycia "czy to sie zmienilo miedzy dwoma migawkami".
type fsEntry struct {
	mode    os.FileMode
	size    int64
	modTime time.Time
}

// snapshotTree przechadza sie po root i zwraca mape sciezka-wzgledna ->
// fsEntry dla kazdego wpisu (pliki, katalogi, symlinki -- wszystko, tak
// jak filepath.Walk je widzi, uzywajac Lstat semantyki dla symlinkow, tak
// samo jak reszta pakietu przy budowaniu tar warstwy).
func snapshotTree(root string) (map[string]fsEntry, error) {
	m := make(map[string]fsEntry)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		m[rel] = fsEntry{mode: info.Mode(), size: info.Size(), modTime: info.ModTime()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// diffSnapshots zwraca sciezki DODANE-LUB-ZMIENIONE (obecne w after,
// nieobecne w before LUB o innym mode/size/modTime) oraz sciezki USUNIETE
// (obecne w before, nieobecne w after) miedzy dwiema migawkami. Wyniki sa
// posortowane; removed jest dodatkowo przycieta przez
// pruneRedundantRemovals (patrz nizej).
func diffSnapshots(before, after map[string]fsEntry) (changed []string, removed []string) {
	for path, a := range after {
		b, ok := before[path]
		if !ok || b.mode != a.mode || b.size != a.size || !b.modTime.Equal(a.modTime) {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			removed = append(removed, path)
		}
	}
	sort.Strings(changed)
	sort.Strings(removed)
	removed = pruneRedundantRemovals(removed)
	return changed, removed
}

// pruneRedundantRemovals usuwa z listy kazda sciezke ktorej rodzic (albo
// dowolny przodek) JUZ jest w liscie usunietych -- whiteout katalogu
// (".wh.<katalog>") oznacza w konwencji AUFS/OCI usuniecie TEGO katalogu
// WRAZ Z CALA JEGO ZAWARTOSCIA (dokladnie tak samo interpretuje to
// internal/ociimage.extractTarStream: os.RemoveAll na calym katalogu), wiec
// osobne whiteouty dla kazdego pliku WEWNATRZ juz-usunietego katalogu sa
// zbedne -- tylko powiekszalyby rozmiar warstwy bez zadnej dodatkowej
// informacji.
func pruneRedundantRemovals(removed []string) []string {
	var pruned []string
	for _, r := range removed {
		skip := false
		for _, p := range pruned {
			if strings.HasPrefix(r, p+"/") {
				skip = true
				break
			}
		}
		if !skip {
			pruned = append(pruned, r)
		}
	}
	return pruned
}

// writeIncrementalLayer pakuje TYLKO sciezki z changed (odczytane na zywo z
// rootfsDir) plus whiteout entries dla removed do destTarGz. Zwraca
// (wrote bool, err error) -- wrote=false gdy changed i removed sa OBA puste
// (nic sie nie zmienilo miedzy dwoma punktami kontrolnymi -- np. projekt
// bez pakietow cybersecurity, gdzie warstwa "packages" moglaby wypasc
// pusta), w takim wypadku NIC nie jest zapisywane na dysk i wywolujacy
// powinien pominac ta warstwe (pusta warstwa OCI jest dopuszczalna przez
// specyfikacje, ale nie ma sensu jej tworzyc i przesylac).
func writeIncrementalLayer(rootfsDir string, changed, removed []string, destTarGz string) (bool, error) {
	if len(changed) == 0 && len(removed) == 0 {
		return false, nil
	}

	out, err := os.Create(destTarGz)
	if err != nil {
		return false, err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	var walkErr error
	for _, rel := range changed {
		if walkErr = writeTarEntry(tw, rootfsDir, rel); walkErr != nil {
			break
		}
	}
	if walkErr == nil {
		for _, rel := range removed {
			if walkErr = writeWhiteoutEntry(tw, rel); walkErr != nil {
				break
			}
		}
	}

	// Patrz createLayerTarball (push.go) dla wyjasnienia dlaczego KAZDY
	// blad Close() jest sprawdzany jawnie zamiast polegac na defer --
	// dokladnie ten sam powod (obciety plik warstwy bez konca strumienia
	// gzip/tar musi przerwac build TUTAJ, nie ujawnic sie dopiero przy
	// pozniejszym pull z registry).
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
		os.Remove(destTarGz)
		return false, walkErr
	}
	return true, nil
}

// writeTarEntry zapisuje pojedynczy wpis (plik/katalog/symlink/etc.) z
// rootfsDir/rel do tw -- ta sama logika co createLayerTarballWalk w
// push.go dla pojedynczego wpisu, wydzielona zeby dalo sie jej uzyc zarowno
// dla pelnego drzewa (warstwa jednolita, stara sciezka) jak i dla
// wybranego podzbioru sciezek (warstwy przyrostowe, ten plik).
func writeTarEntry(tw *tar.Writer, rootfsDir, rel string) error {
	path := filepath.Join(rootfsDir, rel)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", rel, err)
	}

	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("readlink %s: %w", rel, err)
		}
	}

	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("naglowek tar dla %s: %w", rel, err)
	}
	hdr.Name = rel
	if info.IsDir() {
		hdr.Name += "/"
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("zapis naglowka %s: %w", rel, err)
	}

	if info.Mode().IsRegular() {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("otwarcie %s: %w", rel, err)
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("kopiowanie tresci %s: %w", rel, err)
		}
	}
	return nil
}

// writeWhiteoutEntry zapisuje wpis whiteout AUFS/OCI (".wh.<nazwa>") dla
// sciezki rel, ktora zniknela z rootfs miedzy dwoma punktami kontrolnymi --
// dokladnie w formacie ktory internal/ociimage.extractTarStream juz
// odczytuje (patrz jej obsluga ".wh." przy rozpakowywaniu warstw
// pociagnietych z registry).
func writeWhiteoutEntry(tw *tar.Writer, rel string) error {
	dir := filepath.Dir(rel)
	base := filepath.Base(rel)
	name := ".wh." + base
	if dir != "." {
		name = dir + "/" + name
	}
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     0,
		ModTime:  time.Now(),
	}
	return tw.WriteHeader(hdr)
}
