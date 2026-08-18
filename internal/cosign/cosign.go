package cosign

import (
	"fmt"
	"os/exec"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// checkAvailable zwraca czytelny blad jesli binarka "cosign" nie jest
// dostepna w $PATH -- wywolywane PRZED probą Sign/Verify, zeby blad
// od razu wskazywal na brakujace narzedzie (nie na dziwny "exec: cosign:
// nie znaleziono w $PATH" z glebi os/exec).
func checkAvailable() error {
	if _, err := exec.LookPath("cosign"); err != nil {
		return fmt.Errorf(
			"narzedzie 'cosign' nie zostalo znalezione w $PATH -- wymagane przez "+
				"[project] -> sign/verify_signature w config.hk. Zainstaluj cosign: "+
				"https://docs.sigstore.dev/cosign/system_config/installation/ (%w)", err)
	}
	return nil
}

// Sign podpisuje juz wypchniety obraz OCI (imageRef, np.
// "ghcr.io/michal/hackeros-debian:trixie") kluczem prywatnym pod keyPath,
// przez "cosign sign --key <keyPath> --yes <imageRef>". --yes pomija
// interaktywne pytanie potwierdzajace (cosign domyslnie pyta "Are you
// sure you would like to continue?" przy podpisywaniu bez TTY -- w
// nienadzorowanym buildzie nie ma nikogo kto móglby na to odpowiedziec).
//
// Jesli klucz jest zaszyfrowany haslem (typowe dla "cosign generate-key-pair"),
// cosign oczekuje go w zmiennej srodowiskowej COSIGN_PASSWORD -- to
// odpowiedzialnosc wywolujacego (np. eksport w CI z sekretu), nie tego
// pakietu.
func Sign(imageRef, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf(
			"[project] -> sign = true, ale [project] -> cosign_key jest puste -- " +
				"podaj sciezke do klucza prywatnego cosign (wygenerowanego przez " +
				"'cosign generate-key-pair')")
	}
	if err := checkAvailable(); err != nil {
		return err
	}

	util.Infof("Podpisywanie obrazu OCI (cosign): %s", imageRef)
	if err := util.RunStreaming("", "cosign", "sign", "--key", keyPath, "--yes", imageRef); err != nil {
		return fmt.Errorf("cosign sign %s: %w", imageRef, err)
	}
	util.Infof("Obraz podpisany: %s", imageRef)
	return nil
}

// Verify weryfikuje podpis obrazu OCI kluczem publicznym pod keyPath, przez
// "cosign verify --key <keyPath> <imageRef>". Zwraca blad jesli podpis jest
// nieprawidlowy, brakujacy, albo pochodzi z innego klucza -- wywolujacy
// (internal/buildflow.BuildIso) PRZERYWA build iso w takim wypadku, PRZED
// pociagnieciem/uzyciem obrazu.
func Verify(imageRef, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf(
			"[project] -> verify_signature = true, ale [project] -> cosign_key jest puste -- " +
				"podaj sciezke do klucza publicznego cosign odpowiadajacego kluczowi " +
				"ktorym obraz zostal podpisany")
	}
	if err := checkAvailable(); err != nil {
		return err
	}

	util.Infof("Weryfikacja podpisu OCI (cosign): %s", imageRef)
	if err := util.RunStreaming("", "cosign", "verify", "--key", keyPath, imageRef); err != nil {
		return fmt.Errorf(
			"weryfikacja podpisu %s nie powiodla sie: %w -- obraz jest NIEPODPISANY, "+
				"podpisany INNYM kluczem, albo zostal zmodyfikowany po podpisaniu; "+
				"build iso PRZERWANY, nie uzywamy tego obrazu", imageRef, err)
	}
	util.Infof("Podpis zweryfikowany poprawnie: %s", imageRef)
	return nil
}
