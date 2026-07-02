package hk

import "fmt"

// ValueKind opisuje typ przechowywany w HkValue.
type ValueKind int

const (
	KindString ValueKind = iota
	KindNumber
	KindBool
	KindArray
	KindMap
)

// HkValue to pojedyncza wartosc w drzewie konfiguracji .hk -- odpowiednik
// "HkValue" z dokumentacji Rust API, reimplementowany w Go.
type HkValue struct {
	Kind ValueKind

	Str string      // wazne gdy Kind == KindString
	Num float64     // wazne gdy Kind == KindNumber
	B   bool        // wazne gdy Kind == KindBool
	Arr []HkValue   // wazne gdy Kind == KindArray
	Map *OrderedMap // wazne gdy Kind == KindMap
}

// String tworzy HkValue typu string.
func String(s string) HkValue { return HkValue{Kind: KindString, Str: s} }

// Number tworzy HkValue typu number.
func Number(n float64) HkValue { return HkValue{Kind: KindNumber, Num: n} }

// Bool tworzy HkValue typu bool.
func Bool(b bool) HkValue { return HkValue{Kind: KindBool, B: b} }

// Array tworzy HkValue typu array.
func Array(items []HkValue) HkValue { return HkValue{Kind: KindArray, Arr: items} }

// MapValue tworzy HkValue typu map z istniejacej OrderedMap.
func MapValue(m *OrderedMap) HkValue { return HkValue{Kind: KindMap, Map: m} }

// AsString konwertuje String/Number/Bool na string. Blad dla Array/Map --
// zgodnie ze specyfikacja Rust API ("Konwertuje String, Number lub Bool na
// String. Blad dla Array/Map").
func (v HkValue) AsString() (string, error) {
	switch v.Kind {
	case KindString:
		return v.Str, nil
	case KindNumber:
		return formatNumber(v.Num), nil
	case KindBool:
		if v.B {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("hk: TypeMismatch -- nie mozna skonwertowac %s na string", v.kindName())
	}
}

// AsNumber zwraca wartosc jako float64. Blad jesli typ != KindNumber.
func (v HkValue) AsNumber() (float64, error) {
	if v.Kind != KindNumber {
		return 0, fmt.Errorf("hk: TypeMismatch -- oczekiwano number, otrzymano %s", v.kindName())
	}
	return v.Num, nil
}

// AsBool zwraca wartosc jako bool. Blad jesli typ != KindBool.
func (v HkValue) AsBool() (bool, error) {
	if v.Kind != KindBool {
		return false, fmt.Errorf("hk: TypeMismatch -- oczekiwano bool, otrzymano %s", v.kindName())
	}
	return v.B, nil
}

// AsArray zwraca referencje do slice elementow. Blad jesli typ != KindArray.
func (v HkValue) AsArray() ([]HkValue, error) {
	if v.Kind != KindArray {
		return nil, fmt.Errorf("hk: TypeMismatch -- oczekiwano array, otrzymano %s", v.kindName())
	}
	return v.Arr, nil
}

// AsMap zwraca referencje do OrderedMap. Blad jesli typ != KindMap.
func (v HkValue) AsMap() (*OrderedMap, error) {
	if v.Kind != KindMap {
		return nil, fmt.Errorf("hk: TypeMismatch -- oczekiwano map, otrzymano %s", v.kindName())
	}
	return v.Map, nil
}

func (v HkValue) kindName() string {
	switch v.Kind {
	case KindString:
		return "string"
	case KindNumber:
		return "number"
	case KindBool:
		return "bool"
	case KindArray:
		return "array"
	case KindMap:
		return "map"
	default:
		return "unknown"
	}
}

// formatNumber formatuje float64 bez zbednych zer po przecinku (42 -> "42",
// 3.14 -> "3.14") -- tak jak oczekiwalibysmy przy konwersji number -> string.
func formatNumber(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return fmt.Sprintf("%g", n)
}
