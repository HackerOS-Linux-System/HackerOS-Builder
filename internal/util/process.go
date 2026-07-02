package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// RunResult to wynik wykonania komendy zewnetrznej.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Ok zwraca true gdy komenda zakonczyla sie kodem 0.
func (r RunResult) Ok() bool { return r.ExitCode == 0 }

// Run wykonuje komende synchronicznie, przechwytujac stdout/stderr.
// Nie przechodzi przez powloke -- bezpieczne wzgledem injection przy
// argumentach zawierajacych spacje/znaki specjalne (sciezki, nazwy pakietow).
func Run(name string, args ...string) (RunResult, error) {
	return RunInDir("", name, args...)
}

// RunInDir jak Run, ale ustawia katalog roboczy (dir="" = katalog biezacy
// procesu hackeros-builder).
func RunInDir(dir string, name string, args ...string) (RunResult, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	Debugf("exec: %s %v", name, args)

	err := cmd.Run()
	res := RunResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil // exit != 0 nie jest bledem Go -- caller sprawdza Ok()
	}
	if err != nil {
		// Blad startu procesu (np. binarka nie istnieje) -- to JEST blad Go.
		return res, fmt.Errorf("nie mozna uruchomic %q: %w", name, err)
	}

	res.ExitCode = 0
	return res, nil
}

// RunOrError jak Run, ale zwraca blad Go (zamiast samego RunResult) gdy
// exit code != 0 -- wygodne tam, gdzie niepowodzenie powinno natychmiast
// przerwac cala operacje budowania (np. debootstrap, mount).
func RunOrError(name string, args ...string) (RunResult, error) {
	res, err := Run(name, args...)
	if err != nil {
		return res, err
	}
	if !res.Ok() {
		return res, fmt.Errorf(
			"komenda %q %v zakonczona kodem %d\nstderr: %s",
			name, args, res.ExitCode, res.Stderr)
	}
	return res, nil
}

// RunStreaming wykonuje komende przekazujac stdout/stderr bezposrednio do
// terminala uzytkownika (bez buforowania) -- uzywane dla dlugotrwalych
// operacji typu debootstrap/xorriso, gdzie uzytkownik chce widziec progres
// na zywo, nie po fakcie.
func RunStreaming(dir string, name string, args ...string) error {
	return RunStreamingEnv(dir, nil, name, args...)
}

// RunStreamingEnv jak RunStreaming, ale pozwala dodac/nadpisac zmienne
// srodowiskowe procesu potomnego (np. DEBIAN_FRONTEND=noninteractive dla
// apt-get w chroot) -- env to lista "KLUCZ=WARTOSC" DOPISYWANA do biezacego
// srodowiska procesu hackeros-builder (nie zastepuje go), wiec PATH i reszta
// pozostaja nienaruszone.
func RunStreamingEnv(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	Debugf("exec (streaming): %s %v env=%v", name, args, env)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("komenda %q %v nie powiodla sie: %w", name, args, err)
	}
	return nil
}

// RunWithStdin wykonuje komende synchronicznie, podajac stdinData na stdin
// procesu i przechwytujac stdout/stderr -- uzywane np. dla pomocniczych
// komend hosta ktore czytaja dane z wejscia standardowego.
func RunWithStdin(dir string, stdinData []byte, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = bytes.NewReader(stdinData)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	Debugf("exec (stdin): %s %v", name, args)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("komenda %q %v nie powiodla sie: %w\nstderr: %s", name, args, err, stderr.String())
	}
	return nil
}
