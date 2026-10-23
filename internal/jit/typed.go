package jit

// typed.go implements ArrayBuffer, Uint8Array and DataView — the binary views
// the runtime hands to JavaScript. Instances are Objs whose bytes field holds
// one backing array; views store an offset and length into that same slice so
// mutations alias exactly like JavaScript requires. Methods are bound to the
// instance at property-read time, the way array methods are.

import (
	"encoding/binary"
	"math"
	"strconv"
)

const (
	kindNone = iota
	kindAB
	kindU8
	kindDV
)

// installTyped registers the binary view constructors as globals.
func (vm *VM) installTyped() {
	vm.SetGlobal("ArrayBuffer", ClassVal(arrayBufferClass))
	vm.SetGlobal("Uint8Array", ClassVal(uint8Class))
	vm.SetGlobal("DataView", ClassVal(dataViewClass))
}

var (
	arrayBufferClass = newTypedClass("ArrayBuffer", kindAB, initArrayBuffer)
	uint8Class       = newTypedClass("Uint8Array", kindU8, initUint8Array)
	dataViewClass    = newTypedClass("DataView", kindDV, initDataView)
)

// newTypedClass builds a constructor class whose instances a Go function
// initializes in place of a JS constructor body.
func newTypedClass(name string, kind int, native func(o *Obj, args []Value) *ThrowError) *Class {
	c := &Class{
		Name: name,
		Ctor: builtinFn(name+".Ctor", func([]Value) Value {
			panic(&ThrowError{Value: strErr("TypeError", "Class "+name+" cannot be invoked without 'new'")})
		}),
		Proto:  NewObj(),
		Static: NewObj(),
		Typed:  kind,
		Native: native,
	}
	c.Static.set("BYTES_PER_ELEMENT", Num(1))
	if kind == kindAB {
		c.Static.set("isView", builtinFn("ArrayBuffer.isView", func(args []Value) Value {
			if len(args) == 0 {
				return Bool(false)
			}
			ob := args[0].AsObj()
			return Bool(ob != nil && (ob.kind == kindU8 || ob.kind == kindDV))
		}))
	}
	return c
}

// newTyped allocates an instance of a binary view class bound to bytes.
func newTyped(class *Class, kind int, bytes []byte, off, length int) *Obj {
	return &Obj{
		props:   make(map[string]Value, 4),
		class:   class,
		proto:   class.Proto,
		kind:    kind,
		bytes:   bytes,
		byteOff: off,
		byteLen: length,
	}
}

// toIndex implements JS ToIndex: NaN maps to zero, fractions truncate, and
// negative or huge values are rejected with the original value for the error
// message.
func toIndex(v Value) (int, bool) {
	n := v.toNumber()
	if math.IsNaN(n) {
		return 0, true
	}
	i := math.Trunc(n)
	if i < 0 || i > 9e15 {
		return 0, false
	}
	return int(i), true
}

// byteOf converts a JS value to a byte the way typed-array element
// conversion does: ToNumber, truncate, wrap modulo 256.
func byteOf(v Value) byte {
	n := v.toNumber()
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return byte(int64(n))
}

func rangeErr(msg string) *ThrowError {
	return &ThrowError{Value: strErr("RangeError", msg)}
}

func typeErrCtor(msg string) *ThrowError {
	return &ThrowError{Value: strErr("TypeError", msg)}
}

// clampRange resolves an optional (offset, length) argument pair against a
// buffer of the given size. kind selects the Node error text for a bad
// length: "typed" for typed-array views, "dataview" for DataView.
func clampRange(args []Value, argStart, total int, kind string) (int, int, *ThrowError) {
	off := 0
	if len(args) > argStart && !args[argStart].IsUndef() {
		var ok bool
		if off, ok = toIndex(args[argStart]); !ok {
			return 0, 0, rangeErr("Start offset " + numToStr(args[argStart].toNumber()) + " is outside the bounds of the buffer")
		}
	}
	length := total - off
	if len(args) > argStart+1 && !args[argStart+1].IsUndef() {
		var ok bool
		var n int
		if n, ok = toIndex(args[argStart+1]); !ok {
			if kind == "dataview" {
				return 0, 0, rangeErr("Invalid DataView length " + numToStr(args[argStart+1].toNumber()))
			}
			return 0, 0, rangeErr("Invalid typed array length: " + numToStr(args[argStart+1].toNumber()))
		}
		length = n
	}
	if off > total {
		return 0, 0, rangeErr("Start offset " + numToStr(float64(off)) + " is outside the bounds of the buffer")
	}
	if off+length > total {
		if kind == "dataview" {
			return 0, 0, rangeErr("Invalid DataView length " + numToStr(float64(length)))
		}
		return 0, 0, rangeErr("Invalid typed array length: " + numToStr(float64(length)))
	}
	return off, off + length, nil
}

