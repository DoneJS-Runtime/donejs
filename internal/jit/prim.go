package jit

import (
	"math"
	"strconv"
	"strings"
)

// prim.go implements the primitive-wrapper constructors registered as globals:
// String, Number, Boolean and Array, with their statics.

// installPrim registers the primitive wrapper globals.
func (vm *VM) installPrim() {
	vm.SetGlobal("String", vm.vmStringCtor())
	vm.SetGlobal("Number", vm.vmNumberCtor())
	vm.SetGlobal("Boolean", vm.vmBooleanCtor())
	vm.SetGlobal("Array", vm.vmArrayCtor())
	vm.SetGlobal("parseInt", BuiltinFn("parseInt", func(a []Value) Value { return Num(parseIntGlob(a)) }))
	vm.SetGlobal("parseFloat", BuiltinFn("parseFloat", func(a []Value) Value { return Num(parseFloatGlob(a)) }))
}

func parseIntGlob(a []Value) float64 {
	if len(a) == 0 {
		return math.NaN()
	}
	s := strings.TrimSpace(a[0].toStr())
	radix := 0
	if len(a) > 1 {
		radix = int(a[1].toNumber())
	}
	i := 0
	sign := 1.0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		if s[i] == '-' {
			sign = -1
		}
		i++
	}
	if i < len(s) && s[i] == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
		radix = 16
		i += 2
	}
	base := radix
	if base == 0 {
		base = 10
	}
	digits := 0
	var val float64
	for ; i < len(s); i++ {
		d := digitVal(s[i], base)
		if d < 0 {
			break
		}
		val = val*float64(base) + float64(d)
		digits++
	}
	if digits == 0 {
		return math.NaN()
	}
	return sign * val
}

// digitVal returns the numeric value of a digit c in the given base, or -1.
func digitVal(c byte, base int) int {
	var d int
	switch {
	case c >= '0' && c <= '9':
		d = int(c - '0')
	case c >= 'a' && c <= 'z':
		d = int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		d = int(c-'A') + 10
	default:
		return -1
	}
	if d >= base {
		return -1
	}
	return d
}

