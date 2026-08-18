package buildflow

import (
	"fmt"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/cosign"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hkgen"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/isobuild"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
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
//  2. aktualizuje /etc/hammer/oci.hk wewnatrz rozpakowanego
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

	if cfg.Project.IsContainerBuild() || cfg.Project.IsContainerizedIsolator() {
		extra := ""
		if cfg.Project.IsContainerizedIsolator() {
			extra = " + Isolator wbudowany w /usr/bin/"
		}
		return fmt.Errorf(
			"[project] -> type = %q nie produkuje obrazu ISO -- to jest kontener "+
				"roboczy (podman/docker%s), nie system instalowalny na dysk przez Calamares. "+
				"Uzyj 'hackeros-builder build container' zamiast 'build iso'/'build all'",
			cfg.Project.Type, extra)
	}

	repository := opts.Repository
	tag := opts.Tag
	if repository == "" || tag == "" {
		// resolveImageNameAndTag jest WSPOLNE z BuildCloud -- patrz komentarz
		// przy jej definicji (cloud.go) po szczegoly buga ktory istnial tu
		// wczesniej, gdy ta logika byla zduplikowana i rozjechala sie.
		imageName, resolvedTag := resolveImageNameAndTag(cfg, opts.ProjectDir)
		if repository == "" {
			if cfg.Project.Name != "" {
				util.Infof("Nazwa obrazu OCI z [project] -> name: %s", imageName)
			}
			repository = cfg.ImageRepository(defaultRegistryHost, imageName)
		}
		if tag == "" {
			tag = resolvedTag
			util.Infof("Tag obrazu OCI z [project] -> tag: %s", tag)
		}
	}

	rootfsDir := filepath.Join(opts.WorkDir, "rootfs-from-cloud")

	if cfg.Project.VerifySignature {
		imageRef := fmt.Sprintf("%s:%s", repository, tag)
		if err := cosign.Verify(imageRef, cfg.Project.CosignKey); err != nil {
			return fmt.Errorf("weryfikacja podpisu ([project] -> verify_signature=true): %w", err)
		}
	}

	if err := ociimage.PullAndUnpack(ociimage.PullParams{
		Repository: repository,
		Tag:        tag,
		Token:      cfg.Token,
		DestDir:    rootfsDir,
		Insecure:   opts.InsecureRegistry,
	}); err != nil {
		return fmt.Errorf("sciaganie obrazu z registry: %w", err)
	}

	origin := fmt.Sprintf("docker://%s:%s", repository, tag)
	hammerOciHkPath := filepath.Join(rootfsDir, "etc", "hammer", "oci.hk")

	util.Infof("Aktualizacja [origin] w %s -> %s", hammerOciHkPath, origin)
	if err := hkgen.WriteHammerConfig(hammerOciHkPath, hkgen.HammerConfigParams{
		OSName:        "debian",
		RequireGPG:    true,
		OriginRefspec: origin,
	}); err != nil {
		return fmt.Errorf("aktualizacja /etc/hammer/oci.hk: %w", err)
	}

	volumeName := "HACKEROS"
	isoWorkDir := filepath.Join(opts.WorkDir, "iso-build")

	// SkipInstaller: true jezeli uzytkownik przekazal --no-installer CLI
	// LUB jezeli [project] -> installer = none w config.hk.
	skipInstaller := opts.SkipInstaller
	if cfg != nil && !cfg.Project.UseBuiltinInstaller() {
		skipInstaller = true
		util.Infof("Instalator pominiety ([project] -> installer = none)")
	}

	// Mapowanie config.InstallerType -> isobuild.InstallerVariant. Osobne
	// typy w dwoch pakietach sa celowe -- patrz komentarz przy definicji
	// isobuild.InstallerVariant.
	installerVariant := isobuild.InstallerVariantDefault
	if cfg != nil && cfg.Project.UsesCybersecurityInstaller() {
		installerVariant = isobuild.InstallerVariantCybersecurity
		util.Infof("Instalator: wariant Cybersecurity Edition ([project] -> installer = cybersecurity)")
	}

	var installerHooks []liveparse.HookScript
	// UWAGA -- decyzja projektowa: hooks/installer/ dziala gdy INSTALATOR
	// JEST WLACZONY, czyli [project] -> installer != none (obejmuje zarowno
	// installer=default JAK I installer=cybersecurity). Nie ogranicza tego
	// do installer=default WYLACZNIE -- gdyby tak bylo, projekty z
	// installer=cybersecurity nie mialyby zadnego sposobu na customizacje
	// instalatora przez hooki, co wydaje sie niezamierzonym ograniczeniem.
	// Jesli to zachowanie mialo byc inne (scisle installer=default), zmien
	// warunek ponizej na "cfg.Project.Installer == config.InstallerDefault".
	if !skipInstaller {
		hooks, err := liveparse.ParseInstallerHooks(opts.ProjectDir)
		if err != nil {
			return fmt.Errorf("parsowanie hookow instalatora (config/hooks/installer/): %w", err)
		}
		installerHooks = hooks
		if len(installerHooks) > 0 {
			util.Infof("Hooki instalatora znalezione: %d (config/hooks/installer/)", len(installerHooks))
		}
	}

	if err := isobuild.Build(isobuild.BuildParams{
		RootfsDir:        rootfsDir,
		OutputISO:        opts.OutputISO,
		WorkDir:          isoWorkDir,
		VolumeName:       volumeName,
		SkipInstaller:    skipInstaller,
		InstallerVariant: installerVariant,
		InstallerHooks:   installerHooks,
		Arch:             cfg.EffectiveArch(),
	}); err != nil {
		return fmt.Errorf("budowa ISO: %w", err)
	}

	return nil
}
