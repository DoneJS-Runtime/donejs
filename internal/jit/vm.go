package jit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// stack is a frame's operand stack.
type stack struct {
	s  []Value
	sp int
}

func (s *stack) push(v Value) {
	if s.sp >= len(s.s) {
		s.s = append(s.s, make([]Value, 8)...)
	}
	s.s[s.sp] = v
	s.sp++
}

func (s *stack) pop() Value {
	s.sp--
	return s.s[s.sp]
}

// frame is one activation: a function prototype, its local slots and its
// operand stack. Each frame owns a private operand stack, so calls are
// re-entrant without manual spill logic.
type frame struct {
	proto    *FuncProto
	pc       int
	locals   []Value
	st       stack
	nargs    int
	args     []Value // call arguments, for rest-parameter collection
	ret      Value
	trys     []*tryFrame
	pending  Value // in-flight exception
	pendingF bool
	isCtor   bool
	// newTarget is the constructor a `new` call reached this activation with,
	// or undefined for a plain call. `new.target` reads it.
	newTarget Value
	// closure is the function value this activation was created from; its
	// caps are the frame's upvalues. heap holds the locals of this activation
	// that a nested function captured, one shared slot each.
	closure *Closure
	heap    []Value
	// promise is set on the activation of an async function: its return
	// settles it and a suspended `await` hands the frame to a continuation.
	promise *Promise
	// gen names the generator this activation belongs to, so a suspended
	// `await` can hand the frame back to it.
	gen *jsGenerator
}

// tryFrame is an active try/catch handler inside a frame.
type tryFrame struct {
	catchTarget    int
	catchParamSlot int32 // -1 = swallow
}

// ThrowError is raised when a JS exception escapes to the top level.
type ThrowError struct{ Value Value }

// Error renders the throwable the way Node prints an uncaught exception:
// "TypeError: o.x is not a function", or the plain value for a thrown non-Error.
func (e *ThrowError) Error() string {
	if o := e.Value.AsObj(); o != nil {
		if n, m := o.get("name"), o.get("message"); n.tag == tStr && m.tag == tStr {
			return n.AsStr() + ": " + m.AsStr()
		}
	}
	return e.Value.toStr()
}

// VM is the bytecode interpreter: dense switch dispatch over the instruction
// set, private operand stacks, inline-cached property access and object shapes.
type VM struct {
	frames    []*frame // pointers: appending a callee never invalidates the caller
	globals   map[string]Value
	out       func(string)
	baseDepth int
	jobs      []func() // microtasks: promise reactions and awaited continuations
	// GlobalLookup resolves a global name the interpreter does not own itself.
	// A host runtime (internal/vm) uses this to expose its own globals —
	// console, process, require, … — to entry scripts without copying them.
	GlobalLookup func(name string) (Value, bool)
	// numberProto is the object number primitives inherit from, so methods
	// installed on Number.prototype answer `(1).toFixed(2)`-style calls.
	numberProto *Obj
	// objectProto backs plain objects and the Object constructor: the home of
	// Object.prototype methods like hasOwnProperty and propertyIsEnumerable.
	objectProto *Obj
	// gen is the generator currently being driven by a next()/return()/throw;
	// its suspended activation lives in gen.frame.
	gen *jsGenerator
}

// lookupGlobal resolves a global, preferring the host's, then the interpreter's.
func (vm *VM) lookupGlobal(name string) (Value, bool) {
	if vm.GlobalLookup != nil {
		if v, ok := vm.GlobalLookup(name); ok {
			return v, true
		}
	}
	v, ok := vm.globals[name]
	return v, ok
}

func NewVM() *VM {
	vm := &VM{
		globals: map[string]Value{},
		out:     func(s string) { fmt.Println(s) },
	}
	vm.objectProto = NewObj()
	vm.installGlobals()
	vm.installPrim()
	vm.installBigInt()
	vm.installCollections()
	vm.installBuiltins()
	vm.installTyped()
	vm.SetGlobal("Promise", PromiseCtor(vm))
	return vm
}

// frameFor returns a frame for cp, reusing a pooled one when possible so the
// hot call path does not allocate.
func frameFor(cp *FuncProto) *frame {
	if n := len(cp.Free); n > 0 {
		f := cp.Free[n-1]
		cp.Free = cp.Free[:n-1]
		return f
	}
	return newFrame(cp)
}

// newFrame allocates an activation for cp, including the heap slice that a
// nested function's captures point into.
func newFrame(cp *FuncProto) *frame {
	f := &frame{proto: cp, locals: make([]Value, cp.NumLocals), st: stack{s: make([]Value, 8)}}
	if cp.HeapCount > 0 {
		f.heap = make([]Value, cp.HeapCount)
	}
	return f
}

func recycle(f *frame) {
	f.pc = 0
	f.nargs = 0
	f.args = nil
	f.st.sp = 0
	f.pendingF = false
	f.pending = Undef()
	f.isCtor = false
	f.trys = f.trys[:0]
	f.closure = nil
	f.heap = nil // a live closure may still point into the old backing array
	f.promise = nil
	f.gen = nil
	f.proto.Free = append(f.proto.Free, f)
}

func (vm *VM) getGlobal(name string) Value { return vm.globals[name] }

// SetOut replaces the output sink (defaults to fmt.Println).
func (vm *VM) SetOut(f func(string)) { vm.out = f }

func (vm *VM) installBuiltins() {
	vm.SetGlobal("RegExp", Host(&regexpCtor{}))
	reg := func(name string, fn func([]Value) Value) {
		vm.globals[name] = Value{tag: tFunc, ref: &builtin{fn: fn, name: name}}
	}
	one := func(f func(float64) float64) func([]Value) Value {
		return func(a []Value) Value { return Num(f(arg(a, 0))) }
	}
	two := func(f func(float64, float64) float64) func([]Value) Value {
		return func(a []Value) Value { return Num(f(arg(a, 0), arg(a, 1))) }
	}
	// print joins its arguments with spaces, matching Node's console.log.
	reg("print", func(a []Value) Value {
		out := ""
		for i, v := range a {
			if i > 0 {
				out += " "
			}
			out += v.toStr()
		}
		vm.out(out)
		return Undef()
	})
	// Error / RangeError / SyntaxError constructors.
	errObj := func(tag string, msg string) Value {
		return ObjVal(&Obj{props: map[string]Value{"message": Str(msg)}})
	}
	reg("Error", func(a []Value) Value {
		msg := "Error"
		if len(a) > 0 {
			msg = a[0].toStr()
		}
		return errObj("Error", msg)
	})
	reg("RangeError", func(a []Value) Value {
		msg := "RangeError"
		if len(a) > 0 {
			msg = a[0].toStr()
		}
		return errObj("RangeError", msg)
	})
	reg("SyntaxError", func(a []Value) Value {
		msg := "SyntaxError"
		if len(a) > 0 {
			msg = a[0].toStr()
		}
		return errObj("SyntaxError", msg)
	})
	reg("TypeError", func(a []Value) Value {
		msg := "TypeError"
		if len(a) > 0 {
			msg = a[0].toStr()
		}
		return errObj("TypeError", msg)
	})
	reg("sqrt", one(math.Sqrt))
	reg("abs", one(math.Abs))
	reg("floor", one(math.Floor))
	reg("ceil", one(math.Ceil))
	reg("round", one(math.Round))
	reg("pow", two(math.Pow))
	reg("min", two(math.Min))
	reg("max", two(math.Max))
	reg("random", func(a []Value) Value { return Num(randFloat()) })

	mathObj := NewObj()
	for _, n := range []string{"sqrt", "abs", "floor", "ceil", "round", "pow", "min", "max", "random"} {
		mathObj.set(n, vm.globals[n])
	}
	mathObj.set("PI", Num(math.Pi))
	vm.globals["Math"] = ObjVal(mathObj)

	console := NewObj()
	console.set("log", vm.globals["print"])
	console.set("error", vm.globals["print"])
	vm.globals["console"] = ObjVal(console)
}

func arg(a []Value, i int) float64 {
	if i < len(a) {
		return a[i].toNumber()
	}
	return 0
}

var rngState uint64 = 0x2545F4914F6CDD1D