func parseFloatGlob(a []Value) float64 {
	if len(a) == 0 {
		return math.NaN()
	}
	s := strings.TrimSpace(a[0].toStr())
	// Reuse strconv for the common numeric forms; it mirrors the spec's
	// leading-numeric-token behavior for the subset the stdlib needs.
	neg := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if strings.HasPrefix(strings.ToLower(s), "inf") {
		if neg {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if strings.HasPrefix(strings.ToLower(s), "nan") {
		return math.NaN()
	}
	// Consume the leading numeric token (digits, decimal point, exponent).
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || ((c == '+' || c == '-') && i > 0 && (s[i-1] == 'e' || s[i-1] == 'E')) {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return math.NaN()
	}
	if neg {
		f = -f
	}
	return f
}

// vmStringCtor returns the global String constructor.
func (vm *VM) vmStringCtor() Value {
	keys := map[string]Value{}
	keys["fromCharCode"] = BuiltinFn("fromCharCode", func(a []Value) Value { return Str(string(fromCharCodes(a))) })
	keys["fromCodePoint"] = BuiltinFn("fromCodePoint", func(a []Value) Value { return Str(string(fromCodePoints(a))) })
	keys["raw"] = BuiltinFn("raw", func(a []Value) Value { return Undef() })
	c := builtin{name: "String"}
	c.fn = func(a []Value) Value { return Str(strArg(a)) }
	c.props = keys
	return Value{tag: tFunc, ref: &c}
}

func strArg(a []Value) string {
	if len(a) > 0 {
		return a[0].toStr()
	}
	return ""
}

func numArg(a []Value) float64 {
	if len(a) > 0 {
		return a[0].toNumber()
	}
	return math.NaN()
}

func fromCharCodes(a []Value) string {
	var sb []rune
	for _, v := range a {
		if v.tag == tNum {
			sb = append(sb, rune(int(v.toNumber())))
		}
	}
	return string(sb)
}

// fromCodePoints builds a string from Unicode code points, pairing code points
// above the BMP into a surrogate pair the way String.fromCodePoint does.
func fromCodePoints(a []Value) []rune {
	var sb []rune
	for _, v := range a {
		if v.tag != tNum {
			continue
		}
		cp := v.toNumber()
		if cp < 0 || cp > 0x10ffff || math.Trunc(cp) != cp {
			continue
		}
		if cp <= 0xffff {
			sb = append(sb, rune(int(cp)))
			continue
		}
		n := int64(cp) - 0x10000
		sb = append(sb, rune(0xd800+(n>>10)), rune(0xdc00+(n&0x3ff)))
	}
	return sb
}

// vmNumberCtor returns the global Number constructor.
func (vm *VM) vmNumberCtor() Value {
	vm.numberProto = NewObj()
	props := map[string]Value{
		"isInteger": BuiltinFn("isInteger", func(a []Value) Value {
			return Bool(numArg(a) == math.Trunc(numArg(a)) && numArg(a) >= -9007199254740991 && numArg(a) <= 9007199254740991)
		}),
		"isNaN": BuiltinFn("isNaN", func(a []Value) Value { return Bool(numArg(a) != numArg(a)) }),
		"isFinite": BuiltinFn("isFinite", func(a []Value) Value {
			return Bool(numArg(a) == numArg(a) && numArg(a) != math.Inf(1) && numArg(a) != math.Inf(-1))
		}),
		"isSafeInteger": BuiltinFn("isSafeInteger", func(a []Value) Value {
			n := numArg(a)
			return Bool(n == math.Trunc(n) && n >= -9007199254740991 && n <= 9007199254740991)
		}),
		"MAX_SAFE_INTEGER":  BuiltinFn("MAX_SAFE_INTEGER", func(a []Value) Value { return Num(9007199254740991) }),
		"MIN_SAFE_INTEGER":  BuiltinFn("MIN_SAFE_INTEGER", func(a []Value) Value { return Num(-9007199254740991) }),
		"EPSILON":           BuiltinFn("EPSILON", func(a []Value) Value { return Num(math.Ldexp(1, -52)) }),
		"MAX_VALUE":         BuiltinFn("MAX_VALUE", func(a []Value) Value { return Num(math.MaxFloat64) }),
		"MIN_VALUE":         BuiltinFn("MIN_VALUE", func(a []Value) Value { return Num(math.SmallestNonzeroFloat64) }),
		"POSITIVE_INFINITY": BuiltinFn("POSITIVE_INFINITY", func(a []Value) Value { return Num(math.Inf(1)) }),
		"NEGATIVE_INFINITY": BuiltinFn("NEGATIVE_INFINITY", func(a []Value) Value { return Num(math.Inf(-1)) }),
		"NaN":               BuiltinFn("NaN", func(a []Value) Value { return Num(math.NaN()) }),
		"parseInt": BuiltinFn("parseInt", func(a []Value) Value {
			return Num(math.NaN())
		}),
		"parseFloat": BuiltinFn("parseFloat", func(a []Value) Value {
			return Num(math.NaN())
		}),
	}
	n := builtin{name: "Number"}
	n.fn = func(a []Value) Value { return Num(numArg(a)) }
	n.props = props
	return Value{tag: tFunc, ref: &n}
}

// vmBooleanCtor returns the global Boolean constructor.
func (vm *VM) vmBooleanCtor() Value {
	b := builtin{name: "Boolean"}
	b.fn = func(a []Value) Value { return Bool(len(a) > 0 && a[0].truthy()) }
	b.props = map[string]Value{}
	return Value{tag: tFunc, ref: &b}
}

// vmArrayCtor returns the global Array constructor with its statics.
func (vm *VM) vmArrayCtor() Value {
	props := map[string]Value{
		"isArray": BuiltinFn("isArray", func(a []Value) Value { return Bool(a[0].AsArr() != nil) }),
		"from": BuiltinFn("from", func(a []Value) Value {
			arr := NewArr(0)
			if len(a) == 0 {
				return ArrVal(arr)
			}
			src := a[0]
			switch {
			case src.AsArr() != nil:
				arr.elems = append(arr.elems, src.AsArr().elems...)
			case src.tag == tStr:
				s := src.AsStr()
				for i := 0; i < len(s); i++ {
					arr.elems = append(arr.elems, Str(string(s[i])))
				}
			case src.AsObj() != nil:
				o := src.AsObj()
				for _, k := range o.keys() {
					arr.elems = append(arr.elems, o.get(k))
				}
			default:
				// Iterables that are host values (Map, Set, iterators).
				_ = vm.spreadIterable(src, func(v Value) { arr.elems = append(arr.elems, v) })
			}
			return ArrVal(arr)
		}),
		"of": BuiltinFn("of", func(a []Value) Value {
			arr := NewArr(0)
			arr.elems = append(arr.elems, a...)
			return ArrVal(arr)
		}),
	}
	c := builtin{name: "Array"}
	c.fn = func(a []Value) Value {
		arr := NewArr(0)
		if len(a) == 1 {
			arr.elems = make([]Value, int(a[0].toNumber()))
		}
		for _, e := range a {
			arr.elems = append(arr.elems, e)
		}
		return ArrVal(arr)
	}
	c.props = props
	return Value{tag: tFunc, ref: &c}
}
