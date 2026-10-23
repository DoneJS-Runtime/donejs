package jit

import "sort"

// Compiler lowers the AST to the bytecode in bytecode.go. Locals are resolved
// to slots at compile time, so the VM never does a name lookup for them.

type loopCtx struct {
	start           int // continue target
	breakPatches    []int
	continuePatches []int
}

type labelFrame struct {
	name string
	loop int
}

type Compiler struct {
	funcs     []*FuncProto
	cur       *FuncProto
	scopes    []map[string]int32
	loops     []loopCtx
	labels    []labelFrame
	selfName  string // current function's own name, for recursion
	thisSlot  int32  // slot holding `this` (-1 when unused)
	superSlot int32  // slot holding the superclass value (-1 when unused)
	curClass  *Class // enclosing class, for `super`

	// esNamespace is the module namespace object an ES module body writes its
	// exports into, or nil outside ES module compilation.
	esNamespace *Obj
	// esExport is the property name a declaration being compiled exports into
	// the module namespace, or "" when it is not an export.
	esExport string

	// outerFns holds the scope chain and prototype of each enclosing
	// function, inner last, so a nested function can capture one of their
	// locals as an upvalue. The scopes are the ones live when that child
	// started compiling, which is exactly the scope it may name.
	outerFns []outerFn
	// heapN counts captured locals per prototype. It is keyed by prototype
	// rather than stored on outerFns because the same function can sit on
	// the chain more than once. heapSlot maps such a local to its heap index.
	heapN    map[*FuncProto]int32
	heapSlot map[*FuncProto]map[int32]int32
}

// outerFn is one enclosing function on the capture chain.
type outerFn struct {
	scopes []map[string]int32
	proto  *FuncProto
}

// A scope entry carries the kind of storage a variable lives in: a plain
// local slot, a captured local (in the activation's heap slice, so nested
// closures can share it) or an upvalue imported from an enclosing activation.
const (
	kindLocal = 0 << 28
	kindHeap  = 1 << 28
	kindUpval = 2 << 28
	kindMask  = 3 << 28
	slotMask  = (1 << 28) - 1
)

func NewCompiler() *Compiler {
	return &Compiler{
		heapN:    map[*FuncProto]int32{},
		heapSlot: map[*FuncProto]map[int32]int32{},
	}
}

// finishProto publishes the capture bookkeeping for a finished function.
func (c *Compiler) finishProto(np *FuncProto) {
	np.HeapCount = int(c.heapN[np])
	if m := c.heapSlot[np]; len(m) > 0 {
		np.HeapSlots = make([]HeapSlot, 0, len(m))
		for slot, h := range m {
			np.HeapSlots = append(np.HeapSlots, HeapSlot{Slot: slot, Heap: h})
		}
		sort.Slice(np.HeapSlots, func(i, j int) bool {
			return np.HeapSlots[i].Slot < np.HeapSlots[j].Slot
		})
	}
}

// loadVar and storeVar emit a read or write for a scope entry, whichever
// storage it names.
func (c *Compiler) loadVar(slot int32) {
	switch slot & kindMask {
	case kindHeap:
		c.emit(OpLoadHeap, slot&slotMask, 0)
	case kindUpval:
		c.emit(OpLoadUpval, slot&slotMask, 0)
	default:
		c.emit(OpLoadLocal, slot&slotMask, 0)
	}
}

func (c *Compiler) storeVar(slot int32) {
	switch slot & kindMask {
	case kindHeap:
		c.emit(OpStoreHeap, slot&slotMask, 0)
	case kindUpval:
		c.emit(OpStoreUpval, slot&slotMask, 0)
	default:
		c.emit(OpStoreLocal, slot&slotMask, 0)
	}
}

// capture imports name from the nearest enclosing function that declares it.
// The value moves to its owner's heap slice so it outlives the owner's frame,
// and an upvalue is added here — and to every function between the owner and
// here, so the value stays reachable along the closure chain. The result is a
// scope entry the caller can load or store.
func (c *Compiler) capture(name string) (int32, bool) {
	for i := len(c.outerFns) - 1; i >= 0; i-- {
		of := &c.outerFns[i]
		for j := len(of.scopes) - 1; j >= 0; j-- {
			s, ok := of.scopes[j][name]
			if !ok {
				continue
			}
			if s&kindMask != kindHeap {
				h := c.heapN[of.proto]
				c.heapN[of.proto] = h + 1
				if c.heapSlot[of.proto] == nil {
					c.heapSlot[of.proto] = map[int32]int32{}
				}
				c.heapSlot[of.proto][s&slotMask] = h
				of.scopes[j][name] = kindHeap | h
				s = kindHeap | h
			}
			// spec describes where the value sits relative to the frame that
			// creates a closure; walking out, each step moves one frame up.
			spec := UpvalSpec{HeapIdx: s & slotMask, FromParentUpval: -1}
			for k := i + 1; k < len(c.outerFns); k++ {
				spec = UpvalSpec{HeapIdx: -1, FromParentUpval: addUpval(c.outerFns[k].proto, spec)}
			}
			return kindUpval | addUpval(c.cur, spec), true
		}
	}
	return 0, false
}