func randFloat() float64 {
	rngState ^= rngState << 13
	rngState ^= rngState >> 7
	rngState ^= rngState << 17
	return float64(rngState>>11) / float64(1<<53)
}

// builtin is a host function stored in the globals object.
type builtin struct {
	fn    func([]Value) Value
	name  string
	props map[string]Value
	// recvFirst marks a prototype method whose first argument is the receiver,
	// so `obj.m(x)` and `m.call(obj, x)` both read as fn(obj, x).
	recvFirst bool
}

// New makes a host function constructable, so `new X(...)` works for the
// built-ins installed as plain functions.
func (b *builtin) New(args []Value) Value {
	if b.fn != nil {
		return b.fn(args)
	}
	return Undef()
}

// strErr builds an error object for a given Error subclass.
func strErr(kind, msg string) Value {
	return ObjVal(&Obj{props: map[string]Value{"message": Str(msg), "name": Str(kind)}})
}

// errToValue converts an escaping error into the value a JS handler sees.
func errToValue(err error) Value {
	if te, ok := err.(*ThrowError); ok {
		return te.Value
	}
	return strErr("Error", err.Error())
}

// enqueue schedules work on the microtask queue, which RunJobs drains.
func (vm *VM) enqueue(fn func()) { vm.jobs = append(vm.jobs, fn) }

// RunJobs runs queued microtasks — promise reactions and resumed `await`s —
// until none remain. The host event loop calls this after each macrotask.
func (vm *VM) RunJobs() {
	for len(vm.jobs) > 0 {
		fn := vm.jobs[0]
		vm.jobs = vm.jobs[1:]
		fn()
	}
}

// HasJobs reports whether microtasks are still waiting, so a host loop can
// stay alive until they finish.
func (vm *VM) HasJobs() bool { return len(vm.jobs) > 0 }

// errObjFor builds an error object for a builtin error constructor.
func errObjFor(bf *builtin, msg string) Value {
	name := bf.name
	if name == "" {
		name = "Error"
	}
	return ObjVal(&Obj{props: map[string]Value{"message": Str(msg), "name": Str(name)}})
}

// Run parses, compiles and executes src, returning the last value.
func (vm *VM) Run(src string) (Value, error) {
	p, err := Parse(src)
	if err != nil {
		return Undef(), err
	}
	proto := NewCompiler().Compile(p)
	vm.frames = vm.frames[:0]
	vm.frames = append(vm.frames, newFrame(proto))
	return vm.loop()
}

// RunProto executes an already-compiled prototype with the given arguments.
func (vm *VM) RunProto(proto *FuncProto, args ...Value) (Value, error) {
	vm.frames = vm.frames[:0]
	f := newFrame(proto)
	for i := 0; i < len(args) && i+int(proto.ParamBase) < len(f.locals); i++ {
		f.locals[i+int(proto.ParamBase)] = args[i]
	}
	for _, hs := range proto.HeapSlots {
		if hs.Slot < int32(len(f.locals)) && hs.Heap < int32(len(f.heap)) {
			f.heap[hs.Heap] = f.locals[hs.Slot]
		}
	}
	vm.frames = append(vm.frames, f)
	return vm.loop()
}

