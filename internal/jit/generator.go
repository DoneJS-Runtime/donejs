package jit

// generator.go implements generator functions as host values. A generator
// function does not run when called: it returns a generator object whose body
// advances to the next yield on each next() and suspends at that yield, to be
// resumed by the following call.
//
// The suspended activation is a real frame: it keeps its locals, operand stack
// and pc, so closures created before a yield survive across it.
//
// ponytail: return(v) / throw(e) on a generator do not unwind through nested
// try/finally, and concurrent next() calls on an async generator share one
// pending result. Add those when a caller needs the full protocol.

// symIteratorKey is the property key the engine uses for Symbol.iterator: the
// well-known symbol value stringifies as "Symbol(Symbol.iterator)".
const symIteratorKey = "Symbol(Symbol.iterator)"

type jsGenerator struct {
	vm       *VM
	cp       *FuncProto
	cl       *Closure
	this     Value
	args     []Value
	frame    *frame // suspended activation, nil before the first next()
	yielded  Value  // value produced by the last yield
	done     bool
	awaiting bool       // suspended inside an await, promise still unsettled
	pending  []*Promise // promises outstanding next() calls wait on
	// bm memoizes the next/return/throw wrappers returned by Get so repeated
	// iteration steps skip the closure allocation.
	bm map[string]Value
}

// settle completes the promises a pending next() returned, once the body
// yields or returns.
func (g *jsGenerator) settle(v Value, done bool) {
	for _, p := range g.pending {
		p.resolve(iterResult(v, done))
	}
	g.pending = nil
}

