package preflight

import "testing"

// TestCheckTools_AllPresent uzywa narzedzia ktore z duzym prawdopodobienstwem
// istnieje w kazdym srodowisku CI/dev (sh) -- weryfikuje sciezke "sukces".
func TestCheckTools_AllPresent(t *testing.T) {
	tools := []Tool{
		{Binary: "sh", AptPackage: "dash", UsedForStep: "test"},
	}
	if err := checkTools(tools); err != nil {
		t.Fatalf("oczekiwano sukcesu dla 'sh' (powinien istniec w kazdym systemie), otrzymano: %v", err)
	}
}

// TestCheckTools_MissingListsAll sprawdza ze brakujace narzedzia sa
// zglaszane WSZYSTKIE na raz (nie tylko pierwsze znalezione).
func TestCheckTools_MissingListsAll(t *testing.T) {
	tools := []Tool{
		{Binary: "to-na-pewno-nie-istnieje-1", AptPackage: "pkg1", UsedForStep: "krok 1"},
		{Binary: "to-na-pewno-nie-istnieje-2", AptPackage: "pkg2", UsedForStep: "krok 2"},
	}
	err := checkTools(tools)
	if err == nil {
		t.Fatal("oczekiwano bledu dla nieistniejacych binarek")
	}
	msg := err.Error()
	if !contains(msg, "to-na-pewno-nie-istnieje-1") || !contains(msg, "to-na-pewno-nie-istnieje-2") {
		t.Fatalf("oczekiwano ze blad wymienia OBIE brakujace binarki, otrzymano: %s", msg)
	}
}

// TestCheckTools_Deduplication sprawdza ze ten sam binarz wymagany przez
// wiele krokow (np. "mount" w cloudTools) nie jest zglaszany dwukrotnie.
func TestCheckTools_Deduplication(t *testing.T) {
	tools := []Tool{
		{Binary: "to-na-pewno-nie-istnieje-x", AptPackage: "pkgx", UsedForStep: "krok A"},
		{Binary: "to-na-pewno-nie-istnieje-x", AptPackage: "pkgx", UsedForStep: "krok B"},
	}
	err := checkTools(tools)
	if err == nil {
		t.Fatal("oczekiwano bledu")
	}
	count := countOccurrences(err.Error(), "to-na-pewno-nie-istnieje-x")
	if count != 1 {
		t.Fatalf("oczekiwano ze binarz wystapi w komunikacie raz, wystapil %d razy", count)
	}
}

func contains(haystack, needle string) bool {
	return countOccurrences(haystack, needle) > 0
}

func countOccurrences(haystack, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			count++
		}
	}
	return count
}
