package jit

import (
	"math"
	"strings"
)

// Obj is a plain string-keyed object. Property access goes through an inline
// cache (see vm.go) so the hot path is a single map hit plus a shape check.

type Obj struct {
	props map[string]Value
	// order tracks insertion order of props so Object.keys, JSON.stringify and
	// for-in report names in the order JS defines them.
	order []string
	proto *Obj
	class *Class
	// accessors holds get/set accessors declared with `get`/`set` syntax in
	// a class body or object literal. The entry exists only when at least one
	// of the two is present.
	accessors map[string]*Accessor
	// kind marks binary views: kindAB, kindU8 or kindDV (typed.go). Plain
	// objects leave it kindNone and the three fields below unused.
	kind int
	// bytes is the full backing array; byteOff/byteLen bound the view into
	// it, so views alias their buffer the way JavaScript requires.
	bytes   []byte
	byteOff int
	byteLen int
	// back is the owning ArrayBuffer of a view, created on first `.buffer`
	// read and shared from then on.
	back *Obj
	// extensible records whether new own properties may still be added; it is
	// what Object.freeze / Object.preventExtensions manage.
	extensible bool
	// pflags tracks per-property flags; nil means every property is
	// enumerable/writable/configurable.
	pflags map[string]propFlags
}

// window returns the view's byte window over its backing array.
func (o *Obj) window() []byte { return o.bytes[o.byteOff : o.byteOff+o.byteLen] }

// backing returns the ArrayBuffer a view reads through, creating the wrapper
// on first access; the slice is shared so writes alias.
func (o *Obj) backing() *Obj {
	if o.back == nil {
		o.back = &Obj{
			props:   make(map[string]Value, 2),
			kind:    kindAB,
			bytes:   o.bytes,
			byteLen: len(o.bytes),
			class:   arrayBufferClass,
			proto:   arrayBufferClass.Proto,
		}
	}
	return o.back
}

// Accessor is a get/set property pair. Each side is the bound callable value
// (a closure when the accessor captured, a bare prototype otherwise). A nil
// value means that side is absent.
type Accessor struct {
	get Value
	set Value
}

// propFlags is the enumerable/writable/configurable state of one own property.
type propFlags struct {
	enumerable   bool
	writable     bool
	configurable bool
}

func (o *Obj) pflag(name string) propFlags {
	if f, ok := o.pflags[name]; ok {
		return f
	}
	return propFlags{enumerable: true, writable: true, configurable: true}
}

// setf records the property flags Object.freeze / seal need.
func (o *Obj) setf(name string, f propFlags) {
	if o.pflags == nil {
		o.pflags = map[string]propFlags{}
	}
	o.pflags[name] = f
}

func NewObj() *Obj { return &Obj{props: make(map[string]Value, 8), extensible: true} }

// hasOwn reports whether name is an own property of o.
func (o *Obj) hasOwn(name string) bool {
	_, ok := o.props[name]
	return ok
}

// defineProp installs one property on o, as a data or an accessor property.
func (o *Obj) defineProp(name string, dv Value) {
	if o == nil {
		return
	}
	if get := dv.Get("get"); !get.IsUndef() {
		o.setAccessor(name, dv.Get("set").IsUndef(), get)
		return
	}
	if set := dv.Get("set"); !set.IsUndef() {
		o.setAccessor(name, true, set)
		return
	}
	o.set(name, dv.Get("value"))
	o.setf(name, propFlags{
		enumerable:   dv.Get("enumerable").truthy(),
		writable:     dv.Get("writable").truthy(),
		configurable: dv.Get("configurable").truthy(),
	})
}

// setAccessor records a getter or setter for name on this object.
func (o *Obj) setAccessor(name string, isSetter bool, fn Value) {
	if o.accessors == nil {
		o.accessors = make(map[string]*Accessor, 4)
	}
	a := o.accessors[name]
	if a == nil {
		a = &Accessor{}
		o.accessors[name] = a
	}
	if isSetter {
		a.set = fn
	} else {
		a.get = fn
	}
}