func (vm *VM) loop() (Value, error) {
	for {
		f := vm.frames[len(vm.frames)-1]

		// Dispatch any in-flight exception: to this frame's innermost handler,
		// or outward to the caller.
		if f.pendingF {
			if n := len(f.trys); n > 0 {
				tf := f.trys[n-1]
				f.trys = f.trys[:n-1]
				f.pendingF = false
				f.st.push(f.pending)
				f.pending = Undef()
				f.pc = tf.catchTarget
				continue
			}
			val := f.pending
			nargs := f.nargs
			vm.frames = vm.frames[:len(vm.frames)-1]
			if f.promise != nil {
				// An async function rejects its promise; nothing above it on
				// the stack is waiting for the throw.
				f.promise.reject(val)
				recycle(f)
				return Undef(), nil
			}
			recycle(f)
			if len(vm.frames) <= vm.baseDepth {
				return Undef(), &ThrowError{Value: val}
			}
			parent := vm.frames[len(vm.frames)-1]
			for i := 0; i <= nargs && parent.st.sp > 0; i++ {
				parent.st.pop()
			}
			parent.pending = val
			parent.pendingF = true
			continue
		}

		code := f.proto.Code
		in := code[f.pc]
		f.pc++

		// Operate directly on the frame's own operand stack; no per-instruction
		// frame or stack copy, which is what makes the dispatch loop tight.
		st := &f.st

		switch in.Op {
		case OpNop:
		case OpConst:
			st.push(f.proto.Consts[in.A])
		case OpLoadLocal:
			st.push(f.locals[int(in.A)])
		case OpStoreLocal:
			f.locals[int(in.A)] = st.pop()
		case OpLoadHeap:
			st.push(f.heap[int(in.A)])
		case OpStoreHeap:
			f.heap[int(in.A)] = st.pop()
		case OpLoadUpval:
			st.push(*f.closure.Caps[int(in.A)])
		case OpStoreUpval:
			*f.closure.Caps[int(in.A)] = st.pop()
		case OpMakeClosure:
			st.push(f.closureFor(f.proto.Consts[in.A]))
		case OpSelfFunc:
			if f.closure != nil {
				st.push(ClosureVal(f.closure))
			} else {
				st.push(Func(f.proto))
			}
		case OpAwait:
			v := st.pop()
			p := v.AsPromise()
			if p == nil {
				// Await passes a non-promise through, but always yields to the
				// microtask queue first (Node semantics): the code after the
				// await never runs before the current synchronous work.
				if g := f.gen; g != nil && !g.cp.Async {
					// A sync generator cannot suspend on a non-promise: its
					// caller drives it to the next yield synchronously.
					st.push(v)
					continue
				}
				resume := f
				vm.frames = vm.frames[:len(vm.frames)-1]
				if g := f.gen; g != nil {
					g.awaiting = true
					g.frame = f
					vm.enqueue(func() { g.awaiting = false; vm.resume(resume, v, false) })
				} else {
					vm.enqueue(func() { vm.resume(resume, v, false) })
				}
				return Undef(), nil
			}
			switch p.state {
			case pending:
				// Suspend: this activation becomes a continuation and the
				// stack unwinds to whatever is still running.
				resume := f
				vm.frames = vm.frames[:len(vm.frames)-1]
				if g := f.gen; g != nil {
					g.awaiting = true
					g.frame = f
					p.subscribeFn(func(val Value, rej bool) {
						g.awaiting = false
						if err := vm.resume(resume, val, rej); err != nil {
							g.settleErr(err)
						}
					}, nil)
				} else {
					p.subscribeFn(func(val Value, rej bool) { vm.resume(resume, val, rej) }, nil)
				}
				return Undef(), nil
			case rejected:
				f.pending, f.pendingF = p.reason, true
				continue
			default:
				st.push(p.value)
			}
		case OpLoadGlobal:
			v, ok := vm.lookupGlobal(f.proto.Consts[in.A].AsStr())
			if !ok {
				v = Undef()
			}
			st.push(v)
		case OpStoreGlobal:
			vm.globals[f.proto.Consts[in.A].AsStr()] = st.pop()
		case OpAdd:
			b := st.pop()
			a := st.pop()
			st.push(add(a, b))
		case OpSub:
			b := st.pop()
			a := st.pop()
			st.push(Num(a.toNumber() - b.toNumber()))
		case OpMul:
			b := st.pop()
			a := st.pop()
			st.push(Num(a.toNumber() * b.toNumber()))
		case OpDiv:
			b := st.pop()
			a := st.pop()
			st.push(Num(a.toNumber() / b.toNumber()))
		case OpMod:
			b := st.pop()
			a := st.pop()
			st.push(Num(math.Mod(a.toNumber(), b.toNumber())))
		case OpPow:
			b := st.pop()
			a := st.pop()
			st.push(Num(math.Pow(a.toNumber(), b.toNumber())))
		case OpNeg:
			st.push(Num(-st.pop().toNumber()))
		case OpNot:
			st.push(Bool(!st.pop().truthy()))
		case OpBitNot:
			st.push(Num(float64(^int64(st.pop().toNumber()))))
		case OpBitAnd:
			b := st.pop()
			a := st.pop()
			st.push(Num(float64(int32(a.toNumber()) & int32(b.toNumber()))))
		case OpBitOr:
			b := st.pop()
			a := st.pop()
			st.push(Num(float64(int32(a.toNumber()) | int32(b.toNumber()))))
		case OpBitXor:
			b := st.pop()
			a := st.pop()
			st.push(Num(float64(int32(a.toNumber()) ^ int32(b.toNumber()))))
		case OpShl:
			b := st.pop()
			a := st.pop()
			st.push(Num(float64(int32(a.toNumber()) << (int32(b.toNumber()) & 0x1f))))
		case OpShr:
			b := st.pop()
			a := st.pop()
			st.push(Num(float64(int32(a.toNumber()) >> (int32(b.toNumber()) & 0x1f))))
		case OpEq:
			b := st.pop()
			a := st.pop()
			st.push(Bool(looseEq(a, b)))
		case OpNe:
			b := st.pop()
			a := st.pop()
			st.push(Bool(!looseEq(a, b)))
		case OpStrictEq:
			b := st.pop()
			a := st.pop()
			st.push(Bool(strictEq(a, b)))
		case OpStrictNe:
			b := st.pop()
			a := st.pop()
			st.push(Bool(!strictEq(a, b)))
		case OpLt:
			b := st.pop()
			a := st.pop()
			st.push(Bool(compare(a, b) < 0))
		case OpGt:
			b := st.pop()
			a := st.pop()
			st.push(Bool(compare(a, b) > 0))
		case OpLe:
			b := st.pop()
			a := st.pop()
			st.push(Bool(compare(a, b) <= 0))
		case OpGe:
			b := st.pop()
			a := st.pop()
			st.push(Bool(compare(a, b) >= 0))
		case OpJump:
			f.pc = int(in.A)
		case OpJumpIfFalse:
			if !st.pop().truthy() {
				f.pc = int(in.A)
			}
		case OpJumpIfTrue:
			if st.pop().truthy() {
				f.pc = int(in.A)
			}
		case OpPop:
			st.pop()
		case OpDup:
			if st.sp > 0 {
				st.push(st.s[st.sp-1])
			}
		case OpCall:
			nargs := int(in.B)
			base := st.sp - nargs
			callee := st.s[base-1]
			if cp, cl := callee.FuncParts(); cp != nil {
				if res, pushed := vm.startCall(cp, cl, nargs, Undef(), st.s[base:st.sp], nil); !pushed {
					st.sp = base - 1
					st.push(res)
					continue
				}
				continue
			}
			args := st.s[base:st.sp]
			if !isCallable(callee) {
				st.sp = base - 1
				f.pending = strErr("TypeError", callDesc(f.proto.Consts[in.A].AsStr())+" is not a function")
				f.pendingF = true
				continue
			}
			res, te := vm.callBuiltin(callee, args)
			st.sp = base - 1
			if te != nil {
				f.pending, f.pendingF = te.Value, true
				continue
			}
			st.push(res)
		case OpCallMethod:
			nargs := int(in.B)
			obj := st.s[st.sp-1-nargs]
			args := make([]Value, nargs)
			for i := 0; i < nargs; i++ {
				args[i] = st.s[st.sp-nargs+i]
			}
			cs := f.proto.Consts[in.A].AsStr()
			name, desc := cs, cs
			if i := strings.IndexByte(cs, 0); i >= 0 {
				name, desc = cs[:i], cs[i+1:]
			}
			res, pushed, te := vm.callMethodValue(obj, name, desc, args)
			if te != nil {
				// Drop the receiver and arguments: the throw dispatches at the
				// loop head, which pushes the error onto the current stack.
				st.sp -= nargs + 1
				f.pending, f.pendingF = te.Value, true
				continue
			}
			if pushed {
				continue
			}
			for i := 0; i <= nargs; i++ {
				st.pop()
			}
			st.push(res)
		case OpCallDynamic:
			arr := st.pop()
			callee := st.pop()
			var args []Value
			if a := arr.AsArr(); a != nil {
				args = a.elems
			}
			if !isCallable(callee) {
				f.pending = strErr("TypeError", callDesc(f.proto.Consts[in.A].AsStr())+" is not a function")
				f.pendingF = true
				continue
			}
			st.push(callee)
			for _, a := range args {
				st.push(a)
			}
			if cp, cl := callee.FuncParts(); cp != nil {
				if res, pushed := vm.startCall(cp, cl, len(args), Undef(), args, nil); !pushed {
					st.sp -= len(args) + 1
					st.push(res)
					continue
				}
				continue
			}
			st.sp -= len(args) + 1
			dres, dte := vm.callBuiltin(callee, args)
			if dte != nil {
				f.pending, f.pendingF = dte.Value, true
				continue
			}
			st.push(dres)
		case OpMakeArray:
			n := int(in.A)
			arr := NewArr(n)
			for i := n - 1; i >= 0; i-- {
				arr.elems[i] = st.pop()
			}
			st.push(ArrVal(arr))
		case OpNewArray:
			st.push(ArrVal(NewArr(0)))
		case OpArrayPush:
			v := st.pop()
			if a := st.s[st.sp-1].AsArr(); a != nil {
				a.elems = append(a.elems, v)
			}
		case OpArrayPushAll:
			src := st.pop()
			if dst := st.s[st.sp-1].AsArr(); dst != nil {
				if s := src.AsArr(); s != nil {
					dst.elems = append(dst.elems, s.elems...)
				} else if ob := src.AsObj(); ob != nil && ob.kind == kindU8 {
					for _, b := range ob.window() {
						dst.elems = append(dst.elems, Num(float64(b)))
					}
				} else {
					if te := vm.spreadIterable(src, func(v Value) {
						dst.elems = append(dst.elems, v)
					}); te != nil {
						f.pending, f.pendingF = te.Value, true
						continue
					}
				}
			}
		case OpMakeObject:
			n := int(in.A)
			o := NewObj()
			o.proto = vm.objectProto
			// The key/value pairs sit on the stack in source order; pop them
			// into a buffer and replay forwards so insertion order is kept.
			pairs := make([]Value, 2*n)
			for i := 2*n - 1; i >= 0; i-- {
				pairs[i] = st.pop()
			}
			for i := 0; i < n; i++ {
				o.set(pairs[2*i].AsStr(), pairs[2*i+1])
			}
			st.push(ObjVal(o))
		case OpNewObject:
			o := NewObj()
			o.proto = vm.objectProto
			st.push(ObjVal(o))
		case OpObjSet:
			v := st.pop()
			if o := st.s[st.sp-1].AsObj(); o != nil {
				o.set(f.proto.Consts[in.A].AsStr(), v)
			}
		case OpSetAccessor:
			v := st.pop()
			if o := st.s[st.sp-1].AsObj(); o != nil {
				o.setAccessor(f.proto.Consts[in.A].AsStr(), in.B != 0, v)
			}
		case OpObjSetIdx:
			v := st.pop()
			k := st.pop()
			if o := st.s[st.sp-1].AsObj(); o != nil {
				o.set(k.toStr(), v)
			}
		case OpObjAssign:
			src := st.pop()
			if dst := st.s[st.sp-1].AsObj(); dst != nil {
				if s := src.AsObj(); s != nil {
					for k, v := range s.props {
						dst.set(k, v)
					}
				}
			}
		case OpGetField:
			o := st.pop()
			gv, gte := vm.getField(o, f.proto.Consts[in.A].AsStr())
			if gte != nil {
				f.pending, f.pendingF = gte.Value, true
				continue
			}
			st.push(gv)
		case OpSetField:
			v := st.pop()
			o := st.pop()
			if ste := vm.setField(o, f.proto.Consts[in.A].AsStr(), v); ste != nil {
				f.pending, f.pendingF = ste.Value, true
				continue
			}
			st.push(v)
		case OpGetIndex:
			k := st.pop()
			o := st.pop()
			if ob := o.AsObj(); ob != nil && ob.kind != kindNone {
				st.push(vm.typedIndex(ob, k))
			} else if a := o.AsArr(); a != nil {
				st.push(a.get(int(k.toNumber())))
			} else if o.tag == tStr {
				i := int(k.toNumber())
				s := o.AsStr()
				if i >= 0 && i < len(s) {
					st.push(Str(string(s[i])))
				} else {
					st.push(Undef())
				}
			} else {
				gv, gte := vm.getField(o, k.toStr())
				if gte != nil {
					f.pending, f.pendingF = gte.Value, true
					continue
				}
				st.push(gv)
			}
		case OpSetIndex:
			v := st.pop()
			k := st.pop()
			o := st.pop()
			if ob := o.AsObj(); ob != nil && ob.kind != kindNone {
				typedSetIndex(ob, k, v)
			} else if a := o.AsArr(); a != nil {
				a.set(int(k.toNumber()), v)
			} else {
				if ste := vm.setField(o, k.toStr(), v); ste != nil {
					f.pending, f.pendingF = ste.Value, true
					continue
				}
			}
			st.push(v)
		case OpTypeof:
			st.push(Str(typeOf(st.pop())))
		case OpInstanceOf:
			rhs := st.pop()
			lhs := st.pop()
			ok, te := vm.instanceOf(lhs, rhs)
			if te != nil {
				f.pending, f.pendingF = te.Value, true
				continue
			}
			st.push(Bool(ok))
		case OpIn:
			rhs := st.pop()
			lhs := st.pop()
			st.push(Bool(vm.inProp(lhs, rhs)))
		case OpPrint:
			vm.out(st.pop().toStr())
		case OpIsNull:
			st.push(Bool(st.pop().IsNull()))
		case OpIsUndef:
			st.push(Bool(st.pop().IsUndef()))
		case OpIterInit:
			iter := st.pop()
			f.locals[int(in.A)] = iter
			f.locals[int(in.A)+1] = Num(0)
			f.locals[int(in.A)+2] = Num(float64(in.B))
			f.locals[int(in.A)+3] = Num(0)
			// Arrays, strings and typed views iterate by index; anything else
			// goes through the Symbol.iterator protocol.
			if !indexIterable(iter) {
				if it, ite := vm.iteratorOf(iter); ite == nil {
					f.locals[int(in.A)] = it
					f.locals[int(in.A)+3] = Num(1)
				} else {
					f.pending, f.pendingF = ite.Value, true
				}
			}
		case OpIterNext:
			iterSlot := int(in.A)
			if f.locals[iterSlot+3].toNumber() != 0 {
				// Iterator protocol: one next() per iteration.
				it := f.locals[iterSlot]
				res, pushed, te := vm.callMethodValue(it, "next", "it.next", nil)
				if te != nil {
					f.pending, f.pendingF = te.Value, true
					continue
				}
				if pushed {
					continue
				}
				if ob := res.AsObj(); ob != nil && ob.get("done").truthy() {
					continue // done: fall through to the loop exit
				} else if ob != nil {
					st.push(ob.get("value"))
					f.pc = int(in.B)
				}
				continue
			}
			idxSlot := iterSlot + 1
			of := f.locals[iterSlot+2].toNumber() != 0
			i := int(f.locals[idxSlot].toNumber())
			if i < iterLen(f.locals[iterSlot]) {
				st.push(iterElem(f.locals[iterSlot], i, of))
				f.locals[idxSlot] = Num(float64(i + 1))
				f.pc = int(in.B)
			}
		case OpIterGet:
			it, ite := vm.iteratorOf(st.pop())
			if ite != nil {
				f.pending, f.pendingF = ite.Value, true
				continue
			}
			st.push(it)
		case OpSliceFrom:
			start := int(f.proto.Consts[in.A].toNumber())
			v := st.pop()
			if a := v.AsArr(); a != nil {
				if start > len(a.elems) {
					start = len(a.elems)
				}
				na := NewArr(len(a.elems) - start)
				copy(na.elems, a.elems[start:])
				st.push(ArrVal(na))
			} else {
				st.push(ArrVal(NewArr(0)))
			}
		case OpRest:
			// Bit 27 of B marks a captured rest parameter, which lives in the
			// frame's heap slice so closures can share it.
			restHeap := in.B&(1<<27) != 0
			start := int(in.B &^ (1 << 27))
			if start > len(f.args) {
				start = len(f.args)
			}
			na := NewArr(len(f.args) - start)
			copy(na.elems, f.args[start:])
			if restHeap {
				f.heap[int(in.A)] = ArrVal(na)
			} else {
				f.locals[int(in.A)] = ArrVal(na)
			}
		case OpThrow:
			f.pending = st.pop()
			f.pendingF = true
		case OpTryStart:
			f.trys = append(f.trys, &tryFrame{catchTarget: int(in.A), catchParamSlot: -1})
		case OpTryEnd:
			if n := len(f.trys); n > 0 {
				f.trys = f.trys[:n-1]
			}
		case OpCatchStart:
			v := st.pop()
			if in.B == 1 { // captured catch binding
				f.heap[int(in.A)] = v
			} else if in.A >= 0 && int(in.A) < len(f.locals) {
				f.locals[int(in.A)] = v
			}
		case OpNewTarget:
			st.push(f.newTarget)
		case OpNew:
			nargs := int(in.B)
			base := st.sp - nargs
			callee := st.s[base-1]
			args := make([]Value, nargs)
			copy(args, st.s[base:st.sp])
			if cls := callee.AsClass(); cls != nil {
				obj := NewObj()
				obj.class = cls
				obj.proto = cls.Proto
				if cls.Native != nil {
					te := cls.Native(obj, args)
					st.sp = base - 1
					if te != nil {
						f.pending, f.pendingF = te.Value, true
						continue
					}
					st.push(ObjVal(obj))
					continue
				}
				cp, cl := cls.Ctor.FuncParts()
				vm.pushFrame(cp, cl, nargs, ObjVal(obj), args, true, cls)
				vm.frames[len(vm.frames)-1].newTarget = callee
				continue
			}
			if cp, cl := callee.FuncParts(); cp != nil {
				// A plain function used with `new`: allocate an object whose
				// prototype is f.prototype and run the body with it as `this`.
				if cp.Gen || cp.Async || cp.NoThis {
					st.sp = base - 1
					f.pending, f.pendingF = strErr("TypeError", "not a constructor"), true
					continue
				}
				obj := NewObj()
				obj.proto = vm.objectProto
				if p, _ := vm.getField(callee, "prototype"); p.AsObj() != nil {
					obj.proto = p.AsObj()
				}
				vm.pushFrame(cp, cl, nargs, ObjVal(obj), args, true, nil)
				vm.frames[len(vm.frames)-1].newTarget = callee
				continue
			}
			if bf := callee.AsBuiltin(); bf != nil {
				msg := "Error"
				if len(args) > 0 {
					msg = args[0].toStr()
				}
				st.sp = base - 1
				st.push(errObjFor(bf, msg))
				continue
			}
			if hv := callee.AsHost(); hv != nil {
				// A host value with a constructor (e.g. Promise) allocates
				// itself; otherwise `new` just calls it, as before.
				st.sp = base - 1
				if nc, ok := hv.(interface{ New([]Value) Value }); ok {
					st.push(nc.New(args))
				} else {
					st.push(hv.Call(args))
				}
				continue
			}
			res, te := vm.callBuiltin(callee, args)
			st.sp = base - 1
			if te != nil {
				f.pending, f.pendingF = te.Value, true
				continue
			}
			st.push(res)
		case OpSuperCall:
			nargs := int(in.B)
			base := st.sp - nargs
			this := st.s[base-2]
			sup := st.s[base-1]
			args := make([]Value, nargs)
			copy(args, st.s[base:st.sp])
			st.sp = base - 2
			if cls := sup.AsClass(); cls != nil {
				if cls.Native != nil {
					if ob := this.AsObj(); ob != nil {
						te := cls.Native(ob, args)
						if te != nil {
							f.pending, f.pendingF = te.Value, true
							continue
						}
						st.push(ObjVal(ob))
						continue
					}
				}
				cp, cl := cls.Ctor.FuncParts()
				vm.pushFrame(cp, cl, nargs, this, args, true, cls)
				vm.frames[len(vm.frames)-1].newTarget = f.newTarget
				continue
			}
			st.push(Undef())
		case OpSuperCallDyn:
			// stack: [this, super, argsArray]
			av := st.pop()
			sup := st.pop()
			this := st.pop()
			var args []Value
			if a := av.AsArr(); a != nil {
				args = a.elems
			}
			if cls := sup.AsClass(); cls != nil {
				if cls.Native != nil {
					if ob := this.AsObj(); ob != nil {
						if te := cls.Native(ob, args); te != nil {
							f.pending, f.pendingF = te.Value, true
							continue
						}
						st.push(ObjVal(ob))
						continue
					}
				}
				cp, cl := cls.Ctor.FuncParts()
				vm.pushFrame(cp, cl, len(args), this, args, true, cls)
				vm.frames[len(vm.frames)-1].newTarget = f.newTarget
				continue
			}
			st.push(Undef())
		case OpSuperCallMeth:
			nargs := int(in.B)
			base := st.sp - nargs
			this := st.s[base-2]
			sup := st.s[base-1]
			args := make([]Value, nargs)
			copy(args, st.s[base:st.sp])
			st.sp = base - 2
			cls := sup.AsClass()
			if cls != nil {
				if m := lookupMethod(cls.Proto, f.proto.Consts[in.A].AsStr()); !m.IsUndef() {
					cp, cl := m.FuncParts()
					vm.pushFrame(cp, cl, nargs, this, args, false, cls)
					continue
				}
			}
			st.push(Undef())
		case OpClass:
			sup := st.pop()
			cls := f.proto.Consts[in.A].AsClass()
			if cls != nil {
				if len(cls.Methods) > 0 || cls.Ctor.AsFunc() != nil {
					cls = cls.bind(f)
					// A class instance reaches Object.prototype through its own
					// prototype unless it extends something else.
					if cls.Proto != nil && cls.Proto.proto == nil {
						cls.Proto.proto = vm.objectProto
					}
				}
				if s := sup.AsClass(); s != nil {
					cls.Super = s
					if cls.Proto != nil && s.Proto != nil {
						cls.Proto.proto = s.Proto
					}
				}
			}
			st.push(ClassVal(cls))
		case OpRegExp:
			flags := f.proto.Consts[int(in.B)].toStr()
			pattern := f.proto.Consts[int(in.A)].toStr()
			st.push(vm.NewRegExp(pattern, flags))
		case OpDeleteIndex:
			k := st.pop()
			o := st.pop()
			if ob := o.AsObj(); ob != nil && ob.kind == kindNone {
				delete(ob.props, k.toStr())
			} else if a := o.AsArr(); a != nil {
				a.set(int(k.toNumber()), Undef())
			}
			st.push(Bool(true))
		case OpYield:
			v := st.pop()
			// Suspend: the activation leaves the stack and the generator owns
			// it until the next next() resumes it with the sent value.
			f := vm.frames[len(vm.frames)-1]
			vm.frames = vm.frames[:len(vm.frames)-1]
			if vm.gen != nil && f.gen == vm.gen {
				vm.gen.frame = f
				vm.gen.yielded = v
				vm.gen.settle(v, false)
			}
			return v, nil
		case OpReturn:
			var v Value
			if st.sp > 0 {
				v = st.pop()
			} else {
				v = Undef()
			}
			if f.isCtor && in.A == 0 {
				v = f.locals[0]
			}
			callee := vm.frames[len(vm.frames)-1]
			vm.frames = vm.frames[:len(vm.frames)-1]
			if vm.gen != nil && callee.gen == vm.gen {
				// The activation came from a generator drive, so it finished:
				// the generator owns nothing until a later next() resumes it.
				vm.gen.frame = nil
				vm.gen.settle(v, true)
			}
			if callee.promise != nil {
				// An async function settles its promise instead of returning
				// a value to its caller.
				callee.promise.resolve(v)
			}
			if len(vm.frames) <= vm.baseDepth {
				recycle(callee)
				return v, nil
			}
			// The caller's stack still holds callee+args; replace with result.
			cf := vm.frames[len(vm.frames)-1]
			for i := 0; i <= callee.nargs && cf.st.sp > 0; i++ {
				cf.st.pop()
			}
			cf.st.push(v)
			recycle(callee)
			continue
		}
	}
}

