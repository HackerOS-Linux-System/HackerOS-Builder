package hk

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ParseString parsuje zawartosc pliku .hk podana jako string i zwraca
// HkConfig (bez rozwiazanych interpolacji -- patrz ResolveInterpolations).
func ParseString(input string) (*HkConfig, error) {
	p := &parser{lines: strings.Split(input, "\n")}
	return p.parse()
}

// LoadFile wczytuje plik z dysku i parsuje go jako .hk.
// Odpowiednik "load_hk_file(path)" z dokumentacji Rust API.
func LoadFile(path string) (*HkConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("hk: nie mozna otworzyc %s: %w", path, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("hk: blad odczytu %s: %w", path, err)
	}

	p := &parser{lines: lines, sourcePath: path}
	return p.parse()
}

// parser to wewnetrzny stan podczas parsowania.
type parser struct {
	lines      []string
	sourcePath string

	config         *HkConfig
	currentSection string

	// nodeStack to stos map odpowiadajacy aktualnej glebokosci zagniezdzenia
	// (liczba myslnikow przed '>'). nodeStack[0] = mapa sekcji,
	// nodeStack[1] = mapa L1 jesli jestesmy "wewnatrz" mapy inline, itd.
	nodeStack []*OrderedMap
	keyStack  []string
}

func (p *parser) parse() (*HkConfig, error) {
	p.config = NewHkConfig()

	for lineNo, raw := range p.lines {
		line := strings.TrimSpace(raw)

		if line == "" || strings.HasPrefix(line, "!") {
			continue // pusta linia lub komentarz
		}

		if strings.HasPrefix(line, "[") {
			if err := p.handleSectionHeader(line, lineNo+1, raw); err != nil {
				return nil, err
			}
			continue
		}

		if strings.HasPrefix(line, "-") {
			if err := p.handleKeyLine(line, lineNo+1, raw); err != nil {
				return nil, err
			}
			continue
		}

		return nil, &ParseError{
			Line:       lineNo + 1,
			Column:     1,
			Message:    fmt.Sprintf("Nieoczekiwana linia: %q", line),
			Hint:       "Linie musza zaczynac sie od '!', '[' lub '-'",
			SourceLine: raw,
		}
	}

	return p.config, nil
}

// handleSectionHeader przetwarza linie "[nazwa_sekcji]". Resetuje stos
// zagniezdzenia -- kazda sekcja zaczyna od czystej mapy poziomu 1.
func (p *parser) handleSectionHeader(line string, lineNo int, raw string) error {
	if !strings.HasSuffix(line, "]") {
		return &ParseError{
			Line: lineNo, Column: len(line), SourceLine: raw,
			Message: "Naglowek sekcji nie jest zamkniety ']'",
			Hint:    "Czy zapisano: [nazwa_sekcji]",
		}
	}
	name := strings.TrimSpace(line[1 : len(line)-1])
	if name == "" {
		return &ParseError{
			Line: lineNo, Column: 2, SourceLine: raw,
			Message: "Nazwa sekcji nie moze byc pusta",
			Hint:    "Przyklad: [metadata]",
		}
	}

	var sectionMap *OrderedMap
	if existing, ok := p.config.Sections.Get(name); ok {
		// Sekcja juz istnieje (rozdzielona w pliku na dwie czesci) -- dopisujemy
		// do tej samej mapy, zamiast nadpisywac (zgodnie z duchem IndexMap).
		m, err := existing.AsMap()
		if err != nil {
			return &KeyConflictError{Key: name}
		}
		sectionMap = m
	} else {
		sectionMap = NewOrderedMap()
		p.config.Sections.Set(name, MapValue(sectionMap))
	}

	p.currentSection = name
	p.nodeStack = []*OrderedMap{sectionMap}
	p.keyStack = []string{name}
	return nil
}