func initArrayBuffer(o *Obj, args []Value) *ThrowError {
	n := 0
	if len(args) > 0 {
		var ok bool
		if n, ok = toIndex(args[0]); !ok {
			return rangeErr("Invalid array buffer length")
		}
	}
	o.kind = kindAB
	o.bytes = make([]byte, n)
	o.byteLen = n
	return nil
}

func initUint8Array(o *Obj, args []Value) *ThrowError {
	o.kind = kindU8
	if len(args) == 0 {
		return nil
	}
	a := args[0]
	if ob := a.AsObj(); ob != nil && ob.kind != kindNone {
		if ob.kind == kindAB {
			// An ArrayBuffer argument produces a view onto the same storage.
			off, end, te := clampRange(args, 1, len(ob.bytes), "typed")
			if te != nil {
				return te
			}
			o.bytes = ob.bytes
			o.byteOff, o.byteLen = off, end-off
			o.back = ob
			return nil
		}
		// Another typed view copies its window, as Node does.
		w := ob.window()
		off, end, te := clampRange(args, 1, len(w), "typed")
		if te != nil {
			return te
		}
		o.bytes = append([]byte(nil), w[off:end]...)
		o.byteLen = end - off
		return nil
	}
	if arr := a.AsArr(); arr != nil {
		o.bytes = make([]byte, len(arr.elems))
		o.byteLen = len(arr.elems)
		for i, e := range arr.elems {
			o.bytes[i] = byteOf(e)
		}
		return nil
	}
	if ob := a.AsObj(); ob != nil {
		// Array-like object: ToLength maps NaN and negatives to zero.
		ln := ob.get("length").toNumber()
		n := 0
		if !math.IsNaN(ln) && ln > 0 {
			n = int(math.Trunc(ln))
		}
		o.bytes = make([]byte, n)
		o.byteLen = n
		for i := 0; i < n; i++ {
			o.bytes[i] = byteOf(ob.get(strconv.Itoa(i)))
		}
		return nil
	}
	// Everything else converts through ToNumber then ToIndex: NaN gives an
	// empty array, strings and booleans produce their numeric value.
	n, ok := toIndex(a)
	if !ok {
		return rangeErr("Invalid typed array length: " + numToStr(a.toNumber()))
	}
	o.bytes = make([]byte, n)
	o.byteLen = n
	return nil
}

func initDataView(o *Obj, args []Value) *ThrowError {
	if len(args) == 0 {
		return typeErrCtor("First argument to DataView constructor must be an ArrayBuffer")
	}
	ob := args[0].AsObj()
	if ob == nil || ob.kind == kindNone {
		return typeErrCtor("First argument to DataView constructor must be an ArrayBuffer")
	}
	if ob.kind != kindAB {
		ob = ob.backing()
	}
	off, end, te := clampRange(args, 1, len(ob.bytes), "dataview")
	if te != nil {
		return te
	}
	o.kind = kindDV
	o.bytes = ob.bytes
	o.byteOff, o.byteLen = off, end-off
	o.back = ob
	return nil
}

// clampIdx resolves an optional index argument against a window length,
// honouring negative offsets like Array.prototype.slice does.
func clampIdx(v Value, def, length int) int {
	if v.IsUndef() {
		return def
	}
	i := int(v.toNumber())
	if i < 0 {
		i += length
	}
	if i < 0 {
		return 0
	}
	if i > length {
		return length
	}
	return i
}

// typedGet reads a property off a binary view: the view's own state, a
// numeric byte, or a bound method.
func (vm *VM) typedGet(o *Obj, name string) (Value, bool) {
	switch name {
	case "byteLength":
		return Num(float64(o.byteLen)), true
	case "byteOffset":
		if o.kind != kindAB {
			return Num(float64(o.byteOff)), true
		}
	case "buffer":
		if o.kind != kindAB {
			return ObjVal(o.backing()), true
		}
	case "length":
		if o.kind == kindU8 {
			return Num(float64(o.byteLen)), true
		}
	}
	if o.kind == kindU8 {
		if i, err := strconv.Atoi(name); err == nil {
			w := o.window()
			if i >= 0 && i < len(w) {
				return Num(float64(w[i])), true
			}
			return Undef(), true
		}
	}
	if b, ok := vm.typedMethod(o, name); ok {
		return b, true
	}
	return Undef(), false
}