// pushFrame appends a new activation for cp. thisVal becomes local slot 0.
func (vm *VM) pushFrame(cp *FuncProto, cl *Closure, nargs int, thisVal Value, args []Value, isCtor bool, cls *Class) {
	nf := frameFor(cp)
	nf.nargs = nargs
	nf.isCtor = isCtor
	nf.args = args
	nf.closure = cl
	nf.newTarget = Undef()
	if cp.NoThis && len(vm.frames) > 0 {
		// An arrow inherits the enclosing function's `new.target`.
		nf.newTarget = vm.frames[len(vm.frames)-1].newTarget
	}
	for i := range nf.locals {
		nf.locals[i] = Undef()
	}
	if cp.NumLocals > 0 && !cp.NoThis {
		nf.locals[0] = thisVal
	}
	if cp.HeapCount > 0 && nf.heap == nil {
		// Fresh backing array: closures keep pointers into it after we return.
		nf.heap = make([]Value, cp.HeapCount)
	}
	for i := 0; i < nargs && i+int(cp.ParamBase) < len(nf.locals); i++ {
		nf.locals[i+int(cp.ParamBase)] = args[i]
	}
	if cp.SuperSlot >= 0 && cls != nil && cls.Super != nil {
		nf.locals[cp.SuperSlot] = ClassVal(cls.Super)
	}
	for _, hs := range cp.HeapSlots {
		// Parameters, `this` and the superclass are written by the frame
		// itself, not by a store instruction, so mirror the captured ones.
		if hs.Slot < int32(len(nf.locals)) {
			nf.heap[hs.Heap] = nf.locals[hs.Slot]
		}
	}
	vm.frames = append(vm.frames, nf)
}