func (g *jsGenerator) settleErr(err error) {
	for _, p := range g.pending {
		p.reject(errToValue(err))
	}
	g.pending = nil
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
		g.frame = g.vm.frames[len(g.vm.frames)-1]
	} else {
		g.frame.st.push(sent)
		g.vm.frames = append(g.vm.frames, g.frame)
	}
	g.frame.gen = g
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
	if g.awaiting {
		// Suspended inside an await: the continuation resumes the frame and
		// settles the promise when the body yields or returns.
		return Undef(), false
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

type iterKind int

const (
	iterArr iterKind = iota
	iterStr
	iterU8
	iterColl
)

// iterHost is a host iterator over a built-in collection's elements. It
// answers `next()` from an internal cursor and advertises the well-known
// Symbol.iterator key.
type iterHost struct {
	kind    iterKind
	a       *Arr
	o       *Obj
	s       string
	w       []byte
	coll    func(i int) (Value, bool)
	stopped bool
	i       int
	// nextFn / selfFn memoize the `next` and Symbol.iterator wrappers so the
	// one-next-per-iteration loop does not allocate a closure per step.
	nextFn Value
	selfFn Value
}

func (it *iterHost) Call(args []Value) Value { return Host(it) }
func (it *iterHost) Set(string, Value)       {}
func (it *iterHost) Str() string             { return "[object Iterator]" }
func (it *iterHost) Type() string            { return "object" }

func (it *iterHost) Get(name string) Value {
	switch name {
	case "next":
		if it.nextFn.IsUndef() {
			it.nextFn = BuiltinFn("next", func(a []Value) Value { return it.next(a) })
		}
		return it.nextFn
	case "return":
		it.stopped = true
		switch it.kind {
		case iterArr:
			it.i = len(it.a.elems)
		case iterStr:
			it.i = len(it.s)
		case iterU8:
			it.i = len(it.w)
		}
		return iterResult(Undef(), true)
	case "throw":
		return iterResult(Undef(), true)
	}
	if name == symIteratorKey {
		if it.selfFn.IsUndef() {
			it.selfFn = BuiltinFn("[Symbol.iterator]", func(a []Value) Value { return Host(it) })
		}
		return it.selfFn
	}
	return Undef()
}

func (it *iterHost) next(args []Value) Value {
	var v Value
	done := true
	if it.stopped {
		return iterResult(v, done)
	}
	switch it.kind {
	case iterArr:
		if it.i < len(it.a.elems) {
			v, it.i = it.a.elems[it.i], it.i+1
			done = false
		}
	case iterStr:
		if it.i < len(it.s) {
			v, it.i = Str(string(it.s[it.i])), it.i+1
			done = false
		}
	case iterU8:
		if it.i < len(it.w) {
			v, it.i = Num(float64(it.w[it.i])), it.i+1
			done = false
		}
	case iterColl:
		if it.coll != nil {
			if cv, ok := it.coll(it.i); ok {
				v, it.i, done = cv, it.i+1, false
			}
		}
	}
	return iterResult(v, done)
}

// Get returns a generator method, memoizing the wrapper on the generator.
func (g *jsGenerator) Get(name string) Value {
	if v, ok := g.bm[name]; ok {
		return v
	}
	v := g.getMethod(name)
	if v.IsFunc() {
		if g.bm == nil {
			g.bm = make(map[string]Value, 3)
		}
		g.bm[name] = v
	}
	return v
}

func (g *jsGenerator) getMethod(name string) Value {
	switch name {
	case "next":
		return BuiltinFn("next", func(a []Value) Value {
			if !g.cp.Async {
				res, _ := g.next(a)
				return res
			}
			// An async generator settles its { value, done } through a
			// promise. A body that suspends on a pending await keeps the
			// promise open until the body yields or returns.
			p, _, _ := g.vm.NewPromise()
			g.pending = append(g.pending, p)
			g.next(a)
			if g.awaiting {
				return PromiseVal(p)
			}
			g.settle(g.yielded, g.done)
			return PromiseVal(p)
		})
	case "return":
		return BuiltinFn("return", func(a []Value) Value {
			v := Undef()
			if len(a) > 0 {
				v = a[0]
			}
			if g.done || g.frame == nil {
				g.done = true
				g.frame = nil
				res := iterResult(v, true)
				if g.cp.Async {
					// An async generator returns through a promise that
					// settles with { value, done }.
					p, resolve, _ := g.vm.NewPromise()
					resolve(res)
					return PromiseVal(p)
				}
				return res
			}
			// Resume with a return: the value sits where OpReturn reads it.
			g.frame.st.push(v)
			g.frame.gen = g
			g.vm.frames = append(g.vm.frames, g.frame)
			prev := g.vm.gen
			g.vm.gen = g
			old := g.vm.baseDepth
			g.vm.baseDepth = len(g.vm.frames) - 1
			ret, err := g.vm.loop()
			g.vm.baseDepth = old
			g.vm.gen = prev
			g.done = true
			g.frame = nil
			if err != nil {
				panic(err)
			}
			if g.cp.Async {
				p, resolve, _ := g.vm.NewPromise()
				resolve(ret)
				return PromiseVal(p)
			}
			return ret
		})
	case "throw":
		return BuiltinFn("throw", func(a []Value) Value {
			errV := Undef()
			if len(a) > 0 {
				errV = a[0]
			}
			if g.done || g.frame == nil {
				g.done = true
				g.frame = nil
				if g.cp.Async {
					p, _, reject := g.vm.NewPromise()
					reject(errV)
					return PromiseVal(p)
				}
				return iterResult(Undef(), true)
			}
			g.frame.pending, g.frame.pendingF = errV, true
			if len(a) > 0 {
				g.frame.pending = a[0]
			}
			g.frame.gen = g
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
			if g.cp.Async {
				p, resolve, reject := g.vm.NewPromise()
				if err != nil {
					reject(errToValue(err))
				} else {
					resolve(iterResult(Undef(), true))
				}
				return PromiseVal(p)
			}
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
			fn(Str(singleCharCache[s[i]]))
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
		if ih, ok2 := iter.AsHost().(*iterHost); ok2 {
			for {
				res := ih.next(nil).AsObj()
				if res == nil || res.get("done").truthy() {
					return nil
				}
				fn(res.get("value"))
			}
		}
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
