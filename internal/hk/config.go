package hk

// HkConfig to caly sparsowany plik .hk -- mapa nazwa_sekcji -> OrderedMap
// kluczy tej sekcji. Odpowiednik "HkConfig (IndexMap sekcji)" z dokumentacji.
type HkConfig struct {
	Sections *OrderedMap
}

// NewHkConfig tworzy pusty HkConfig.
func NewHkConfig() *HkConfig {
	return &HkConfig{Sections: NewOrderedMap()}
}

// Section zwraca OrderedMap danej sekcji, lub blad MissingFieldError jesli
// sekcja nie istnieje w pliku.
func (c *HkConfig) Section(name string) (*OrderedMap, error) {
	v, err := c.Sections.MustGet(name)
	if err != nil {
		return nil, err
	}
	return v.AsMap()
}

// Get to wygodny skrot do odczytu zagniezdzonej wartosci po pelnej sciezce
// kropkowej, np. Get("server.tls.cert") odpowiada ${server.tls.cert} bez
// interpolacji -- czyste przejscie po drzewie sekcji/map.
//
// Zwraca InvalidReferenceError jesli jakikolwiek segment sciezki nie istnieje.
func (c *HkConfig) Get(path string) (HkValue, error) {
	segments := splitPath(path)
	if len(segments) == 0 {
		return HkValue{}, &InvalidReferenceError{Path: path}
	}

	// Pierwszy segment to nazwa sekcji najwyzszego poziomu.
	cur, ok := c.Sections.Get(segments[0])
	if !ok {
		return HkValue{}, &InvalidReferenceError{Path: path}
	}

	for _, seg := range segments[1:] {
		idx, hasIdx, base := parseArrayIndex(seg)
		m, err := cur.AsMap()
		if err != nil {
			return HkValue{}, &InvalidReferenceError{Path: path}
		}
		next, ok := m.Get(base)
		if !ok {
			return HkValue{}, &InvalidReferenceError{Path: path}
		}
		cur = next

		if hasIdx {
			arr, err := cur.AsArray()
			if err != nil || idx < 0 || idx >= len(arr) {
				return HkValue{}, &InvalidReferenceError{Path: path}
			}
			cur = arr[idx]
		}
	}
	return cur, nil
}