// addUpval appends spec to f's upvalues unless it already has it, returning
// the index the creating frame resolves it to.
func addUpval(f *FuncProto, spec UpvalSpec) int32 {
	for i, s := range f.Upvals {
		if s == spec {
			return int32(i)
		}
	}
	f.Upvals = append(f.Upvals, spec)
	return int32(len(f.Upvals) - 1)
}

// fixCaptures moves the remaining direct accesses to captured locals onto the
// heap slice. A capture is discovered while compiling the nested function,
// which can be after the variable was declared and used, so those earlier
// instructions are rewritten here.
func (c *Compiler) fixCaptures() {
	for _, p := range c.funcs {
		m := c.heapSlot[p]
		if len(m) == 0 {
			continue
		}
		for i := range p.Code {
			in := &p.Code[i]
			switch in.Op {
			case OpLoadLocal:
				if h, ok := m[in.A]; ok {
					in.Op, in.A = OpLoadHeap, h
				}
			case OpStoreLocal:
				if h, ok := m[in.A]; ok {
					in.Op, in.A = OpStoreHeap, h
				}
			case OpCatchStart:
				if h, ok := m[in.A]; ok {
					in.A, in.B = h, 1
				}
			case OpRest:
				if h, ok := m[in.A]; ok {
					in.A, in.B = h, in.B|(1<<27)
				}
			}
		}
	}
}

// loadSuper reads the superclass value of the enclosing method, capturing it
// when this function is not the method itself.
func (c *Compiler) loadSuper() {
	if s, ok := c.resolve("super"); ok {
		c.loadVar(s)
		return
	}
	if s, ok := c.capture("super"); ok {
		c.loadVar(s)
		return
	}
	c.emit(OpLoadLocal, c.superSlot, 0)
}

// Compile produces the top-level function proto for a whole program. The
// completion value of the last statement is left on the stack so that a
// program which is a single expression — the CommonJS module wrapper — yields
// its function to the caller.
func (c *Compiler) Compile(p *Program) *FuncProto {
	c.cur = &FuncProto{Name: "main", SuperSlot: -1}
	c.funcs = append(c.funcs, c.cur)
	c.pushScope()
	c.thisSlot = c.allocLocal("this")
	c.outerFns = append(c.outerFns, outerFn{scopes: c.scopes, proto: c.cur})
	c.cur.ParamBase = int32(c.cur.NumLocals)
	c.hoist(p.Body)
	last := len(p.Body) - 1
	for i, s := range p.Body {
		if i == last {
			if es, ok := s.(*ExprStmt); ok {
				c.expr(es.X)
				break
			}
		}
		c.stmt(s)
	}
	c.emit(OpReturn, 0, 0)
	c.popScope()
	c.finishProto(c.cur)
	c.fixCaptures()
	return c.funcs[0]
}

func (c *Compiler) proto() *FuncProto {
	f := &FuncProto{Name: c.cur.Name}
	c.funcs = append(c.funcs, f)
	return f
}

func (c *Compiler) pushScope() { c.scopes = append(c.scopes, map[string]int32{}) }
func (c *Compiler) popScope()  { c.scopes = c.scopes[:len(c.scopes)-1] }
func (c *Compiler) allocLocal(n string) int32 {
	slot := int32(c.cur.NumLocals)
	c.cur.NumLocals++
	c.scopes[len(c.scopes)-1][n] = slot
	return slot
}

func (c *Compiler) resolve(n string) (int32, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if s, ok := c.scopes[i][n]; ok {
			return s, true
		}
	}
	return 0, false
}

