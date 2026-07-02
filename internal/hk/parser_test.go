package hk

import "testing"

func TestParseString_BasicSection(t *testing.T) {
	cfg, err := ParseString("[account]\n-> type => user\n-> name => michal\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}

	sec, err := cfg.Section("account")
	if err != nil {
		t.Fatalf("Section(account) zwrocilo blad: %v", err)
	}

	val, ok := sec.Get("type")
	if !ok {
		t.Fatal("oczekiwano klucza 'type' w sekcji [account]")
	}
	str, err := val.AsString()
	if err != nil || str != "user" {
		t.Fatalf("oczekiwano type=user, otrzymano %q (err=%v)", str, err)
	}
}

func TestParseString_Comments(t *testing.T) {
	cfg, err := ParseString("! to jest komentarz\n\n[a]\n! kolejny\n-> x => 1\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	v, err := cfg.Get("a.x")
	if err != nil {
		t.Fatalf("Get(a.x) zwrocilo blad: %v", err)
	}
	n, err := v.AsNumber()
	if err != nil || n != 1 {
		t.Fatalf("oczekiwano a.x=1, otrzymano %v (err=%v)", n, err)
	}
}

func TestParseString_NestedKeys(t *testing.T) {
	cfg, err := ParseString("[a]\n-> sub\n--> x => 1\n--> y => 2\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	v, err := cfg.Get("a.sub.x")
	if err != nil {
		t.Fatalf("Get(a.sub.x) zwrocilo blad: %v", err)
	}
	n, _ := v.AsNumber()
	if n != 1 {
		t.Fatalf("oczekiwano a.sub.x=1, otrzymano %v", n)
	}
}

func TestParseString_DottedKey(t *testing.T) {
	cfg, err := ParseString("[a]\n-> b.c => wartosc\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	v, err := cfg.Get("a.b.c")
	if err != nil {
		t.Fatalf("Get(a.b.c) zwrocilo blad: %v", err)
	}
	s, _ := v.AsString()
	if s != "wartosc" {
		t.Fatalf("oczekiwano 'wartosc', otrzymano %q", s)
	}
}

func TestParseString_Array(t *testing.T) {
	cfg, err := ParseString("[a]\n-> items => [1, 2, 3]\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	v, err := cfg.Get("a.items")
	if err != nil {
		t.Fatalf("Get(a.items) zwrocilo blad: %v", err)
	}
	arr, err := v.AsArray()
	if err != nil {
		t.Fatalf("AsArray zwrocilo blad: %v", err)
	}
	if len(arr) != 3 {
		t.Fatalf("oczekiwano 3 elementow, otrzymano %d", len(arr))
	}
}

func TestParseString_QuotedStringWithEscapes(t *testing.T) {
	cfg, err := ParseString(`[a]` + "\n" + `-> s => "linia1\nlinia2"` + "\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	v, err := cfg.Get("a.s")
	if err != nil {
		t.Fatalf("Get(a.s) zwrocilo blad: %v", err)
	}
	s, _ := v.AsString()
	if s != "linia1\nlinia2" {
		t.Fatalf("oczekiwano stringu z newline, otrzymano %q", s)
	}
}

func TestParseString_ErrorOnKeyBeforeSection(t *testing.T) {
	_, err := ParseString("-> klucz => wartosc\n")
	if err == nil {
		t.Fatal("oczekiwano bledu dla klucza przed sekcja")
	}
}

func TestParseString_ErrorOnUnclosedSection(t *testing.T) {
	_, err := ParseString("[niezamknieta\n-> x => 1\n")
	if err == nil {
		t.Fatal("oczekiwano bledu dla niezamknietej sekcji")
	}
}

func TestResolveInterpolations_SimpleReference(t *testing.T) {
	cfg, err := ParseString("[a]\n-> base => /opt\n-> full => \"${a.base}/bin\"\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	if err := ResolveInterpolations(cfg); err != nil {
		t.Fatalf("ResolveInterpolations zwrocilo blad: %v", err)
	}
	v, _ := cfg.Get("a.full")
	s, _ := v.AsString()
	if s != "/opt/bin" {
		t.Fatalf("oczekiwano '/opt/bin', otrzymano %q", s)
	}
}

func TestResolveInterpolations_CyclicReference(t *testing.T) {
	cfg, err := ParseString("[a]\n-> x => \"${a.y}\"\n-> y => \"${a.x}\"\n")
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	if err := ResolveInterpolations(cfg); err == nil {
		t.Fatal("oczekiwano CyclicReferenceError")
	}
}

func TestSerialize_RoundTrip(t *testing.T) {
	original := "[account]\n-> type => user\n-> name => michal\n\n"
	cfg, err := ParseString(original)
	if err != nil {
		t.Fatalf("ParseString zwrocilo blad: %v", err)
	}
	out := Serialize(cfg)

	cfg2, err := ParseString(out)
	if err != nil {
		t.Fatalf("ParseString(Serialize(cfg)) zwrocilo blad: %v -- output:\n%s", err, out)
	}
	v, err := cfg2.Get("account.type")
	if err != nil {
		t.Fatalf("Get(account.type) po round-trip zwrocilo blad: %v", err)
	}
	s, _ := v.AsString()
	if s != "user" {
		t.Fatalf("oczekiwano 'user' po round-trip, otrzymano %q", s)
	}
}

func TestBuilder_FluentAPI(t *testing.T) {
	b := NewBuilder()
	b.Section("account").Set("type", String("user")).Set("name", String("michal"))
	b.Section("release").Set("name", String("trixie"))

	cfg := b.Build()
	v, err := cfg.Get("account.type")
	if err != nil {
		t.Fatalf("Get(account.type) zwrocilo blad: %v", err)
	}
	s, _ := v.AsString()
	if s != "user" {
		t.Fatalf("oczekiwano 'user', otrzymano %q", s)
	}
}
