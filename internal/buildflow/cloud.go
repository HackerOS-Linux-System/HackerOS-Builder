package buildflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/buildlock"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/ociimage"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/preflight"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/rootfs"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// defaultRegistryHost to domyslny registry uzywany przy budowaniu sciezki
// obrazu OCI z [account] w config.hk. GitHub Container Registry jest
// naturalnym wyborem domyslnym w ekosystemie HackerOS (hostowanym na GitHub).
const defaultRegistryHost = "ghcr.io"

// defaultImageName zwraca nazwe katalogu projektu jako nazwe obrazu w
// registry -- kazdy projekt hackeros-builder ma naturalnie unikalna sciezke.
func defaultImageName(projectDir string) string {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "hackeros-image"
	}
	return filepath.Base(abs)
}

// CloudOptions to parametry komendy "build cloud". Wyodrebnione do struct
// (zamiast pozycyjnych argumentow funkcji) zeby dodawanie nowych opcji
// nie wymagalo zmiany sygnatury w kazdym miejscu wywolania.
type CloudOptions struct {
	ProjectDir string
	WorkDir    string

	// InsecureRegistry wylacza weryfikacje TLS przy push do registry --
	// patrz internal/ociimage.BuildParams.Insecure dla pelnego opisu.
	InsecureRegistry bool

	// SkipPreflight pomija sprawdzenie dostepnosci debootstrap/chroot/mount
	// na starcie. Domyslnie false -- preflight jest WLACZONY domyslnie,
	// bo cel jego istnienia to wlasnie ochrona przed odkryciem braku
	// debootstrap po 10 minutach budowania.
	SkipPreflight bool

	// SkipLock pomija wziecie buildlock.Acquire wewnatrz BuildCloud.
	// Uzywane gdy wolajacy (np. BuildAll) JUZ przytrzymuje blokade na tym
	// samym WorkDir -- flock(2) NIE jest reentrant w ramach tego samego
	// procesu na dwoch roznych deskryptorach pliku otwartych do tej samej
	// sciezki (druga proba Acquire zwrocilaby blad "juz zablokowane" mimo
	// ze to ten sam proces), wiec BuildAll musi explicite poprosic o
	// pominiecie drugiej blokady.
	SkipLock bool
}

// CloudResult to wynik komendy "build cloud" -- zawiera dane potrzebne
// kolejnym krokom (np. "build iso" w ramach "build all").
type CloudResult struct {
	Repository string // pelna sciezka repo w registry
	Tag        string // tag wypchnietego obrazu
	Refspec    string // "deb-ostree-oci:Repository:Tag" -- gotowe dla origin deb-ostree
}

