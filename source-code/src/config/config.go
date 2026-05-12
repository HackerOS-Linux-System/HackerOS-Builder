package config

import (
	"hackeros-builder/src/hk"
	"hackeros-builder/src/ui"
	"path/filepath"
	"strings"
)

var defaultRepos = []string{"main", "contrib", "non-free", "non-free-firmware"}

var validReleases = map[string][]string{
	"debian": {"bookworm", "trixie", "forky", "sid"},
}

// BuildMode określa tryb budowania
type BuildMode int

const (
	ModeLiveBuild  BuildMode = iota
	ModeStandalone
	ModeContainer
)

type BuildConfig struct {
	raw  *hk.Config
	Mode BuildMode
}

func Load(projectDir string) (*BuildConfig, error) {
	path := filepath.Join(projectDir, "config", "config.hk")
	raw, err := hk.Load(path)
	if err != nil {
		return nil, err
	}
	bc := &BuildConfig{raw: raw}
	bc.detectMode()
	bc.validate()
	return bc, nil
}

func (c *BuildConfig) detectMode() {
	mode := c.raw.GetString("build", "mode", "livebuild")
	switch strings.ToLower(mode) {
	case "standalone":
		c.Mode = ModeStandalone
	case "container":
		c.Mode = ModeContainer
	default:
		c.Mode = ModeLiveBuild
	}
}

func (c *BuildConfig) ModeString() string {
	switch c.Mode {
	case ModeStandalone:
		return "standalone"
	case ModeContainer:
		return "container (bootc-style)"
	default:
		return "livebuild"
	}
}

func (c *BuildConfig) validate() {
	distro := c.Distro()
	release := c.Release()
	valid, ok := validReleases[distro]
	if !ok {
		ui.Warn("Nieznana dystrybucja: " + distro)
		return
	}
	found := false
	for _, r := range valid {
		if r == release {
			found = true
			break
		}
	}
	if !found {
		ui.Warn("Nieznana wersja '" + release + "'. Możliwe: " + strings.Join(valid, ", "))
	}
}

// ── System ────────────────────────────────────────────────────────────────────
func (c *BuildConfig) Distro() string    { return c.raw.GetString("system", "distro", "debian") }
func (c *BuildConfig) Release() string   { return c.raw.GetString("system", "release", "trixie") }
func (c *BuildConfig) Arch() string      { return c.raw.GetString("system", "arch", "amd64") }
func (c *BuildConfig) Mirror() string    { return c.raw.GetString("system", "mirror", "http://deb.debian.org/debian") }
func (c *BuildConfig) Hostname() string  { return c.raw.GetString("system", "hostname", "hackeros") }
func (c *BuildConfig) Desktop() string   { return c.raw.GetString("system", "desktop", "kde") }
func (c *BuildConfig) Kernel() string    { return c.raw.GetString("system", "kernel", "linux-image-amd64") }
func (c *BuildConfig) Locale() string    { return c.raw.GetString("system", "locale", "pl_PL.UTF-8") }
func (c *BuildConfig) Timezone() string  { return c.raw.GetString("system", "timezone", "Europe/Warsaw") }
func (c *BuildConfig) PkgManager() string { return c.raw.GetString("system", "pkg_manager", "apt") }

func (c *BuildConfig) Repos() []string {
	return c.raw.GetStringSlice("system", "repos", defaultRepos)
}

// ── User ──────────────────────────────────────────────────────────────────────
func (c *BuildConfig) Username() string { return c.raw.GetString("user", "username", "user") }
func (c *BuildConfig) Password() string { return c.raw.GetString("user", "password", "live") }
func (c *BuildConfig) AptRecommends() bool {
	return c.raw.GetBool("system", "apt_recommends", true)
}

// ── Build ─────────────────────────────────────────────────────────────────────
func (c *BuildConfig) IsoName() string     { return c.raw.GetString("build", "iso_name", "hackeros-live") }
func (c *BuildConfig) Compression() string { return c.raw.GetString("build", "compression", "xz") }

// ── Pakiety ───────────────────────────────────────────────────────────────────
func (c *BuildConfig) Packages() []string {
	sec := c.raw.Section("packages")
	if sec.IsZero() {
		return nil
	}
	var pkgs []string
	seen := map[string]bool{}
	for _, key := range sec.Order {
		val, ok := sec.Get(key)
		if !ok {
			continue
		}
		for _, p := range val.AsStringSlice() {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				pkgs = append(pkgs, p)
				seen[p] = true
			}
		}
	}
	return pkgs
}

// ── Container ─────────────────────────────────────────────────────────────────

func (c *BuildConfig) ContainerfilePath() string {
	return c.raw.GetString("container", "containerfile", "config/container/Containerfile")
}

func (c *BuildConfig) ContainerBaseImage() string {
	release := c.Release()
	switch release {
	case "trixie", "bookworm":
		return c.raw.GetString("container", "base_image", "debian:"+release)
	case "forky", "sid":
		return c.raw.GetString("container", "base_image", "debian:"+release)
	default:
		return c.raw.GetString("container", "base_image", "debian:trixie")
	}
}

func (c *BuildConfig) ContainerBuildArgs() map[string]string {
	sec := c.raw.Section("container")
	args := map[string]string{}
	val, ok := sec.Get("build_args")
	if !ok {
		return args
	}
	// build_args to mapa key => value
	for _, key := range val.Order {
		v, _ := val.Get(key)
		args[key] = v.AsString()
	}
	return args
}

func (c *BuildConfig) ContainerRegistry() string {
	return c.raw.GetString("container", "registry", "")
}

// ── live-build opcje ──────────────────────────────────────────────────────────
func (c *BuildConfig) LBBootappend() string {
	def := "boot=live components quiet splash username=" + c.Username()
	return c.raw.GetString("livebuild", "bootappend", def)
}

func (c *BuildConfig) LBDebian_installer() string {
	return c.raw.GetString("livebuild", "debian_installer", "none")
}

func (c *BuildConfig) LBSecureboot() string {
	return c.raw.GetString("livebuild", "secure_boot", "auto")
}
