package vm

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// registerIntl exposes the CLDR backed half of Intl.  Pure JS cannot evaluate
// CLDR plural rules or collate in multiple languages, so those live here; the
// number and date formatters read the generated locale tables directly.
func (rt *Runtime) registerIntl() {
	mod := rt.vm.NewObject()
	ops := opTable{
		"locale":       func(a []goja.Value) (any, error) { return intlLocaleRecord(argStr(a, 0)), nil },
		"hasLocale":    func(a []goja.Value) (any, error) { return intlHasLocale(argStr(a, 0)), nil },
		"localeList":   func(a []goja.Value) (any, error) { return intlLocaleList(), nil },
		"supplemental": func(a []goja.Value) (any, error) { return intlSupplementalText(), nil },
		"catalog":      func(a []goja.Value) (any, error) { return intlCatalog(argStr(a, 0)), nil },
		"canon":        func(a []goja.Value) (any, error) { return intlCanon(argStr(a, 0)), nil },
		"tagInfo":      func(a []goja.Value) (any, error) { return intlTagInfo(argStr(a, 0)), nil },
		"plural":       func(a []goja.Value) (any, error) { return intlPlural(a), nil },
		"categories":   func(a []goja.Value) (any, error) { return intlCategories(a), nil },
		"compare":      func(a []goja.Value) (any, error) { return intlCompare(a), nil },
		"localZone":    func(a []goja.Value) (any, error) { return intlLocalZone(), nil },
		"timeZone":     func(a []goja.Value) (any, error) { return intlTimeZone(argStr(a, 0)), nil },
		"zoneOffset": func(a []goja.Value) (any, error) {
			return intlZoneOffset(argStr(a, 0), int64(argNum(a, 1))), nil
		},	}
	mod.Set("sync", rt.syncDispatch(ops))
	rt.natives["__intl"] = mod
}

func intlLocaleRecord(name string) string {
	blob, ok := intlLocaleBlobs[name]
	if !ok {
		return ""
	}
	return intlInflate(blob)
}

// intlCanon canonicalises a BCP-47 tag, e.g. "EN_us" -> "en-US".
func intlCanon(tag string) string {
	t, err := language.Parse(strings.ReplaceAll(tag, "_", "-"))
	if err != nil {
		return ""
	}
	return t.String()
}

func intlTagInfo(tag string) string {
	t, err := language.Parse(strings.ReplaceAll(tag, "_", "-"))
	if err != nil {
		return ""
	}
	variants := []string{}
	for _, v := range t.Variants() {
		variants = append(variants, v.String())
	}
	region, _ := t.Region()
	script, _ := t.Script()
	regionName := region.String()
	scriptName := script.String()
	if regionName == "ZZ" {
		regionName = ""
	}
	if scriptName == "Zzzz" {
		scriptName = ""
	}
	base, _ := t.Base()
	return fmt.Sprintf(`{"language":%q,"script":%q,"region":%q,"variants":%q}`,
		base.String(), scriptName, regionName, strings.Join(variants, "-"))
}

func intlCatalog(key string) any {
	sup := intlSupplement()
	table, ok := sup[key]
	if !ok {
		return nil
	}
	list, ok := table.([]any)
	if !ok {
		return nil
	}
	return list
}

// ---------------------------------------------------------------------------
// Plural rules
// ---------------------------------------------------------------------------

// pluralValue holds the CLDR plural operands of a source number.
type pluralValue struct {
	n float64
	i int
	v int
	w int
	f int
	t int
	e int
}

type pluralCond func(ops pluralValue) bool

var (
	intlPluralMu     sync.Mutex
	intlPluralParsed = map[string][]pluralCond{}
	intlPluralCats   = map[string][]string{}
)

var pluralFormOrder = []string{"zero", "one", "two", "few", "many", "other"}

// intlSelectPlural evaluates the CLDR rule list of a language, returning the
// first matching category or "other".
func intlSelectPlural(kind, lang string, ops pluralValue) string {
	conds, cats := intlPluralRules(kind, lang)
	for i, cond := range conds {
		if cond(ops) {
			return cats[i]
		}
	}
	return "other"
}

func intlPluralCategories(kind, lang string) []string {
	_, cats := intlPluralRules(kind, lang)
	out := make([]string, len(cats))
	copy(out, cats)
	return out
}

