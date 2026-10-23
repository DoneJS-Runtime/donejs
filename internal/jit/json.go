package jit

import (
	"encoding/json"
	"math"
	"strings"
)

// json.go implements the JSON built-in: parse and stringify over the jit value
// model.

func (vm *VM) jsonCtor() Value {
	keys := map[string]Value{}
	keys["parse"] = BuiltinFn("parse", func(a []Value) Value {
		if len(a) == 0 {
			return Undef()
		}
		text := a[0].toStr()
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			panic(&ThrowError{Value: ErrObj("SyntaxError", "Unexpected token in JSON")})
		}
		return jsonToValue(v)
	})
	keys["stringify"] = BuiltinFn("stringify", func(a []Value) Value {
		if len(a) == 0 {
			return Undef()
		}
		var sb strings.Builder
		jsonWrite(&sb, a[0], 0)
		return Str(sb.String())
	})
	ctor := builtin{name: "JSON"}
	ctor.fn = func(a []Value) Value { return Undef() }
	ctor.props = keys
	return Value{tag: tFunc, ref: &ctor}
}

// jsonToValue converts a decoded JSON value into a jit value.
func jsonToValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Null()
	case bool:
		return Bool(x)
	case string:
		return Str(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return Num(math.NaN())
		}
		return Num(f)
	case float64:
		return Num(x)
	case []any:
		arr := NewArr(len(x))
		for i, e := range x {
			arr.elems[i] = jsonToValue(e)
		}
		return ArrVal(arr)
	case map[string]any:
		o := NewObj()
		for k, e := range x {
			o.set(k, jsonToValue(e))
		}
		return ObjVal(o)
	}
	return Undef()
}

// jsonWrite renders a value the way JSON.stringify would. Undefined, functions
// and symbols drop out; numbers that are not finite render as null.
func jsonWrite(sb *strings.Builder, v Value, depth int) {
	if depth > 200 {
		sb.WriteString("null")
		return
	}
	switch v.tag {
	case tUndef, tFunc:
		sb.WriteString("null")
		return
	case tNull:
		sb.WriteString("null")
		return
	case tBool:
		if v.AsBool() {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
		return
	case tNum:
		f := v.toNumber()
		if f != f || f == math.Inf(1) || f == math.Inf(-1) {
			sb.WriteString("null")
			return
		}
		b, _ := json.Marshal(f)
		sb.Write(b)
		return
	case tStr:
		b, _ := json.Marshal(v.AsStr())
		sb.Write(b)
		return
	case tHost:
		// Host values have no JSON form unless they expose one.
		sb.WriteString("null")
		return
	}
	if a := v.AsArr(); a != nil {
		sb.WriteByte('[')
		for i, e := range a.elems {
			if i > 0 {
				sb.WriteByte(',')
			}
			if e.tag == tUndef || e.tag == tFunc || e.tag == tHost {
				sb.WriteString("null")
				continue
			}
			jsonWrite(sb, e, depth+1)
		}
		sb.WriteByte(']')
		return
	}
	if o := v.AsObj(); o != nil {
		sb.WriteByte('{')
		first := true
		for _, k := range o.keys() {
			e := o.props[k]
			if e.tag == tUndef || e.tag == tFunc || e.tag == tHost {
				continue
			}
			if !first {
				sb.WriteByte(',')
			}
			first = false
			kb, _ := json.Marshal(k)
			sb.Write(kb)
			sb.WriteByte(':')
			jsonWrite(sb, e, depth+1)
		}
		sb.WriteByte('}')
		return
	}
	sb.WriteString("null")
}

// jsonQuote is a helper for building JSON output strings.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