// hoist reserves function-scoped slots for function declarations so that a
// reference earlier in the body resolves to the same local the declaration
// later stores into. var hoisting remains a documented gap.
// hoist reserves slots for declarations in the current scope before the body
// is compiled, so a closure created earlier can reference a declaration that
// appears later (function/var/let/const/class bindings).
func (c *Compiler) hoist(body []Stmt) {
	top := c.scopes[len(c.scopes)-1]
	var walk func([]Stmt)
	walk = func(stmts []Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *FuncDecl:
				if n.Fn.Name != "" {
					c.declare(top, n.Fn.Name)
				}
			case *ClassDecl:
				c.declare(top, n.Name)
			case *VarDecl:
				for _, name := range n.Names {
					c.declare(top, name)
				}
			case *BlockStmt:
				walk(n.Body)
			case *IfStmt:
				walk([]Stmt{n.Then})
				if n.Else != nil {
					walk([]Stmt{n.Else})
				}
			case *ForStmt:
				if n.Body != nil {
					walk([]Stmt{n.Body})
				}
			case *ForOfStmt:
				if n.Body != nil {
					walk([]Stmt{n.Body})
				}
			case *WhileStmt:
				walk([]Stmt{n.Body})
			case *DoWhileStmt:
				walk([]Stmt{n.Body})
			case *TryStmt:
				walk(n.Body)
				walk(n.CatchBody)
				walk(n.Finally)
			case *SwitchStmt:
				for _, cc := range n.Cases {
					walk(cc.Body)
				}
				walk(n.Default)
			case *LabeledStmt:
				walk([]Stmt{n.Body})
			}
		}
	}
	walk(body)
}

// declare reserves a local slot for name in scope unless it is already bound.
func (c *Compiler) declare(scope map[string]int32, name string) {
	if name == "" {
		return
	}
	if _, ok := scope[name]; ok {
		return
	}
	scope[name] = int32(c.cur.NumLocals)
	c.cur.NumLocals++
}

func (c *Compiler) emit(op Op, a, b int32) int {
	c.cur.Code = append(c.cur.Code, Instr{op, a, b})
	return len(c.cur.Code) - 1
}

func (c *Compiler) constIndex(v Value) int32 {
	c.cur.Consts = append(c.cur.Consts, v)
	return int32(len(c.cur.Consts) - 1)
}

// patch fills in a jump target.
func (c *Compiler) patch(pc, target int) { c.cur.Code[pc].A = int32(target) }

// ---- statements ----