// accessorFor finds a getter/setter pair for name on this object or its
// prototype chain.
func (o *Obj) accessorFor(name string) *Accessor {
	for p := o; p != nil; p = p.proto {
		if a, ok := p.accessors[name]; ok {
			return a
		}
	}
	return nil
}

// keys returns the own property names in insertion order where tracked.
func (o *Obj) keys() []string {
	seen := make(map[string]bool, len(o.props))
	ks := make([]string, 0, len(o.props))
	for _, k := range o.order {
		if _, ok := o.props[k]; ok {
			ks = append(ks, k)
			seen[k] = true
		}
	}
	for k := range o.props {
		if !seen[k] {
			ks = append(ks, k)
		}
	}
	return ks
}

func (o *Obj) get(name string) Value {
	if v, ok := o.props[name]; ok {
		return v
	}
	if o.proto != nil {
		return o.proto.get(name)
	}
	return Undef()
}

func (o *Obj) set(name string, v Value) {
	if _, ok := o.props[name]; !ok {
		o.order = append(o.order, name)
	}
	o.props[name] = v
}

// GetOwn returns the own property value and whether it is defined. Exposed for
// hosts (like the module loader) that need to read the exports of a module
// namespace object directly.
func (o *Obj) GetOwn(name string) (Value, bool) {
	v, ok := o.props[name]
	return v, ok
}

// del removes a property, keeping the insertion order consistent.
func (o *Obj) del(name string) {
	if _, ok := o.props[name]; !ok {
		return
	}
	delete(o.props, name)
	for i, k := range o.order {
		if k == name {
			o.order = append(o.order[:i], o.order[i+1:]...)
			break
		}
	}
}

// Arr is an array: index-keyed storage with a length, stored as an Obj.
type Arr struct {
	elems []Value
}

func NewArr(n int) *Arr { return &Arr{elems: make([]Value, n)} }

func (a *Arr) get(i int) Value {
	if i >= 0 && i < len(a.elems) {
		return a.elems[i]
	}
	return Undef()
}

func (a *Arr) set(i int, v Value) {
	if i < 0 {
		return
	}
	if i < len(a.elems) {
		a.elems[i] = v
		return
	}
	// Assigning past the end grows the array, the way JS does. Grow
	// geometrically so long appends do not reallocate-and-copy per write.
	n := i + 1
	if len(a.elems) > 0 && n < len(a.elems)*2 {
		n = len(a.elems) * 2
	}
	na := NewArr(n)
	copy(na.elems, a.elems)
	na.elems[i] = v
	a.elems = na.elems
}

var arrayMethods = []string{
	"push", "pop", "join", "indexOf", "includes", "slice", "concat",
	"shift", "unshift", "reverse", "map", "filter", "forEach", "reduce",
	"find", "findIndex", "some", "every", "sort",
	"at", "flat", "flatMap", "fill", "copyWithin", "findLast", "findLastIndex",
	"splice", "reduceRight", "lastIndexOf", "toString", "valueOf", "entries",
	"keys", "values",
}

// arrayMethodByName indexes the array method names once so lookups are a
// single hash instead of a linear scan per property access.
var arrayMethodByName = func() map[string]int {
	m := make(map[string]int, len(arrayMethods))
	for i, n := range arrayMethods {
		m[n] = i
	}
	return m
}()

func arrayMethod(name string) int {
	if idx, ok := arrayMethodByName[name]; ok {
		return idx
	}
	return -1
}

