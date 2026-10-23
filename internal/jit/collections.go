package jit

// collections.go implements the Map and Set built-ins as host values. They
// keep insertion order and key entries by SameValueZero, matching the JS
// semantics the standard library relies on.
//
// ponytail: Map/Set have no WeakMap-style subclasses and no `size` accessor on
// the prototype; add those when a caller needs them.

import (
	"fmt"
	"math"
	"strconv"
)

type mapPair struct{ key, val Value }

type jsMap struct {
	vm     *VM
	pairs  []mapPair
	index  map[string]int
	hasNaN bool
}

type jsSet struct {
	vm     *VM
	items  []Value
	member map[string]bool
}

func (m *jsMap) Call([]Value) Value {
	return strErr("TypeError", "Map constructor cannot be called without 'new'")
}
func (m *jsSet) Call([]Value) Value {
	return strErr("TypeError", "Set constructor cannot be called without 'new'")
}

// keyID renders a Map/Set key as a string identity token.
func keyID(v Value) string {
	switch v.tag {
	case tUndef:
		return "u"
	case tNull:
		return "z"
	case tBool:
		if v.AsBool() {
			return "b1"
		}
		return "b0"
	case tNum:
		n := v.AsNum()
		if math.IsNaN(n) {
			return "nNaN"
		}
		if n == 0 {
			n = 0 // fold -0 into 0
		}
		return "n" + strconv.FormatFloat(n, 'g', -1, 64)
	case tStr:
		return "s" + v.AsStr()
	default:
		return "o" + fmt.Sprintf("%p", v.ref)
	}
}

func (m *jsMap) find(k Value) (int, bool) {
	id := keyID(k)
	i, ok := m.index[id]
	if !ok {
		return -1, false
	}
	return i, true
}

func (m *jsMap) Get(name string) Value {
	switch name {
	case "size":
		return Num(float64(len(m.pairs)))
	case "get":
		return BuiltinFn("get", func(a []Value) Value {
			if len(a) == 0 {
				return Undef()
			}
			if i, ok := m.find(a[0]); ok {
				return m.pairs[i].val
			}
			return Undef()
		})
	case "set":
		return BuiltinFn("set", func(a []Value) Value {
			var k, v Value = Undef(), Undef()
			if len(a) > 0 {
				k = a[0]
			}
			if len(a) > 1 {
				v = a[1]
			}
			if k.tag == tNum && math.IsNaN(k.AsNum()) {
				m.hasNaN = true
			}
			id := keyID(k)
			if i, ok := m.index[id]; ok {
				m.pairs[i].val = v
			} else {
				m.index[id] = len(m.pairs)
				m.pairs = append(m.pairs, mapPair{key: k, val: v})
			}
			return Host(m)
		})
	case "has":
		return BuiltinFn("has", func(a []Value) Value {
			if len(a) == 0 {
				return Bool(false)
			}
			if a[0].tag == tNum && math.IsNaN(a[0].AsNum()) {
				return Bool(m.hasNaN)
			}
			_, ok := m.index[keyID(a[0])]
			return Bool(ok)
		})
	case "delete":
		return BuiltinFn("delete", func(a []Value) Value {
			if len(a) == 0 {
				return Bool(false)
			}
			id := keyID(a[0])
			if i, ok := m.index[id]; ok {
				m.pairs = append(m.pairs[:i], m.pairs[i+1:]...)
				m.reindex()
				if m.hasNaN {
					m.hasNaN = false
					for _, p := range m.pairs {
						if p.key.tag == tNum && math.IsNaN(p.key.AsNum()) {
							m.hasNaN = true
							break
						}
					}
				}
				return Bool(true)
			}
			return Bool(false)
		})
	case "clear":
		return BuiltinFn("clear", func(a []Value) Value {
			m.pairs = nil
			m.index = map[string]int{}
			m.hasNaN = false
			return Undef()
		})
	case "forEach":
		return BuiltinFn("forEach", func(a []Value) Value {
			var cb, thisArg Value = Undef(), Undef()
			if len(a) > 0 {
				cb = a[0]
			}
			if len(a) > 1 {
				thisArg = a[1]
			}
			if !isCallable(cb) {
				return Undef()
			}
			snap := append([]mapPair(nil), m.pairs...)
			for _, p := range snap {
				_, _ = m.vm.callThis(cb, thisArg, []Value{p.val, p.key, Host(m)})
			}
			return Undef()
		})
	case "keys":
		return BuiltinFn("keys", func(a []Value) Value { return m.iterColl(func(p mapPair) Value { return p.key }) })
	case "values":
		return BuiltinFn("values", func(a []Value) Value { return m.iterColl(func(p mapPair) Value { return p.val }) })
	case "entries":
		return BuiltinFn("entries", func(a []Value) Value { return m.iterColl(mapEntry) })
	}
	if name == symIteratorKey {
		return BuiltinFn("entries", func(a []Value) Value { return m.iterColl(mapEntry) })
	}
	return Undef()
}

