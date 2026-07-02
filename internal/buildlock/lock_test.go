package buildlock

import (
	"os"
	"testing"
)

func TestAcquire_SucceedsOnFreshDir(t *testing.T) {
	dir := t.TempDir()
	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire na swiezym katalogu zwrocilo blad: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release zwrocilo blad: %v", err)
	}
}

func TestAcquire_FailsWhenAlreadyLocked(t *testing.T) {
	dir := t.TempDir()

	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("pierwsze Acquire zwrocilo blad: %v", err)
	}
	defer first.Release()

	_, err = Acquire(dir)
	if err == nil {
		t.Fatal("oczekiwano bledu przy drugim Acquire na tym samym workDir")
	}
}

func TestAcquire_SucceedsAfterRelease(t *testing.T) {
	dir := t.TempDir()

	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("pierwsze Acquire zwrocilo blad: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release zwrocilo blad: %v", err)
	}

	second, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire po Release powinno sie powiesc, otrzymano: %v", err)
	}
	defer second.Release()
}

func TestAcquire_CreatesWorkDirIfMissing(t *testing.T) {
	dir := t.TempDir() + "/nieistniejacy-podkatalog"

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire powinno utworzyc katalog, otrzymano blad: %v", err)
	}
	defer lock.Release()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("katalog %s powinien istniec po Acquire: %v", dir, err)
	}
}

func TestRelease_NilLockIsNoOp(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("Release na nil Lock powinno byc no-op, otrzymano: %v", err)
	}
}