// typedIndex reads a numeric or string-keyed index of a binary view.
func (vm *VM) typedIndex(o *Obj, k Value) Value {
	name := k.AsStr()
	if name == "" && k.tag == tNum {
		name = numToStr(k.n)
	}
	if o.kind == kindU8 {
		if i, err := strconv.Atoi(name); err == nil {
			w := o.window()
			if i >= 0 && i < len(w) {
				return Num(float64(w[i]))
			}
		}
	}
	if b, ok := vm.typedMethod(o, name); ok {
		return b
	}
	return Undef()
}

// typedSetIndex writes a byte of a Uint8Array; out-of-range writes are
// ignored the way JavaScript ignores them.
func typedSetIndex(o *Obj, k Value, v Value) bool {
	if o.kind != kindU8 {
		return false
	}
	name := k.AsStr()
	if name == "" && k.tag == tNum {
		name = numToStr(k.n)
	}
	i, err := strconv.Atoi(name)
	if err != nil {
		return false
	}
	w := o.window()
	if i >= 0 && i < len(w) {
		w[i] = byteOf(v)
	}
	return true
}

// u8Methods are the bound Uint8Array instance methods.
func (vm *VM) typedMethod(o *Obj, name string) (Value, bool) {
	if o.kind == kindAB {
		if name == "slice" {
			return builtinFn("ArrayBuffer.slice", func(args []Value) Value {
				w := o.window()
				off := clampIdx(argsAt(args, 0), 0, len(w))
				end := clampIdx(argsAt(args, 1), len(w), len(w))
				if end < off {
					end = off
				}
				return ObjVal(newTyped(arrayBufferClass, kindAB, append([]byte(nil), w[off:end]...), 0, end-off))
			}), true
		}
		return Undef(), false
	}
	if o.kind == kindU8 {
		return vm.u8Method(o, name)
	}
	return dvMethod(o, name)
}

func (vm *VM) u8Method(o *Obj, name string) (Value, bool) {
	switch name {
	case "fill":
		return builtinFn("Uint8Array.fill", func(args []Value) Value {
			if len(args) == 0 {
				panic(&ThrowError{Value: strErr("TypeError", "Missing value to fill with")})
			}
			b := byteOf(args[0])
			w := o.window()
			off := clampIdx(argsAt(args, 1), 0, len(w))
			end := clampIdx(argsAt(args, 2), len(w), len(w))
			for i := off; i < end; i++ {
				w[i] = b
			}
			return ObjVal(o)
		}), true
	case "subarray":
		return builtinFn("Uint8Array.subarray", func(args []Value) Value {
			w := o.window()
			off := clampIdx(argsAt(args, 0), 0, len(w))
			end := clampIdx(argsAt(args, 1), len(w), len(w))
			if end < off {
				end = off
			}
			return ObjVal(newTyped(uint8Class, kindU8, o.bytes, o.byteOff+off, end-off))
		}), true
	case "set":
		return builtinFn("Uint8Array.set", func(args []Value) Value {
			if len(args) == 0 {
				return Undef()
			}
			w := o.window()
			off := clampIdx(argsAt(args, 1), 0, len(w))
			var src []byte
			if sb := args[0].AsObj(); sb != nil && sb.kind != kindNone {
				src = sb.window()
			} else if arr := args[0].AsArr(); arr != nil {
				src = make([]byte, len(arr.elems))
				for i, e := range arr.elems {
					src[i] = byteOf(e)
				}
			} else {
				panic(&ThrowError{Value: strErr("TypeError", "Uint8Array.set expects an array or typed array")})
			}
			if off+len(src) > len(w) {
				panic(&ThrowError{Value: strErr("RangeError", "Offset is outside the bounds of the Uint8Array")})
			}
			copy(w[off:], src)
			return Undef()
		}), true
	case "slice":
		return builtinFn("Uint8Array.slice", func(args []Value) Value {
			w := o.window()
			off := clampIdx(argsAt(args, 0), 0, len(w))
			end := clampIdx(argsAt(args, 1), len(w), len(w))
			if end < off {
				end = off
			}
			b := append([]byte(nil), w[off:end]...)
			return ObjVal(newTyped(uint8Class, kindU8, b, 0, len(b)))
		}), true
	case "indexOf":
		return builtinFn("Uint8Array.indexOf", func(args []Value) Value {
			if len(args) == 0 {
				return Num(-1)
			}
			w := o.window()
			if i := bytesIndex(w, byteOf(args[0]), clampIdx(argsAt(args, 1), 0, len(w))); i >= 0 {
				return Num(float64(i))
			}
			return Num(-1)
		}), true
	case "copyWithin":
		return builtinFn("Uint8Array.copyWithin", func(args []Value) Value {
			w := o.window()
			if len(args) == 0 {
				return ObjVal(o)
			}
			dst := clampIdx(args[0], 0, len(w))
			src := clampIdx(argsAt(args, 1), dst, len(w))
			end := clampIdx(argsAt(args, 2), len(w), len(w))
			n := end - src
			if n > 0 {
				if dst+n > len(w) {
					n = len(w) - dst
				}
				copy(w[dst:dst+n], w[src:src+n])
			}
			return ObjVal(o)
		}), true
	case "join":
		return builtinFn("Uint8Array.join", func(args []Value) Value {
			sep := ","
			if len(args) > 0 && !args[0].IsUndef() {
				sep = args[0].toStr()
			}
			w := o.window()
			out := ""
			for i, b := range w {
				if i > 0 {
					out += sep
				}
				out += numToStr(float64(b))
			}
			return Str(out)
		}), true
	}
	return Undef(), false
}