func (c *Compiler) stmt(s Stmt) {
	switch n := s.(type) {
	case *ExprStmt:
		if _, isAssign := n.X.(*AssignExpr); isAssign {
			c.expr(n.X)
		} else {
			c.expr(n.X)
			c.emit(OpPop, 0, 0)
		}
	case *BlockStmt:
		c.pushScope()
		c.hoist(n.Body)
		for _, st := range n.Body {
			c.stmt(st)
		}
		c.popScope()
	case *VarDecl:
		for i, name := range n.Names {
			if len(n.Patterns) > 0 && i < len(n.Patterns) && n.Patterns[i] != nil {
				if n.Inits[i] != nil {
					c.expr(n.Inits[i])
				} else {
					c.emit(OpConst, c.constIndex(Undef()), 0)
				}
				c.destructure(n.Patterns[i])
				continue
			}
			slot, ok := c.resolve(name)
			if !ok {
				slot = c.allocLocal(name)
			}
			if n.Inits[i] != nil {
				c.expr(n.Inits[i])
			} else {
				c.emit(OpConst, c.constIndex(Undef()), 0)
			}
			c.storeVar(slot)
		}
	case *FuncDecl:
		expName := c.esExport
		c.esExport = ""
		idx := c.addFunc(&n.Fn)
		c.emit(OpMakeClosure, idx, 0)
		if n.Fn.Name != "" {
			slot, ok := c.resolve(n.Fn.Name)
			if !ok {
				slot = c.allocLocal(n.Fn.Name)
			}
			c.storeVar(slot)
			if expName != "" {
				c.exportStore(expName, slot)
			}
		} else if expName != "" && c.esNamespace != nil {
			tmp := c.allocLocal("__esDefault")
			c.storeVar(tmp)
			c.exportStore(expName, tmp)
		} else {
			c.emit(OpPop, 0, 0)
		}
		c.esExport = expName
	case *ImportDecl:
		for _, s := range n.Specifiers {
			slot, ok := c.resolve(s.Local)
			if !ok {
				slot = c.allocLocal(s.Local)
			}
			prop := s.Imported
			if s.IsNamespace {
				prop = ""
			}
			if s.IsDefault {
				prop = "default"
			}
			c.emit(OpLoadGlobal, c.constIndex(Str("__donejsImport")), 0)
			c.emit(OpConst, c.constIndex(Str(n.Module)), 0)
			c.emit(OpConst, c.constIndex(Str(prop)), 0)
			c.emit(OpCall, c.constIndex(Str("import")), 2)
			c.storeVar(slot)
		}
		if len(n.Specifiers) == 0 && n.Module != "" {
			c.emit(OpLoadGlobal, c.constIndex(Str("__donejsImport")), 0)
			c.emit(OpConst, c.constIndex(Str(n.Module)), 0)
			c.emit(OpConst, c.constIndex(Str("")), 0)
			c.emit(OpCall, c.constIndex(Str("import")), 2)
			c.emit(OpPop, 0, 0)
		}
	case *ExportDefault:
		c.expr(n.X)
		if c.esNamespace != nil {
			tmp := c.allocLocal("__esDefault")
			c.storeVar(tmp)
			c.exportStore("default", tmp)
		} else {
			c.emit(OpPop, 0, 0)
		}
	case *ExportNamed:
		for _, en := range n.Names {
			src := en.Local
			if src == "" {
				src = en.Exported
			}
			if n.From != "" {
				c.emit(OpLoadGlobal, c.constIndex(Str("__donejsImport")), 0)
				c.emit(OpConst, c.constIndex(Str(n.From)), 0)
				c.emit(OpConst, c.constIndex(Str(src)), 0)
				c.emit(OpCall, c.constIndex(Str("import")), 2)
			} else {
				slot, ok := c.resolve(src)
				if !ok {
					slot = c.allocLocal(src)
				}
				c.emit(OpConst, c.constIndex(ObjVal(c.esNamespace)), 0)
				c.emit(OpConst, c.constIndex(Str(en.Exported)), 0)
				c.loadVar(slot)
				c.emit(OpSetIndex, 0, 0)
				continue
			}
			c.emit(OpConst, c.constIndex(ObjVal(c.esNamespace)), 0)
			c.emit(OpConst, c.constIndex(Str(en.Exported)), 0)
			c.emit(OpSetIndex, 0, 0)
		}
	case *ExportValue:
		switch n.Kind {
		case "const", "let", "var":
			c.esExport = n.Name
			c.stmt(&VarDecl{Names: []string{n.Name}, Inits: []Expr{n.X}})
			c.esExport = ""
			if c.esNamespace != nil {
				if slot, ok := c.resolve(n.Name); ok {
					c.exportStore(n.Name, slot)
				}
			}
		case "function":
			c.esExport = n.Name
			c.stmt(n.Fn)
			c.esExport = ""
		case "class":
			c.esExport = n.Name
			c.stmt(n.Cls)
			c.esExport = ""
		}
	case *ClassDecl:
		expName := c.esExport
		c.esExport = ""
		c.classDecl(n)
		c.esExport = expName
		if expName != "" && c.esNamespace != nil {
			if slot, ok := c.resolve(expName); ok {
				c.exportStore(expName, slot)
			}
		}
	case *IfStmt:
		c.expr(n.Cond)
		jf := c.emit(OpJumpIfFalse, -1, 0)
		c.stmt(n.Then)
		if n.Else != nil {
			jend := c.emit(OpJump, -1, 0)
			c.patch(jf, len(c.cur.Code))
			c.stmt(n.Else)
			c.patch(jend, len(c.cur.Code))
		} else {
			c.patch(jf, len(c.cur.Code))
		}
	case *WhileStmt:
		start := len(c.cur.Code)
		c.expr(n.Cond)
		jf := c.emit(OpJumpIfFalse, -1, 0)
		c.loops = append(c.loops, loopCtx{start: start})
		c.stmt(n.Body)
		lc := c.loops[len(c.loops)-1]
		c.loops = c.loops[:len(c.loops)-1]
		for _, p := range lc.continuePatches {
			c.patch(p, start)
		}
		c.emit(OpJump, int32(start), 0)
		c.patch(jf, len(c.cur.Code))
		for _, p := range lc.breakPatches {
			c.patch(p, len(c.cur.Code))
		}
	case *DoWhileStmt:
		start := len(c.cur.Code)
		c.loops = append(c.loops, loopCtx{start: start})
		c.stmt(n.Body)
		lc := c.loops[len(c.loops)-1]
		c.loops = c.loops[:len(c.loops)-1]
		for _, p := range lc.continuePatches {
			c.patch(p, len(c.cur.Code))
		}
		c.expr(n.Cond)
		jt := c.emit(OpJumpIfTrue, -1, 0)
		c.patch(jt, start)
		for _, p := range lc.breakPatches {
			c.patch(p, len(c.cur.Code))
		}
	case *ForStmt:
		c.pushScope()
		if n.Init != nil {
			c.stmt(n.Init)
		}
		start := len(c.cur.Code)
		var jf = -1
		if n.Cond != nil {
			c.expr(n.Cond)
			jf = c.emit(OpJumpIfFalse, -1, 0)
		}
		c.loops = append(c.loops, loopCtx{start: start})
		c.stmt(n.Body)
		lc := c.loops[len(c.loops)-1]
		c.loops = c.loops[:len(c.loops)-1]
		cont := len(c.cur.Code)
		for _, p := range lc.continuePatches {
			c.patch(p, cont)
		}
		if n.Post != nil {
			c.expr(n.Post)
		}
		c.emit(OpJump, int32(start), 0)
		if jf >= 0 {
			c.patch(jf, len(c.cur.Code))
		}
		for _, p := range lc.breakPatches {
			c.patch(p, len(c.cur.Code))
		}
		c.popScope()
	case *ReturnStmt:
		if n.Value != nil {
			c.expr(n.Value)
			c.emit(OpReturn, 1, 0)
		} else {
			c.emit(OpConst, c.constIndex(Undef()), 0)
			c.emit(OpReturn, 0, 0)
		}
	case *BreakStmt:
		p := c.emit(OpJump, -1, 0)
		if n.Label == "" {
			if len(c.loops) > 0 {
				c.loops[len(c.loops)-1].breakPatches = append(c.loops[len(c.loops)-1].breakPatches, p)
			}
			return
		}
		lf := -1
		for i := len(c.labels) - 1; i >= 0; i-- {
			if c.labels[i].name == n.Label {
				lf = i
				break
			}
		}
		if lf >= 0 {
			if li := c.labels[lf].loop; li < len(c.loops) {
				c.loops[li].breakPatches = append(c.loops[li].breakPatches, p)
			}
		}
	case *ContinueStmt:
		p := c.emit(OpJump, -1, 0)
		if n.Label == "" {
			if len(c.loops) > 0 {
				c.loops[len(c.loops)-1].continuePatches = append(c.loops[len(c.loops)-1].continuePatches, p)
			}
			return
		}
		lf := -1
		for i := len(c.labels) - 1; i >= 0; i-- {
			if c.labels[i].name == n.Label {
				lf = i
				break
			}
		}
		if lf >= 0 {
			if li := c.labels[lf].loop; li < len(c.loops) {
				c.loops[li].continuePatches = append(c.loops[li].continuePatches, p)
			}
		}
	case *LabeledStmt:
		c.labeled(n)
	case *ForOfStmt:
		if n.Async {
			// `for await` is compiled by its own path, over the
			// Symbol.iterator protocol.
			c.forOfAsync(n)
			return
		}
		c.forOf(n)
	case *SwitchStmt:
		c.switchStmt(n)
	case *TryStmt:
		c.tryStmt(n)
	case *ThrowStmt:
		c.expr(n.Value)
		c.emit(OpThrow, 0, 0)
	}
}

