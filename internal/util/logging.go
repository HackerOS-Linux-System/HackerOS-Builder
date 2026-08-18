package util

import (
	"fmt"
	"os"
	"strings"
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
	colorReset     = "\033[0m"
	colorBold      = "\033[1m"
	colorDim       = "\033[2m"
	colorItalic    = "\033[3m"
	colorUnderline = "\033[4m"
	ColorCyan      = "\033[36m"
	ColorYellow    = "\033[33m"
	ColorRed       = "\033[1;31m"
	ColorGreen     = "\033[1;32m"
	ColorMagenta   = "\033[35m"
	ColorBlue      = "\033[34m"
	ColorBoldCyan  = "\033[1;36m"
	ColorBoldWhite = "\033[1;37m"
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

// Dim przygasza tekst (tylko na terminalu) -- uzywane dla informacji
// drugorzednych (sciezki, timery, szczegoly techniczne w nawiasach).
func Dim(text string) string {
	return Colorize(colorDim, text)
}

// Underline podkresla tekst (tylko na terminalu) -- uzywane dla nazw
// plikow/URL-i w komunikatach koncowych ("gotowy plik: ...").
func Underline(text string) string {
	return Colorize(colorUnderline, text)
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
	fmt.Fprintf(os.Stdout, colorPrefix(colorDim)+"  · "+format+resetSuffix()+"\n", args...)
}

func Infof(format string, args ...any) {
	fmt.Fprintf(os.Stdout, colorPrefix(ColorBoldCyan)+"  ➜ "+resetSuffix()+" "+format+"\n", args...)
}

func Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorPrefix(ColorYellow)+"  ⚠ OSTRZEZENIE:"+resetSuffix()+" "+format+"\n", args...)
}

func Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorPrefix(ColorRed)+"  ✗ BLAD:"+resetSuffix()+" "+format+"\n", args...)
}

// Section wypisuje pogrubiony naglowek etapu budowy z kolorowym dzielnikiem
// na cala szerokosc -- uzywane przy przejsciu miedzy duzymi etapami
// ("build cloud" -> "build iso" w "build all", albo poczatek kazdego
// glownego kroku rootfs/isobuild) zeby dlugi log dalo sie skanowac wzrokiem.
func Section(title string) {
	line := strings.Repeat("─", 62)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, Colorize(ColorBoldCyan, "┌"+line))
	fmt.Fprintf(os.Stdout, "%s %s\n", Colorize(ColorBoldCyan, "│"), Colorize(colorBold, title))
	fmt.Fprintln(os.Stdout, Colorize(ColorBoldCyan, "└"+line))
}

// Step wypisuje pojedynczy ponumerowany krok w obrebie etapu ("Krok 3/10:
// ..."), pogrubiony i kolorowy, tak zeby numeracja od razu rzucala sie w
// oczy przy skanowaniu dlugiego logu budowy. Zastepuje surowe
// Infof("Krok %d/%d: ...") w internal/rootfs i internal/isobuild.
func Step(current, total int, format string, args ...any) {
	prefix := fmt.Sprintf("[%d/%d]", current, total)
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stdout, "%s %s %s\n",
		Colorize(ColorBoldWhite, prefix),
		Colorize(ColorBoldCyan, "➜"),
		Colorize(colorBold, msg))
}

// SubStep wypisuje linie podrzedna wzgledem ostatniego Step (np. pojedynczy
// hook/pakiet wewnatrz jednego kroku) -- wciecie + delikatniejszy kolor,
// zeby hierarchia byla widoczna na pierwszy rzut oka.
func SubStep(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "      %s %s\n", Colorize(ColorCyan, "·"), fmt.Sprintf(format, args...))
}

// PrintErrorBox wyswietla blad w wyraznej, czerwonej ramce -- uzywane przez
// main.go (fail()) jako ostatnia linia obrony przed wyjsciem z bledem,
// zeby najwazniejszy komunikat (przyczyna niepowodzenia calego builda) nie
// zgubil sie w dziesiatkach wczesniejszych linii [INFO]/[DEBUG].
func PrintErrorBox(title string, detail string) {
	width := 78
	line := strings.Repeat("═", width)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, Colorize(ColorRed, "╔"+line+"╗"))
	titleText := "BUDOWA PRZERWANA: " + title
	fmt.Fprintf(os.Stderr, "%s %s%s%s\n",
		Colorize(ColorRed, "║"),
		Colorize(ColorRed, Bold(titleText)),
		strings.Repeat(" ", maxInt(0, width-2-len(titleText))),
		Colorize(ColorRed, "║"))
	fmt.Fprintln(os.Stderr, Colorize(ColorRed, "╠"+line+"╣"))
	for _, wrapped := range wrapText(detail, width-2) {
		fmt.Fprintf(os.Stderr, "%s %-*s%s\n", Colorize(ColorRed, "║"), width-1, wrapped, Colorize(ColorRed, "║"))
	}
	fmt.Fprintln(os.Stderr, Colorize(ColorRed, "╚"+line+"╝"))
	fmt.Fprintln(os.Stderr)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrapText dzieli tekst na linie o maksymalnej dlugosci width, lamiac po
// spacjach (nie w srodku slowa) -- uzywane WYLACZNIE do formatowania ramki
// PrintErrorBox, nie ma ambicji byc ogolnym algorytmem zawijania tekstu
// (np. nie obsluguje wielobajtowych znakow specjalnie -- wystarczajace dla
// komunikatow bledow w jezyku polskim/angielskim uzywanych w tym CLI).
func wrapText(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := words[0]
		for _, w := range words[1:] {
			if len(cur)+1+len(w) > width {
				lines = append(lines, cur)
				cur = w
				continue
			}
			cur += " " + w
		}
		lines = append(lines, cur)
	}
	return lines
}
