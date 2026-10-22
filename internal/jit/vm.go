package jit

import (
	"fmt"
	"math"
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
	proto  *FuncProto
	pc     int
	locals []Value
	st     stack
	nargs  int
	ret    Value
}

// VM is the bytecode interpreter: dense switch dispatch over the instruction
// set, private operand stacks, inline-cached property access and object shapes.
type VM struct {
	frames  []*frame // pointers: appending a callee never invalidates the caller
	globals map[string]Value
	out     func(string)
}

func NewVM() *VM {
	vm := &VM{
		globals: map[string]Value{},
		out:     func(s string) { fmt.Println(s) },
	}
	vm.installBuiltins()
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
	return &frame{proto: cp, locals: make([]Value, cp.NumLocals), st: stack{s: make([]Value, 8)}}
}

func recycle(f *frame) {
	f.pc = 0
	f.nargs = 0
	f.st.sp = 0
	f.proto.Free = append(f.proto.Free, f)
}

func (vm *VM) getGlobal(name string) Value { return vm.globals[name] }

// SetOut replaces the output sink (defaults to fmt.Println).
func (vm *VM) SetOut(f func(string)) { vm.out = f }

func (vm *VM) installBuiltins() {
	reg := func(name string, fn func([]Value) Value) {
		vm.globals[name] = Value{tag: tFunc, ref: &builtin{fn: fn}}
	}
	one := func(f func(float64) float64) func([]Value) Value {
		return func(a []Value) Value { return Num(f(arg(a, 0))) }
	}
	two := func(f func(float64, float64) float64) func([]Value) Value {
		return func(a []Value) Value { return Num(f(arg(a, 0), arg(a, 1))) }
	}
	reg("print", func(a []Value) Value {
		if len(a) > 0 {
			vm.out(a[0].toStr())
		}
		return Undef()
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
	fn func([]Value) Value
}

// Run parses, compiles and executes src, returning the last value.
func (vm *VM) Run(src string) (Value, error) {
	p, err := Parse(src)
	if err != nil {
		return Undef(), err
	}
	proto := NewCompiler().Compile(p)
	vm.frames = vm.frames[:0]
	vm.frames = append(vm.frames, &frame{proto: proto, locals: make([]Value, proto.NumLocals)})
	return vm.loop(), nil
}

func (vm *VM) loop() Value {
	for {
		f := vm.frames[len(vm.frames)-1]
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
		case OpLoadGlobal:
			st.push(vm.globals[f.proto.Consts[in.A].AsStr()])
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
			if cp := callee.AsFunc(); cp != nil {
				nf := frameFor(cp)
				nf.nargs = nargs
				for i := range nf.locals {
					nf.locals[i] = Undef()
				}
				for i := 0; i < nargs && i < len(nf.locals); i++ {
					nf.locals[i] = st.s[base+i]
				}
				vm.frames = append(vm.frames, nf)
				continue
			}
			args := st.s[base:st.sp]
			res := vm.callBuiltin(callee, args)
			st.sp = base - 1
			st.push(res)
		case OpCallMethod:
			nargs := int(in.B)
			obj := st.s[st.sp-1-nargs]
			args := make([]Value, nargs)
			for i := 0; i < nargs; i++ {
				args[i] = st.s[st.sp-nargs+i]
			}
			res := vm.callMethod(obj, f.proto.Consts[in.A].AsStr(), args)
			for i := 0; i <= nargs; i++ {
				st.pop()
			}
			st.push(res)
		case OpMakeArray:
			n := int(in.A)
			arr := NewArr(n)
			for i := n - 1; i >= 0; i-- {
				arr.elems[i] = st.pop()
			}
			st.push(ArrVal(arr))
		case OpMakeObject:
			n := int(in.A)
			o := NewObj()
			for i := 0; i < n; i++ {
				v := st.pop()
				k := st.pop()
				o.set(k.AsStr(), v)
			}
			st.push(ObjVal(o))
		case OpGetField:
			o := st.pop()
			st.push(vm.getField(o, f.proto.Consts[in.A].AsStr()))
		case OpSetField:
			v := st.pop()
			o := st.pop()
			vm.setField(o, f.proto.Consts[in.A].AsStr(), v)
			st.push(v)
		case OpGetIndex:
			k := st.pop()
			o := st.pop()
			if a := o.AsArr(); a != nil {
				st.push(a.get(int(k.toNumber())))
			} else {
				st.push(vm.getField(o, k.AsStr()))
			}
		case OpSetIndex:
			v := st.pop()
			k := st.pop()
			o := st.pop()
			if a := o.AsArr(); a != nil {
				a.set(int(k.toNumber()), v)
			} else {
				vm.setField(o, k.AsStr(), v)
			}
			st.push(v)
		case OpTypeof:
			st.push(Str(typeOf(st.pop())))
		case OpPrint:
			vm.out(st.pop().toStr())
		case OpReturn:
			var v Value
			if st.sp > 0 {
				v = st.pop()
			} else {
				v = Undef()
			}
			callee := vm.frames[len(vm.frames)-1]
			vm.frames = vm.frames[:len(vm.frames)-1]
			if len(vm.frames) == 0 {
				recycle(callee)
				return v
			}
			// The caller's stack still holds callee+args; replace with result.
			cf := vm.frames[len(vm.frames)-1]
			for i := 0; i <= callee.nargs; i++ {
				cf.st.pop()
			}
			cf.st.push(v)
			recycle(callee)
			continue
		}
	}
}

// callBuiltin dispatches a host function.
func (vm *VM) callBuiltin(callee Value, args []Value) Value {
	if bf := callee.AsBuiltin(); bf != nil {
		return bf.fn(args)
	}
	return Undef()
}

// callMethod dispatches `obj.name(args)` for host objects.
func (vm *VM) callMethod(obj Value, name string, args []Value) Value {
	if o := obj.AsObj(); o != nil {
		if m := o.get(name); m.tag == tFunc {
			return vm.callBuiltin(m, args)
		}
	}
	return Undef()
}

func (vm *VM) getField(o Value, name string) Value {
	if o.tag == tObj {
		if a := o.AsArr(); a != nil {
			if name == "length" {
				return Num(float64(len(a.elems)))
			}
			if idx := arrayMethod(name); idx >= 0 {
				return builtinMethod(idx, a)
			}
		}
		if ob := o.AsObj(); ob != nil {
			return ob.get(name)
		}
	}
	if o.tag == tStr {
		if name == "length" {
			return Num(float64(len(o.AsStr())))
		}
	}
	return Undef()
}

func (vm *VM) setField(o Value, name string, v Value) {
	if o.tag == tObj {
		if ob := o.AsObj(); ob != nil {
			ob.set(name, v)
		}
	}
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
	case tObj, tFunc:
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
