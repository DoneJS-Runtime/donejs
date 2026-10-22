package jit

import "strings"

// Obj is a plain string-keyed object. Property access goes through an inline
// cache (see vm.go) so the hot path is a single map hit plus a shape check.

type Obj struct {
	props map[string]Value
	proto *Obj
}

func NewObj() *Obj { return &Obj{props: make(map[string]Value, 8)} }

func (o *Obj) get(name string) Value {
	if v, ok := o.props[name]; ok {
		return v
	}
	if o.proto != nil {
		return o.proto.get(name)
	}
	return Undef()
}

func (o *Obj) set(name string, v Value) { o.props[name] = v }

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
	if i >= 0 && i < len(a.elems) {
		a.elems[i] = v
	}
}

var arrayMethods = []string{"push", "pop", "join", "indexOf"}

func arrayMethod(name string) int {
	for i, m := range arrayMethods {
		if m == name {
			return i
		}
	}
	return -1
}

// builtinMethod returns a host function bound to a specific array method.
func builtinMethod(idx int, a *Arr) Value {
	return Value{tag: tFunc, ref: &builtin{fn: func(args []Value) Value {
		switch idx {
		case 0: // push
			a.elems = append(a.elems, args...)
			return Num(float64(len(a.elems)))
		case 1: // pop
			if len(a.elems) == 0 {
				return Undef()
			}
			v := a.elems[len(a.elems)-1]
			a.elems = a.elems[:len(a.elems)-1]
			return v
		case 2: // join
			sep := ","
			if len(args) > 0 {
				sep = args[0].toStr()
			}
			var sb strings.Builder
			for i, e := range a.elems {
				if i > 0 {
					sb.WriteString(sep)
				}
				sb.WriteString(e.toStr())
			}
			return Str(sb.String())
		case 3: // indexOf
			if len(args) > 0 {
				for i, e := range a.elems {
					if strictEq(e, args[0]) {
						return Num(float64(i))
					}
				}
			}
			return Num(-1)
		}
		return Undef()
	}}}
}
