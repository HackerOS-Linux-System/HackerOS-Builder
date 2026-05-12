package hk

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type ValueKind int

const (
	KindString ValueKind = iota
	KindNumber
	KindBool
	KindArray
	KindMap
)

type Value struct {
	Kind  ValueKind
	Str   string
	Num   float64
	Bool  bool
	Arr   []Value
	Map   map[string]Value
	Order []string
}

// ── Konstruktory ──────────────────────────────────────────────────────────────

func Str(s string) Value  { return Value{Kind: KindString, Str: s} }
func Num(n float64) Value { return Value{Kind: KindNumber, Num: n} }
func Bool(b bool) Value   { return Value{Kind: KindBool, Bool: b} }
func Arr(a []Value) Value { return Value{Kind: KindArray, Arr: a} }
func Map(m map[string]Value, order []string) Value {
	return Value{Kind: KindMap, Map: m, Order: order}
}

func emptyMap() Value {
	return Map(make(map[string]Value), nil)
}

// ── Gettery ───────────────────────────────────────────────────────────────────

func (v Value) AsString() string {
	switch v.Kind {
	case KindString:
		return v.Str
	case KindNumber:
		if v.Num == float64(int64(v.Num)) {
			return strconv.FormatInt(int64(v.Num), 10)
		}
		return strconv.FormatFloat(v.Num, 'f', -1, 64)
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	}
	return ""
}

func (v Value) AsStringOr(def string) string {
	s := v.AsString()
	if s == "" {
		return def
	}
	return s
}

func (v Value) AsBool() bool {
	if v.Kind == KindBool {
		return v.Bool
	}
	lower := strings.ToLower(v.Str)
	return lower == "true" || lower == "1" || lower == "yes"
}

func (v Value) AsStringSlice() []string {
	if v.Kind == KindArray {
		out := make([]string, 0, len(v.Arr))
		for _, a := range v.Arr {
			s := strings.TrimSpace(a.AsString())
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	s := strings.TrimSpace(v.AsString())
	if s == "" {
		return nil
	}
	return []string{s}
}

func (v Value) Get(key string) (Value, bool) {
	if v.Kind != KindMap {
		return Value{}, false
	}
	val, ok := v.Map[key]
	return val, ok
}

func (v Value) GetOr(key string, def Value) Value {
	val, ok := v.Get(key)
	if !ok {
		return def
	}
	return val
}

func (v Value) IsZero() bool {
	return v.Kind == KindString && v.Str == ""
}

// ── Config ────────────────────────────────────────────────────────────────────

type Config struct {
	Sections map[string]Value
	Order    []string
}

func NewConfig() *Config {
	return &Config{Sections: make(map[string]Value)}
}

func (c *Config) Section(name string) Value {
	v, ok := c.Sections[name]
	if !ok {
		return emptyMap()
	}
	return v
}

func (c *Config) GetString(section, key, def string) string {
	sec := c.Section(section)
	val, ok := sec.Get(key)
	if !ok {
		return def
	}
	return val.AsStringOr(def)
}

func (c *Config) GetBool(section, key string, def bool) bool {
	sec := c.Section(section)
	val, ok := sec.Get(key)
	if !ok {
		return def
	}
	return val.AsBool()
}

func (c *Config) GetStringSlice(section, key string, def []string) []string {
	sec := c.Section(section)
	val, ok := sec.Get(key)
	if !ok {
		return def
	}
	s := val.AsStringSlice()
	if len(s) == 0 {
		return def
	}
	return s
}

// ── Parser ────────────────────────────────────────────────────────────────────

type parser struct {
	lines   []string
	cfg     *Config
	section string
	stack   []map[string]Value
	orders  [][]string
}

func Parse(text string) (*Config, error) {
	p := &parser{
		lines: strings.Split(text, "\n"),
		cfg:   NewConfig(),
	}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.cfg, nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("nie można odczytać %s: %w", path, err)
	}
	return Parse(string(data))
}

func (p *parser) parse() error {
	for _, raw := range p.lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") {
			continue
		}
		// Sekcja
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			p.enterSection(name)
			continue
		}
		// Klucz
		if strings.HasPrefix(line, "-") && strings.Contains(line, ">") {
			p.parseLine(line)
		}
	}
	p.syncSection()
	return nil
}

func (p *parser) enterSection(name string) {
	p.syncSection()
	p.section = name
	if _, ok := p.cfg.Sections[name]; !ok {
		p.cfg.Sections[name] = emptyMap()
		p.cfg.Order = append(p.cfg.Order, name)
	}
	sec := p.cfg.Sections[name]
	p.stack = []map[string]Value{sec.Map}
	p.orders = [][]string{sec.Order}
}

func (p *parser) syncSection() {
	if p.section == "" || len(p.stack) == 0 {
		return
	}
	existing := p.cfg.Sections[p.section]
	existing.Map = p.stack[0]
	if len(p.orders) > 0 {
		existing.Order = p.orders[0]
	}
	p.cfg.Sections[p.section] = existing
}

func (p *parser) parseLine(line string) {
	depth := 0
	i := 0
	for i < len(line) && line[i] == '-' {
		depth++
		i++
	}
	if i >= len(line) || line[i] != '>' {
		return
	}
	i++
	rest := strings.TrimSpace(line[i:])

	var key, valRaw string
	hasVal := false
	if idx := strings.Index(rest, " => "); idx >= 0 {
		key = strings.TrimSpace(rest[:idx])
		valRaw = strings.TrimSpace(rest[idx+4:])
		hasVal = true
	} else {
		key = strings.TrimSpace(rest)
	}

	if p.section == "" {
		p.enterSection("__root__")
	}

	targetDepth := depth - 1
	for len(p.stack) > targetDepth+1 {
		p.stack = p.stack[:len(p.stack)-1]
		p.orders = p.orders[:len(p.orders)-1]
	}

	currentMap := p.stack[len(p.stack)-1]
	currentOrder := &p.orders[len(p.orders)-1]

	if hasVal {
		val := p.parseValue(valRaw)
		if _, exists := currentMap[key]; !exists {
			*currentOrder = append(*currentOrder, key)
		}
		currentMap[key] = val
	} else {
		if _, ok := currentMap[key]; !ok {
			newM := make(map[string]Value)
			currentMap[key] = Map(newM, nil)
			*currentOrder = append(*currentOrder, key)
			p.stack = append(p.stack, newM)
			p.orders = append(p.orders, nil)
		}
	}

	p.syncSection()
}

func (p *parser) parseValue(raw string) Value {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := raw[1 : len(raw)-1]
		var items []Value
		for _, part := range strings.Split(inner, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				items = append(items, p.parseScalar(part))
			}
		}
		return Arr(items)
	}
	return p.parseScalar(raw)
}

func (p *parser) parseScalar(raw string) Value {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) && len(raw) >= 2 {
		s := raw[1 : len(raw)-1]
		s = strings.ReplaceAll(s, `\n`, "\n")
		s = strings.ReplaceAll(s, `\t`, "\t")
		s = strings.ReplaceAll(s, `\"`, `"`)
		return Str(s)
	}
	lower := strings.ToLower(raw)
	if lower == "true" {
		return Bool(true)
	}
	if lower == "false" {
		return Bool(false)
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		return Num(n)
	}
	return Str(raw)
}