// cap resolves one upvalue spec of this activation's function value.
func (f *frame) cap(spec UpvalSpec) *Value {
	if spec.FromParentUpval >= 0 {
		return f.closure.Caps[spec.FromParentUpval]
	}
	return &f.heap[spec.HeapIdx]
}

// closureFor binds v (a bare function prototype) to the captures it needs,
// resolving them against the frame creating it. Functions with no captures
// stay as a bare *FuncProto, so the common path allocates nothing.
func (f *frame) closureFor(v Value) Value {
	cp, existing := v.FuncParts()
	if existing != nil || cp == nil || len(cp.Upvals) == 0 {
		return v
	}
	caps := make([]*Value, len(cp.Upvals))
	for i, sp := range cp.Upvals {
		caps[i] = f.cap(sp)
	}
	return ClosureVal(&Closure{Proto: cp, Caps: caps})
}

// lookupMethod walks a prototype chain for a method, returning the stored
// value so a closure's captures survive the call.
func lookupMethod(proto *Obj, name string) Value {
	for p := proto; p != nil; p = p.proto {
		if v, ok := p.props[name]; ok {
			if v.AsFunc() != nil {
				return v
			}
		}
	}
	return Undef()
}

// tryHost runs a host or builtin call, converting a native panic into a JS
// throw. Natives panic with *ThrowError to raise; anything else is a bug.
func tryHost(fn func() Value) (v Value, te *ThrowError) {
	defer func() {
		if r := recover(); r != nil {
			if t, ok := r.(*ThrowError); ok {
				v, te = Undef(), t
				return
			}
			panic(r)
		}
	}()
	return fn(), nil
}

// callBuiltin dispatches a host function.
func (vm *VM) callBuiltin(callee Value, args []Value) (Value, *ThrowError) {
	if hv := callee.AsHost(); hv != nil {
		return tryHost(func() Value { return hv.Call(args) })
	}
	if bf := callee.AsBuiltin(); bf != nil {
		return tryHost(func() Value { return bf.fn(args) })
	}
	if cp, _ := callee.FuncParts(); cp != nil {
		v, err := vm.callCallable(callee, args)
		if err != nil {
			return Undef(), asThrow(err)
		}
		return v, nil
	}
	return Undef(), nil
}

// asThrow normalizes an escaping error to a JS throw value.
func asThrow(err error) *ThrowError {
	if te, ok := err.(*ThrowError); ok {
		return te
	}
	return &ThrowError{Value: strErr("Error", err.Error())}
}