// exportStore writes the value of slot into the module namespace under name.
func (c *Compiler) exportStore(name string, slot int32) {
	if c.esNamespace == nil {
		return
	}
	c.emit(OpConst, c.constIndex(ObjVal(c.esNamespace)), 0)
	c.emit(OpConst, c.constIndex(Str(name)), 0)
	c.loadVar(slot)
	c.emit(OpSetIndex, 0, 0)
}

// calleeDesc renders a callee expression for TypeError messages: "o.x",
// "a.b.c", or "(intermediate value)" when the expression has no stable name.
// It mirrors the source text V8 puts in "<expr> is not a function".
func calleeDesc(e Expr) string {
	switch x := e.(type) {
	case *Ident:
		return x.Name
	case *ThisExpr:
		return "this"
	case *MemberExpr:
		if !x.Computed {
			if s, ok := x.Prop.(*StringLit); ok {
				return calleeDesc(x.Obj) + "." + s.Value
			}
		}
		return "(intermediate value)"
	default:
		return "(intermediate value)"
	}
}

// addFunc compiles a nested function and returns a const index for it.
func (c *Compiler) addFunc(f *FuncExpr) int32 { return c.addFn(f, false) }

// addArrow compiles an arrow function: lexically scoped, so it reads `this`
// from the enclosing function instead of binding its own.
func (c *Compiler) addArrow(f *FuncExpr) int32 { return c.addFn(f, true) }

