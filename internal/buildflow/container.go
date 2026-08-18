package buildflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/buildlock"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/config"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/download"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/ociimage"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/preflight"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/rootfs"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// isolatorContainerPackages sa DODAWANE do project.Packages TYLKO gdy
// [project] -> type = containerized -- Isolator jest podman-owym menedzerem
// pakietow, wiec kontener z wbudowanym Isolatorem musi miec podman ZANIM
// `isolator install <pakiet>` ma jakiekolwiek szanse zadzialac.
// ca-certificates jest potrzebne zeby zarowno podman (pull obrazow HTTPS)
// jak i sam Isolator (pobieranie repo/checksumow z GitHuba wewnatrz
// kontenera) mialy dzialajaca weryfikacje TLS -- dokladnie ta sama para
// pakietow co w prawdziwym Isolator Builder (patrz
// builder/pipeline.go, runPostBootstrap: "podman", "ca-certificates").
var isolatorContainerPackages = []string{"podman", "ca-certificates"}

// ContainerOptions to parametry komendy "build container".
type ContainerOptions struct {
	ProjectDir string
	WorkDir    string

	// LocalOnly: gdy true, obraz NIGDY nie jest wypychany do registry --
	// powstaje WYLACZNIE lokalne archiwum (.tar) wczytywalne przez
	// `podman load`/`docker load`. Gdy false (domyslnie), push do registry
	// jest wykonywany DODATKOWO (obok lokalnego archiwum, ktore powstaje
	// zawsze) TYLKO jesli [account]/[auth] w config.hk sa faktycznie
	// wypelnione -- "build container" nie wymaga konta w registry, w
	// odroznieniu od "build cloud".
	LocalOnly bool

	InsecureRegistry bool
	SkipPreflight    bool
	SkipLock         bool
}

// ContainerResult to wynik komendy "build container".
type ContainerResult struct {
	LocalArchivePath string // sciezka lokalnego archiwum .tar (zawsze ustawiona)
	Repository       string
	Tag              string
	Pushed           bool // true jesli obraz zostal DODATKOWO wypchniety do registry

	// IsolatorVersion i IsolatorBinaries: USTAWIONE TYLKO gdy
	// [project] -> type = containerized -- patrz embedIsolatorIntoRootfs
	// nizej. Puste dla zwyklego type = container.
	IsolatorVersion  string
	IsolatorBinaries []string
}