// invoke runs fn to completion with the given arguments, returning its result.
// Used when a host function needs to call back into JS (e.g. array callbacks).
func (vm *VM) invoke(fn Value, args []Value) Value {
	v, _ := vm.callCallable(fn, args)
	return v
}

// invokeMethod runs fn to completion with `this` bound to receiver. Accessors
// use it so a getter can read the properties of the object it was read off.
func (vm *VM) invokeMethod(fn Value, receiver Value, args []Value) (Value, *ThrowError) {
	cp, cl := fn.FuncParts()
	if cp == nil {
		v, err := vm.callCallable(fn, args)
		if err != nil {
			return Undef(), asThrow(err)
		}
		return v, nil
	}
	old := vm.baseDepth
	vm.baseDepth = len(vm.frames)
	vm.pushFrame(cp, cl, len(args), receiver, args, false, nil)
	v, err := vm.loop()
	vm.baseDepth = old
	if err != nil {
		return Undef(), asThrow(err)
	}
	return v, nil
}

// callCallable runs any callable to completion with `this` undefined.
func (vm *VM) callCallable(fn Value, args []Value) (Value, error) {
	return vm.callThis(fn, Undef(), args)
}

// callThis runs any callable to completion with an explicit `this`. An async
// function returns the promise it produces; an error that escapes reports as
// a ThrowError.
func (vm *VM) callThis(fn Value, this Value, args []Value) (Value, error) {
	cp, cl := fn.FuncParts()
	if cp == nil {
		if hv := fn.AsHost(); hv != nil {
			v, te := tryHost(func() Value { return hv.Call(args) })
			if te != nil {
				return Undef(), te
			}
			return v, nil
		}
		if bf := fn.AsBuiltin(); bf != nil {
			v, te := tryHost(func() Value { return bf.fn(args) })
			if te != nil {
				return Undef(), te
			}
			return v, nil
		}
		return Undef(), &ThrowError{Value: strErr("TypeError", typeOf(fn)+" is not a function")}
	}
	if cp.Async {
		p := newPromise(vm)
		vm.runAsync(cp, cl, len(args), this, args, p)
		return PromiseVal(p), nil
	}
	old := vm.baseDepth
	vm.baseDepth = len(vm.frames)
	vm.pushFrame(cp, cl, len(args), this, args, false, nil)
	v, err := vm.loop()
	vm.baseDepth = old
	return v, err
}

// CallBind calls fn as a method of recv, reporting errors the host can
// surface. Natives use it for listeners and callbacks that must run with a
// receiver and can throw.
func (vm *VM) CallBind(fn Value, recv Value, args ...Value) (Value, error) {
	return vm.callThis(fn, recv, args)
}

// startCall enters a JS function from an instruction. It reports whether a
// frame was pushed for the caller's loop to run; for an async function the
// promise it produced is returned instead.
func (vm *VM) startCall(cp *FuncProto, cl *Closure, nargs int, this Value, args []Value, cls *Class) (Value, bool) {
	if cp.Gen {
		// A generator function does not run on call: it returns a generator
		// object whose body advances on each next().
		return vm.newGenerator(cp, cl, this, args), false
	}
	if !cp.Async {
		vm.pushFrame(cp, cl, nargs, this, args, false, cls)
		return Undef(), true
	}
	p := newPromise(vm)
	vm.runAsync(cp, cl, nargs, this, args, p)
	return PromiseVal(p), false
}

// runAsync runs an async function to its return or to its first suspension.
func (vm *VM) runAsync(cp *FuncProto, cl *Closure, nargs int, this Value, args []Value, p *Promise) {
	// The frame outlives the caller's operand stack once it suspends, so it
	// must own its arguments.
	a := make([]Value, len(args))
	copy(a, args)
	old := vm.baseDepth
	vm.baseDepth = len(vm.frames)
	vm.pushFrame(cp, cl, len(a), this, a, false, nil)
	vm.frames[len(vm.frames)-1].promise = p
	vm.loop()
	vm.baseDepth = old
}

// resume continues a frame suspended on an `await`, as a microtask. It
// reports the error an uncaught throw produced, so a suspended generator can
// settle its pending next() promise.
func (vm *VM) resume(f *frame, v Value, rej bool) error {
	vm.frames = append(vm.frames, f)
	if rej {
		f.pending, f.pendingF = v, true
	} else {
		f.st.push(v)
	}
	old := vm.baseDepth
	vm.baseDepth = len(vm.frames) - 1
	// A resumed generator continuation drives its own frame, so yields and
	// returns settle through it.
	prev := vm.gen
	if f.gen != nil {
		vm.gen = f.gen
	}
	_, err := vm.loop()
	vm.gen = prev
	vm.baseDepth = old
	return err
}

