package hkgen

import (
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hk"
)

// HammerConfigParams to dane potrzebne do wygenerowania /etc/hammer/oci.hk.
//
// Nazwy pol odpowiadaja kluczom odczytanym bezposrednio z binarki hammer
// (analiza `strings`/`readelf` wydania oci-mode v0.6.0 -- hammer nie
// publikuje osobnej dokumentacji schematu configu w chwili pisania tego
// kodu): "refspec", "lists_paths", "sources_list", "sources_dir",
// "keyring_dir", "require_gpg", "osname", "repo_path". Grupowanie w sekcje
// ponizej jest odwzorowaniem analogicznym do /etc/deb-ostree/deb-ostree.hk
// (ktory hammer zastepuje) -- jesli faktyczny parser hammer (podzbior
// internal/hk, jak deb-ostree.hk mial swoj w C++) oczekuje innego
// grupowania sekcji, dostosuj SectionBuilder ponizej; klucze same w sobie
// sa zweryfikowane wprost z binarki.
type HammerConfigParams struct {
	SysrootPath    string
	OstreeRepoPath string
	OSName         string
	ListsPath      string
	SourcesList    string
	SourcesDir     string
	KeyringDir     string
	RequireGPG     bool

	// OriginRefspec to refspec obrazu OCI ktory hammer powinien uznac za
	// "origin" tego deploymentu. hackeros-builder wypelnia to automatycznie
	// na podstawie obrazu ktory wlasnie zbudowal i wypchnal w komendzie
	// "build cloud" -- format: "docker://<repository>:<tag>", zgodnie z
	// prefiksem transportu widocznym w binarce hammer (docker://, oci:,
	// containers-storage:), analogicznym do transportow uzywanych przez
	// skopeo/podman i natywne wsparcie libostree dla kontenerow OCI.
	OriginRefspec string
}

// GenerateHammerConfig buduje HkConfig odpowiadajacy plikowi
// /etc/hammer/oci.hk, gotowy do zapisania przez hk.WriteFile.
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
//	[apt]
//	-> lists_paths => /var/lib/hammer/lists
//
//	[sources]
//	-> sources_list => /etc/hammer/sources-list.hk
//	-> sources_dir  => /etc/hammer/sources-list.d
//	-> keyring_dir  => /etc/hammer/trusted.gpg.d
//	-> require_gpg  => true
//
//	[origin]
//	-> refspec => docker://ghcr.io/michal/hackeros:trixie
func GenerateHammerConfig(p HammerConfigParams) *hk.HkConfig {
	b := hk.NewBuilder()

	b.Section("sysroot").Set("path", hk.String(orDefault(p.SysrootPath, "/")))

	b.Section("ostree").Set("repo_path",
		hk.String(orDefault(p.OstreeRepoPath, "/ostree/repo")))

	b.Section("system").Set("osname", hk.String(orDefault(p.OSName, "debian")))

	b.Section("apt").Set("lists_paths",
		hk.String(orDefault(p.ListsPath, "/var/lib/hammer/lists")))

	sources := b.Section("sources")
	sources.Set("sources_list", hk.String(orDefault(p.SourcesList, "/etc/hammer/sources-list.hk")))
	sources.Set("sources_dir", hk.String(orDefault(p.SourcesDir, "/etc/hammer/sources-list.d")))
	sources.Set("keyring_dir", hk.String(orDefault(p.KeyringDir, "/etc/hammer/trusted.gpg.d")))
	sources.Set("require_gpg", hk.Bool(p.RequireGPG))

	if p.OriginRefspec != "" {
		b.Section("origin").Set("refspec", hk.String(p.OriginRefspec))
	}

	return b.Build()
}

// WriteHammerConfig generuje config i zapisuje go bezposrednio do destPath
// (typowo "<rootfs>/etc/hammer/oci.hk" podczas budowy obrazu).
func WriteHammerConfig(destPath string, p HammerConfigParams) error {
	cfg := GenerateHammerConfig(p)
	return hk.WriteFile(destPath, cfg)
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
