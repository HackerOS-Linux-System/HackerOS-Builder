package hk

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Serialize zamienia HkConfig z powrotem na tekst w formacie .hk, zachowujac
// kolejnosc sekcji i kluczy (dzieki OrderedMap). Odpowiednik "serialize_hk(config)".
func Serialize(config *HkConfig) string {
	var b strings.Builder
	for _, sectionName := range config.Sections.Keys() {
		val, _ := config.Sections.Get(sectionName)
		sectionMap, err := val.AsMap()
		if err != nil {
			continue // sekcja musi byc mapa -- nie powinno sie zdarzyc po Parse()
		}
		fmt.Fprintf(&b, "[%s]\n", sectionName)
		writeMapLines(&b, sectionMap, 1)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// WriteFile serializuje config i zapisuje do pliku na dysku.
// Odpowiednik "write_hk_file(path, config)".
func WriteFile(path string, config *HkConfig) error {
	content := Serialize(config)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("hk: nie mozna zapisac %s: %w", path, err)
	}
	return nil
}

// writeMapLines zapisuje zawartosc mapy na poziomie zagniezdzenia "depth"
// (depth=1 odpowiada prefiksowi "->", depth=2 "-->", itd.).
func writeMapLines(b *strings.Builder, m *OrderedMap, depth int) {
	prefix := strings.Repeat("-", depth) + ">"

	for _, key := range m.Keys() {
		val, _ := m.Get(key)
		switch val.Kind {
		case KindMap:
			fmt.Fprintf(b, "%s %s\n", prefix, key)
			writeMapLines(b, val.Map, depth+1)
		default:
			fmt.Fprintf(b, "%s %s => %s\n", prefix, key, serializeScalarOrArray(val))
		}
	}
}

// serializeScalarOrArray zamienia HkValue (nie-Map) na jego reprezentacje
// tekstowa do zapisu po "=>".
func serializeScalarOrArray(v HkValue) string {
	switch v.Kind {
	case KindString:
		return serializeString(v.Str)
	case KindNumber:
		return formatNumber(v.Num)
	case KindBool:
		if v.B {
			return "true"
		}
		return "false"
	case KindArray:
		parts := make([]string, len(v.Arr))
		for i, item := range v.Arr {
			parts[i] = serializeScalarOrArray(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return ""
	}
}

// serializeString cytuje string i ucieka znaki specjalne TYLKO jesli to
// konieczne (spacje na krawedziach, znaki ucieczki, wyglada jak liczba/bool).
// Dla prostych identyfikatorow zostawiamy forme plain (czytelniejszy plik).
func serializeString(s string) string {
	needsQuotes := s == "" ||
		strings.ContainsAny(s, "\n\t\r\"\\") ||
		looksLikeNumberOrBool(s) ||
		strings.TrimSpace(s) != s

	if !needsQuotes {
		return s
	}

	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	).Replace(s)
	return `"` + escaped + `"`
}

func looksLikeNumberOrBool(s string) bool {
	lower := strings.ToLower(s)
	if lower == "true" || lower == "false" {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