// callMethodValue dispatches `obj.name(args)`. It returns (result, false) for a
// callee that completed here, or (zero, true) when it pushed a JS frame to run.
// desc is the callee's source description ("o.x"); it names the TypeError
// thrown when the call target is missing or not callable.
func (vm *VM) callMethodValue(obj Value, name, desc string, args []Value) (Value, bool, *ThrowError) {
	if obj.IsNull() {
		w := "undefined"
		if obj.tag == tNull {
			w = "null"
		}
		return Undef(), false, &ThrowError{Value: strErr("TypeError",
			"Cannot read properties of "+w+" (reading '"+name+"')")}
	}
	if hv := obj.AsHost(); hv != nil {
		gv, gte := tryHost(func() Value { return hv.Get(name) })
		if gte != nil {
			return Undef(), false, gte
		}
		return vm.callResolved(gv, desc, obj, nil, args)
	}
	if cls := obj.AsClass(); cls != nil {
		if a := cls.Static.accessorFor(name); a != nil && !a.get.IsUndef() {
			v, te := vm.invokeMethod(a.get, obj, nil)
			if te != nil {
				return Undef(), false, te
			}
			return vm.callResolved(v, desc, obj, cls, args)
		}
		if v := cls.Static.get(name); v.tag == tFunc {
			if cp, cl := v.FuncParts(); cp != nil {
				res, pushed := vm.startCall(cp, cl, len(args), Undef(), args, cls)
				return res, pushed, nil
			}
			r, te := vm.callBuiltin(v, args)
			return r, false, te
		}
	}
	if o := obj.AsObj(); o != nil {
		// A method call that resolves to a getter runs the getter first; its
		// result is then called with the receiver as `this`.
		if a := o.accessorFor(name); a != nil && !a.get.IsUndef() {
			v, te := vm.invokeMethod(a.get, obj, nil)
			if te != nil {
				return Undef(), false, te
			}
			return vm.callResolved(v, desc, obj, o.class, args)
		}
		if m := o.get(name); m.tag == tFunc {
			if cp, cl := m.FuncParts(); cp != nil {
				res, pushed := vm.startCall(cp, cl, len(args), obj, args, o.class)
				return res, pushed, nil
			}
			if b := m.AsBuiltin(); b != nil && b.recvFirst {
				// A prototype method takes the receiver first, so
				// `obj.hasOwnProperty(key)` reads as fn(obj, key).
				all := make([]Value, 0, len(args)+1)
				all = append(all, obj)
				all = append(all, args...)
				r, te := vm.callBuiltin(Value{tag: tFunc, ref: b}, all)
				return r, false, te
			}
			r, te := vm.callBuiltin(m, args)
			return r, false, te
		}
		if o.kind != kindNone {
			if b, ok := vm.typedMethod(o, name); ok {
				return vm.callResolved(b, desc, obj, o.class, args)
			}
		}
	}
	if obj.tag == tNum && vm.numberProto != nil {
		// Number primitives inherit from Number.prototype; a method installed
		// there is called with the primitive as `this`.
		switch name {
		case "valueOf":
			return obj, false, nil
		case "toString":
			return Str(numToStr(obj.n)), false, nil
		}
		if v := vm.numberProto.get(name); v.tag == tFunc {
			if cp, cl := v.FuncParts(); cp != nil {
				res, pushed := vm.startCall(cp, cl, len(args), obj, args, nil)
				return res, pushed, nil
			}
			r, te := vm.callBuiltin(v, args)
			return r, false, te
		}
	}
	if a := obj.AsArr(); a != nil {
		if idx := arrayMethod(name); idx >= 0 {
			r, te := vm.callBuiltin(vm.builtinMethod(idx, a), args)
			return r, false, te
		}
	}
	if b := obj.AsBuiltin(); b != nil {
		switch name {
		case "apply":
			argVals := []Value{}
			if len(args) > 1 {
				if arr := args[1].AsArr(); arr != nil {
					argVals = arr.elems
				}
			}
			if b.recvFirst {
				all := make([]Value, 0, len(argVals)+1)
				if len(args) > 0 {
					all = append(all, args[0])
				}
				all = append(all, argVals...)
				v, te := tryHost(func() Value { return b.fn(all) })
				return v, false, te
			}
			v, te := tryHost(func() Value { return b.fn(argVals) })
			return v, false, te
		case "call":
			argVals := []Value{}
			if len(args) > 1 {
				argVals = args[1:]
			}
			if b.recvFirst {
				all := make([]Value, 0, len(argVals)+1)
				if len(args) > 0 {
					all = append(all, args[0])
				}
				all = append(all, argVals...)
				v, te := tryHost(func() Value { return b.fn(all) })
				return v, false, te
			}
			v, te := tryHost(func() Value { return b.fn(argVals) })
			return v, false, te
		case "bind":
			var bound []Value
			if len(args) > 1 {
				bound = append(bound, args[1:]...)
			}
			return BuiltinFn("bound", func(a []Value) Value {
				all := append(append([]Value{}, bound...), a...)
				v, te := tryHost(func() Value { return b.fn(all) })
				if te != nil {
					panic(asThrow(te))
				}
				return v
			}), false, nil
		}
		if m, ok := b.props[name]; ok {
			r, te := vm.callBuiltin(m, args)
			return r, false, te
		}
	}
	if cls := obj.AsClass(); cls != nil {
		if m := lookupMethod(cls.Proto, name); !m.IsUndef() {
			cp, cl := m.FuncParts()
			res, pushed := vm.startCall(cp, cl, len(args), Undef(), args, cls)
			return res, pushed, nil
		}
	}
	if cp, _ := obj.FuncParts(); cp != nil {
		// Function.prototype.call / apply / bind over interpreter functions.
		switch name {
		case "apply":
			thisVal := Undef()
			var spread []Value
			if len(args) > 0 {
				thisVal = args[0]
			}
			if len(args) > 1 {
				if arr := args[1].AsArr(); arr != nil {
					spread = arr.elems
				}
			}
			v, err := vm.callThis(obj, thisVal, spread)
			if err != nil {
				return Undef(), false, asThrow(err)
			}
			return v, false, nil
		case "call":
			thisVal := Undef()
			var rest []Value
			if len(args) > 0 {
				thisVal = args[0]
			}
			if len(args) > 1 {
				rest = args[1:]
			}
			v, err := vm.callThis(obj, thisVal, rest)
			if err != nil {
				return Undef(), false, asThrow(err)
			}
			return v, false, nil
		case "bind":
			thisVal := Undef()
			var bound []Value
			if len(args) > 0 {
				thisVal = args[0]
			}
			if len(args) > 1 {
				bound = append(bound, args[1:]...)
			}
			target := obj
			return BuiltinFn("bound", func(a []Value) Value {
				all := append(append(make([]Value, 0, len(bound)+len(a)), bound...), a...)
				v, err := vm.callThis(target, thisVal, all)
				if err != nil {
					panic(asThrow(err))
				}
				return v
			}), false, nil
		}
	}
	if obj.tag == tStr {
		if m := stringMethod(name, obj.AsStr()); !m.IsUndef() {
			r, te := vm.callBuiltin(m, args)
			return r, false, te
		}
	}
	return Undef(), false, &ThrowError{Value: strErr("TypeError", desc+" is not a function")}
}

// callResolved runs an already-resolved callee, throwing Node's
// "<desc> is not a function" TypeError when it is not callable.
func (vm *VM) callResolved(callee Value, desc string, this Value, cls *Class, args []Value) (Value, bool, *ThrowError) {
	if cp, cl := callee.FuncParts(); cp != nil {
		res, pushed := vm.startCall(cp, cl, len(args), this, args, cls)
		return res, pushed, nil
	}
	if hv := callee.AsHost(); hv != nil {
		v, te := tryHost(func() Value { return hv.Call(args) })
		return v, false, te
	}
	if bf := callee.AsBuiltin(); bf != nil {
		v, te := tryHost(func() Value { return bf.fn(args) })
		return v, false, te
	}
	return Undef(), false, &ThrowError{Value: strErr("TypeError", desc+" is not a function")}
}

// callDesc guards TypeError subjects: every emit site passes calleeDesc output,
// which is never empty, but a stale constant must not render a blank message.
func callDesc(s string) string {
	if s == "" {
		return "(intermediate value)"
	}
	return s
}

func (vm *VM) getField(o Value, name string) (Value, *ThrowError) {
	if hv := o.AsHost(); hv != nil {
		v, te := tryHost(func() Value { return hv.Get(name) })
		return v, te
	}
	if b := o.AsBuiltin(); b != nil {
		if v, ok := b.props[name]; ok {
			return v, nil
		}
		if name == "prototype" && vm.numberProto != nil && b.name == "Number" {
			return ObjVal(vm.numberProto), nil
		}
		return BuiltinFn(b.name, b.fn), nil
	}
	if cls := o.AsClass(); cls != nil {
		if cls.Static != nil {
			if a := cls.Static.accessorFor(name); a != nil && !a.get.IsUndef() {
				return vm.invokeMethod(a.get, o, nil)
			}
			if v := cls.Static.get(name); !v.IsUndef() {
				return v, nil
			}
		}
		if name == "prototype" && cls.Proto != nil {
			return ObjVal(cls.Proto), nil
		}
		if cls.Proto != nil {
			return cls.Proto.get(name), nil
		}
		return Undef(), nil
	}
	if cp := o.AsFunc(); cp != nil {
		// A plain function's `prototype`, created on first use so instances
		// made with `new` share it and `instanceof` can walk to it.
		if name == "prototype" {
			if cp.ProtoObj == nil {
				po := NewObj()
				po.proto = vm.objectProto
				po.set("constructor", o)
				cp.ProtoObj = po
			}
			return ObjVal(cp.ProtoObj), nil
		}
		return Undef(), nil
	}
	if o.tag == tObj {
		if a := o.AsArr(); a != nil {
			if name == "length" {
				return Num(float64(len(a.elems))), nil
			}
			if idx := arrayMethod(name); idx >= 0 {
				return vm.builtinMethod(idx, a), nil
			}
		}
		if ob := o.AsObj(); ob != nil {
			// An accessor is invoked with the receiver as `this`, matching a
			// plain method call, so a getter can read sibling properties.
			if name == "__proto__" {
				if ob.proto == nil {
					return Null(), nil
				}
				return ObjVal(ob.proto), nil
			}
			if a := ob.accessorFor(name); a != nil && !a.get.IsUndef() {
				return vm.invokeMethod(a.get, ObjVal(ob), nil)
			}
			if v := ob.get(name); !v.IsUndef() {
				return v, nil
			}
			if ob.kind != kindNone {
				if v, ok := vm.typedGet(ob, name); ok {
					return v, nil
				}
			}
			return Undef(), nil
		}
	}
	if o.tag == tStr {
		if name == "length" {
			return Num(float64(len(o.AsStr()))), nil
		}
		if m := stringMethod(name, o.AsStr()); !m.IsUndef() {
			return m, nil
		}
	}
	if o.tag == tNum && vm.numberProto != nil {
		if v := vm.numberProto.get(name); !v.IsUndef() {
			return v, nil
		}
	}
	return Undef(), nil
}