// builtinMethod returns a host function bound to a specific array method.
func (vm *VM) builtinMethod(idx int, a *Arr) Value {
	return Value{tag: tFunc, ref: &builtin{fn: func(args []Value) Value {
		switch arrayMethods[idx] {
		case "push":
			a.elems = append(a.elems, args...)
			return Num(float64(len(a.elems)))
		case "pop":
			if len(a.elems) == 0 {
				return Undef()
			}
			v := a.elems[len(a.elems)-1]
			a.elems = a.elems[:len(a.elems)-1]
			return v
		case "shift":
			if len(a.elems) == 0 {
				return Undef()
			}
			v := a.elems[0]
			a.elems = a.elems[1:]
			return v
		case "unshift":
			a.elems = append(append([]Value{}, args...), a.elems...)
			return Num(float64(len(a.elems)))
		case "reverse":
			for i, j := 0, len(a.elems)-1; i < j; i, j = i+1, j-1 {
				a.elems[i], a.elems[j] = a.elems[j], a.elems[i]
			}
			return ArrVal(a)
		case "join":
			sep := ","
			if len(args) > 0 {
				sep = args[0].toStr()
			}
			if len(a.elems) == 0 {
				return Str("")
			}
			if len(a.elems) == 1 {
				return Str(a.elems[0].toStr())
			}
			var sb strings.Builder
			for i, e := range a.elems {
				if i > 0 {
					sb.WriteString(sep)
				}
				sb.WriteString(e.toStr())
			}
			return Str(sb.String())
		case "indexOf":
			if len(args) > 0 {
				for i, e := range a.elems {
					if strictEq(e, args[0]) {
						return Num(float64(i))
					}
				}
			}
			return Num(-1)
		case "includes":
			if len(args) > 0 {
				for _, e := range a.elems {
					if strictEq(e, args[0]) {
						return Bool(true)
					}
				}
			}
			return Bool(false)
		case "slice":
			start, end := 0, len(a.elems)
			if len(args) > 0 {
				start = normIdx(int(args[0].toNumber()), len(a.elems))
			}
			if len(args) > 1 && !args[1].IsUndef() {
				end = normIdx(int(args[1].toNumber()), len(a.elems))
			}
			if end < start {
				end = start
			}
			na := NewArr(end - start)
			copy(na.elems, a.elems[start:end])
			return ArrVal(na)
		case "concat":
			na := NewArr(0)
			na.elems = append(na.elems, a.elems...)
			for _, x := range args {
				if o := x.AsArr(); o != nil {
					na.elems = append(na.elems, o.elems...)
				} else {
					na.elems = append(na.elems, x)
				}
			}
			return ArrVal(na)
		case "map":
			na := NewArr(len(a.elems))
			for i, e := range a.elems {
				na.elems[i] = vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)})
			}
			return ArrVal(na)
		case "filter":
			na := NewArr(0)
			for i, e := range a.elems {
				if vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)}).truthy() {
					na.elems = append(na.elems, e)
				}
			}
			return ArrVal(na)
		case "forEach":
			for i, e := range a.elems {
				vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)})
			}
			return Undef()
		case "reduce":
			fn := argFn(args, 0)
			var acc Value
			start := 0
			if len(args) > 1 {
				acc = args[1]
			} else if len(a.elems) > 0 {
				acc = a.elems[0]
				start = 1
			}
			for i := start; i < len(a.elems); i++ {
				acc = vm.callAny(fn, []Value{acc, a.elems[i], Num(float64(i)), ArrVal(a)})
			}
			return acc
		case "find":
			for i, e := range a.elems {
				if vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)}).truthy() {
					return e
				}
			}
			return Undef()
		case "findIndex":
			for i, e := range a.elems {
				if vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)}).truthy() {
					return Num(float64(i))
				}
			}
			return Num(-1)
		case "some":
			for i, e := range a.elems {
				if vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)}).truthy() {
					return Bool(true)
				}
			}
			return Bool(false)
		case "every":
			for i, e := range a.elems {
				if !vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)}).truthy() {
					return Bool(false)
				}
			}
			return Bool(true)
		case "sort":
			fn := argFn(args, 0)
			less := func(x, y Value) bool {
				if fn.tag == tFunc || fn.tag == tObj {
					return vm.callAny(fn, []Value{x, y}).toNumber() < 0
				}
				return x.toStr() < y.toStr()
			}
			insertionSort(a.elems, less)
			return ArrVal(a)
		case "at":
			idx := 0
			if len(args) > 0 {
				idx = int(args[0].toNumber())
			}
			n := len(a.elems)
			j := idx
			if j < 0 {
				j = n + j
			}
			if j < 0 || j >= n {
				return Undef()
			}
			return a.elems[j]
		case "flat":
			depth := 1
			if len(args) > 0 {
				depth = int(args[0].toNumber())
			}
			return ArrVal(flattenArr(a, depth))
		case "flatMap":
			na := NewArr(0)
			for i, e := range a.elems {
				r := vm.callAny(argFn(args, 0), []Value{e, Num(float64(i)), ArrVal(a)})
				if o := r.AsArr(); o != nil {
					na.elems = append(na.elems, o.elems...)
				} else {
					na.elems = append(na.elems, r)
				}
			}
			return ArrVal(na)
		case "fill":
			val := Undef()
			from := 0
			if len(args) > 0 {
				val = args[0]
			}
			if len(args) > 1 {
				from = int(args[1].toNumber())
			}
			if from < 0 {
				from = 0
			}
			if from > len(a.elems) {
				from = len(a.elems)
			}
			for i := from; i < len(a.elems); i++ {
				a.elems[i] = val
			}
			return ArrVal(a)
		case "copyWithin":
			target, from := 0, 0
			to := len(a.elems)
			if len(args) > 0 {
				target = int(args[0].toNumber())
			}
			if len(args) > 1 {
				from = int(args[1].toNumber())
			}
			if len(args) > 2 {
				to = int(args[2].toNumber())
			}
			if target < 0 {
				target = len(a.elems) + target
			}
			if from < 0 {
				from = len(a.elems) + from
			}
			if to < 0 {
				to = len(a.elems) + to
			}
			if target < 0 || target >= len(a.elems) {
				return ArrVal(a)
			}
			end := to
			if end > len(a.elems) {
				end = len(a.elems)
			}
			if end < from {
				return ArrVal(a)
			}
			tmp := make([]Value, end-from)
			copy(tmp, a.elems[from:end])
			for i, v := range tmp {
				a.elems[target+i] = v
			}
			return ArrVal(a)
		case "findLast":
			fn := argFn(args, 0)
			for i := len(a.elems) - 1; i >= 0; i-- {
				if vm.callAny(fn, []Value{a.elems[i], Num(float64(i)), ArrVal(a)}).truthy() {
					return a.elems[i]
				}
			}
			return Undef()
		case "findLastIndex":
			fn := argFn(args, 0)
			for i := len(a.elems) - 1; i >= 0; i-- {
				if vm.callAny(fn, []Value{a.elems[i], Num(float64(i)), ArrVal(a)}).truthy() {
					return Num(float64(i))
				}
			}
			return Num(-1)
		case "splice":
			start := 0
			if len(args) > 0 {
				start = int(args[0].toNumber())
			}
			delCount := len(a.elems) - start
			if len(args) > 1 {
				delCount = int(args[1].toNumber())
			}
			if delCount < 0 {
				delCount = 0
			}
			end := start + delCount
			if end > len(a.elems) {
				end = len(a.elems)
			}
			removed := NewArr(0)
			removed.elems = append(removed.elems, a.elems[start:end]...)
			a.elems = append(a.elems[:start], args[2:]...)
			return ArrVal(removed)
		case "reduceRight":
			fn := argFn(args, 0)
			var acc Value
			start := len(a.elems)
			if len(args) > 1 {
				acc = args[1]
			} else if len(a.elems) > 0 {
				acc = a.elems[len(a.elems)-1]
				start = len(a.elems) - 1
			}
			for i := start; i >= 0; i-- {
				acc = vm.callAny(fn, []Value{acc, a.elems[i], Num(float64(i)), ArrVal(a)})
			}
			return acc
		case "lastIndexOf":
			if len(args) > 0 {
				for i := len(a.elems) - 1; i >= 0; i-- {
					if strictEq(a.elems[i], args[0]) {
						return Num(float64(i))
					}
				}
			}
			return Num(-1)
		case "toString":
			return Str(arrJoin(a))
		case "valueOf":
			return ArrVal(a)
		case "entries":
			na := NewArr(0)
			for i, e := range a.elems {
				p := NewArr(2)
				p.elems[0] = Num(float64(i))
				p.elems[1] = e
				na.elems = append(na.elems, ArrVal(p))
			}
			return ArrVal(na)
		case "keys":
			na := NewArr(0)
			for i := range a.elems {
				na.elems = append(na.elems, Num(float64(i)))
			}
			return ArrVal(na)
		case "values":
			na := NewArr(0)
			na.elems = a.elems
			return ArrVal(na)
		}
		return Undef()
	}}}
}

