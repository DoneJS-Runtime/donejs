package jit

import (
	"math"
	"strconv"
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
)

type Value struct {
	tag uint8
	n   float64
	ref any
}

func Undef() Value          { return Value{tag: tUndef} }
func Null() Value           { return Value{tag: tNull} }
func Bool(b bool) Value     { if b { return Value{tag: tBool, n: 1} }; return Value{tag: tBool} }
func Num(f float64) Value   { return Value{tag: tNum, n: f} }
func Str(s string) Value    { return Value{tag: tStr, ref: s} }
func ObjVal(o *Obj) Value      { return Value{tag: tObj, ref: o} }
func ArrVal(a *Arr) Value      { return Value{tag: tObj, ref: a} }
func Func(f *FuncProto) Value { return Value{tag: tFunc, ref: f} }

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
func (v Value) AsFunc() *FuncProto { if f, ok := v.ref.(*FuncProto); ok { return f }; return nil }
func (v Value) AsBuiltin() *builtin { if b, ok := v.ref.(*builtin); ok { return b }; return nil }

func (v Value) IsNum() bool   { return v.tag == tNum }
func (v Value) IsUndef() bool { return v.tag == tUndef }
func (v Value) IsNull() bool  { return v.tag == tNull || v.tag == tUndef }
func (v Value) IsObj() bool   { return v.tag == tObj }
func (v Value) AsStr() string { if s, ok := v.ref.(string); ok { return s }; return "" }

// truthy follows JS ToBoolean for the types we have.
func (v Value) truthy() bool {
	switch v.tag {
	case tUndef, tNull:
		return false
	case tBool:
		return v.n != 0
	case tNum:
		return v.n != 0 && !math.IsNaN(v.n)
	case tStr:
		return v.ref.(string) != ""
	case tObj:
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
	if a.tag == tStr || b.tag == tStr {
		return Str(a.toStr() + b.toStr())
	}
	return Num(a.toNumber() + b.toNumber())
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
		return trimInt(f)
	}
	return trimFloat(f)
}

func trimInt(f float64) string {
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
