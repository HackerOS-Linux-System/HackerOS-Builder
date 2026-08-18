package ociimage

import (
	"fmt"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// LocalArchiveParams to dane potrzebne do zapisania rootfs jako lokalnego
// archiwum obrazu OCI/Docker, wczytywalnego bezposrednio przez
// `podman load -i <plik>` lub `docker load -i <plik>` -- BEZ potrzeby
// posiadania konta/tokena w zadnym registry. Uzywane przez
// buildflow.BuildContainer ([project] -> type = container).
type LocalArchiveParams struct {
	RootfsDir  string // katalog z gotowym rootfs (po rootfs.Builder.Build(), ContainerMode=true)
	Repository string // nazwa obrazu w archiwum, np. "ghcr.io/michal/moj-kontener" (uzywana jako repo tag, NIE jest wypychana nigdzie)
	Tag        string // tag obrazu, np. "latest"
	WorkDir    string // katalog tymczasowy na warstwe tar (podobnie jak BuildParams.WorkDir)
	OutputPath string // docelowa sciezka pliku .tar
}

// containerDefaultEnv to minimalny, rozsadny PATH dla kontenera roboczego --
// taki sam ukladu jak w standardowych obrazach bazowych Debiana, zeby
// polecenia zainstalowane przez apt-get (Project.Packages) byly od razu
// znajdowane bez dodatkowej konfiguracji przez uzytkownika kontenera.
var containerDefaultEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
}

// SaveLocalArchive pakuje RootfsDir do jednowarstwowego obrazu OCI (dokladnie
// tak samo jak BuildAndPush -- ta sama funkcja createLayerTarball/
// validateLayerTarball/buildImageFromLayer z push.go, wiec walidacja warstwy
// jest identyczna) i zapisuje go LOKALNIE jako archiwum w formacie Docker
// (tarball.WriteToFile), zamiast wypychac do registry.
//
// Domyslny Cmd/WorkingDir obrazu sa ustawione tak, by `podman run -it --rm
// <repo>:<tag>` od razu dawal uzywalna powloke w /root -- typowe
// oczekiwanie dla "zwyklego kontenera do codziennej pracy", w odroznieniu
// od obrazow bazowych bez zdefiniowanego Cmd (ktore bez jawnego polecenia
// przy `run` koncza sie bledem "no command specified").
func SaveLocalArchive(p LocalArchiveParams) error {
	if err := os.MkdirAll(p.WorkDir, 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu roboczego %s: %w", p.WorkDir, err)
	}

	util.Infof("Pakowanie rootfs do warstwy OCI (kontener lokalny)...")
	layerTarPath := filepath.Join(p.WorkDir, "layer.tar.gz")
	if err := createLayerTarball(p.RootfsDir, layerTarPath); err != nil {
		return fmt.Errorf("tworzenie warstwy tar: %w", err)
	}
	defer os.Remove(layerTarPath)

	if err := validateLayerTarball(layerTarPath); err != nil {
		return fmt.Errorf("zbudowana warstwa OCI jest uszkodzona (%s): %w -- "+
			"NIE zapisujemy lokalnego archiwum; sprawdz miejsce na dysku w %s i uruchom build ponownie",
			layerTarPath, err, p.WorkDir)
	}

	util.Infof("Budowanie obrazu OCI (v1.Image)...")
	img, err := buildImageFromLayer(layerTarPath)
	if err != nil {
		return fmt.Errorf("budowanie obrazu OCI: %w", err)
	}

	img, err = mutateContainerDefaults(img)
	if err != nil {
		return fmt.Errorf("ustawianie domyslnej konfiguracji kontenera: %w", err)
	}

	refStr := fmt.Sprintf("%s:%s", p.Repository, p.Tag)
	tag, err := name.NewTag(refStr)
	if err != nil {
		return fmt.Errorf("nieprawidlowa referencja obrazu %q: %w", refStr, err)
	}

	if err := os.MkdirAll(filepath.Dir(p.OutputPath), 0o755); err != nil {
		return fmt.Errorf("tworzenie katalogu docelowego %s: %w", filepath.Dir(p.OutputPath), err)
	}

	util.Infof("Zapis lokalnego archiwum kontenera -> %s", p.OutputPath)
	if err := tarball.WriteToFile(p.OutputPath, tag, img); err != nil {
		return fmt.Errorf("zapis archiwum %s: %w", p.OutputPath, err)
	}

	util.Infof("Archiwum kontenera zapisane: %s (%s)", p.OutputPath, refStr)
	return nil
}

// mutateContainerDefaults nadaje obrazowi rozsadna domyslna konfiguracje
// wykonawcza (Cmd/WorkingDir/Env) -- BEZ tego `podman run`/`docker run` na
// swiezo wczytanym obrazie konczy sie bledem "no command specified", bo
// rootfs zbudowany przez debootstrap sam w sobie nie niesie zadnych
// metadanych obrazu OCI (Dockerfile-like), tylko pliki.
func mutateContainerDefaults(img v1.Image) (v1.Image, error) {
	return mutate.Config(img, v1.Config{
		Cmd:        []string{"/bin/bash"},
		WorkingDir: "/root",
		Env:        containerDefaultEnv,
	})
}