// handleKeyLine przetwarza linie zaczynajaca sie od '-' -- klucz na
// dowolnym poziomie zagniezdzenia ("->", "-->", "--->", ...).
func (p *parser) handleKeyLine(line string, lineNo int, raw string) error {
	if p.currentSection == "" {
		return &ParseError{
			Line: lineNo, Column: 1, SourceLine: raw,
			Message: "Klucz zdefiniowany przed jakakolwiek sekcja",
			Hint:    "Kazdy klucz musi byc wewnatrz [sekcji]",
		}
	}

	depth, rest, err := countDashDepth(line, lineNo, raw)
	if err != nil {
		return err
	}

	rest = strings.TrimSpace(rest)
	if rest == "" {
		return &ParseError{
			Line: lineNo, Column: depth + 1, SourceLine: raw,
			Message: "Brak klucza po myslnikach",
			Hint:    "Przyklad: -> klucz => wartosc",
		}
	}

	// Ustawiamy nodeStack/keyStack tak, by ich dlugosc == depth (mapa, w
	// ktora wstawiamy ten klucz, to nodeStack[depth-1]).
	if depth > len(p.nodeStack) {
		return &ParseError{
			Line: lineNo, Column: 1, SourceLine: raw,
			Message: fmt.Sprintf("Zbyt gleboki poziom zagniezdzenia (%d) -- brak rodzica", depth),
			Hint:    "Kazdy poziom (myslnik) musi miec rodzica zdefiniowanego linia wyzej",
		}
	}
	p.nodeStack = p.nodeStack[:depth]
	p.keyStack = p.keyStack[:depth]
	targetMap := p.nodeStack[depth-1]

	arrowIdx := strings.Index(rest, "=>")
	if arrowIdx == -1 {
		// Brak "=>" -- linia tworzaca mape inline (podsekcja), np. "-> obsidian".
		keyName := stripQuotes(strings.TrimSpace(rest))

		newMap := NewOrderedMap()
		if err := p.insertWithDotExpansion(targetMap, keyName, MapValue(newMap), lineNo, raw); err != nil {
			return err
		}

		p.nodeStack = append(p.nodeStack, newMap)
		p.keyStack = append(p.keyStack, keyName)
		return nil
	}

	keyPart := strings.TrimSpace(rest[:arrowIdx])
	valPart := strings.TrimSpace(rest[arrowIdx+2:])
	if keyPart == "" {
		return &ParseError{
			Line: lineNo, Column: depth + 1, SourceLine: raw,
			Message: "Brak nazwy klucza przed '=>'",
			Hint:    "Przyklad: -> klucz => wartosc",
		}
	}

	value, perr := parseScalarOrArray(valPart)
	if perr != nil {
		return &ParseError{Line: lineNo, Column: depth + 1, Message: perr.Error(), SourceLine: raw}
	}

	keyPart = stripQuotes(keyPart)
	if err := p.insertWithDotExpansion(targetMap, keyPart, value, lineNo, raw); err != nil {
		return err
	}

	// Klucz ze "=>" nie otwiera nowego poziomu w nodeStack -- jest lisciem.
	return nil
}

// insertWithDotExpansion wstawia wartosc pod kluczem, ktory MOZE zawierac
// kropki ("a.b.c") -- tworzy/rozwija zagniezdzone mapy zgodnie ze specyfikacja:
// "Klucz kropkowy: -> a.b.c => wartosc -- Skrot tworzacy zagniezdzone mapy: a -> b -> c".
//
// Wykrywa KeyConflict: nie mozna miec jednoczesnie klucza prostego "a" i
// kropkowego "a.b" wskazujacego ta sama lokalizacje w drzewie.
func (p *parser) insertWithDotExpansion(target *OrderedMap, key string, value HkValue, lineNo int, raw string) error {
	segments := splitPath(key)
	if len(segments) <= 1 {
		if existing, ok := target.Get(key); ok && existing.Kind == KindMap && value.Kind != KindMap {
			return &KeyConflictError{Key: key}
		}
		target.Set(key, value)
		return nil
	}

	cur := target
	for i, seg := range segments[:len(segments)-1] {
		existing, ok := cur.Get(seg)
		if !ok {
			newMap := NewOrderedMap()
			cur.Set(seg, MapValue(newMap))
			cur = newMap
			continue
		}
		m, err := existing.AsMap()
		if err != nil {
			return &KeyConflictError{Key: strings.Join(segments[:i+1], ".")}
		}
		cur = m
	}

	last := segments[len(segments)-1]
	if existing, ok := cur.Get(last); ok && existing.Kind == KindMap && value.Kind != KindMap {
		return &KeyConflictError{Key: key}
	}
	cur.Set(last, value)
	return nil
}

// countDashDepth liczy liczbe myslnikow przed '>' na poczatku linii i
// zwraca (glebokosc, reszta_linii_po_'>'). Np. "--> klucz => 1" -> (2, " klucz => 1").
func countDashDepth(line string, lineNo int, raw string) (int, string, error) {
	i := 0
	for i < len(line) && line[i] == '-' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != '>' {
		return 0, "", &ParseError{
			Line: lineNo, Column: i + 1, SourceLine: raw,
			Message: "Oczekiwano '>' po myslnikach",
			Hint:    "Przyklad: -> klucz => wartosc",
		}
	}
	return i, line[i+1:], nil
}

// stripQuotes usuwa otaczajace cudzyslowy z nazwy klucza, jesli sa obecne.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