// BuildContainer wykonuje pelny przeplyw "hackeros-builder build container":
//
//  0. preflight.CheckCloud() -- ten sam zestaw narzedzi co "build cloud"
//     (debootstrap/chroot/mount), bo rootfs jest budowany identycznie
//  0. buildlock.Acquire(workDir)
//  1. parsuje config/config.hk i strukture live-build (liveparse)
//  2. TYLKO gdy [project] -> type = containerized: dopisuje
//     isolatorContainerPackages (podman, ca-certificates) do
//     project.Packages -- PRZED Build(), zeby zostaly zainstalowane w
//     normalnym kroku "instalacja pakietow" (z tym samym paskiem postepu
//     co reszta pakietow projektu, bez duplikowania logiki apt-get)
//  3. buduje rootfs (debootstrap + hooks + packages) w ContainerMode --
//     BEZ wstrzykiwania hammer/oci.hk i BEZ usuwania apt/apt-get (patrz
//     rootfs.Builder.ContainerMode)
//  4. TYLKO gdy [project] -> type = containerized: pobiera najnowsza (albo
//     [project] -> isolator_version) wersje Isolatora z GitHub Releases i
//     wypakowuje ja do rootfs/usr/bin/ (patrz embedIsolatorIntoRootfs) --
//     PRZED walidacja/pakowaniem, zeby ValidateRootfs i finalne archiwum
//     juz go zawieraly
//  5. pakuje rootfs jako obraz OCI/Docker i zapisuje lokalne archiwum
//     wczytywalne przez podman/docker (ociimage.SaveLocalArchive)
//  6. jesli [account]/[auth] sa skonfigurowane i LocalOnly=false, DODATKOWO
//     wypycha ten sam obraz do registry (ociimage.BuildAndPush) -- wygodne
//     dla wspoldzielenia kontenera roboczego z innymi maszynami bez
//     recznego kopiowania pliku .tar
//
// W odroznieniu od "build cloud", ten przeplyw NIGDY nie wstrzykuje hammer
// i nigdy nie buduje ISO -- kontener roboczy jest przeznaczony wylacznie do
// uruchamiania przez podman/docker, nie do instalacji na dysk.
func BuildContainer(opts ContainerOptions) (*ContainerResult, error) {
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

	if !cfg.Project.IsContainerBuild() && !cfg.Project.IsContainerizedIsolator() {
		util.Warnf("[project] -> type = %q (nie \"container\"/\"containerized\") -- 'build container' "+
			"zawsze buduje kontener roboczy niezaleznie od [project] -> type, ale rozwaz ustawienie "+
			"[project] -> type = container w config.hk, zeby jasno udokumentowac przeznaczenie projektu.",
			cfg.Project.Type)
	}

	project, err := liveparse.Parse(opts.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("parsowanie struktury projektu: %w", err)
	}

	if cfg.Project.IsContainerizedIsolator() {
		// Dopisywane PRZED Build() -- zainstalowane jak kazdy inny
		// pakiet projektu, z tym samym paskiem postepu. Duplikaty
		// (gdyby projekt juz mial "podman" we wlasnych package-lists)
		// sa nieszkodliwe -- apt-get install jest idempotentny.
		project.Packages = append(project.Packages, isolatorContainerPackages...)
		util.Infof("[project] -> type=containerized: dopisano %s do pakietow (wymagane przez Isolator)",
			strings.Join(isolatorContainerPackages, ", "))
	}

	util.Infof("Projekt zinterpretowany:\n%s", project.Summary())

	rootfsDir := filepath.Join(opts.WorkDir, "rootfs-container")
	builder := rootfs.New(project, cfg, rootfsDir, opts.WorkDir)
	// ContainerMode: pomija wstrzykiwanie hammer/oci.hk -- kontener roboczy
	// NIE jest zarzadzany atomowo. Patrz rootfs.Builder.ContainerMode.
	builder.ContainerMode = true

	if err := builder.Build(); err != nil {
		return nil, fmt.Errorf("budowa rootfs kontenera: %w", err)
	}

	result := &ContainerResult{}

	if cfg.Project.IsContainerizedIsolator() {
		version, binaries, err := embedIsolatorIntoRootfs(cfg, rootfsDir)
		if err != nil {
			return nil, fmt.Errorf("wbudowywanie Isolatora ([project] -> type=containerized): %w", err)
		}
		result.IsolatorVersion = version
		result.IsolatorBinaries = binaries
	}

	if err := rootfs.ValidateRootfs(rootfsDir, rootfs.ValidateOptions{RequireHammerConfig: false}); err != nil {
		return nil, fmt.Errorf("walidacja kontenera przed pakowaniem: %w", err)
	}

	imageName, imageTag := resolveImageNameAndTag(cfg, opts.ProjectDir)
	if cfg.Project.Name != "" {
		util.Infof("Nazwa kontenera z [project] -> name: %s", imageName)
	}
	repository := cfg.ImageRepository(defaultRegistryHost, imageName)

	archiveDir := filepath.Join(opts.WorkDir, "container")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("tworzenie katalogu archiwum kontenera: %w", err)
	}
	archivePath := filepath.Join(archiveDir, fmt.Sprintf("%s-%s.tar", imageName, imageTag))

	if err := ociimage.SaveLocalArchive(ociimage.LocalArchiveParams{
		RootfsDir:  rootfsDir,
		Repository: repository,
		Tag:        imageTag,
		WorkDir:    filepath.Join(opts.WorkDir, "container-pack"),
		OutputPath: archivePath,
	}); err != nil {
		return nil, fmt.Errorf("zapis lokalnego archiwum kontenera: %w", err)
	}

	result.LocalArchivePath = archivePath
	result.Repository = repository
	result.Tag = imageTag

	// Push do registry jest DODATKOWY (nie zastepuje archiwum lokalnego) i
	// calkowicie opcjonalny -- w odroznieniu od "build cloud", "build
	// container" nie wymaga konta w registry w ogole (typowy przypadek
	// uzycia to lokalny kontener roboczy, nigdy nigdzie niewypychany).
	shouldPush := !opts.LocalOnly && cfg.AccountName != "" && cfg.Token != ""
	if shouldPush {
		util.Infof("Wypychanie kontenera rowniez do registry %s:%s...", repository, imageTag)
		pushWorkDir := filepath.Join(opts.WorkDir, "oci-push-container")
		if err := os.MkdirAll(pushWorkDir, 0o755); err != nil {
			return nil, fmt.Errorf("tworzenie katalogu roboczego push: %w", err)
		}
		if _, err := ociimage.BuildAndPush(ociimage.BuildParams{
			RootfsDir:  rootfsDir,
			Repository: repository,
			Tag:        imageTag,
			Token:      cfg.Token,
			WorkDir:    pushWorkDir,
			Insecure:   opts.InsecureRegistry,
		}); err != nil {
			return nil, fmt.Errorf("push kontenera do registry: %w", err)
		}
		result.Pushed = true
	} else if opts.LocalOnly {
		util.Infof("--local-only: pomijam push do registry -- kontener dostepny WYLACZNIE lokalnie")
	}

	util.Infof("Kontener roboczy gotowy: %s", archivePath)
	util.Infof("  Zaladuj:   podman load -i %s   (albo: docker load -i %s)", archivePath, archivePath)
	util.Infof("  Uruchom:   podman run -it --rm %s:%s   (albo: docker run -it --rm %s:%s)",
		repository, imageTag, repository, imageTag)
	if result.Pushed {
		util.Infof("  Lub sciagnij z registry: podman pull %s:%s", repository, imageTag)
	}
	if cfg.Project.IsContainerizedIsolator() {
		util.Infof("  Isolator %s wbudowany: %s -- w kontenerze dostepne od razu:",
			result.IsolatorVersion, strings.Join(result.IsolatorBinaries, ", "))
		util.Infof("    isolator init")
		util.Infof("    isolator install <pakiet>")
	}

	return result, nil
}

