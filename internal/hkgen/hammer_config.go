package hkgen

import (
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hk"
)

// DebOstreeConfigParams to dane potrzebne do wygenerowania deb-ostree.hk.
// Pola odpowiadaja 1:1 polom wczytywanym przez deb-ostree z jego wlasnego
// pliku konfiguracyjnego (cmd/types.h -> struct Config w repo deb-ostree).
type DebOstreeConfigParams struct {
	SysrootPath    string
	OstreeRepoPath string
	OSName         string
	OverlayWorkDir string
	AptListsPath   string

	// OriginRefspec to refspec obrazu OCI ktory deb-ostree powinien uznac za
	// "origin" tego deploymentu, np. "deb-ostree-oci:ghcr.io/michal/hackeros:trixie".
	// hackeros-builder wypelnia to automatycznie na podstawie obrazu ktory
	// wlasnie zbudowal i wypchnal w komendzie "build cloud".
	OriginRefspec string
}

// GenerateDebOstreeConfig buduje HkConfig odpowiadajacy plikowi
// /etc/deb-ostree/deb-ostree.hk, gotowy do zapisania przez hk.WriteFile.
//
// Struktura sekcji:
//
//	[sysroot]
//	-> path => /
//
//	[ostree]
//	-> repo_path => /ostree/repo
//
//	[system]
//	-> osname => debian
//
//	[overlay]
//	-> work_dir => /var/lib/deb-ostree/overlay-work
//
//	[apt]
//	-> lists_path => /var/lib/deb-ostree/apt-cache
//
//	[origin]
//	-> refspec => deb-ostree-oci:ghcr.io/michal/hackeros:trixie
func GenerateDebOstreeConfig(p DebOstreeConfigParams) *hk.HkConfig {
	b := hk.NewBuilder()

	b.Section("sysroot").Set("path", hk.String(orDefault(p.SysrootPath, "/")))

	b.Section("ostree").Set("repo_path",
		hk.String(orDefault(p.OstreeRepoPath, "/ostree/repo")))

	b.Section("system").Set("osname", hk.String(orDefault(p.OSName, "debian")))

	b.Section("overlay").Set("work_dir",
		hk.String(orDefault(p.OverlayWorkDir, "/var/lib/deb-ostree/overlay-work")))

	b.Section("apt").Set("lists_path",
		hk.String(orDefault(p.AptListsPath, "/var/lib/deb-ostree/apt-cache")))

	if p.OriginRefspec != "" {
		b.Section("origin").Set("refspec", hk.String(p.OriginRefspec))
	}

	return b.Build()
}

// WriteDebOstreeConfig generuje config i zapisuje go bezposrednio do destPath
// (typowo "<rootfs>/etc/deb-ostree/deb-ostree.hk" podczas budowy obrazu).
func WriteDebOstreeConfig(destPath string, p DebOstreeConfigParams) error {
	cfg := GenerateDebOstreeConfig(p)
	return hk.WriteFile(destPath, cfg)
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
