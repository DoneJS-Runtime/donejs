package jit

// generator.go implements generator functions as host values. A generator
// function does not run when called: it returns a generator object whose body
// advances to the next yield on each next() and suspends at that yield, to be
// resumed by the following call.
//
// The suspended activation is a real frame: it keeps its locals, operand stack
// and pc, so closures created before a yield survive across it.
//
// ponytail: `yield*` delegation lowers to a plain yield, and return(v) /
// throw(e) do not unwind through nested try/finally. Add those when a caller
// needs the full protocol.

// symIteratorKey is the property key the engine uses for Symbol.iterator: the
// well-known symbol value stringifies as "Symbol(Symbol.iterator)".
const symIteratorKey = "Symbol(Symbol.iterator)"

type jsGenerator struct {
	vm      *VM
	cp      *FuncProto
	cl      *Closure
	this    Value
	args    []Value
	frame   *frame  // suspended activation, nil before the first next()
	yielded Value   // value produced by the last yield
	done    bool
}

func (vm *VM) newGenerator(cp *FuncProto, cl *Closure, this Value, args []Value) Value {
	g := &jsGenerator{vm: vm, cp: cp, cl: cl, this: this, args: append([]Value(nil), args...)}
	return Host(g)
}

// step runs the suspended activation until it yields or finishes. The value the
// caller sent is pushed where the yield left off. It reports the yielded or
// returned value.
func (g *jsGenerator) step(sent Value) (Value, bool) {
	if g.done {
		return Undef(), true
	}
	if g.frame == nil {
		g.vm.pushFrame(g.cp, g.cl, len(g.args), g.this, g.args, false, nil)
	} else {
		g.frame.st.push(sent)
		g.vm.frames = append(g.vm.frames, g.frame)
	}
	prev := g.vm.gen
	g.vm.gen = g
	old := g.vm.baseDepth
	g.vm.baseDepth = len(g.vm.frames) - 1
	ret, err := g.vm.loop()
	g.vm.baseDepth = old
	g.vm.gen = prev
	if err != nil {
		g.done = true
		g.frame = nil
		panic(err)
	}
	if g.frame != nil {
		// Suspended at a yield; the activation is kept for the next call.
		return g.yielded, false
	}
	g.done = true
	return ret, true
}

// next advances one step and reports { value, done }.
func (g *jsGenerator) next(args []Value) (Value, bool) {
	sent := Undef()
	if len(args) > 0 {
		sent = args[0]
	}
	v, done := g.step(sent)
	return iterResult(v, done), done
}

// iterResult builds the { value, done } object the iterator protocol returns.
func iterResult(v Value, done bool) Value {
	o := NewObj()
	o.set("value", v)
	o.set("done", Bool(done))
	return ObjVal(o)
}

func (g *jsGenerator) Get(name string) Value {
	switch name {
	case "next":
		return BuiltinFn("next", func(a []Value) Value {
			res, _ := g.next(a)
			return res
		})
	case "return":
		return BuiltinFn("return", func(a []Value) Value {
			v := Undef()
			if len(a) > 0 {
				v = a[0]
			}
			if g.done {
				return iterResult(v, true)
			}
			if g.frame == nil {
				g.done = true
				return iterResult(v, true)
			}
			// Resume with a return: the value sits where OpReturn reads it.
			g.frame.st.push(v)
			g.vm.frames = append(g.vm.frames, g.frame)
			prev := g.vm.gen
			g.vm.gen = g
			old := g.vm.baseDepth
			g.vm.baseDepth = len(g.vm.frames) - 1
			ret, err := g.vm.loop()
			g.vm.baseDepth = old
			g.vm.gen = prev
			if err != nil {
				g.done = true
				g.frame = nil
				panic(err)
			}
			g.done = true
			g.frame = nil
			return iterResult(ret, true)
		})
	case "throw":
		return BuiltinFn("throw", func(a []Value) Value {
			if g.done {
				return iterResult(Undef(), true)
			}
			if g.frame == nil {
				g.done = true
				return iterResult(Undef(), true)
			}
			g.frame.pending, g.frame.pendingF = Undef(), true
			if len(a) > 0 {
				g.frame.pending = a[0]
			}
			g.vm.frames = append(g.vm.frames, g.frame)
			prev := g.vm.gen
			g.vm.gen = g
			old := g.vm.baseDepth
			g.vm.baseDepth = len(g.vm.frames) - 1
			_, err := g.vm.loop()
			g.vm.baseDepth = old
			g.vm.gen = prev
			g.done = true
			g.frame = nil
			if err != nil {
				panic(err)
			}
			return iterResult(Undef(), true)
		})
	case symIteratorKey:
		// A generator is its own iterator.
		return Host(g)
	}
	return Undef()
}

func (g *jsGenerator) Call([]Value) Value { return Host(g) }
func (g *jsGenerator) Set(string, Value)  {}
func (g *jsGenerator) Str() string        { return "[object Generator]" }
func (g *jsGenerator) Type() string       { return "object" }

// spreadIterable calls fn for each value src yields: the elements of an array
// or string, or the values an object's Symbol.iterator produces.
func (vm *VM) spreadIterable(src Value, fn func(v Value)) *ThrowError {
	if a := src.AsArr(); a != nil {
		for _, e := range a.elems {
			fn(e)
		}
		return nil
	}
	if src.tag == tStr {
		s := src.AsStr()
		for i := 0; i < len(s); i++ {
			fn(Str(string(s[i])))
		}
		return nil
	}
	if o := src.AsObj(); o != nil && o.kind == kindU8 {
		for _, b := range o.window() {
			fn(Num(float64(b)))
		}
		return nil
	}
	gv, te := vm.getField(src, symIteratorKey)
	if te != nil {
		return te
	}
	iter, err := vm.callThis(gv, Undef(), nil)
	if err != nil {
		return asThrow(err)
	}
	gen, ok := iter.AsHost().(*jsGenerator)
	if !ok {
		return &ThrowError{Value: strErr("TypeError", src.toStr()+" is not iterable")}
	}
	for {
		res, done := gen.next(nil)
		if done {
			return nil
		}
		fn(res.AsObj().get("value"))
	}
}
