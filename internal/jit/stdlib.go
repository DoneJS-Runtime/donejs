package jit

// stdlib.go installs the globals the bootstrap layer needs: globalThis, the
// Symbol constructor, the Object statics and the primitive constants. Bigger
// built-ins (Map, Set, Date, JSON) live beside their own files.

import (
	"math"
)

// globalThisHost proxies the VM's global namespace, so `globalThis.foo = x`
// and a later bare `foo` resolve to the same value.
type globalThisHost struct{ vm *VM }

func (g *globalThisHost) Call(args []Value) Value { return Undef() }
func (g *globalThisHost) Get(name string) Value {
	if v, ok := g.vm.lookupGlobal(name); ok {
		return v
	}
	return Undef()
}
func (g *globalThisHost) Set(name string, v Value) { g.vm.SetGlobal(name, v) }
func (g *globalThisHost) Str() string              { return "[object globalThis]" }
func (g *globalThisHost) Type() string             { return "object" }

// symVal is a Symbol value: it prints as `Symbol(desc)` and never coerces to
// a number, matching ToNumber(Symbol) throwing.
type symVal struct{ desc string }

func (s *symVal) Call(args []Value) Value  { return Undef() }
func (s *symVal) Get(name string) Value    { return Undef() }
func (s *symVal) Set(name string, v Value) {}
func (s *symVal) Str() string              { return "Symbol(" + s.desc + ")" }
func (s *symVal) Type() string             { return "symbol" }

var symRegistry = map[string]*symVal{}

// symbolFor returns the registered global symbol for a key, creating it on
// first use, so `Symbol.for(k) === Symbol.for(k)`.
func symbolFor(desc string) *symVal {
	if v, ok := symRegistry[desc]; ok {
		return v
	}
	v := &symVal{desc: "Symbol.for(" + desc + ")"}
	symRegistry[desc] = v
	return v
}

var wellKnownSyms = map[string]*symVal{
	"iterator":           &symVal{desc: "Symbol.iterator"},
	"asyncIterator":      &symVal{desc: "Symbol.asyncIterator"},
	"asyncDispose":       &symVal{desc: "Symbol.asyncDispose"},
	"hasInstance":        &symVal{desc: "Symbol.hasInstance"},
	"toPrimitive":        &symVal{desc: "Symbol.toPrimitive"},
	"toStringTag":        &symVal{desc: "Symbol.toStringTag"},
	"isConcatSpreadable": &symVal{desc: "Symbol.isConcatSpreadable"},
	"unscopables":        &symVal{desc: "Symbol.unscopables"},
	"match":              &symVal{desc: "Symbol.match"},
	"matchAll":           &symVal{desc: "Symbol.matchAll"},
	"replace":            &symVal{desc: "Symbol.replace"},
	"search":             &symVal{desc: "Symbol.search"},
	"split":              &symVal{desc: "Symbol.split"},
	"species":            &symVal{desc: "Symbol.species"},
}

// symForHost backs the static `Symbol.for(key)` method.
type symForHost struct{}

func (symForHost) Call(args []Value) Value {
	key := ""
	if len(args) > 0 {
		key = args[0].toStr()
	}
	return Host(symbolFor(key))
}
func (symForHost) Get(name string) Value    { return Host(&symForHost{}) }
func (symForHost) Set(name string, v Value) {}
func (symForHost) Str() string              { return "function Symbol.for() { [native code] }" }
func (symForHost) Type() string             { return "function" }

// symCtor is the global `Symbol` function, whose statics are the well-known
// symbols and the registry lookup.
type symCtor struct{}

func (symCtor) New(args []Value) Value { return Undef() }
func (symCtor) Call(args []Value) Value {
	desc := ""
	if len(args) > 0 {
		desc = args[0].toStr()
	}
	return Host(&symVal{desc: desc})
}

func (symCtor) Get(name string) Value {
	if v, ok := wellKnownSyms[name]; ok {
		return Host(v)
	}
	if name == "for" {
		return Host(&symForHost{})
	}
	return Undef()
}
func (symCtor) Set(name string, v Value) {}
func (symCtor) Str() string              { return "function Symbol() { [native code] }" }
func (symCtor) Type() string             { return "function" }

