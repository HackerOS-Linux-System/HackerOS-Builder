package rootfs

import (
	"testing"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
)

func TestPackagesWithLiveBoot(t *testing.T) {
	b := &Builder{Project: &liveparse.Project{Packages: []string{"vim"}}}
	got := b.packagesWithLiveBoot()
	want := map[string]bool{"vim": true, "live-boot": true, "live-boot-initramfs-tools": true}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("niespodziewany pakiet %q w %v", p, got)
		}
	}

	// ContainerMode: bez zmian
	b = &Builder{ContainerMode: true, Project: &liveparse.Project{Packages: []string{"vim"}}}
	if got := b.packagesWithLiveBoot(); len(got) != 1 {
		t.Fatalf("kontener nie powinien dostac live-boot: %v", got)
	}

	// swiadome wykluczenie
	b = &Builder{Project: &liveparse.Project{
		Packages:         []string{"vim"},
		ExcludedPackages: map[string]bool{"live-boot": true, "live-boot-initramfs-tools": true},
	}}
	if got := b.packagesWithLiveBoot(); len(got) != 1 {
		t.Fatalf("wykluczone pakiety nie powinny byc dopisane: %v", got)
	}

	// juz obecne -- bez duplikatow
	b = &Builder{Project: &liveparse.Project{Packages: []string{"live-boot", "live-boot-initramfs-tools"}}}
	if got := b.packagesWithLiveBoot(); len(got) != 2 {
		t.Fatalf("duplikaty: %v", got)
	}
}
