package buildflow

import (
	"fmt"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/buildlock"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/preflight"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// AllOptions to parametry komendy "build all" -- suma parametrow BuildCloud
// i BuildIso (ProjectDir/WorkDir sa wspoldzielone).
type AllOptions struct {
	ProjectDir       string
	WorkDir          string
	OutputISO        string
	InsecureRegistry bool
	SkipInstaller    bool
}

// BuildAll wykonuje preflight.CheckAll() RAZ na samym starcie (zamiast
// dwukrotnie -- raz w BuildCloud, raz w BuildIso), zeby brak np.
// grub-mkrescue zostal wykryty PRZED kosztownym etapem cloud, nie po nim.
//
// buildlock.Acquire jest rowniez wziety RAZ na caly przeplyw cloud+iso --
// BuildCloud i BuildIso dostaja SkipPreflight/SkipLock=true, zeby nie
// probowaly ponownie blokowac tego samego workDir (flock(2) nie jest
// reentrant w ramach tego samego procesu na dwoch deskryptorach pliku
// wskazujacych ta sama sciezke -- druga proba zwrocilaby blad "zajete").
func BuildAll(opts AllOptions) error {
	if err := preflight.CheckAll(); err != nil {
		return fmt.Errorf("preflight: %w", err)
	}

	lock, err := buildlock.Acquire(opts.WorkDir)
	if err != nil {
		return err
	}
	defer lock.Release()

	util.Infof("=== build all: krok 1/2 -- build cloud ===")
	cloudResult, err := BuildCloud(CloudOptions{
		ProjectDir:       opts.ProjectDir,
		WorkDir:          opts.WorkDir,
		InsecureRegistry: opts.InsecureRegistry,
		SkipPreflight:    true,
		SkipLock:         true,
	})
	if err != nil {
		return err
	}

	util.Infof("=== build all: krok 2/2 -- build iso ===")
	return BuildIso(IsoOptions{
		ProjectDir:       opts.ProjectDir,
		WorkDir:          opts.WorkDir,
		OutputISO:        opts.OutputISO,
		Repository:       cloudResult.Repository,
		Tag:              cloudResult.Tag,
		InsecureRegistry: opts.InsecureRegistry,
		SkipPreflight:    true,
		SkipInstaller:    opts.SkipInstaller,
	})
}
