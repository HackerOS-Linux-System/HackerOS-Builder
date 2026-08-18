package rootfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeMinimalValidRootfs tworzy najmniejszy rootfs ktory przechodzi
// ValidateRootfs (poza rozmiarem -- caller musi dopelnic MinSizeBytes
// odpowiednio niskim, testowy rootfs jest celowo malutki).
func makeMinimalValidRootfs(t *testing.T, pkgCount int) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "etc", "os-release"), "ID=debian\n")
	writeTestFile(t, filepath.Join(root, "usr", "bin", "true"), "binarka")

	var status strings.Builder
	for i := 0; i < pkgCount; i++ {
		status.WriteString("Package: pkg" + string(rune('a'+i)) + "\nStatus: install ok installed\n\n")
	}
	writeTestFile(t, filepath.Join(root, "var", "lib", "dpkg", "status"), status.String())
	return root
}

func TestValidateRootfs_AcceptsMinimalValidTree(t *testing.T) {
	root := makeMinimalValidRootfs(t, 12)
	err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1, MinPackages: 10})
	if err != nil {
		t.Fatalf("ValidateRootfs: %v", err)
	}
}

func TestValidateRootfs_RejectsMissingRequiredPaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "etc", "hostname"), "x\n")
	// Brak usr/bin i var/lib/dpkg/status -- powinno odrzucic.
	err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1, MinPackages: 1})
	if err == nil {
		t.Fatal("oczekiwano bledu dla rootfs bez usr/bin i var/lib/dpkg")
	}
}

func TestValidateRootfs_RejectsTooFewPackages(t *testing.T) {
	root := makeMinimalValidRootfs(t, 2)
	err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1, MinPackages: 10})
	if err == nil {
		t.Fatal("oczekiwano bledu dla zbyt malej liczby pakietow dpkg")
	}
}

func TestValidateRootfs_RejectsTooSmall(t *testing.T) {
	root := makeMinimalValidRootfs(t, 12)
	err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1024 * 1024 * 1024, MinPackages: 10})
	if err == nil {
		t.Fatal("oczekiwano bledu dla rootfs mniejszego niz MinSizeBytes")
	}
}

func TestValidateRootfs_RequireHammerConfig(t *testing.T) {
	root := makeMinimalValidRootfs(t, 12)
	if err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1, MinPackages: 10, RequireHammerConfig: true}); err == nil {
		t.Fatal("oczekiwano bledu -- brak /etc/hammer/oci.hk")
	}

	writeTestFile(t, filepath.Join(root, "etc", "hammer", "oci.hk"), "[origin]\n")
	if err := ValidateRootfs(root, ValidateOptions{MinSizeBytes: 1, MinPackages: 10, RequireHammerConfig: true}); err != nil {
		t.Fatalf("ValidateRootfs po dodaniu oci.hk: %v", err)
	}
}

func TestValidateRootfs_MissingDir(t *testing.T) {
	err := ValidateRootfs(filepath.Join(t.TempDir(), "nie-istnieje"), ValidateOptions{})
	if err == nil {
		t.Fatal("oczekiwano bledu dla nieistniejacego katalogu rootfs")
	}
}

func TestCountDpkgPackages(t *testing.T) {
	root := t.TempDir()
	statusPath := filepath.Join(root, "status")
	content := "Package: a\nStatus: install ok installed\n\nPackage: b\nStatus: install ok installed\n\n"
	if err := os.WriteFile(statusPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	count, err := countDpkgPackages(statusPath)
	if err != nil {
		t.Fatalf("countDpkgPackages: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, chcialem 2", count)
	}
}
