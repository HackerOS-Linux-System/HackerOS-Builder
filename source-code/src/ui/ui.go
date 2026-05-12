package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ── Kolory ANSI ───────────────────────────────────────────────────────────────
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Red     = "\033[91m"
	Green   = "\033[92m"
	Yellow  = "\033[93m"
	Blue    = "\033[94m"
	Magenta = "\033[95m"
	Cyan    = "\033[96m"
	White   = "\033[97m"
	BgBlue  = "\033[44m"
	BgGreen = "\033[42m"
)

func C(color, text string) string { return color + text + Reset }

func Info(msg string)  { fmt.Printf("  %s %s\n", C(Cyan, "●"), msg) }
func Ok(msg string)    { fmt.Printf("  %s %s\n", C(Green, "✔"), msg) }
func Warn(msg string)  { fmt.Printf("  %s %s\n", C(Yellow, "⚠"), C(Yellow, msg)) }
func Err(msg string)   { fmt.Fprintf(os.Stderr, "  %s %s\n", C(Red, "✘"), C(Red, msg)) }
func Step(msg string)  { fmt.Printf("\n%s %s\n", C(Bold+Blue, "▸"), C(Bold, msg)) }
func Banner(msg string) {
	fmt.Printf("\n%s\n\n", C(BgBlue+White+Bold, "  "+msg+"  "))
}
func Die(msg string) { Err(msg); os.Exit(1) }

func PrintKV(key, val string) {
	fmt.Printf("  %s%-22s%s %s\n", Dim, key+":", Reset, C(Cyan, val))
}
func PrintKVDim(key, val string) {
	fmt.Printf("  %s%-22s%s %s\n", Dim, key+":", Reset, C(Dim, val))
}

// ── Logo ──────────────────────────────────────────────────────────────────────
func PrintLogo(version string) {
	lines := []string{
		`  ██╗  ██╗ █████╗  ██████╗██╗  ██╗███████╗██████╗  ██████╗ ███████╗`,
		`  ██║  ██║██╔══██╗██╔════╝██║ ██╔╝██╔════╝██╔══██╗██╔═══██╗██╔════╝`,
		`  ███████║███████║██║     █████╔╝ █████╗  ██████╔╝██║   ██║███████╗`,
		`  ██╔══██║██╔══██║██║     ██╔═██╗ ██╔══╝  ██╔══██╗██║   ██║╚════██║`,
		`  ██║  ██║██║  ██║╚██████╗██║  ██╗███████╗██║  ██║╚██████╔╝███████║`,
		`  ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝`,
	}
	fmt.Println()
	for i, l := range lines {
		if i < len(lines)-1 {
			fmt.Println(C(Cyan+Bold, l))
		} else {
			fmt.Println(C(Cyan, l))
		}
	}
	fmt.Printf("%s\n\n", C(Dim, fmt.Sprintf("  Builder v%s — narzędzie do budowania obrazów ISO HackerOS", version)))
}

// ── Yarn-style Progress Bar ───────────────────────────────────────────────────
//
// Wygląd (wzorowany na yarn/indicatif):
//
//   ⠙ [00:00:04] [████████████░░░░░░░░░░░░░░░░░░░░]  6/15 Instalacja pakietów...
//   ✔ [00:00:12] [████████████████████████████████] 15/15 Gotowe!         12.4s
//

var spinnerFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

type ProgressBar struct {
	mu      sync.Mutex
	total   int
	current int
	label   string
	frame   int
	start   time.Time
	done    bool
	failed  bool
	stopCh  chan struct{}
}

func NewProgress(total int, label string) *ProgressBar {
	return &ProgressBar{
		total:  total,
		label:  label,
		start:  time.Now(),
		stopCh: make(chan struct{}),
	}
}

func (p *ProgressBar) Start() {
	go func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.mu.Lock()
				if !p.done && !p.failed {
					p.frame = (p.frame + 1) % len(spinnerFrames)
					p.render()
				}
				p.mu.Unlock()
			case <-p.stopCh:
				return
			}
		}
	}()
}

func (p *ProgressBar) Update(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current++
	if label != "" {
		p.label = label
	}
	p.render()
}

func (p *ProgressBar) SetLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
	p.render()
}

func (p *ProgressBar) Finish(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.current = p.total
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
	elapsed := time.Since(p.start)
	if label == "" {
		label = "Gotowe!"
	}
	bar := p.buildBar(p.total, p.total, 32)
	elapsed_str := fmt.Sprintf("%.1fs", elapsed.Seconds())
	timer := p.formatDuration(elapsed)
	fmt.Printf("\r  %s [%s] %s %s%3d/%-3d%s %s %s\033[K\n",
		C(Green, "✔"),
		C(Dim, timer),
		bar,
		Dim, p.total, p.total, Reset,
		C(Green, label),
		C(Dim, elapsed_str),
	)
}

func (p *ProgressBar) Fail(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = true
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
	if label == "" {
		label = "Błąd!"
	}
	bar := p.buildBar(p.current, p.total, 32)
	timer := p.formatDuration(time.Since(p.start))
	fmt.Printf("\r  %s [%s] %s %s%3d/%-3d%s %s\033[K\n",
		C(Red, "✘"),
		C(Dim, timer),
		bar,
		Dim, p.current, p.total, Reset,
		C(Red, label),
	)
}

func (p *ProgressBar) render() {
	spin := C(Cyan, spinnerFrames[p.frame])
	bar := p.buildBar(p.current, p.total, 32)
	timer := p.formatDuration(time.Since(p.start))

	label := p.label
	runes := []rune(label)
	if len(runes) > 45 {
		label = string(runes[:44]) + "…"
	}

	fmt.Printf("\r  %s [%s] %s %s%3d/%-3d%s %s",
		spin,
		C(Dim, timer),
		bar,
		Dim, p.current, p.total, Reset,
		C(Dim, label),
	)
}

func (p *ProgressBar) buildBar(current, total, width int) string {
	pct := 0.0
	if total > 0 {
		pct = float64(current) / float64(total)
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled

	filledStr := C(Cyan, strings.Repeat("█", filled))
	emptyStr := C(Dim, strings.Repeat("░", empty))
	return fmt.Sprintf("[%s%s]", filledStr, emptyStr)
}

func (p *ProgressBar) formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
