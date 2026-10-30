package jit

import (
	"math"
	"strconv"
	"strings"
)

// Tagged value. Numbers live entirely in the struct (no heap), which keeps the
// hot arithmetic path allocation-free. ref holds string/*Obj/*Func/undefined-ish
// payloads only.

const (
	tUndef uint8 = iota
	tNull
	tBool
	tNum
	tStr
	tObj
	tFunc
	tHost
)

type Value struct {
	tag uint8
	n   float64
	ref any
}

func Undef() Value { return Value{tag: tUndef} }
func Null() Value  { return Value{tag: tNull} }
func Bool(b bool) Value {
	if b {
		return Value{tag: tBool, n: 1}
	}
	return Value{tag: tBool}
}
func Num(f float64) Value     { return Value{tag: tNum, n: f} }
func Str(s string) Value      { return Value{tag: tStr, ref: s} }
func ObjVal(o *Obj) Value     { return Value{tag: tObj, ref: o} }
func ArrVal(a *Arr) Value     { return Value{tag: tObj, ref: a} }
func Func(f *FuncProto) Value { return Value{tag: tFunc, ref: f} }

// singleCharCache holds the 256 single-character strings so indexing into a
// string returns a shared value instead of allocating per access.
var singleCharCache = func() [256]string {
	var s [256]string
	for i := range s {
		s[i] = string([]byte{byte(i)})
	}
	return s
}()

// Closure binds a function to the captured variables of the frame it was
// created in. Functions with no captures are still wrapped so each function
// value gets its own `prototype` object, matching one function object per
// evaluation in JavaScript.
type Closure struct {
	Proto *FuncProto
	Caps  []*Value
	// ProtoObj is this function object's own `prototype` object, created on
	// first use. Distinct from every other function value of the same
	// prototype.
	ProtoObj *Obj
}

func ClosureVal(c *Closure) Value { return Value{tag: tFunc, ref: c} }

func (v Value) AsObj() *Obj {
	if o, ok := v.ref.(*Obj); ok {
		return o
	}
	return nil
}
func (v Value) AsArr() *Arr {
	if a, ok := v.ref.(*Arr); ok {
		return a
	}
	return nil
}
func (v Value) AsFunc() *FuncProto {
	switch f := v.ref.(type) {
	case *FuncProto:
		return f
	case *Closure:
		return f.Proto
	}
	return nil
}

// AsClosure returns the closure a function value was created with, or nil.
func (v Value) AsClosure() *Closure {
	if c, ok := v.ref.(*Closure); ok {
		return c
	}
	return nil
}

// FuncParts splits a callable into its prototype and its captures.
func (v Value) FuncParts() (*FuncProto, *Closure) {
	switch f := v.ref.(type) {
	case *Closure:
		return f.Proto, f
	case *FuncProto:
		return f, nil
	}
	return nil, nil
}
func (v Value) AsBuiltin() *builtin {
	if b, ok := v.ref.(*builtin); ok {
		return b
	}
	return nil
}

func (v Value) IsNum() bool   { return v.tag == tNum }
func (v Value) IsUndef() bool { return v.tag == tUndef }
func (v Value) IsNull() bool  { return v.tag == tNull || v.tag == tUndef }
func (v Value) IsObj() bool   { return v.tag == tObj }
func (v Value) IsBool() bool  { return v.tag == tBool }
func (v Value) IsStr() bool   { return v.tag == tStr }
func (v Value) IsFunc() bool  { return v.tag == tFunc }

// AsNum exposes the numeric payload (used by the host bridge).
func (v Value) AsNum() float64 { return v.n }

// AsBool exposes the boolean payload (used by the host bridge).
func (v Value) AsBool() bool { return v.n != 0 }

// AsString exposes the string payload (used by the host bridge).
func (v Value) AsString() string { return v.AsStr() }

// AsProto exposes the function prototype (used by the host bridge).
func (v Value) AsProto() *FuncProto { return v.AsFunc() }

// AsObject exposes the object payload (used by the host bridge).
func (v Value) AsObject() *Obj { return v.AsObj() }

// AsArray exposes the array payload (used by the host bridge).
func (v Value) AsArray() *Arr { return v.AsArr() }
func (v Value) AsStr() string {
	if v.tag == tStr {
		return v.ref.(string)
	}
	return ""
}

// truthy follows JS ToBoolean for the types we have.
func (v Value) truthy() bool {
	switch v.tag {
	case tUndef, tNull:
		return false
	case tBool:
		return v.n != 0
	case tNum:
		n := v.n
		return n != 0 && n == n
	case tStr:
		return v.ref.(string) != ""
	case tObj, tFunc, tHost:
		return true
	}
	return false
}

