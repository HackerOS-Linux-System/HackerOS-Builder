package buildlock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockFileName = ".hackeros-builder.lock"

// Lock reprezentuje przytrzymana blokade na danym workDir. Wywolaj Release()
// (najlepiej przez defer) gdy build sie zakonczy, by zwolnic blokade dla
// kolejnych uruchomien -- choc flock(2) i tak zwalnia automatycznie gdy
// proces (lub jego deskryptor pliku) sie zakonczy.
type Lock struct {
	file *os.File
	path string
}

// Acquire tworzy (jesli nie istnieje) katalog workDir i probuje uzyskac
// wylaczna blokade flock na pliku workDir/.hackeros-builder.lock.
//
// Jesli blokada jest juz przytrzymana przez inny proces, Acquire zwraca
// blad NATYCHMIAST (nie czeka/nie blokuje) -- celem jest szybki, czytelny
// komunikat "ktos juz buduje w tym katalogu", nie zawieszenie się w
// nieskonczonosc czekajac na zwolnienie.
func Acquire(workDir string) (*Lock, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("buildlock: nie mozna utworzyc %s: %w", workDir, err)
	}

	lockPath := filepath.Join(workDir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("buildlock: nie mozna otworzyc %s: %w", lockPath, err)
	}

	// LOCK_EX (wylaczna) | LOCK_NB (nieblokujaca) -- zwraca blad natychmiast
	// jesli ktos inny juz trzyma blokade, zamiast czekac w nieskonczonosc.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf(
			"buildlock: katalog roboczy %q jest juz uzywany przez inny build "+
				"hackeros-builder (nie mozna uzyskac blokady %s: %v). "+
				"Uzyj innego --workdir dla rownoleglych buildow, albo zaczekaj "+
				"na zakonczenie poprzedniego",
			workDir, lockPath, err)
	}

	// Zapisujemy PID do pliku -- czysto informacyjnie (np. do debugowania
	// "kto trzyma ta blokade"), flock nie wymaga tej zawartosci do dzialania.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)

	return &Lock{file: f, path: lockPath}, nil
}

// Release zwalnia blokade i zamyka plik. Bezpieczne do wielokrotnego
// wywolania (kolejne wywolania po pierwszym sa no-op).
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("buildlock: zwolnienie blokady %s: %w", l.path, err)
	}
	return closeErr
}