func mapEntry(p mapPair) Value {
	arr := NewArr(2)
	arr.elems[0], arr.elems[1] = p.key, p.val
	return ArrVal(arr)
}

// iterColl is a live iterator over the map's entries, in insertion order.
func (m *jsMap) iterColl(f func(mapPair) Value) Value {
	return Host(&iterHost{kind: iterColl, coll: func(i int) (Value, bool) {
		if i < len(m.pairs) {
			return f(m.pairs[i]), true
		}
		return Undef(), false
	}})
}

func (m *jsMap) reindex() {
	m.index = make(map[string]int, len(m.pairs))
	for i, p := range m.pairs {
		m.index[keyID(p.key)] = i
	}
}

func (m *jsMap) Set(string, Value) {}
func (m *jsMap) Str() string       { return "[object Map]" }
func (m *jsMap) Type() string      { return "object" }

func (s *jsSet) Get(name string) Value {
	switch name {
	case "size":
		return Num(float64(len(s.items)))
	case "add":
		return BuiltinFn("add", func(a []Value) Value {
			v := Undef()
			if len(a) > 0 {
				v = a[0]
			}
			id := keyID(v)
			if !s.member[id] {
				s.member[id] = true
				s.items = append(s.items, v)
			}
			return Host(s)
		})
	case "has":
		return BuiltinFn("has", func(a []Value) Value {
			if len(a) == 0 {
				return Bool(false)
			}
			return Bool(s.member[keyID(a[0])])
		})
	case "delete":
		return BuiltinFn("delete", func(a []Value) Value {
			if len(a) == 0 {
				return Bool(false)
			}
			id := keyID(a[0])
			if !s.member[id] {
				return Bool(false)
			}
			delete(s.member, id)
			for i, it := range s.items {
				if keyID(it) == id {
					s.items = append(s.items[:i], s.items[i+1:]...)
					break
				}
			}
			return Bool(true)
		})
	case "clear":
		return BuiltinFn("clear", func(a []Value) Value {
			s.items = nil
			s.member = map[string]bool{}
			return Undef()
		})
	case "forEach":
		return BuiltinFn("forEach", func(a []Value) Value {
			var cb, thisArg Value = Undef(), Undef()
			if len(a) > 0 {
				cb = a[0]
			}
			if len(a) > 1 {
				thisArg = a[1]
			}
			if !isCallable(cb) {
				return Undef()
			}
			snap := append([]Value(nil), s.items...)
			for _, it := range snap {
				_, _ = s.vm.callThis(cb, thisArg, []Value{it, it, Host(s)})
			}
			return Undef()
		})
	case "keys", "values":
		return BuiltinFn(name, func(a []Value) Value { return s.iterColl(func(it Value) Value { return it }) })
	case "entries":
		return BuiltinFn("entries", func(a []Value) Value {
			return s.iterColl(func(it Value) Value {
				pair := NewArr(2)
				pair.elems[0], pair.elems[1] = it, it
				return ArrVal(pair)
			})
		})
	}
	if name == symIteratorKey {
		return BuiltinFn("values", func(a []Value) Value { return s.iterColl(func(it Value) Value { return it }) })
	}
	return Undef()
}

// iterColl is a live iterator over the set's items, in insertion order.
func (s *jsSet) iterColl(f func(Value) Value) Value {
	return Host(&iterHost{kind: iterColl, coll: func(i int) (Value, bool) {
		if i < len(s.items) {
			return f(s.items[i]), true
		}
		return Undef(), false
	}})
}

func (s *jsSet) Set(string, Value) {}
func (s *jsSet) Str() string       { return "[object Set]" }
func (s *jsSet) Type() string      { return "object" }

