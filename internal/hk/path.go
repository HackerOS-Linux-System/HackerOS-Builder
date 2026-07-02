package hk

import "strings"

// splitPath dzieli sciezke kropkowa "a.b.c" na segmenty ["a", "b", "c"].
// Nie dzieli wewnatrz cudzyslowow -- klucz w cudzyslowach jest literalem
// (kropki wewnatrz nie tworza zagniezdzenia), zgodnie ze specyfikacja:
// 'Klucz w cudzyslowach ("klucz") jest traktowany jako literal'.
func splitPath(path string) []string {
	var segments []string
	var cur strings.Builder
	inQuotes := false

	for _, r := range path {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == '.' && !inQuotes:
			segments = append(segments, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || len(segments) > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

// parseArrayIndex rozpoznaje segment sciezki w formie "nazwa[n]" i zwraca
// (n, true, "nazwa") jesli segment zawiera indeks tablicowy, albo
// (0, false, segment) jesli nie zawiera.
//
// Uzywane do obslugi ${sekcja.tablica[n]} -- "Indeksowanie elementu tablicy (od 0)".
func parseArrayIndex(segment string) (index int, hasIndex bool, base string) {
	openIdx := strings.IndexByte(segment, '[')
	closeIdx := strings.IndexByte(segment, ']')
	if openIdx == -1 || closeIdx == -1 || closeIdx < openIdx {
		return 0, false, segment
	}

	base = segment[:openIdx]
	numStr := segment[openIdx+1 : closeIdx]

	n := 0
	neg := false
	started := false
	for _, c := range numStr {
		if c == '-' && !started {
			neg = true
			started = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, false, segment // nieprawidlowy indeks -- traktuj jako brak indeksu
		}
		n = n*10 + int(c-'0')
		started = true
	}
	if neg {
		n = -n
	}
	return n, true, base
}