// BuildCloud wykonuje pelny przeplyw "hackeros-builder build cloud":
//
//  0. preflight.CheckCloud() -- weryfikuje debootstrap/chroot/mount/umount
//  0. buildlock.Acquire(workDir) -- chroni przed wspolbieznym buildem w tym samym workDir
//  1. parsuje config/config.hk i strukture live-build (liveparse)
//  2. buduje rootfs (debootstrap + hooks + packages + deb-ostree + deb-ostree.hk)
//  3. pakuje rootfs do obrazu OCI i wypycha go do registry (ociimage)
//
// Po zakonczeniu obraz jest dostepny w registry -- to jest "wyslanie w swiat"
// o ktorym mowi nazwa komendy. Nic lokalnego (poza plikami tymczasowymi w
// workDir) nie jest tworzone -- "build cloud" nie generuje ISO.
func BuildCloud(opts CloudOptions) (*CloudResult, error) {
	if !opts.SkipPreflight {
		if err := preflight.CheckCloud(); err != nil {
			return nil, fmt.Errorf("preflight: %w", err)
		}
	}

	if !opts.SkipLock {
		lock, err := buildlock.Acquire(opts.WorkDir)
		if err != nil {
			return nil, err
		}
		defer lock.Release()
	}

	cfg, err := loadAndValidateConfig(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	project, err := liveparse.Parse(opts.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("parsowanie struktury projektu: %w", err)
	}
	util.Infof("Projekt zinterpretowany:\n%s", project.Summary())

	rootfsDir := filepath.Join(opts.WorkDir, "rootfs")
	builder := rootfs.New(project, cfg, rootfsDir, opts.WorkDir)

	if err := builder.Build(); err != nil {
		return nil, fmt.Errorf("budowa rootfs: %w", err)
	}

	// Nazwa obrazu OCI z [project] -> name (jesli ustawione), fallback na nazwe katalogu.
	imageName := defaultImageName(opts.ProjectDir)
	if cfg.Project.Name != "" {
		imageName = cfg.Project.Name
		util.Infof("Nazwa obrazu OCI z [project] -> name: %s", imageName)
	}

	// Tag obrazu OCI z [project] -> tag, domyslnie "latest".
	imageTag := cfg.Project.ImageTag()
	util.Infof("Tag obrazu OCI: %s", imageTag)

	repository := cfg.ImageRepository(defaultRegistryHost, imageName)

	// Walidacja trybu projektu -- typy normal/official wymagaja live-build
	// na hoscie i nie sa jeszcze w pelni zaimplementowane przez ten builder.
	if cfg.Project.RequiresLiveBuild() {
		if _, err := exec.LookPath("lb"); err != nil {
			return nil, fmt.Errorf(
				"[project] -> type=%s wymaga zainstalowanego live-build na hoscie "+
					"(komenda 'lb' nie znaleziona w $PATH). "+
					"Zainstaluj live-build: apt-get install live-build",
				cfg.Project.Type)
		}
		util.Infof("Typ projektu: %s -- delegowanie do live-build (lb build)...", cfg.Project.Type)
		return runLiveBuild(opts.ProjectDir)
	}

	if cfg.Project.Type == config.ProjectTypeIndependent {
		util.Infof("Typ projektu: independent -- build bez OCI i bez deb-ostree")
	}

	// Tag: uzywamy [project] -> tag jesli ustawiony, fallback na nazwe release Debiana.
	// Pozwala wersjonowac obrazy OCI niezaleznie od wersji Debiana.
	tag := imageTag

	util.Infof("Pakowanie i wypychanie obrazu OCI do %s:%s...", repository, tag)
	pushWorkDir := filepath.Join(opts.WorkDir, "oci-push")
	if err := os.MkdirAll(pushWorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("tworzenie katalogu roboczego push: %w", err)
	}

	refspec, err := ociimage.BuildAndPush(ociimage.BuildParams{
		RootfsDir:  rootfsDir,
		Repository: repository,
		Tag:        tag,
		Token:      cfg.Token,
		WorkDir:    pushWorkDir,
		Insecure:   opts.InsecureRegistry,
	})
	if err != nil {
		return nil, fmt.Errorf("build cloud: %w", err)
	}

	util.Infof("Build cloud zakonczony: %s", refspec)

	return &CloudResult{
		Repository: repository,
		Tag:        tag,
		Refspec:    "deb-ostree-oci:" + refspec,
	}, nil
}

// loadAndValidateConfig wczytuje config/config.hk i wypisuje ostrzezenie
// (nie blad) jesli release nie jest na liscie znanych wersji Debiana.
func loadAndValidateConfig(projectDir string) (*config.Config, error) {
	configPath := filepath.Join(projectDir, "config", "config.hk")
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("wczytywanie %s: %w", configPath, err)
	}

	if !cfg.IsKnownRelease() {
		util.Warnf("Release %q nie jest na liscie znanych wersji Debiana "+
			"(bookworm/trixie/forky/sid/unstable) -- kontynuuje, ale sprawdz "+
			"czy nazwa suite istnieje w uzywanym mirror.", cfg.Release)
	}
	return cfg, nil
}

// runLiveBuild deleguje caly build do "lb build" z live-build.
// Uzywane dla typow projektu normal i official ([project] -> type = normal/official).
// Katalog roboczy to projectDir -- live-build oczekuje ze jego pliki konfiguracyjne
// (config/, hooks/, package-lists/ itp.) sa w biezacym katalogu.
func runLiveBuild(projectDir string) (*CloudResult, error) {
	util.Infof("Uruchamianie live-build (lb build) w %s...", projectDir)
	util.Infof("(hackeros-builder deleguje build do live-build -- wyjscie pochodzi z lb)")
	util.Infof("---")

	cmd := exec.Command("lb", "build")
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf(
			"live-build (lb build) nie powiodl sie: %w\n"+
				"Sprawdz logi powyzej. Mozliwe przyczyny:\n"+
				"  - brak konfiguracji live-build (lb config nie zostal uruchomiony)\n"+
				"  - blad w hooks lub package-lists\n"+
				"  - brak dostepu do internetu (apt-get update zawiodlo)\n"+
				"  - brak uprawnien roota (lb build wymaga sudo lub uruchomienia jako root)",
			err)
	}

	util.Infof("---")
	util.Infof("live-build zakonczony. Wynikowy obraz ISO jest w %s/", projectDir)
	util.Infof("(live-build nie pushuje obrazu OCI -- to jest zwykly non-atomowy build)")

	// Dla typow normal/official nie pushujemy OCI i nie wstrzykujemy deb-ostree,
	// wiec zwracamy pusty CloudResult -- buildflow nie bedzie probowal robic
	// "build iso" po tym kroku.
	return &CloudResult{}, nil
}
