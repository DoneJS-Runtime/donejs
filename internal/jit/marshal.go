package jit

// marshal.go converts between Go and JS values for host code. Natives in
// internal/vm marshal arguments with the Value methods (String, ToInteger,
// Get...) and results with FromGo.

import (
	"fmt"
	"strconv"
)

// FromGo wraps a Go value the way a foreign engine's ToValue did: nil becomes null, slices
// and maps become arrays and objects, and []byte becomes a Uint8Array.
func FromGo(x any) Value {
	switch t := x.(type) {
	case nil:
		return Null()
	case Value:
		return t
	case string:
		return Str(t)
	case bool:
		return Bool(t)
	case int:
		return Num(float64(t))
	case int8:
		return Num(float64(t))
	case int16:
		return Num(float64(t))
	case int32:
		return Num(float64(t))
	case int64:
		return Num(float64(t))
	case uint:
		return Num(float64(t))
	case uint8:
		return Num(float64(t))
	case uint16:
		return Num(float64(t))
	case uint32:
		return Num(float64(t))
	case uint64:
		return Num(float64(t))
	case float32:
		return Num(float64(t))
	case float64:
		return Num(t)
	case []byte:
		// Engine parity: a byte slice becomes a Uint8Array view.
		return Uint8ArrayValue(t)
	case []string:
		a := NewArr(len(t))
		for i, s := range t {
			a.elems[i] = Str(s)
		}
		return ArrVal(a)
	case []any:
		a := NewArr(len(t))
		for i, e := range t {
			a.elems[i] = FromGo(e)
		}
		return ArrVal(a)
	case map[string]any:
		o := NewObj()
		for k, e := range t {
			o.set(k, FromGo(e))
		}
		return ObjVal(o)
	case map[string]string:
		o := NewObj()
		for k, e := range t {
			o.set(k, Str(e))
		}
		return ObjVal(o)
	case error:
		return ErrObj("Error", t.Error())
	case func(args []Value) Value:
		return BuiltinFn("", t)
	case func() Value:
		return BuiltinFn("", func([]Value) Value { return t() })
	case func() bool:
		return BuiltinFn("", func([]Value) Value { return Bool(t()) })
	case func() int:
		return BuiltinFn("", func([]Value) Value { return Num(float64(t())) })
	case func() string:
		return BuiltinFn("", func([]Value) Value { return Str(t()) })
	default:
		// Ponytail: exotic Go types stringify; add reflection when a native
		// actually passes one.
		return Str(fmt.Sprint(x))
	}
}

// Export unwraps a JS value into plain Go data: undefined and null become nil,
// objects become maps, arrays become slices, binary views become []byte.
func (v Value) Export() any {
	switch v.tag {
	case tUndef, tNull:
		return nil
	case tBool:
		return v.n != 0
	case tNum:
		return v.n
	case tStr:
		return v.ref.(string)
	case tObj:
		switch o := v.ref.(type) {
		case *Arr:
			out := make([]any, len(o.elems))
			for i, e := range o.elems {
				out[i] = e.Export()
			}
			return out
		case *Obj:
			if o.kind != kindNone {
				return append([]byte(nil), o.window()...)
			}
			out := make(map[string]any, len(o.props))
			for k, pv := range o.props {
				out[k] = pv.Export()
			}
			return out
		}
	case tHost:
		if g, ok := v.ref.(interface{ ToGo() any }); ok {
			return g.ToGo()
		}
	}
	// Functions, classes and promises have no Go representation.
	return nil
}

// String renders the value the way the engine's Value.String did: JavaScript's
// ToString for primitives.
func (v Value) String() string { return v.toStr() }

// ToInteger converts to an integer the way JS ToInteger does: NaN and
// infinity become zero.
func (v Value) ToInteger() int64 {
	n := v.toNumber()
	if n != n || n > 9e18 || n < -9e18 {
		return 0
	}
	return int64(n)
}

// ToFloat converts to a number.
func (v Value) ToFloat() float64 { return v.toNumber() }

// ToBoolean converts to a boolean the way JS ToBoolean does.
func (v Value) ToBoolean() bool { return v.truthy() }

// IsObject reports whether v can carry properties: an object, array, host
// object or function — the values the engine's value type accepted.
func (v Value) IsObject() bool {
	return v.tag == tObj || v.tag == tHost || v.tag == tFunc
}

// Get reads a property, mirroring the engine's Object.Get. Accessor reads return
// undefined: invoking a getter needs a VM, which this layer does not hold.
// Read such properties from JS instead.
func (v Value) Get(name string) Value {
	if hv := v.AsHost(); hv != nil {
		return hv.Get(name)
	}
	if a := v.AsArr(); a != nil {
		if name == "length" {
			return Num(float64(len(a.elems)))
		}
		if i, err := strconv.Atoi(name); err == nil {
			return a.get(i)
		}
		return Undef()
	}
	if ob := v.AsObj(); ob != nil {
		if x := ob.accessorFor(name); x != nil {
			return Undef()
		}
		return ob.get(name)
	}
	return Undef()
}

// Set writes a property, mirroring the engine's Object.Set.
func (v Value) Set(name string, x any) {
	val := FromGo(x)
	if hv := v.AsHost(); hv != nil {
		hv.Set(name, val)
		return
	}
	if a := v.AsArr(); a != nil {
		if i, err := strconv.Atoi(name); err == nil {
			a.set(i, val)
		}
		return
	}
	if ob := v.AsObj(); ob != nil {
		if ob.kind == kindU8 {
			if i, err := strconv.Atoi(name); err == nil {
				w := ob.window()
				if i >= 0 && i < len(w) {
					w[i] = byteOf(val)
				}
				return
			}
		}
		ob.set(name, val)
	}
}

// Keys lists own property names: indices for arrays, keys for objects.
func (v Value) Keys() []string {
	if a := v.AsArr(); a != nil {
		out := make([]string, len(a.elems))
		for i := range a.elems {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	if ob := v.AsObj(); ob != nil {
		return ob.keys()
	}
	return nil
}

// ErrObj builds an Error-class object — the value a JS throw carries.
func ErrObj(kind, msg string) Value {
	return ObjVal(&Obj{props: map[string]Value{"name": Str(kind), "message": Str(msg)}})
}