// embedIsolatorIntoRootfs pobiera Isolatora (najnowsza wersja, albo
// [project] -> isolator_version jesli ustawione) i wypakowuje go do
// rootfsDir/usr/bin/ (patrz download.DownloadAndEmbedIsolator -- to
// dokladnie ten sam mechanizm co pobieranie/wstrzykiwanie hammer dla
// ProjectTypeDefault, tylko innego binarnego narzedzia). Dodatkowo pisze
// systemd unit uruchamiajacy "isolator init" przy pierwszym starcie --
// dokladnie to samo co robi prawdziwy Isolator Builder
// (writeFirstBootUnit w builder/pipeline.go) -- zeby zachowanie
// hackeros-buildera bylo spojne z Isolator Builderem, nawet jesli akurat
// TEN kontener nigdy nie zostanie uzyty jako pelny system z systemd
// (np. uzywany bezposrednio przez podman/docker run bez systemd w ogole --
// wtedy ten unit po prostu nigdy sie nie uruchamia, co jest nieszkodliwe).
func embedIsolatorIntoRootfs(cfg *config.Config, rootfsDir string) (version string, binaries []string, err error) {
	version = cfg.Project.IsolatorVersion
	if version == "" {
		version, err = download.LatestIsolatorVersion()
		if err != nil {
			return "", nil, err
		}
	} else {
		util.Infof("[project] -> isolator_version=%s (pomijam automatyczne wykrywanie)", version)
	}

	binaries, err = download.DownloadAndEmbedIsolator(version, rootfsDir)
	if err != nil {
		return "", nil, err
	}

	if err := writeIsolatorFirstBootUnit(rootfsDir); err != nil {
		// Nieszkodliwy brak (np. rootfs bez systemd zainstalowanego) --
		// ostrzezenie, nie przerywamy calego builda kontenera za to.
		util.Warnf("Nie udalo sie zapisac isolator-first-boot.service: %v", err)
	}

	return version, binaries, nil
}

// writeIsolatorFirstBootUnit -- ten sam unit co writeFirstBootUnit w
// prawdziwym Isolator Builder (builder/pipeline.go), przepisany 1:1 (ten
// sam tekst jednostki, ta sama sciezka /usr/local/bin/isolator -- UWAGA:
// TO INNA SCIEZKA niz /usr/bin/ gdzie DownloadAndEmbedIsolator faktycznie
// wypakowuje binarki; dopisujemy wiec symlink /usr/local/bin/isolator ->
// /usr/bin/isolator, zeby ten skopiowany 1:1 unit dzialal bez modyfikacji).
func writeIsolatorFirstBootUnit(rootfsDir string) error {
	localBinDir := filepath.Join(rootfsDir, "usr", "local", "bin")
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		return err
	}
	symlinkPath := filepath.Join(localBinDir, "isolator")
	os.Remove(symlinkPath)
	if err := os.Symlink("/usr/bin/isolator", symlinkPath); err != nil {
		return fmt.Errorf("symlink /usr/local/bin/isolator -> /usr/bin/isolator: %w", err)
	}

	unit := `[Unit]
Description=Isolator first-boot setup
ConditionPathExists=!/etc/isolator-first-boot-done
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/isolator init
ExecStartPost=/usr/bin/touch /etc/isolator-first-boot-done
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	unitDir := filepath.Join(rootfsDir, "etc", "systemd", "system")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir, "isolator-first-boot.service"), []byte(unit), 0o644); err != nil {
		return err
	}

	wantsDir := filepath.Join(unitDir, "multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, "isolator-first-boot.service")
	os.Remove(link)
	return os.Symlink("../isolator-first-boot.service", link)
}

// wyciszenie "unused import" gdyby przyszla zmiana usuwala jedno z uzyc
// config.* ponizej (obie funkcje sa juz uzywane wprost powyzej -- ten
// blok istnieje wylacznie jako czytelny punkt odniesienia dla typow
// config.ProjectTypeContainer/config.ProjectTypeContainerized uzywanych
// posrednio przez IsContainerBuild()/IsContainerizedIsolator()).
var _ config.ProjectType = config.ProjectTypeContainer