// toNumber follows ToNumber for the types we have.
func (v Value) toNumber() float64 {
	switch v.tag {
	case tNum:
		return v.n
	case tBool:
		if v.n != 0 {
			return 1
		}
		return 0
	case tUndef:
		return math.NaN()
	case tNull:
		return 0
	case tStr:
		return parseNum(v.ref.(string))
	case tHost:
		return math.NaN()
	}
	return math.NaN()
}

func parseNum(s string) float64 {
	var f float64
	neg := false
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	seen := false
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		f = f*10 + float64(s[i]-'0')
		seen = true
	}
	if i < len(s) && s[i] == '.' {
		i++
		scale := 0.1
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			f += float64(s[i]-'0') * scale
			scale *= 0.1
			seen = true
		}
	}
	if !seen {
		return math.NaN()
	}
	if neg {
		return -f
	}
	return f
}

// add implements the + operator (string concat or numeric add).
func add(a, b Value) Value {
	if a.tag == tStr && b.tag == tStr {
		return Str(a.AsStr() + b.AsStr())
	}
	if a.tag == tStr || b.tag == tStr {
		return Str(a.toStr() + b.toStr())
	}
	if a.tag == tNum && b.tag == tNum {
		return Num(a.n + b.n)
	}
	return Num(a.toNumber() + b.toNumber())
}

// sub implements the - operator (numeric sub, string concat fallback n/a).
func sub(a, b Value) Value {
	if a.tag == tNum && b.tag == tNum {
		return Num(a.n - b.n)
	}
	return Num(a.toNumber() - b.toNumber())
}

// mul implements the * operator.
func mul(a, b Value) Value {
	if a.tag == tNum && b.tag == tNum {
		return Num(a.n * b.n)
	}
	return Num(a.toNumber() * b.toNumber())
}

// div implements the / operator.
func div(a, b Value) Value {
	if a.tag == tNum && b.tag == tNum {
		return Num(a.n / b.n)
	}
	return Num(a.toNumber() / b.toNumber())
}

// mod implements the % operator.
func mod(a, b Value) Value {
	if a.tag == tNum && b.tag == tNum {
		return Num(math.Mod(a.n, b.n))
	}
	return Num(math.Mod(a.toNumber(), b.toNumber()))
}

// pow implements the ** operator.
func pow(a, b Value) Value {
	if a.tag == tNum && b.tag == tNum {
		return Num(math.Pow(a.n, b.n))
	}
	return Num(math.Pow(a.toNumber(), b.toNumber()))
}

func (v Value) toStr() string {
	switch v.tag {
	case tUndef:
		return "undefined"
	case tNull:
		return "null"
	case tBool:
		if v.n != 0 {
			return "true"
		}
		return "false"
	case tNum:
		return numToStr(v.n)
	case tStr:
		return v.ref.(string)
	case tObj:
		if a, ok := v.ref.(*Arr); ok {
			if len(a.elems) == 0 {
				return ""
			}
			if len(a.elems) == 1 {
				e := a.elems[0]
				if e.IsNull() {
					return ""
				}
				return e.toStr()
			}
			var sb strings.Builder
			for i, e := range a.elems {
				if i > 0 {
					sb.WriteString(",")
				}
				if !e.IsNull() {
					sb.WriteString(e.toStr())
				}
			}
			return sb.String()
		}
		return "[object Object]"
	case tHost:
		if h, ok := v.ref.(HostValue); ok {
			return h.Str()
		}
		return "[object Object]"
	}
	return ""
}

func numToStr(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if f == math.Inf(1) {
		return "Infinity"
	}
	if f == math.Inf(-1) {
		return "-Infinity"
	}
	if f == 0 {
		return "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return smallIntStr(f)
	}
	return trimFloat(f)
}

// smallIntCache covers the small integer range, the shape of the counters and
// array indices that dominate string conversions in hot loops; it skips the
// per-conversion allocation entirely.
var smallIntCache = func() [20001]string {
	var s [20001]string
	for i := range s {
		s[i] = strconv.FormatInt(int64(i)-10000, 10)
	}
	return s
}()

func smallIntStr(f float64) string {
	if f >= -10000 && f <= 10000 {
		return smallIntCache[int(f)+10000]
	}
	return trimInt(f)
}

func trimInt(f float64) string {
	// integral fast path: render exactly when the value fits an int64 so the
	// common integer case skips the per-digit float division loop.
	if f >= -9e18 && f < 9e18 {
		i := int64(f)
		if float64(i) == f {
			return strconv.FormatInt(i, 10)
		}
	}
	// integer fast path
	neg := f < 0
	if neg {
		f = -f
	}
	var buf [24]byte
	i := len(buf)
	for f >= 1 {
		d := int(f - math.Floor(f/10)*10)
		i--
		buf[i] = byte('0' + d)
		f = math.Floor(f / 10)
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
