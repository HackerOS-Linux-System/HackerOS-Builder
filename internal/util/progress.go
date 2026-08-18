package util

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ProgressBar to pasek postepu ANSI aktualizowany w miejscu (przez "\r"),
// pokazujacy REALNY postep -- current/total podawane przez wywolujacego na
// podstawie faktycznie wykonanej pracy (bajty zapisane/wyslane, pakiety
// rozpakowane, hooki wykonane), NIGDY animacja "na oko" bez pokrycia w
// rzeczywistosci. Bezpieczny na non-terminal (pipe/plik/CI): wtedy w ogole
// nie rysuje paska w trakcie (isTerminal z logging.go), tylko jedno zwiezle
// podsumowanie przy Finish/Fail -- zeby przekierowane logi nie zamienily sie
// w tysiace linii "\r".
type ProgressBar struct {
	mu sync.Mutex

	label string
	total int64  // <=0 = total nieznany z gory -- pasek pokazuje sam licznik, bez %
	unit  string // np. "MB", "pakietow", "hookow" -- dopisywane przy total==0

	current    int64
	startedAt  time.Time
	lastRender time.Time
	lastLine   int // dlugosc ostatnio wypisanej linii, do czyszczenia przy nastepnym renderze
	done       bool
}

// minRenderInterval ogranicza czestotliwosc przerysowania -- bez tego pasek
// aktualizowany w petli czytajacej bajt-po-bajcie potrafilby wypisywac
// dziesiatki tysiecy linii "\r" na sekunde, obciazajac terminal bez zadnej
// dodatkowej czytelnosci dla czlowieka.
const minRenderInterval = 80 * time.Millisecond

const progressBarWidth = 30

// NewProgressBar tworzy pasek z podanym labelem i calkowita "praca do
// zrobienia" (total). total<=0 oznacza "total nieznany" (np. debootstrap
// zanim uda sie policzyc pakiety z --print-debs) -- pasek wtedy pokazuje
// tylko rosnacy licznik z jednostka unit, bez procentow/wypelnienia.
func NewProgressBar(label string, total int64, unit string) *ProgressBar {
	pb := &ProgressBar{
		label:     label,
		total:     total,
		unit:      unit,
		startedAt: time.Now(),
	}
	pb.render(true)
	return pb
}

// Set ustawia BEZWZGLEDNA wartosc aktualnego postepu (nie delte) --
// wygodne gdy zrodlem prawdy jest np. "bajtow juz zapisanych do tej pory"
// zamiast przyrostu od ostatniego wywolania.
func (pb *ProgressBar) Set(current int64) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	if pb.done {
		return
	}
	pb.current = current
	pb.render(false)
}

// Add dodaje delta do aktualnego postepu (np. +1 po kazdym wykonanym hooku,
// +n po kazdej porcji bajtow odczytanej ze strumienia).
func (pb *ProgressBar) Add(delta int64) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	if pb.done {
		return
	}
	pb.current += delta
	pb.render(false)
}

// Finish konczy pasek na 100% (albo na aktualnej wartosci, jesli total byl
// nieznany) i przechodzi do nowej linii -- kolejne Infof/Warnf nie nadpisuja
// juz tej linii.
func (pb *ProgressBar) Finish() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	if pb.done {
		return
	}
	if pb.total > 0 {
		pb.current = pb.total
	}
	pb.done = true
	pb.render(true)
	pb.newline(Colorize(ColorGreen, "OK"))
}

// Fail konczy pasek w miejscu bledu (nie skacze do 100%) i wypisuje krotki
// czerwony komunikat -- uzywane gdy krok budowy zawodzi w trakcie.
func (pb *ProgressBar) Fail(reason string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	if pb.done {
		return
	}
	pb.done = true
	pb.render(true)
	pb.newline(Colorize(ColorRed, "BLAD: "+reason))
}

// newline dopisuje krotki tag na koncu ostatniej linii paska (np. "OK",
// "BLAD: ...") i przechodzi do nowej linii terminala. Na non-terminal po
// prostu drukuje jedna zwiezla linie podsumowania.
func (pb *ProgressBar) newline(tag string) {
	if !isTerminal {
		fmt.Fprintf(os.Stdout, "[INFO ] %s: %s\n", pb.label, stripANSITag(tag))
		return
	}
	fmt.Fprintf(os.Stdout, " %s\n", tag)
}

// stripANSITag usuwa kody ANSI z tag -- uzywane w galezi non-terminal, zeby
// logi przekierowane do pliku nie mialy nieczytelnych bajtow escape.
func stripANSITag(s string) string {
	// Colorize/Colorize juz nie dodaly kodow gdy !isTerminal, wiec to no-op
	// w praktyce -- zachowane jako zabezpieczenie na wypadek przyszlych zmian.
	return s
}

// render rysuje aktualny stan paska na tej samej linii terminala (nadpisuje
// przez "\r" + spacje czyszczace poprzednia, dluzsza linie). force=true
// wymusza rysowanie z pominieciem throttlingu (pierwszy i ostatni render).
func (pb *ProgressBar) render(force bool) {
	if !isTerminal {
		return // na non-terminal render nic nie robi -- patrz newline()/Finish()/Fail()
	}
	now := time.Now()
	if !force && now.Sub(pb.lastRender) < minRenderInterval {
		return
	}
	pb.lastRender = now

	elapsed := now.Sub(pb.startedAt)
	line := pb.formatLine(elapsed)

	pad := ""
	if len(line) < pb.lastLine {
		pad = strings.Repeat(" ", pb.lastLine-len(line))
	}
	fmt.Fprintf(os.Stdout, "\r%s%s", line, pad)
	pb.lastLine = len(line)
}

func (pb *ProgressBar) formatLine(elapsed time.Duration) string {
	label := Colorize(colorBold, pb.label)

	if pb.total <= 0 {
		// Total nieznany -- sam rosnacy licznik + jednostka + czas.
		return fmt.Sprintf("  %s  %s %s  %s",
			label,
			Colorize(ColorCyan, fmt.Sprintf("%d", pb.current)),
			pb.unit,
			Colorize(colorDim, formatElapsed(elapsed)))
	}

	frac := float64(pb.current) / float64(pb.total)
	if frac > 1 {
		frac = 1
	}
	if frac < 0 {
		frac = 0
	}
	filled := int(frac * float64(progressBarWidth))
	if filled > progressBarWidth {
		filled = progressBarWidth
	}

	bar := Colorize(ColorGreen, strings.Repeat("#", filled)) +
		Colorize(colorDim, strings.Repeat("-", progressBarWidth-filled))

	pct := int(frac * 100)

	return fmt.Sprintf("  %s  [%s] %s  %s/%s %s  %s",
		label,
		bar,
		Colorize(colorBold, fmt.Sprintf("%3d%%", pct)),
		Colorize(ColorCyan, fmt.Sprintf("%d", pb.current)),
		fmt.Sprintf("%d", pb.total),
		pb.unit,
		Colorize(colorDim, formatElapsed(elapsed)))
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm%02ds", m, s)
}