func (c *Compiler) addFn(f *FuncExpr, arrow bool) int32 {
	outer := c.cur
	outerScopes := c.scopes
	outerSelf := c.selfName
	outerThis := c.thisSlot
	np := &FuncProto{Name: f.Name, SuperSlot: -1, NoThis: arrow, Async: f.Async, Gen: f.Gen}
	c.funcs = append(c.funcs, np)
	c.outerFns = append(c.outerFns, outerFn{scopes: c.scopes, proto: outer})
	c.cur = np
	c.scopes = nil
	c.selfName = f.Name
	c.pushScope()
	if !arrow {
		c.thisSlot = c.allocLocal("this")
	}
	np.ParamBase = int32(np.NumLocals)
	restSlot, restIdx := int32(-1), -1
	for i, p := range f.Params {
		name, isRest := isRestParam(p)
		slot := c.allocLocal(name)
		if isRest {
			restSlot, restIdx = slot, i
		}
	}
	if restIdx >= 0 {
		c.emit(OpRest, restSlot, int32(restIdx))
	}
	c.hoist(f.Body)
	for _, st := range f.Body {
		c.stmt(st)
	}
	c.emit(OpReturn, 0, 0)
	c.popScope()
	c.finishProto(np)
	c.cur = outer
	c.scopes = outerScopes
	c.outerFns = c.outerFns[:len(c.outerFns)-1]
	c.selfName = outerSelf
	c.thisSlot = outerThis
	return c.constIndex(Func(np))
}

func (c *Compiler) emitFuncConst(np *FuncProto) int32 { return c.constIndex(Func(np)) }

// ---- expressions ----

func (c *Compiler) expr(e Expr) {
	switch n := e.(type) {
	case *NumberLit:
		c.emit(OpConst, c.constIndex(Num(n.Value)), 0)
	case *StringLit:
		c.emit(OpConst, c.constIndex(Str(n.Value)), 0)
	case *RegExpLit:
		c.emit(OpRegExp, c.constIndex(Str(n.Pattern)), c.constIndex(Str(n.Flags)))
	case *BoolLit:
		c.emit(OpConst, c.constIndex(Bool(n.Value)), 0)
	case *NullLit:
		c.emit(OpConst, c.constIndex(Null()), 0)
	case *UndefLit:
		c.emit(OpConst, c.constIndex(Undef()), 0)
	case *Ident:
		if n.Name == "super" && c.curClass != nil {
			c.loadSuper()
			return
		}
		if slot, ok := c.resolve(n.Name); ok {
			c.loadVar(slot)
			return
		}
		if n.Name == c.selfName {
			// Recursive self-reference: the function value itself, with the
			// captures it was created with.
			c.emit(OpSelfFunc, 0, 0)
			return
		}
		if s, ok := c.capture(n.Name); ok {
			c.loadVar(s)
			return
		}
		if c.curClass != nil && n.Name == c.curClass.Name {
			// Self-reference inside a class (e.g. `new ClassName()` in a method).
			c.emit(OpConst, c.constIndex(ClassVal(c.curClass)), 0)
			return
		}
		c.emit(OpLoadGlobal, c.constIndex(Str(n.Name)), 0)
	case *ThisExpr:
		if slot, ok := c.resolve("this"); ok {
			c.loadVar(slot)
			return
		}
		if s, ok := c.capture("this"); ok {
			c.loadVar(s)
			return
		}
		c.emit(OpLoadLocal, c.thisSlot, 0)
	case *AwaitExpr:
		c.expr(n.X)
		c.emit(OpAwait, 0, 0)
	case *UnaryExpr:
		c.unary(n)
	case *BinaryExpr:
		c.expr(n.X)
		c.expr(n.Y)
		c.emit(c.binOp(n.Op), 0, 0)
	case *LogicalExpr:
		c.logical(n)
	case *AssignExpr:
		c.assign(n)
	case *CondExpr:
		c.expr(n.Cond)
		jf := c.emit(OpJumpIfFalse, -1, 0)
		c.expr(n.Then)
		jend := c.emit(OpJump, -1, 0)
		c.patch(jf, len(c.cur.Code))
		c.expr(n.Else)
		c.patch(jend, len(c.cur.Code))
	case *DynamicImport:
		c.emit(OpLoadGlobal, c.constIndex(Str("__donejsImportDyn")), 0)
		c.expr(n.X)
		c.emit(OpCall, c.constIndex(Str("import")), 1)
	case *CallExpr:
		if id, ok := n.Callee.(*Ident); ok && id.Name == "super" && c.curClass != nil {
			c.expr(&ThisExpr{})
			c.loadSuper()
			for _, a := range n.Args {
				c.expr(a)
			}
			c.emit(OpSuperCall, 0, int32(len(n.Args)))
			return
		}
		if me, ok := n.Callee.(*MemberExpr); ok && !me.Computed {
			if id, ok := me.Obj.(*Ident); ok && id.Name == "super" && c.curClass != nil {
				c.expr(&ThisExpr{})
				c.loadSuper()
				for _, a := range n.Args {
					c.expr(a)
				}
				c.emit(OpSuperCallMeth, c.constIndex(Str(me.Prop.(*StringLit).Value)), int32(len(n.Args)))
				return
			}
			c.expr(me.Obj)
			for _, a := range n.Args {
				c.expr(a)
			}
			mname := me.Prop.(*StringLit).Value
			c.emit(OpCallMethod, c.constIndex(Str(mname+"\x00"+calleeDesc(me.Obj)+"."+mname)), int32(len(n.Args)))
			return
		}
		hasSpread := false
		for _, a := range n.Args {
			if _, ok := a.(*SpreadExpr); ok {
				hasSpread = true
				break
			}
		}
		if hasSpread {
			c.expr(n.Callee)
			c.emit(OpNewArray, 0, 0)
			for _, a := range n.Args {
				if sp, ok := a.(*SpreadExpr); ok {
					c.expr(sp.X)
					c.emit(OpArrayPushAll, 0, 0)
				} else {
					c.expr(a)
					c.emit(OpArrayPush, 0, 0)
				}
			}
			c.emit(OpCallDynamic, c.constIndex(Str(calleeDesc(n.Callee))), 0)
			return
		}
		c.expr(n.Callee)
		for _, a := range n.Args {
			c.expr(a)
		}
		c.emit(OpCall, c.constIndex(Str(calleeDesc(n.Callee))), int32(len(n.Args)))
	case *MemberExpr:
		c.expr(n.Obj)
		if n.Computed {
			c.expr(n.Prop)
			c.emit(OpGetIndex, 0, 0)
		} else {
			c.emit(OpGetField, c.constIndex(Str(n.Prop.(*StringLit).Value)), 0)
		}
	case *ArrayLit:
		hasSpread := false
		for _, el := range n.Elems {
			if _, ok := el.(*SpreadExpr); ok {
				hasSpread = true
				break
			}
		}
		if !hasSpread {
			for _, el := range n.Elems {
				c.expr(el)
			}
			c.emit(OpMakeArray, int32(len(n.Elems)), 0)
			return
		}
		c.emit(OpNewArray, 0, 0)
		for _, el := range n.Elems {
			if sp, ok := el.(*SpreadExpr); ok {
				c.expr(sp.X)
				c.emit(OpArrayPushAll, 0, 0)
			} else {
				c.expr(el)
				c.emit(OpArrayPush, 0, 0)
			}
		}
	case *ObjectLit:
		c.objectLit(n)
	case *ArrowFunc:
		body := n.Body
		if n.Expr != nil {
			body = []Stmt{&ReturnStmt{Value: n.Expr}}
		}
		idx := c.addArrow(&FuncExpr{Name: "", Params: n.Params, Body: body})
		c.emit(OpMakeClosure, idx, 0)
	case *TemplateLit:
		c.templateLit(n)
	case *SpreadExpr:
		c.expr(n.X)
	case *OptMemberExpr:
		c.optMember(n)
	case *OptCallExpr:
		c.optCall(n)
	case *NewExpr:
		c.newExpr(n)
	case *FuncExpr:
		idx := c.addFunc(n)
		c.emit(OpMakeClosure, idx, 0)
	case *YieldExpr:
		if n.Delegate {
			c.yieldDelegate(n.X)
			return
		}
		// `yield` suspends the generator frame; the sent value resumes here.
		c.expr(n.X)
		c.emit(OpYield, 0, 0)
	}
}