// intlPluralRules returns the ordered rule list and its categories, parsing
// the rule text the first time a language is used.
func intlPluralRules(kind, lang string) ([]pluralCond, []string) {
	key := kind + "|" + lang
	intlPluralMu.Lock()
	defer intlPluralMu.Unlock()
	if conds, ok := intlPluralParsed[key]; ok {
		return conds, intlPluralCats[key]
	}
	sup := intlSupplement()
	table, _ := sup[lang].(map[string]any)
	// The data tables store cardinal rules under "c" and ordinal under "o".
	field := "c"
	if kind == "ordinal" {
		field = "o"
	}
	rules, _ := table[field].(map[string]any)
	var (
		conds []pluralCond
		cats  []string
	)
	for _, cat := range pluralFormOrder {
		text, ok := rules[cat].(string)
		if !ok {
			continue
		}
		cond, err := pluralParse(text)
		if err != nil {
			continue
		}
		conds = append(conds, cond)
		cats = append(cats, cat)
	}
	intlPluralParsed[key] = conds
	intlPluralCats[key] = cats
	return conds, cats
}

func intlPlural(a []goja.Value) string {
	lang := argStr(a, 0)
	ops := pluralValue{
		n: argsAt(a, 1).ToFloat(),
		i: int(argsAt(a, 2).ToInteger()),
		v: int(argsAt(a, 3).ToInteger()),
		w: int(argsAt(a, 4).ToInteger()),
		f: int(argsAt(a, 5).ToInteger()),
		t: int(argsAt(a, 6).ToInteger()),
		e: int(argsAt(a, 7).ToInteger()),
	}
	kind := argStr(a, 8)
	if kind != "ordinal" {
		kind = "cardinal"
	}
	// The operand tables index the integer part modulo 10^7 at most.
	if ops.i > 10000000 {
		ops.i %= 10000000
	}
	return intlSelectPlural(kind, lang, ops)
}

func intlCategories(a []goja.Value) []string {
	lang := argStr(a, 0)
	kind := argStr(a, 1)
	if kind != "ordinal" {
		kind = "cardinal"
	}
	return intlPluralCategories(kind, lang)
}

// --- parser ----------------------------------------------------------------

type pluralTok struct {
	kind byte // 'w' word, 'n' number, 's' symbol
	val  string
}

func pluralTokens(s string) []pluralTok {
	out := make([]pluralTok, 0, 8)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '%' || c == ',' || c == '=':
			out = append(out, pluralTok{'s', string(c)})
			i++
		case c == '!' && i+1 < len(s) && s[i+1] == '=':
			out = append(out, pluralTok{'s', "!="})
			i += 2
		case c == '.' && i+1 < len(s) && s[i+1] == '.':
			out = append(out, pluralTok{'s', ".."})
			i += 2
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			out = append(out, pluralTok{'n', s[i:j]})
			i = j
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < len(s) && (isAlnum(s[j])) {
				j++
			}
			out = append(out, pluralTok{'w', s[i:j]})
			i = j
		default:
			i++
		}
	}
	return out
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

type pluralParser struct {
	toks []pluralTok
	pos  int
}

var errPluralSyntax = fmt.Errorf("invalid plural rule")

func (p *pluralParser) peekSym(sym string) bool {
	return p.pos < len(p.toks) && p.toks[p.pos].kind == 's' && p.toks[p.pos].val == sym
}

func (p *pluralParser) peekWord(words ...string) bool {
	if p.pos >= len(p.toks) || p.toks[p.pos].kind != 'w' {
		return false
	}
	for _, w := range words {
		if p.toks[p.pos].val == w {
			return true
		}
	}
	return false
}

func (p *pluralParser) next() (pluralTok, error) {
	if p.pos >= len(p.toks) {
		return pluralTok{}, errPluralSyntax
	}
	tok := p.toks[p.pos]
	p.pos++
	return tok, nil
}

// pluralParse compiles one CLDR rule into a predicate over the operands.
// Grammar: condition := and ('or' and)* ; and := rel ('and' rel)* ;
// rel := expr [mod N] ('='|'!='|'in'|'within') ranges | expr [mod N]
func pluralParse(text string) (pluralCond, error) {
	p := &pluralParser{toks: pluralTokens(text)}
	cond, err := p.condition()
	if err != nil {
		return nil, err
	}
	return cond, nil
}

func (p *pluralParser) condition() (pluralCond, error) {
	left, err := p.andCondition()
	if err != nil {
		return nil, err
	}
	for p.peekWord("or") {
		p.next()
		right, err := p.andCondition()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(o pluralValue) bool { return l(o) || r(o) }
	}
	return left, nil
}

func (p *pluralParser) andCondition() (pluralCond, error) {
	left, err := p.relation()
	if err != nil {
		return nil, err
	}
	for p.peekWord("and") {
		p.next()
		right, err := p.relation()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(o pluralValue) bool { return l(o) && r(o) }
	}
	return left, nil
}

