package jit

// Compiler lowers the AST to the bytecode in bytecode.go. Locals are resolved
// to slots at compile time, so the VM never does a name lookup for them.

type loopCtx struct {
	start          int // continue target
	breakPatches   []int
	continuePatches []int
}

type Compiler struct {
	funcs    []*FuncProto
	cur      *FuncProto
	scopes   []map[string]int32
	loops    []loopCtx
	selfName string // current function's own name, for recursion
}

func NewCompiler() *Compiler {
	return &Compiler{}
}

// Compile produces the top-level function proto for a whole program.
func (c *Compiler) Compile(p *Program) *FuncProto {
	c.cur = &FuncProto{Name: "main"}
	c.funcs = append(c.funcs, c.cur)
	c.pushScope()
	for _, s := range p.Body {
		c.stmt(s)
	}
	c.emit(OpReturn, 0, 0)
	c.popScope()
	return c.funcs[0]
}

func (c *Compiler) proto() *FuncProto {
	f := &FuncProto{Name: c.cur.Name}
	c.funcs = append(c.funcs, f)
	return f
}

func (c *Compiler) pushScope()      { c.scopes = append(c.scopes, map[string]int32{}) }
func (c *Compiler) popScope()       { c.scopes = c.scopes[:len(c.scopes)-1] }
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
		for _, st := range n.Body {
			c.stmt(st)
		}
		c.popScope()
	case *VarDecl:
		for i, name := range n.Names {
			slot := c.allocLocal(name)
			if n.Inits[i] != nil {
				c.expr(n.Inits[i])
			} else {
				c.emit(OpConst, c.constIndex(Undef()), 0)
			}
			c.emit(OpStoreLocal, slot, 0)
		}
	case *FuncDecl:
		idx := c.addFunc(&n.Fn)
		c.emit(OpConst, idx, 0)
		if n.Fn.Name != "" {
			slot := c.allocLocal(n.Fn.Name)
			c.emit(OpStoreLocal, slot, 0)
		} else {
			c.emit(OpPop, 0, 0)
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
		} else {
			c.emit(OpConst, c.constIndex(Undef()), 0)
		}
		c.emit(OpReturn, 0, 0)
	case *BreakStmt:
		p := c.emit(OpJump, -1, 0)
		if len(c.loops) > 0 {
			c.loops[len(c.loops)-1].breakPatches = append(c.loops[len(c.loops)-1].breakPatches, p)
		}
	case *ContinueStmt:
		p := c.emit(OpJump, -1, 0)
		if len(c.loops) > 0 {
			c.loops[len(c.loops)-1].continuePatches = append(c.loops[len(c.loops)-1].continuePatches, p)
		}
	}
}

// addFunc compiles a nested function and returns a const index for it.
func (c *Compiler) addFunc(f *FuncExpr) int32 {
	outer := c.cur
	outerScopes := c.scopes
	outerSelf := c.selfName
	np := &FuncProto{Name: f.Name}
	c.funcs = append(c.funcs, np)
	c.cur = np
	c.scopes = nil
	c.selfName = f.Name
	c.pushScope()
	for _, p := range f.Params {
		c.allocLocal(p)
	}
	for _, st := range f.Body {
		c.stmt(st)
	}
	c.emit(OpReturn, 0, 0)
	c.popScope()
	c.cur = outer
	c.scopes = outerScopes
	c.selfName = outerSelf
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
	case *BoolLit:
		c.emit(OpConst, c.constIndex(Bool(n.Value)), 0)
	case *NullLit:
		c.emit(OpConst, c.constIndex(Null()), 0)
	case *UndefLit:
		c.emit(OpConst, c.constIndex(Undef()), 0)
	case *Ident:
		if slot, ok := c.resolve(n.Name); ok {
			c.emit(OpLoadLocal, slot, 0)
			return
		}
		if n.Name == c.selfName {
			// Recursive self-reference: the function value itself.
			c.emit(OpConst, c.constIndex(Func(c.cur)), 0)
			return
		}
		c.emit(OpLoadGlobal, c.constIndex(Str(n.Name)), 0)
	case *ThisExpr:
		c.emit(OpLoadLocal, 0, 0)
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
	case *CallExpr:
		c.expr(n.Callee)
		for _, a := range n.Args {
			c.expr(a)
		}
		c.emit(OpCall, 0, int32(len(n.Args)))
	case *MemberExpr:
		c.expr(n.Obj)
		if n.Computed {
			c.expr(n.Prop)
			c.emit(OpGetIndex, 0, 0)
		} else {
			c.emit(OpGetField, c.constIndex(Str(n.Prop.(*StringLit).Value)), 0)
		}
	case *ArrayLit:
		for _, el := range n.Elems {
			c.expr(el)
		}
		c.emit(OpMakeArray, int32(len(n.Elems)), 0)
	case *ObjectLit:
		for _, pr := range n.Props {
			c.emit(OpConst, c.constIndex(Str(pr.Key)), 0)
			c.expr(pr.Value)
		}
		c.emit(OpMakeObject, int32(len(n.Props)), 0)
	case *FuncExpr:
		c.addFunc(n)
	}
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
	}
	return OpNop
}

func (c *Compiler) logical(n *LogicalExpr) {
	c.expr(n.X)
	c.emit(OpDup, 0, 0)
	var j int
	if n.Op == "||" {
		j = c.emit(OpJumpIfTrue, -1, 0)
	} else {
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
			if slot, ok := c.resolve(t.Name); ok {
				c.emit(OpStoreLocal, slot, 0)
				return
			}
			c.emit(OpStoreGlobal, c.constIndex(Str(t.Name)), 0)
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
			c.emit(OpLoadLocal, slot, 0)
			c.expr(n.Value)
			c.emit(c.compoundOp(n.Op), 0, 0)
			c.emit(OpStoreLocal, slot, 0)
			return
		}
	}
	c.expr(n.Value)
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
