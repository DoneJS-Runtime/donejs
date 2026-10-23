package jit

// api.go collects the surface the host runtime (internal/vm) uses: building
// builtins, throwing, promises and scheduling.

import "fmt"

// BuiltinFn wraps a Go function as a callable value. fn may panic with a
// *ThrowError to throw into JS.
func BuiltinFn(name string, fn func(args []Value) Value) Value {
	return builtinFn(name, fn)
}

// Err builds a *ThrowError for the given error class, ready to panic with.
func Err(kind, msg string) *ThrowError { return &ThrowError{Value: ErrObj(kind, msg)} }

// Throw panics with a JS error of the given class.
func Throw(kind, msg string) { panic(Err(kind, msg)) }

// Throwf is Throw with printf-style formatting.
func Throwf(kind, format string, a ...any) { Throw(kind, fmt.Sprintf(format, a...)) }

// ThrowValue panics with an arbitrary JS value, so a native can rethrow.
func ThrowValue(v Value) { panic(&ThrowError{Value: v}) }

// Object returns a fresh empty object value.
func Object() Value { return ObjVal(NewObj()) }

// ArrayBufferValue wraps bytes as a JS ArrayBuffer.
func ArrayBufferValue(b []byte) Value {
	return ObjVal(newTyped(arrayBufferClass, kindAB, b, 0, len(b)))
}

// Uint8ArrayValue wraps bytes as a JS Uint8Array view over the slice.
func Uint8ArrayValue(b []byte) Value {
	return ObjVal(newTyped(uint8Class, kindU8, b, 0, len(b)))
}

// ArrOf builds an array value from its elements.
func ArrOf(vals ...Value) Value {
	a := NewArr(len(vals))
	copy(a.elems, vals)
	return ArrVal(a)
}

// Enqueue schedules fn on the interpreter's microtask queue; the host event
// loop drains it with RunJobs.
func (vm *VM) Enqueue(fn func()) { vm.enqueue(fn) }

// NewPromise creates a promise the host settles later. Call the returned
// settle functions on the interpreter goroutine, normally inside an Enqueue
// callback.
func (vm *VM) NewPromise() (*Promise, func(Value), func(Value)) {
	p := newPromise(vm)
	return p, p.resolve, p.reject
}

// Compile parses and compiles source into a callable prototype.
func Compile(name, src string) (*FuncProto, error) {
	pr, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return NewCompiler().Compile(pr), nil
}

// CallProtoErr runs a compiled prototype to completion with the given
// arguments, returning the error when the callee throws.
func (vm *VM) CallProtoErr(fn *FuncProto, args ...Value) (Value, error) {
	return vm.callThis(Func(fn), Undef(), args)
}

// IsCallable reports whether v can be called as a function.
func (v Value) IsCallable() bool { return isCallable(v) }

// Kind identifies the backing storage of a binary object.
type Kind int

const (
	KindNone Kind = iota
	KindArrayBuffer
	KindUint8Array
	KindDataView
)

// KindOf reports which binary kind o is (KindNone for plain objects).
func KindOf(o *Obj) Kind {
	if o == nil {
		return KindNone
	}
	switch o.kind {
	case kindAB:
		return KindArrayBuffer
	case kindU8:
		return KindUint8Array
	case kindDV:
		return KindDataView
	}
	return KindNone
}

// Bytes returns the visible byte window of a binary object.
func (o *Obj) Bytes() []byte { return o.window() }