// flattenArr recursively flattens an array up to depth levels.
func flattenArr(a *Arr, depth int) *Arr {
	out := NewArr(0)
	var walk func(vals []Value, d int)
	walk = func(vals []Value, d int) {
		for _, e := range vals {
			if o := e.AsArr(); o != nil && d > 0 {
				walk(o.elems, d-1)
			} else {
				out.elems = append(out.elems, e)
			}
		}
	}
	walk(a.elems, depth)
	return out
}

// arrJoin renders an array the way Array.prototype.join does.
func arrJoin(a *Arr) string {
	if len(a.elems) == 0 {
		return ""
	}
	if len(a.elems) == 1 {
		return a.elems[0].toStr()
	}
	sep := ","
	var sb strings.Builder
	for i, e := range a.elems {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(e.toStr())
	}
	return sb.String()
}

func argFn(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return Undef()
}

func (vm *VM) callAny(fn Value, args []Value) Value {
	if fn.AsFunc() != nil {
		return vm.invoke(fn, args)
	}
	if hv := fn.AsHost(); hv != nil {
		return hv.Call(args)
	}
	if bf := fn.AsBuiltin(); bf != nil {
		return bf.fn(args)
	}
	return Undef()
}

func normIdx(i, n int) int {
	if i < 0 {
		i += n
	}
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

func insertionSort(a []Value, less func(x, y Value) bool) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && less(v, a[j]) {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// stringMethod returns a bound host function for common String.prototype
// methods, or Undef when the name is unknown.
func stringMethod(name, s string) Value {
	idx := -1
	for i, m := range stringMethods {
		if m == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Undef()
	}
	return Value{tag: tFunc, ref: &builtin{fn: func(args []Value) Value {
		switch stringMethods[idx] {
		case "charAt":
			i := 0
			if len(args) > 0 {
				i = int(args[0].toNumber())
			}
			if i >= 0 && i < len(s) {
				return Str(string(s[i]))
			}
			return Str("")
		case "charCodeAt":
			i := 0
			if len(args) > 0 {
				i = int(args[0].toNumber())
			}
			if i >= 0 && i < len(s) {
				return Num(float64(s[i]))
			}
			return Num(math.NaN())
		case "toUpperCase":
			return Str(strings.ToUpper(s))
		case "toLowerCase":
			return Str(strings.ToLower(s))
		case "trim":
			return Str(strings.TrimSpace(s))
		case "trimStart":
			return Str(strings.TrimLeft(s, " \t\n\r"))
		case "trimEnd":
			return Str(strings.TrimRight(s, " \t\n\r"))
		case "indexOf":
			if len(args) > 0 {
				return Num(float64(indexFrom(s, args[0].toStr(), args, 1)))
			}
			return Num(-1)
		case "lastIndexOf":
			if len(args) > 0 {
				return Num(float64(lastIndexFrom(s, args[0].toStr(), args, 1)))
			}
			return Num(-1)
		case "includes":
			if len(args) > 0 {
				return Bool(strings.Contains(s, args[0].toStr()))
			}
			return Bool(false)
		case "startsWith":
			if len(args) > 0 {
				return Bool(strings.HasPrefix(s, args[0].toStr()))
			}
			return Bool(false)
		case "endsWith":
			if len(args) > 0 {
				return Bool(strings.HasSuffix(s, args[0].toStr()))
			}
			return Bool(false)
		case "repeat":
			n := 0
			if len(args) > 0 {
				n = int(args[0].toNumber())
			}
			if n < 0 {
				n = 0
			}
			return Str(strings.Repeat(s, n))
		case "slice":
			start, end := 0, len(s)
			if len(args) > 0 {
				start = normIdx(int(args[0].toNumber()), len(s))
			}
			if len(args) > 1 && !args[1].IsUndef() {
				end = normIdx(int(args[1].toNumber()), len(s))
			}
			if end < start {
				end = start
			}
			return Str(s[start:end])
		case "substring":
			start, end := 0, len(s)
			if len(args) > 0 {
				start = int(args[0].toNumber())
			}
			if len(args) > 1 && !args[1].IsUndef() {
				end = int(args[1].toNumber())
			}
			if start < 0 {
				start = 0
			}
			if end > len(s) {
				end = len(s)
			}
			if start > end {
				start, end = end, start
			}
			return Str(s[start:end])
		case "split":
			sep := ""
			if len(args) > 0 {
				sep = args[0].toStr()
			}
			if sep == "" {
				na := NewArr(len(s))
				for i := 0; i < len(s); i++ {
					na.elems[i] = Str(string(s[i]))
				}
				return ArrVal(na)
			}
			parts := strings.Split(s, sep)
			na := NewArr(len(parts))
			for i, p := range parts {
				na.elems[i] = Str(p)
			}
			return ArrVal(na)
		case "replace":
			if len(args) >= 2 {
				return Str(strings.Replace(s, args[0].toStr(), args[1].toStr(), 1))
			}
			return Str(s)
		case "replaceAll":
			if len(args) >= 2 {
				return Str(strings.ReplaceAll(s, args[0].toStr(), args[1].toStr()))
			}
			return Str(s)
		case "padStart":
			n := 0
			pad := " "
			if len(args) > 0 {
				n = int(args[0].toNumber())
			}
			if len(args) > 1 {
				pad = args[1].toStr()
			}
			return Str(padLeft(s, n, pad))
		case "padEnd":
			n := 0
			pad := " "
			if len(args) > 0 {
				n = int(args[0].toNumber())
			}
			if len(args) > 1 {
				pad = args[1].toStr()
			}
			return Str(padRight(s, n, pad))
		case "toString", "valueOf":
			return Str(s)
		}
		return Undef()
	}}}
}

var stringMethods = []string{
	"charAt", "charCodeAt", "toUpperCase", "toLowerCase", "trim", "trimStart",
	"trimEnd", "indexOf", "lastIndexOf", "includes", "startsWith", "endsWith",
	"repeat", "slice", "substring", "split", "replace", "replaceAll",
	"padStart", "padEnd", "toString", "valueOf",
}

func padLeft(s string, n int, pad string) string {
	if len(pad) == 0 {
		return s
	}
	for len(s) < n {
		s = pad[:min(len(pad), n-len(s))] + s
	}
	return s
}

func padRight(s string, n int, pad string) string {
	if len(pad) == 0 {
		return s
	}
	for len(s) < n {
		s = s + pad[:min(len(pad), n-len(s))]
	}
	return s
}

// indexFrom returns the first index of sub in s at or after the position held
// in args[pos], or -1. JS measures positions in UTF-16 code units; the engine
// stores strings as bytes, so a position here is a byte offset.
func indexFrom(s, sub string, args []Value, pos int) int {
	from := 0
	if pos < len(args) && !args[pos].IsUndef() {
		if f := args[pos].toNumber(); f == f && f > 0 {
			from = int(f)
		}
	}
	if from > len(s) {
		return -1
	}
	i := strings.Index(s[from:], sub)
	if i < 0 {
		return -1
	}
	return i + from
}

// lastIndexFrom returns the last index of sub in s at or before the position
// held in args[pos], or -1.
func lastIndexFrom(s, sub string, args []Value, pos int) int {
	limit := len(s)
	if pos < len(args) && !args[pos].IsUndef() {
		if f := args[pos].toNumber(); f == f && f >= 0 && int(f) < limit {
			limit = int(f) + len(sub)
			if limit > len(s) {
				limit = len(s)
			}
		}
	}
	return strings.LastIndex(s[:limit], sub)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