func (vm *VM) setField(o Value, name string, v Value) *ThrowError {
	if hv := o.AsHost(); hv != nil {
		_, te := tryHost(func() Value { hv.Set(name, v); return Undef() })
		return te
	}
	if cls := o.AsClass(); cls != nil && cls.Static != nil {
		if a := cls.Static.accessorFor(name); a != nil && !a.set.IsUndef() {
			_, te := vm.invokeMethod(a.set, o, []Value{v})
			return te
		}
		cls.Static.set(name, v)
		return nil
	}
	if cp := o.AsFunc(); cp != nil {
		if name == "prototype" {
			cp.ProtoObj = v.AsObj()
		}
		return nil
	}
	if o.tag == tObj {
		if ob := o.AsObj(); ob != nil {
			if ob.kind != kindNone && typedSetIndex(ob, Str(name), v) {
				return nil
			}
			if name == "__proto__" {
				ob.proto = v.AsObj()
				return nil
			}
			if a := ob.accessorFor(name); a != nil && !a.set.IsUndef() {
				_, te := vm.invokeMethod(a.set, o, []Value{v})
				return te
			}
			ob.set(name, v)
		}
	}
	return nil
}

// instanceOf implements `lhs instanceof rhs`: true when lhs's prototype chain
// reaches rhs's prototype.
// hostInstanceOf reports `lhs instanceof rhs` for host collection values.
func (vm *VM) hostInstanceOf(lhs HostValue, rhs HostValue) (bool, *ThrowError) {
	switch rhs.(type) {
	case *mapCtor:
		_, ok := lhs.(*jsMap)
		return ok, nil
	case *setCtor:
		_, ok := lhs.(*jsSet)
		return ok, nil
	}
	return false, &ThrowError{Value: strErr("TypeError", "Right-hand side of 'instanceof' is not callable")}
}

func (vm *VM) instanceOf(lhs, rhs Value) (bool, *ThrowError) {
	var proto *Obj
	if cls := rhs.AsClass(); cls != nil {
		proto = cls.Proto
	} else if cp, cl := rhs.FuncParts(); cp != nil || cl != nil {
		p, _ := vm.getField(rhs, "prototype")
		proto = p.AsObj()
	} else if rhs.AsBuiltin() != nil {
		// A native function has no own prototype; only an explicitly assigned
		// one participates in `instanceof`.
		p, _ := vm.getField(rhs, "prototype")
		proto = p.AsObj()
	} else if rhv := rhs.AsHost(); rhv != nil {
		var lh HostValue
		if lhv := lhs.AsHost(); lhv != nil {
			lh = lhv
		}
		return vm.hostInstanceOf(lh, rhv)
	} else {
		return false, &ThrowError{Value: strErr("TypeError", "Right-hand side of 'instanceof' is not callable")}
	}
	if proto == nil {
		return false, nil
	}
	ob := lhs.AsObj()
	if ob == nil {
		// Ponytail: jit arrays carry no class, so `[] instanceof Array` is
		// false; add an Array class when a builtin needs it.
		return false, nil
	}
	for p := ob; p != nil; p = p.proto {
		if p == proto {
			return true, nil
		}
	}
	return false, nil
}

// inProp implements `k in o`.
func (vm *VM) inProp(k Value, o Value) bool {
	name := k.AsStr()
	if name == "" && k.tag == tNum {
		name = numToStr(k.n)
	}
	if a := o.AsArr(); a != nil {
		if name == "length" {
			return true
		}
		if i, err := strconv.Atoi(name); err == nil && i >= 0 && i < len(a.elems) {
			return true
		}
	}
	if ob := o.AsObj(); ob != nil {
		if ob.kind != kindNone {
			if !vm.typedIndex(ob, k).IsUndef() {
				return true
			}
			if _, ok := vm.typedMethod(ob, name); ok {
				return true
			}
		}
		for p := ob; p != nil; p = p.proto {
			if _, ok := p.props[name]; ok {
				return true
			}
			if _, ok := p.accessors[name]; ok {
				return true
			}
		}
	}
	return false
}

// indexIterable reports whether v iterates by position, which the fast for-of
// path handles without building an iterator.
func indexIterable(v Value) bool {
	if v.AsArr() != nil || v.tag == tStr {
		return true
	}
	if o := v.AsObj(); o != nil {
		return o.kind == kindU8 || o.kind == kindNone
	}
	return false
}

// iteratorOf resolves src to an iterator object. Arrays, strings and byte
// views get a built-in iterator; anything else goes through the
// Symbol.iterator protocol.
func (vm *VM) iteratorOf(src Value) (Value, *ThrowError) {
	switch {
	case src.AsArr() != nil:
		return Host(&iterHost{kind: iterArr, a: src.AsArr()}), nil
	case src.tag == tStr:
		return Host(&iterHost{kind: iterStr, s: src.AsStr()}), nil
	case src.AsObj() != nil && src.AsObj().kind == kindU8:
		o := src.AsObj()
		return Host(&iterHost{kind: iterU8, w: o.window()}), nil
	}
	if _, ok := src.AsHost().(*jsGenerator); ok {
		// A generator is its own iterator.
		return src, nil
	}
	gv, gte := vm.getField(src, symIteratorKey)
	if gte != nil {
		return Undef(), gte
	}
	if !isCallable(gv) {
		return Undef(), &ThrowError{Value: strErr("TypeError", src.toStr()+" is not iterable")}
	}
	it, err := vm.callThis(gv, Undef(), nil)
	if err != nil {
		return Undef(), asThrow(err)
	}
	return it, nil
}

// iterLen reports the number of elements iterated by for-of / for-in.
func iterLen(v Value) int {
	if a := v.AsArr(); a != nil {
		return len(a.elems)
	}
	if v.tag == tStr {
		return len(v.AsStr())
	}
	if o := v.AsObj(); o != nil {
		if o.kind == kindU8 {
			return o.byteLen
		}
		return len(o.props)
	}
	return 0
}

// iterElem returns the i-th iteration value: the element for for-of, the key
// for for-in.
func iterElem(v Value, i int, of bool) Value {
	if a := v.AsArr(); a != nil {
		if of {
			return a.get(i)
		}
		return Str(numToStr(float64(i)))
	}
	if v.tag == tStr {
		s := v.AsStr()
		if i < len(s) {
			return Str(string(s[i]))
		}
		return Undef()
	}
	if o := v.AsObj(); o != nil {
		if o.kind == kindU8 {
			if of {
				w := o.window()
				if i < len(w) {
					return Num(float64(w[i]))
				}
				return Undef()
			}
			return Str(numToStr(float64(i)))
		}
		keys := o.keys()
		if i < len(keys) {
			if of {
				return o.get(keys[i])
			}
			return Str(keys[i])
		}
	}
	return Undef()
}

func typeOf(v Value) string {
	switch v.tag {
	case tUndef:
		return "undefined"
	case tNull:
		return "object"
	case tBool:
		return "boolean"
	case tNum:
		return "number"
	case tStr:
		return "string"
	case tFunc:
		return "function"
	case tObj:
		return "object"
	case tHost:
		if h, ok := v.ref.(HostValue); ok {
			return h.Type()
		}
		return "object"
	}
	return "undefined"
}

func strictEq(a, b Value) bool {
	if a.tag != b.tag {
		return false
	}
	switch a.tag {
	case tNum:
		return a.n == b.n
	case tStr:
		return a.AsStr() == b.AsStr()
	case tBool:
		return a.n == b.n
	case tUndef, tNull:
		return true
	case tObj, tFunc, tHost:
		return a.ref == b.ref
	}
	return false
}

func looseEq(a, b Value) bool {
	if a.tag == b.tag {
		return strictEq(a, b)
	}
	if a.IsNull() && b.IsNull() {
		return true
	}
	if a.IsNum() && b.IsNum() {
		return a.n == b.n
	}
	if a.tag == tStr && b.tag == tStr {
		return a.AsStr() == b.AsStr()
	}
	if a.IsNum() && b.tag == tStr || a.tag == tStr && b.IsNum() {
		return a.toNumber() == b.toNumber()
	}
	if a.tag == tBool {
		return looseEq(Num(a.toNumber()), b)
	}
	if b.tag == tBool {
		return looseEq(a, Num(b.toNumber()))
	}
	return false
}

func compare(a, b Value) int {
	if a.tag == tStr && b.tag == tStr {
		if a.AsStr() < b.AsStr() {
			return -1
		}
		if a.AsStr() > b.AsStr() {
			return 1
		}
		return 0
	}
	x, y := a.toNumber(), b.toNumber()
	if math.IsNaN(x) || math.IsNaN(y) {
		return -2
	}
	if x < y {
		return -1
	}
	if x > y {
		return 1
	}
	return 0
}