func (p *pluralParser) relation() (pluralCond, error) {
	op, err := p.next()
	if err != nil || op.kind != 'w' {
		return nil, errPluralSyntax
	}
	field := op.val
	mod := 0.0
	if p.peekWord("mod") || p.peekSym("%") {
		p.next()
		n, err := p.next()
		if err != nil || n.kind != 'n' {
			return nil, errPluralSyntax
		}
		mod, _ = strconv.ParseFloat(n.val, 64)
	}
	get := pluralOperand(field, mod)
	negate := false
	switch {
	case p.peekSym("!="):
		p.next()
		negate = true
	case p.peekSym("="):
		p.next()
	case p.peekWord("in", "within"):
		p.next()
	default:
		// A bare operand is a test for a non zero value.
		return func(o pluralValue) bool { return get(o) != 0 }, nil
	}
	ranges, err := p.rangeList()
	if err != nil {
		return nil, err
	}
	match := func(o pluralValue) bool {
		v := get(o)
		for _, r := range ranges {
			if v >= r[0] && v <= r[1] {
				return true
			}
		}
		return false
	}
	if negate {
		return func(o pluralValue) bool { return !match(o) }, nil
	}
	return match, nil
}

func (p *pluralParser) rangeList() ([][2]float64, error) {
	var out [][2]float64
	for {
		lo, err := p.next()
		if err != nil || lo.kind != 'n' {
			return nil, errPluralSyntax
		}
		loVal, _ := strconv.ParseFloat(lo.val, 64)
		hiVal := loVal
		if p.peekSym("..") {
			p.next()
			hi, err := p.next()
			if err != nil || hi.kind != 'n' {
				return nil, errPluralSyntax
			}
			hiVal, _ = strconv.ParseFloat(hi.val, 64)
		}
		out = append(out, [2]float64{loVal, hiVal})
		if !p.peekSym(",") {
			return out, nil
		}
		p.next()
	}
}

func pluralOperand(field string, mod float64) func(pluralValue) float64 {
	return func(o pluralValue) float64 {
		var v float64
		switch field {
		case "i":
			v = float64(o.i)
		case "v":
			v = float64(o.v)
		case "w":
			v = float64(o.w)
		case "f":
			v = float64(o.f)
		case "t":
			v = float64(o.t)
		case "e", "c":
			v = float64(o.e)
		default:
			v = o.n
		}
		if mod > 0 {
			return math.Mod(v, mod)
		}
		return v
	}
}

// ---------------------------------------------------------------------------
// Collation
// ---------------------------------------------------------------------------

var (
	intlCollateMu   sync.Mutex
	intlCollateMemo = map[string]*collate.Collator{}
)

func intlCollator(lang, sensitivity string, numeric bool) *collate.Collator {
	key := lang + "|" + sensitivity + "|" + strconv.FormatBool(numeric)
	intlCollateMu.Lock()
	defer intlCollateMu.Unlock()
	if c, ok := intlCollateMemo[key]; ok {
		return c
	}
	tag, err := language.Parse(strings.ReplaceAll(lang, "_", "-"))
	if err != nil {
		tag = language.Make("en")
	}
	opts := []collate.Option{}
	switch sensitivity {
	case "base":
		opts = append(opts, collate.IgnoreCase, collate.IgnoreDiacritics)
	case "accent":
		opts = append(opts, collate.IgnoreCase)
	case "case":
		opts = append(opts, collate.IgnoreDiacritics)
	}
	if numeric {
		opts = append(opts, collate.Numeric)
	}
	c := collate.New(tag, opts...)
	if len(intlCollateMemo) < 128 {
		intlCollateMemo[key] = c
	}
	return c
}

func intlCompare(a []goja.Value) int {
	lang := argStr(a, 0)
	left := argStr(a, 1)
	right := argStr(a, 2)
	sensitivity := argStr(a, 3)
	numeric := argBool(a, 4)
	return intlCollator(lang, sensitivity, numeric).CompareString(left, right)
}

// ---------------------------------------------------------------------------
// Supplemental tables
// ---------------------------------------------------------------------------

var (
	intlSuppOnce       sync.Mutex
	intlSupplementData = map[string]any{}
)

func intlSupplement() map[string]any {
	intlSuppOnce.Lock()
	defer intlSuppOnce.Unlock()
	if len(intlSupplementData) == 0 {
		var out map[string]any
		if err := json.Unmarshal([]byte(intlSupplementalText()), &out); err == nil {
			intlSupplementData = out
		}
	}
	return intlSupplementData
}