// yieldDelegate compiles `yield* x`: drive the sub-iterator's next(), await
// the { value, done } result, and yield each value until the sub-iterator is
// done. Values forwarded this way are yielded by the delegating generator.
func (c *Compiler) yieldDelegate(x Expr) {
	c.pushScope()
	iterSlot := c.allocLocal("")
	valSlot := c.allocLocal("")
	c.expr(x)
	c.emit(OpIterGet, 0, 0)
	c.emit(OpAwait, 0, 0)
	c.storeVar(iterSlot)
	loop := len(c.cur.Code)
	c.loops = append(c.loops, loopCtx{start: loop})
	c.loadVar(iterSlot)
	c.emit(OpCallMethod, c.constIndex(Str("next\x00it.next")), 0)
	c.emit(OpAwait, 0, 0)
	c.emit(OpDup, 0, 0)
	c.emit(OpGetField, c.constIndex(Str("done")), 0)
	jend := c.emit(OpJumpIfTrue, -1, 0)
	c.emit(OpGetField, c.constIndex(Str("value")), 0)
	c.emit(OpStoreLocal, valSlot, 0)
	c.emit(OpLoadLocal, valSlot, 0)
	c.emit(OpYield, 0, 0)
	c.emit(OpJump, int32(loop), 0)
	end := len(c.cur.Code)
	c.emit(OpPop, 0, 0)
	c.patch(jend, end)
	lc := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	for _, p := range lc.continuePatches {
		c.patch(p, loop)
	}
	c.popScope()
}