// installGlobals registers the interpreter-owned globals.
func (vm *VM) installGlobals() {
	vm.SetGlobal("globalThis", Host(&globalThisHost{vm: vm}))
	vm.SetGlobal("Symbol", Host(symCtor{}))
	vm.SetGlobal("Infinity", Num(math.Inf(1)))
	vm.SetGlobal("NaN", Num(math.NaN()))
	vm.SetGlobal("isNaN", BuiltinFn("isNaN", func(args []Value) Value {
		if len(args) == 0 {
			return Bool(true)
		}
		f := args[0].toNumber()
		return Bool(f != f)
	}))
	vm.SetGlobal("isFinite", BuiltinFn("isFinite", func(args []Value) Value {
		if len(args) == 0 {
			return Bool(false)
		}
		f := args[0].toNumber()
		return Bool(f == f && f != math.Inf(1) && f != math.Inf(-1))
	}))
	vm.SetGlobal("Object", vm.objectCtor())
	vm.SetGlobal("JSON", vm.jsonCtor())
	vm.SetGlobal("Date", ClassVal(dateClass))
	vm.SetGlobal("eval", BuiltinFn("eval", func(args []Value) Value {
		if len(args) == 0 {
			return Undef()
		}
		return args[0]
	}))
}

// objectCtor returns the global Object constructor with its statics.
func (vm *VM) objectCtor() Value {
	keys := map[string]Value{}
	def := func(name string, fn func([]Value) Value) {
		keys[name] = BuiltinFn(name, fn)
	}
	def("keys", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return ArrVal(NewArr(0))
		}
		out := NewArr(0)
		for _, k := range o.keys() {
			if o.pflag(k).enumerable {
				out.elems = append(out.elems, Str(k))
			}
		}
		return ArrVal(out)
	})
	def("assign", func(a []Value) Value {
		if len(a) == 0 {
			return Undef()
		}
		dst := a[0].AsObj()
		if dst == nil {
			return Undef()
		}
		for _, src := range a[1:] {
			if so := src.AsObj(); so != nil {
				for _, k := range so.keys() {
					if so.pflag(k).enumerable {
						dst.set(k, so.props[k])
					}
				}
			}
		}
		return a[0]
	})
	def("freeze", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return a[0]
		}
		o.extensible = false
		for k := range o.props {
			o.setf(k, propFlags{enumerable: true, writable: false, configurable: false})
		}
		return a[0]
	})
	def("seal", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return a[0]
		}
		for k := range o.props {
			o.setf(k, propFlags{enumerable: true, writable: true, configurable: false})
		}
		return a[0]
	})
	def("preventExtensions", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return a[0]
		}
		o.extensible = false
		return a[0]
	})
	def("isFrozen", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return Bool(false)
		}
		if o.extensible && len(o.props) > 0 {
			for k := range o.props {
				f := o.pflag(k)
				if f.writable || f.configurable {
					return Bool(false)
				}
			}
		}
		return Bool(!o.extensible)
	})
	def("isSealed", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return Bool(false)
		}
		// A sealed object is still extensible but every own prop is
		// non-configurable; a frozen object (not extensible) is not sealed.
		if !o.extensible {
			return Bool(false)
		}
		for k := range o.props {
			if o.pflag(k).configurable {
				return Bool(false)
			}
		}
		return Bool(true)
	})
	def("isExtensible", func(a []Value) Value {
		o := a[0].AsObj()
		return Bool(o != nil && o.extensible)
	})
	def("hasOwn", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		if o := a[0].AsObj(); o != nil {
			_, ok := o.props[a[1].toStr()]
			return Bool(ok)
		}
		return Bool(false)
	})
	def("entries", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return ArrVal(NewArr(0))
		}
		var out []Value
		for _, k := range o.keys() {
			v := o.props[k]
			pair := NewArr(2)
			pair.elems[0] = Str(k)
			pair.elems[1] = v
			out = append(out, ArrVal(pair))
		}
		return ArrVal(&Arr{elems: out})
	})
	def("values", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return ArrVal(NewArr(0))
		}
		out := make([]Value, 0, len(o.props))
		for _, k := range o.keys() {
			out = append(out, o.props[k])
		}
		return ArrVal(&Arr{elems: out})
	})
	def("fromEntries", func(a []Value) Value {
		o := NewObj()
		if arr := a[0].AsArr(); arr != nil {
			for _, e := range arr.elems {
				if p := e.AsArr(); p != nil && len(p.elems) >= 2 {
					o.set(p.elems[0].toStr(), p.elems[1])
				}
			}
		}
		return ObjVal(o)
	})
	def("create", func(a []Value) Value {
		o := NewObj()
		if len(a) > 0 {
			if po := a[0].AsObj(); po != nil {
				o.proto = po
			}
		}
		return ObjVal(o)
	})
	def("getPrototypeOf", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil || o.proto == nil {
			return Null()
		}
		return ObjVal(o.proto)
	})
	def("setPrototypeOf", func(a []Value) Value {
		if len(a) < 2 {
			return a[0]
		}
		if o := a[0].AsObj(); o != nil {
			o.proto = a[1].AsObj()
		}
		return a[0]
	})
	def("isPrototypeOf", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		v := a[0].AsObj()
		s := a[1].AsObj()
		if v == nil || s == nil {
			return Bool(false)
		}
		for p := s; p != nil; p = p.proto {
			if p == v {
				return Bool(true)
			}
		}
		return Bool(false)
	})
	def("defineProperty", func(a []Value) Value {
		if len(a) < 3 {
			return a[0]
		}
		o := a[0].AsObj()
		if o == nil {
			return a[0]
		}
		o.defineProp(a[1].toStr(), a[2])
		return a[0]
	})
	def("defineProperties", func(a []Value) Value {
		if len(a) < 2 {
			return a[0]
		}
		o := a[0].AsObj()
		if o == nil {
			return a[0]
		}
		if dv := a[1].AsObj(); dv != nil {
			for _, k := range dv.keys() {
				if dv.pflag(k).enumerable {
					o.defineProp(k, dv.props[k])
				}
			}
		}
		return a[0]
	})
	def("getOwnPropertyNames", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return ArrVal(NewArr(0))
		}
		out := NewArr(0)
		for _, k := range o.keys() {
			out.elems = append(out.elems, Str(k))
		}
		return ArrVal(out)
	})
	def("getOwnPropertyDescriptor", func(a []Value) Value {
		if len(a) < 2 {
			return Undef()
		}
		o := a[0].AsObj()
		if o == nil {
			return Undef()
		}
		v, ok := o.props[a[1].toStr()]
		if !ok {
			return Undef()
		}
		d := NewObj()
		if acc := o.accessorFor(a[1].toStr()); acc != nil && !acc.get.IsUndef() {
			d.set("get", acc.get)
			d.set("set", acc.set)
			return ObjVal(d)
		}
		d.set("value", v)
		d.set("writable", Bool(o.pflag(a[1].toStr()).writable))
		d.set("enumerable", Bool(o.pflag(a[1].toStr()).enumerable))
		d.set("configurable", Bool(o.pflag(a[1].toStr()).configurable))
		return ObjVal(d)
	})
	def("getOwnPropertyDescriptors", func(a []Value) Value {
		o := a[0].AsObj()
		if o == nil {
			return ObjVal(NewObj())
		}
		out := NewObj()
		for _, k := range o.keys() {
			v, ok := o.props[k]
			if !ok {
				continue
			}
			d := NewObj()
			if acc := o.accessorFor(k); acc != nil && !acc.get.IsUndef() {
				d.set("get", acc.get)
				d.set("set", acc.set)
			} else {
				d.set("value", v)
				d.set("writable", Bool(o.pflag(k).writable))
			}
			d.set("enumerable", Bool(o.pflag(k).enumerable))
			d.set("configurable", Bool(o.pflag(k).configurable))
			out.set(k, ObjVal(d))
		}
		return ObjVal(out)
	})
	def("is", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		return Bool(strictEq(a[0], a[1]))
	})
	ctor := builtin{name: "Object"}
	ctor.fn = func(a []Value) Value {
		if len(a) == 0 {
			o := NewObj()
			o.proto = vm.objectProto
			return ObjVal(o)
		}
		return a[0]
	}
	ctor.props = keys
	ctor.props["prototype"] = ObjVal(vm.objectProto)
	// Object.prototype: the home of the shared instance methods.
	op := vm.objectProto
	op.set("constructor", Value{tag: tFunc, ref: &ctor})
	op.set("toString", MethodFn("toString", func(a []Value) Value { return Str("[object Object]") }))
	op.set("valueOf", MethodFn("valueOf", func(a []Value) Value { return a[0] }))
	op.set("hasOwnProperty", MethodFn("hasOwnProperty", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		o := a[0].AsObj()
		return Bool(o != nil && o.hasOwn(a[1].toStr()))
	}))
	op.set("propertyIsEnumerable", MethodFn("propertyIsEnumerable", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		o := a[0].AsObj()
		return Bool(o != nil && o.hasOwn(a[1].toStr()) && o.pflag(a[1].toStr()).enumerable)
	}))
	op.set("isPrototypeOf", MethodFn("isPrototypeOf", func(a []Value) Value {
		if len(a) < 2 {
			return Bool(false)
		}
		v := a[0].AsObj()
		s := a[1].AsObj()
		if v == nil || s == nil {
			return Bool(false)
		}
		for p := s; p != nil; p = p.proto {
			if p == v {
				return Bool(true)
			}
		}
		return Bool(false)
	}))
	return Value{tag: tFunc, ref: &ctor}
}
