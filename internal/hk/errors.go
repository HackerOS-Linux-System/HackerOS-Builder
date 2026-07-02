package hk

import "fmt"

// ParseError to blad skladni z numerem linii i kolumny:
// "Parser zwraca precyzyjne bledy z numerem linii i kolumny. Kazdy blad ma
// wbudowany hint wyswietlany w terminalu."
type ParseError struct {
	Line       int
	Column     int
	Message    string
	Hint       string
	SourceLine string // oryginalna linia tekstu, do wyswietlenia z markerem "^"
}

func (e *ParseError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("parse error\n  -> at %d:%d\n\n  %s\n\nHint: %s",
			e.Line, e.Column, e.Message, e.Hint)
	}
	return fmt.Sprintf("parse error\n  -> at %d:%d\n\n  %s", e.Line, e.Column, e.Message)
}

// TypeMismatchError -- "Proba odczytu wartosci jako nieodpowiedni typ."
type TypeMismatchError struct {
	Expected string
	Found    string
}

func (e *TypeMismatchError) Error() string {
	return fmt.Sprintf("hk: TypeMismatch -- oczekiwano %s, otrzymano %s", e.Expected, e.Found)
}

// InvalidReferenceError -- "Interpolacja wskazuje na nieistniejacy klucz."
type InvalidReferenceError struct {
	Path string
}

func (e *InvalidReferenceError) Error() string {
	return fmt.Sprintf("hk: InvalidReference(%s) -- sprawdz czy sciezka istnieje", e.Path)
}

// CyclicReferenceError -- "Dwie wartosci interpoluja sie nawzajem."
type CyclicReferenceError struct {
	Path string
}

func (e *CyclicReferenceError) Error() string {
	return fmt.Sprintf("hk: CyclicReference(%s)", e.Path)
}

// KeyConflictError -- "Duplikat klucza lub konflikt miedzy kluczem prostym
// a kluczem z kropka wskazujacym to samo miejsce."
type KeyConflictError struct {
	Key string
}

func (e *KeyConflictError) Error() string {
	return fmt.Sprintf("hk: KeyConflict(%s)", e.Key)
}

// MissingFieldError -- "Wymagane pole nie zostalo znalezione podczas
// deserializacji struktury."
type MissingFieldError struct {
	Name string
}

func (e *MissingFieldError) Error() string {
	return fmt.Sprintf("hk: MissingField(%s)", e.Name)
}

// EmptyContentError -- plik jest pusty (uzywane glownie przez parser .hacker,
// ale czesc wspolnego zestawu bledow ekosystemu).
type EmptyContentError struct{}

func (e *EmptyContentError) Error() string {
	return "hk: EmptyContent -- plik jest pusty"
}