// ---- constructors ----

type mapCtor struct{ vm *VM }

func (c *mapCtor) Call([]Value) Value {
	return strErr("TypeError", "Map constructor cannot be called without 'new'")
}
func (c *mapCtor) Get(name string) Value {
	if name == "prototype" {
		return Host(&mapProto{})
	}
	return Undef()
}
func (c *mapCtor) Set(string, Value) {}
func (c *mapCtor) Str() string       { return "function Map() { [native code] }" }
func (c *mapCtor) Type() string      { return "function" }

func (c *mapCtor) New(args []Value) Value {
	m := &jsMap{vm: c.vm, index: map[string]int{}}
	if len(args) > 0 {
		copyIterable(c.vm, args[0], func(k, v Value) {
			id := keyID(k)
			if _, ok := m.index[id]; !ok {
				m.index[id] = len(m.pairs)
				m.pairs = append(m.pairs, mapPair{key: k, val: v})
			} else {
				m.pairs[m.index[id]].val = v
			}
		})
	}
	return Host(m)
}

type mapProto struct{}

func (p *mapProto) Call([]Value) Value { return strErr("TypeError", "not a function") }
func (p *mapProto) Get(name string) Value {
	switch name {
	case "get":
		return BuiltinFn("get", func(a []Value) Value { return Undef() })
	}
	return Undef()
}
func (p *mapProto) Set(string, Value) {}
func (p *mapProto) Str() string       { return "[object Map]" }
func (p *mapProto) Type() string      { return "object" }

type setCtor struct{ vm *VM }

func (c *setCtor) Call([]Value) Value {
	return strErr("TypeError", "Set constructor cannot be called without 'new'")
}
func (c *setCtor) Get(name string) Value {
	if name == "prototype" {
		return Host(&setProto{})
	}
	return Undef()
}
func (c *setCtor) Set(string, Value) {}
func (c *setCtor) Str() string       { return "function Set() { [native code] }" }
func (c *setCtor) Type() string      { return "function" }

func (c *setCtor) New(args []Value) Value {
	s := &jsSet{vm: c.vm, member: map[string]bool{}}
	if len(args) > 0 {
		copyIterable(c.vm, args[0], func(k, _ Value) {
			if k.IsNull() {
				return
			}
			id := keyID(k)
			if !s.member[id] {
				s.member[id] = true
				s.items = append(s.items, k)
			}
		})
	}
	return Host(s)
}

type setProto struct{}

func (p *setProto) Call([]Value) Value { return strErr("TypeError", "not a function") }
func (p *setProto) Get(name string) Value {
	switch name {
	case "add":
		return BuiltinFn("add", func(a []Value) Value { return Undef() })
	}
	return Undef()
}
func (p *setProto) Set(string, Value) {}
func (p *setProto) Str() string       { return "[object Set]" }
func (p *setProto) Type() string      { return "object" }

// copyIterable walks an iterable (array or Map/Set) into fn.
func copyIterable(vm *VM, src Value, fn func(k, v Value)) {
	if a := src.AsArr(); a != nil {
		for _, e := range a.elems {
			if pair := e.AsArr(); pair != nil && len(pair.elems) >= 2 {
				fn(pair.elems[0], pair.elems[1])
			} else if pair := e.AsArr(); pair != nil && len(pair.elems) == 1 {
				fn(pair.elems[0], Undef())
			} else {
				fn(e, Undef())
			}
		}
		return
	}
	if hv := src.AsHost(); hv != nil {
		switch coll := hv.(type) {
		case *jsMap:
			for _, p := range coll.pairs {
				fn(p.key, p.val)
			}
		case *jsSet:
			for _, it := range coll.items {
				fn(it, it)
			}
		case *iterHost:
			for {
				res := coll.next(nil).AsObj()
				if res == nil || res.get("done").truthy() {
					return
				}
				v := res.get("value")
				if pair := v.AsArr(); pair != nil && len(pair.elems) >= 2 {
					fn(pair.elems[0], pair.elems[1])
				} else {
					fn(v, Undef())
				}
			}
		}
	}
}

// installCollections registers the Map and Set globals.
func (vm *VM) installCollections() {
	vm.SetGlobal("Map", Host(&mapCtor{vm: vm}))
	vm.SetGlobal("Set", Host(&setCtor{vm: vm}))
}
