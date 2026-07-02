package hk

// OrderedMap to mapa klucz->HkValue zachowujaca kolejnosc wstawiania kluczy,
// odpowiednik "IndexMap" uzywanego przez oryginalny parser Rust (hk-parser).
// Kolejnosc ma znaczenie przy serializacji z powrotem do .hk (zachowuje
// porzadek pliku wejsciowego).
type OrderedMap struct {
	keys   []string
	values map[string]HkValue
}

// NewOrderedMap tworzy nowa, pusta OrderedMap.
func NewOrderedMap() *OrderedMap {
	return &OrderedMap{values: make(map[string]HkValue)}
}

// Set wstawia lub nadpisuje wartosc pod danym kluczem. Jesli klucz juz
// istnieje, jego pozycja w Keys() NIE zmienia sie (nadpisanie w miejscu) --
// zachowanie identyczne z IndexMap::insert.
func (m *OrderedMap) Set(key string, val HkValue) {
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = val
}

// Get zwraca wartosc i informacje czy klucz istnieje.
func (m *OrderedMap) Get(key string) (HkValue, bool) {
	v, ok := m.values[key]
	return v, ok
}

// MustGet zwraca wartosc pod danym kluczem, lub blad MissingFieldError jesli
// klucz nie istnieje -- przydatne przy deserializacji wymaganych pol.
func (m *OrderedMap) MustGet(key string) (HkValue, error) {
	v, ok := m.values[key]
	if !ok {
		return HkValue{}, &MissingFieldError{Name: key}
	}
	return v, nil
}

// Keys zwraca klucze w oryginalnej kolejnosci wstawienia.
func (m *OrderedMap) Keys() []string {
	return m.keys
}

// Has zwraca true jesli klucz istnieje w mapie.
func (m *OrderedMap) Has(key string) bool {
	_, ok := m.values[key]
	return ok
}

// Len zwraca liczbe kluczy najwyzszego poziomu w tej mapie.
func (m *OrderedMap) Len() int {
	return len(m.keys)
}
