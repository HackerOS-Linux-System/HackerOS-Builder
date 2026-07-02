package hk

import (
	"strconv"
	"strings"
)

// parseScalarOrArray parsuje surowy tekst wartosci (to co jest po "=>")
// i zwraca odpowiedni HkValue z automatycznym wykryciem typu:
// string (cytowany lub plain), number, bool, array.
//
// Zgodnie ze specyfikacja:
//   - string cytowany: "..." z sekwencjami ucieczki \n \t \r \" \\
//   - string plain: dowolny tekst bez cudzyslowow
//   - number: parsowane jako f64 (int i float)
//   - bool: true/false, bez rozróżniania wielkosci liter
//   - array: [v1, v2, ...] w nawiasach kwadratowych, elementy roznych typow
func parseScalarOrArray(raw string) (HkValue, error) {
	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return parseArrayLiteral(raw)
	}

	return parseScalar(raw)
}

// parseScalar parsuje pojedyncza wartosc skalarna (nie-array): string
// cytowany/plain, number lub bool.
func parseScalar(raw string) (HkValue, error) {
	raw = strings.TrimSpace(raw)

	// String cytowany -- z obsluga sekwencji ucieczki.
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		unescaped, err := unescapeString(raw[1 : len(raw)-1])
		if err != nil {
			return HkValue{}, err
		}
		return String(unescaped), nil
	}

	// Bool -- bez rozrozniania wielkosci liter (True, FALSE, etc. sa poprawne).
	lower := strings.ToLower(raw)
	if lower == "true" {
		return Bool(true), nil
	}
	if lower == "false" {
		return Bool(false), nil
	}

	// Number -- probujemy sparsowac jako f64; jesli sie nie uda, to plain string.
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		return Number(n), nil
	}

	// Plain string (bez cudzyslowow) -- fallback.
	return String(raw), nil
}

// parseArrayLiteral parsuje "[v1, v2, v3]" na HkValue typu KindArray.
// Respektuje przecinki wewnatrz cytowanych stringow (nie dzieli ich na poly).
func parseArrayLiteral(raw string) (HkValue, error) {
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return Array(nil), nil
	}

	parts := splitArrayElements(inner)
	items := make([]HkValue, 0, len(parts))
	for _, p := range parts {
		v, err := parseScalar(strings.TrimSpace(p))
		if err != nil {
			return HkValue{}, err
		}
		items = append(items, v)
	}
	return Array(items), nil
}

// splitArrayElements dzieli zawartosc tablicy na przecinkach, ignorujac
// przecinki ktore znajduja sie wewnatrz cytowanego stringa "...".
func splitArrayElements(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuotes := false
	escaped := false

	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuotes:
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// unescapeString rozwiazuje sekwencje ucieczki \n \t \r \" \\ w cytowanym stringu.
func unescapeString(s string) (string, error) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				// Nieznana sekwencja -- zachowujemy jak jest (backslash + znak),
				// zamiast rzucac blad, dla wiekszej tolerancji parsera.
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}
