package hk

// Builder to pomocnicze, fluent API do programowego tworzenia HkConfig --
// uzywane przez hackeros-builder do wygenerowania gotowego pliku
// deb-ostree.hk bez recznego sklejania stringow.
//
// Przyklad uzycia:
//
//	b := hk.NewBuilder()
//	b.Section("account").
//	  Set("type", hk.String("user")).
//	  Set("name", hk.String("michal"))
//	hk.WriteFile("config.hk", b.Build())
type Builder struct {
	config *HkConfig
}

// NewBuilder tworzy nowy, pusty Builder.
func NewBuilder() *Builder {
	return &Builder{config: NewHkConfig()}
}

// Section zwraca SectionBuilder dla danej sekcji, tworzac ja jesli nie istnieje.
func (b *Builder) Section(name string) *SectionBuilder {
	var m *OrderedMap
	if existing, ok := b.config.Sections.Get(name); ok {
		m, _ = existing.AsMap()
	} else {
		m = NewOrderedMap()
		b.config.Sections.Set(name, MapValue(m))
	}
	return &SectionBuilder{m: m}
}

// Build zwraca skompletowany HkConfig gotowy do Serialize/WriteFile.
func (b *Builder) Build() *HkConfig {
	return b.config
}

// SectionBuilder to fluent API do wypelniania pojedynczej sekcji kluczami.
type SectionBuilder struct {
	m *OrderedMap
}

// Set wstawia klucz->wartosc na poziomie 1 tej sekcji. Zwraca samego siebie
// dla chainowania (b.Set(...).Set(...)).
func (s *SectionBuilder) Set(key string, val HkValue) *SectionBuilder {
	s.m.Set(key, val)
	return s
}

// SubMap tworzy (lub zwraca istniejaca) podmape pod danym kluczem i pozwala
// dalej budowac ja przez kolejny SectionBuilder -- odpowiada zagniezdzeniu
// "-->" w pliku .hk.
func (s *SectionBuilder) SubMap(key string) *SectionBuilder {
	var sub *OrderedMap
	if existing, ok := s.m.Get(key); ok {
		sub, _ = existing.AsMap()
	}
	if sub == nil {
		sub = NewOrderedMap()
		s.m.Set(key, MapValue(sub))
	}
	return &SectionBuilder{m: sub}
}
