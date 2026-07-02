package util

import (
	"fmt"
	"os"
)

var verbose = false

// SetVerbose wlacza/wylacza wyswietlanie logow DEBUG.
func SetVerbose(v bool) { verbose = v }

// isTerminal wykrywa czy stdout jest faktycznie terminalem (nie pipe/plik),
// uzywajac os.ModeCharDevice -- to dziala bez dodatkowych zaleznosci (np.
// golang.org/x/term), ktore wymagalyby ponownego "go mod tidy" z dostepem
// do sieci. Kolory ANSI sa wlaczane tylko gdy to jest prawda, zeby
// przekierowane logi (np. "hackeros-builder build cloud > log.txt") nie
// byly zasmiecone kodami escape.
var isTerminal = detectTerminal()

func detectTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Kolory ANSI eksportowane dla main.go (printUsage, komunikaty sukcesu/bledu) --
// ta sama paleta co logi poziomowe nizej, zeby caly CLI mial jednolity styl.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	ColorCyan   = "\033[36m"
	ColorYellow = "\033[33m"
	ColorRed    = "\033[1;31m"
	ColorGreen  = "\033[1;32m"
)

// Colorize otacza tekst kodem ANSI, ale tylko jesli stdout jest terminalem --
// w przeciwnym razie zwraca tekst bez zmian (bezpieczne dla pipe/CI/redirect).
func Colorize(colorCode, text string) string {
	if !isTerminal {
		return text
	}
	return colorCode + text + colorReset
}

// Bold pogrubia tekst (tylko na terminalu) -- uzywane w printUsage dla
// naglowkow sekcji ("Komendy:", "Opcje globalne:").
func Bold(text string) string {
	return Colorize(colorBold, text)
}

func colorPrefix(code string) string {
	if !isTerminal {
		return ""
	}
	return code
}

func resetSuffix() string {
	if !isTerminal {
		return ""
	}
	return colorReset
}

func Debugf(format string, args ...any) {
	if !verbose {
		return
	}
	fmt.Fprintf(os.Stdout, colorPrefix(colorDim)+"[DEBUG] "+format+resetSuffix()+"\n", args...)
}

func Infof(format string, args ...any) {
	fmt.Fprintf(os.Stdout, colorPrefix(ColorCyan)+"[INFO ]"+resetSuffix()+" "+format+"\n", args...)
}

func Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorPrefix(ColorYellow)+"[WARN ]"+resetSuffix()+" "+format+"\n", args...)
}

func Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorPrefix(ColorRed)+"[ERROR]"+resetSuffix()+" "+format+"\n", args...)
}
