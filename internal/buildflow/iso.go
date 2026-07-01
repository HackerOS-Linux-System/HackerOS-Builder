package buildflow

import (
	"fmt"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hkgen"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/isobuild"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/ociimage"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/preflight"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// IsoOptions to parametry komendy "build iso".
type IsoOptions struct {
	ProjectDir string
	WorkDir    string
	OutputISO  string

	// Repository/Tag: jesli oba sa puste, BuildIso wyliczy je tak samo jak
	// BuildCloud (na podstawie config.hk + nazwy katalogu projektu) --
	// pozwala to uzyc samego "build iso" gdy obraz juz istnieje w registry
	// z wczesniejszego "build cloud", bez potrzeby podawania repo/tag recznie.
	Repository string
	Tag        string

	InsecureRegistry bool
	SkipPreflight    bool
	SkipInstaller    bool
}

// BuildIso wykonuje pelny przeplyw "hackeros-builder build iso":
//
//  0. preflight.CheckIso() -- weryfikuje mksquashfs/grub-mkrescue/xorriso
//  1. sciaga obraz OCI z registry (ociimage.PullAndUnpack)
//  2. aktualizuje /etc/deb-ostree/deb-ostree.hk wewnatrz rozpakowanego
//     rootfs, wpisujac poprawny [origin] -> refspec
//  3. buduje hybrydowe ISO (BIOS+UEFI) z tego rootfs przez isobuild
//
// Uwaga: BuildIso NIE uzywa buildlock samodzielnie gdy jest wywolywane jako
// drugi krok BuildAll -- blokada jest juz przytrzymana przez BuildCloud
// (ten sam workDir). Gdy "build iso" jest wywolywane samodzielnie z CLI,
// main.go przytrzymuje blokade przed wywolaniem.
func BuildIso(opts IsoOptions) error {
	if !opts.SkipPreflight {
		if err := preflight.CheckIso(); err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}

	cfg, err := loadAndValidateConfig(opts.ProjectDir)
	if err != nil {
		return err
	}

	repository := opts.Repository
	tag := opts.Tag
	if repository == "" {
		imageName := defaultImageName(opts.ProjectDir)
		repository = cfg.ImageRepository(defaultRegistryHost, imageName)
	}
	if tag == "" {
		tag = cfg.Release
	}

	rootfsDir := filepath.Join(opts.WorkDir, "rootfs-from-cloud")

	if err := ociimage.PullAndUnpack(ociimage.PullParams{
		Repository: repository,
		Tag:        tag,
		Token:      cfg.Token,
		DestDir:    rootfsDir,
		Insecure:   opts.InsecureRegistry,
	}); err != nil {
		return fmt.Errorf("sciaganie obrazu z registry: %w", err)
	}

	origin := fmt.Sprintf("deb-ostree-oci:%s:%s", repository, tag)
	debOstreeHkPath := filepath.Join(rootfsDir, "etc", "deb-ostree", "deb-ostree.hk")

	util.Infof("Aktualizacja [origin] w %s -> %s", debOstreeHkPath, origin)
	if err := hkgen.WriteDebOstreeConfig(debOstreeHkPath, hkgen.DebOstreeConfigParams{
		OSName:        "debian",
		OriginRefspec: origin,
	}); err != nil {
		return fmt.Errorf("aktualizacja deb-ostree.hk: %w", err)
	}

	volumeName := "HACKEROS"
	isoWorkDir := filepath.Join(opts.WorkDir, "iso-build")

	if err := isobuild.Build(isobuild.BuildParams{
		RootfsDir:     rootfsDir,
		OutputISO:     opts.OutputISO,
		WorkDir:       isoWorkDir,
		VolumeName:    volumeName,
		SkipInstaller: opts.SkipInstaller,
	}); err != nil {
		return fmt.Errorf("budowa ISO: %w", err)
	}

	return nil
}