func (c *Compiler) unary(n *UnaryExpr) {
	switch n.Op {
	case "-":
		c.expr(n.X)
		c.emit(OpNeg, 0, 0)
	case "+":
		c.expr(n.X)
	case "!":
		c.expr(n.X)
		c.emit(OpNot, 0, 0)
	case "~":
		c.expr(n.X)
		c.emit(OpBitNot, 0, 0)
	case "typeof":
		c.expr(n.X)
		c.emit(OpTypeof, 0, 0)
	case "void":
		c.expr(n.X)
		c.emit(OpPop, 0, 0)
		c.emit(OpConst, c.constIndex(Undef()), 0)
	case "delete":
		if m, ok := n.X.(*MemberExpr); ok {
			c.expr(m.Obj)
			if m.Computed {
				c.expr(m.Prop)
			} else {
				c.emit(OpConst, c.constIndex(Str(m.Prop.(*StringLit).Value)), 0)
			}
			c.emit(OpDeleteIndex, 0, 0)
			return
		}
		// delete of a non-reference evaluates the operand and yields true.
		c.expr(n.X)
		c.emit(OpPop, 0, 0)
		c.emit(OpConst, c.constIndex(Bool(true)), 0)
	}
}

func (c *Compiler) binOp(op string) Op {
	switch op {
	case "+":
		return OpAdd
	case "-":
		return OpSub
	case "*":
		return OpMul
	case "/":
		return OpDiv
	case "%":
		return OpMod
	case "**":
		return OpPow
	case "==":
		return OpEq
	case "!=":
		return OpNe
	case "===":
		return OpStrictEq
	case "!==":
		return OpStrictNe
	case "<":
		return OpLt
	case ">":
		return OpGt
	case "<=":
		return OpLe
	case ">=":
		return OpGe
	case "instanceof":
		return OpInstanceOf
	case "in":
		return OpIn
	case "&":
		return OpBitAnd
	case "|":
		return OpBitOr
	case "^":
		return OpBitXor
	case "<<":
		return OpShl
	case ">>":
		return OpShr
	}
	return OpNop
}

func (c *Compiler) logical(n *LogicalExpr) {
	c.expr(n.X)
	c.emit(OpDup, 0, 0)
	var j int
	switch n.Op {
	case "||":
		j = c.emit(OpJumpIfTrue, -1, 0)
	case "??":
		c.emit(OpIsNull, 0, 0)
		j = c.emit(OpJumpIfFalse, -1, 0)
	default:
		j = c.emit(OpJumpIfFalse, -1, 0)
	}
	c.emit(OpPop, 0, 0)
	c.expr(n.Y)
	c.patch(j, len(c.cur.Code))
}

func (c *Compiler) assign(n *AssignExpr) {
	if n.Op == "=" {
		switch t := n.Target.(type) {
		case *Ident:
			c.expr(n.Value)
			c.assignIdent(t.Name)
			return
		case *MemberExpr:
			c.expr(t.Obj)
			if t.Computed {
				c.expr(t.Prop)
				c.expr(n.Value)
				c.emit(OpSetIndex, 0, 0)
			} else {
				c.expr(n.Value)
				c.emit(OpSetField, c.constIndex(Str(t.Prop.(*StringLit).Value)), 0)
			}
			return
		}
		c.expr(n.Value)
		return
	}
	// compound assignment: read, modify, write
	if t, ok := n.Target.(*Ident); ok {
		if slot, ok := c.resolve(t.Name); ok {
			c.loadVar(slot)
			c.expr(n.Value)
			c.emit(c.compoundOp(n.Op), 0, 0)
			c.storeVar(slot)
			return
		}
		if s, ok := c.capture(t.Name); ok {
			c.loadVar(s)
			c.expr(n.Value)
			c.emit(c.compoundOp(n.Op), 0, 0)
			c.storeVar(s)
			return
		}
	}
	c.expr(n.Value)
}

// assignIdent stores the value on the stack into name: its local slot, a
// captured upvalue, or a global.
func (c *Compiler) assignIdent(name string) {
	if slot, ok := c.resolve(name); ok {
		c.storeVar(slot)
		return
	}
	if s, ok := c.capture(name); ok {
		c.storeVar(s)
		return
	}
	c.emit(OpStoreGlobal, c.constIndex(Str(name)), 0)
}

func (c *Compiler) compoundOp(op string) Op {
	switch op {
	case "+=":
		return OpAdd
	case "-=":
		return OpSub
	case "*=":
		return OpMul
	case "/=":
		return OpDiv
	case "%=":
		return OpMod
	}
	return OpNop
}
