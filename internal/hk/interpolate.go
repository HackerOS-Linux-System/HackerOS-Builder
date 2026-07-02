package hk

import (
	"fmt"
	"os"
	"strings"
)

// ResolveInterpolations rozwiazuje wszystkie wystapienia ${...} w wartosciach
// typu string w calym config -- modyfikuje config w miejscu.
//
// Wsparte formy:
//
//	${sekcja.klucz}        -- referencja do innej wartosci
//	${sekcja.mapa.klucz}   -- referencja gleboko zagniezdzona
//	${sekcja.tablica[n]}   -- indeksowanie elementu tablicy (od 0)
//	${env:NAZWA}           -- zmienna srodowiskowa (pusta jesli nieustawiona)
func ResolveInterpolations(config *HkConfig) error {
	r := &resolver{config: config, resolving: make(map[string]bool)}
	return r.resolveMap(config.Sections, "")
}

type resolver struct {
	config *HkConfig
	// resolving sledzi sciezki aktualnie "w trakcie" rozwiazywania -- jesli
	// natrafimy na sciezke ktora juz jest na tej liscie, to cykl.
	resolving map[string]bool
}

// resolveMap rekurencyjnie przechodzi po mapie i rozwiazuje interpolacje
// w kazdej wartosci typu string.
func (r *resolver) resolveMap(m *OrderedMap, pathPrefix string) error {
	for _, key := range m.Keys() {
		val, _ := m.Get(key)
		fullPath := key
		if pathPrefix != "" {
			fullPath = pathPrefix + "." + key
		}

		switch val.Kind {
		case KindString:
			resolved, err := r.resolveString(val.Str, fullPath)
			if err != nil {
				return err
			}
			m.Set(key, String(resolved))

		case KindArray:
			newArr := make([]HkValue, len(val.Arr))
			for i, item := range val.Arr {
				if item.Kind == KindString {
					resolved, err := r.resolveString(item.Str, fmt.Sprintf("%s[%d]", fullPath, i))
					if err != nil {
						return err
					}
					newArr[i] = String(resolved)
				} else {
					newArr[i] = item
				}
			}
			m.Set(key, Array(newArr))

		case KindMap:
			if err := r.resolveMap(val.Map, fullPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveString rozwiazuje wszystkie ${...} wewnatrz pojedynczego stringa.
func (r *resolver) resolveString(s string, selfPath string) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(s) {
		start := strings.Index(s[i:], "${")
		if start == -1 {
			out.WriteString(s[i:])
			break
		}
		out.WriteString(s[i : i+start])
		absStart := i + start

		end := strings.Index(s[absStart:], "}")
		if end == -1 {
			return "", &ParseError{
				Message: fmt.Sprintf("Niezamknieta interpolacja w %q (brak '}')", s),
			}
		}
		absEnd := absStart + end

		ref := s[absStart+2 : absEnd] // tresc miedzy "${" i "}"
		resolved, err := r.resolveRef(ref, selfPath)
		if err != nil {
			return "", err
		}
		out.WriteString(resolved)

		i = absEnd + 1
	}
	return out.String(), nil
}

// resolveRef rozwiazuje pojedyncza referencje (tresc wewnatrz ${...}) --
// albo zmienna srodowiskowa "env:NAZWA", albo sciezke do innej wartosci.
func (r *resolver) resolveRef(ref string, selfPath string) (string, error) {
	if strings.HasPrefix(ref, "env:") {
		envName := strings.TrimPrefix(ref, "env:")
		return os.Getenv(envName), nil // pusta jesli nieustawiona
	}

	if r.resolving[ref] || ref == selfPath {
		return "", &CyclicReferenceError{Path: ref}
	}

	val, err := r.config.Get(ref)
	if err != nil {
		return "", &InvalidReferenceError{Path: ref}
	}

	// Jesli wartosc docelowa SAMA zawiera interpolacje (lancuch referencji),
	// rozwiazujemy ja rekurencyjnie, z ochrona przed cyklem.
	if val.Kind == KindString && strings.Contains(val.Str, "${") {
		r.resolving[ref] = true
		resolved, rerr := r.resolveString(val.Str, ref)
		delete(r.resolving, ref)
		if rerr != nil {
			return "", rerr
		}
		return resolved, nil
	}

	return val.AsString()
}