// bytesIndex finds b in w starting at from.
func bytesIndex(w []byte, b byte, from int) int {
	for i := from; i < len(w); i++ {
		if w[i] == b {
			return i
		}
	}
	return -1
}

// argsAt returns args[i] or undefined — the argument accessor every native
// binding uses.
func argsAt(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return Undef()
}

// dvSpec describes one DataView accessor: its element size and flavour.
type dvSpec struct {
	size   int
	signed bool
	float  bool
	set    bool
}

var dvSpecs = map[string]dvSpec{
	"getUint8": {1, false, false, false}, "getUint16": {2, false, false, false},
	"getUint32": {4, false, false, false}, "getInt8": {1, true, false, false},
	"getInt16": {2, true, false, false}, "getInt32": {4, true, false, false},
	"getFloat32": {4, false, true, false}, "getFloat64": {8, false, true, false},
	"setUint8": {1, false, false, true}, "setUint16": {2, false, false, true},
	"setUint32": {4, false, false, true}, "setInt8": {1, true, false, true},
	"setInt16": {2, true, false, true}, "setInt32": {4, true, false, true},
	"setFloat32": {4, false, true, true}, "setFloat64": {8, false, true, true},
}

// dvMethod binds a DataView get/set accessor to its window.
func dvMethod(o *Obj, name string) (Value, bool) {
	spec, ok := dvSpecs[name]
	if !ok {
		return Undef(), false
	}
	return builtinFn("DataView."+name, func(args []Value) Value {
		off := clampIdx(argsAt(args, 0), 0, o.byteLen)
		le := false
		leArg := 1
		if spec.set {
			leArg = 2
		}
		if len(args) > leArg {
			le = args[leArg].truthy()
		}
		w := o.window()
		if off < 0 || off+spec.size > len(w) {
			panic(&ThrowError{Value: strErr("RangeError", "Offset is outside the bounds of the DataView")})
		}
		b := w[off : off+spec.size]
		var order binary.ByteOrder = binary.BigEndian
		if le {
			order = binary.LittleEndian
		}
		if spec.float {
			if spec.set {
				if spec.size == 4 {
					order.PutUint32(b, math.Float32bits(float32(args[1].toNumber())))
				} else {
					order.PutUint64(b, math.Float64bits(args[1].toNumber()))
				}
				return Undef()
			}
			if spec.size == 4 {
				return Num(float64(math.Float32frombits(order.Uint32(b))))
			}
			return Num(math.Float64frombits(order.Uint64(b)))
		}
		if spec.set {
			i := int64(args[1].toNumber())
			switch spec.size {
			case 1:
				b[0] = byte(i)
			case 2:
				order.PutUint16(b, uint16(i))
			case 4:
				order.PutUint32(b, uint32(i))
			}
			return Undef()
		}
		switch spec.size {
		case 1:
			if spec.signed {
				return Num(float64(int8(b[0])))
			}
			return Num(float64(b[0]))
		case 2:
			u := order.Uint16(b)
			if spec.signed {
				return Num(float64(int16(u)))
			}
			return Num(float64(u))
		}
		u := order.Uint32(b)
		if spec.signed {
			return Num(float64(int32(u)))
		}
		return Num(float64(u))
	}), true
}
