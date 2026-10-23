package jit

// compiler_ext.go holds the compilation of the extended language features:
// for-of/in, switch, try/catch/finally, throw, classes, destructuring,
// template literals, optional chaining, spread and `new`.

// ---- for-of / for-in ----

func (c *Compiler) forOf(n *ForOfStmt) {
	c.pushScope()
	iterSlot := c.allocLocal("")
	c.allocLocal("") // index
	c.allocLocal("") // of-flag
	c.allocLocal("") // protocol flag
	c.expr(n.Right)
	of := int32(0)
	if n.Of {
		of = 1
	}
	c.emit(OpIterInit, iterSlot, of)
	loop := len(c.cur.Code)
	c.emit(OpIterNext, iterSlot, int32(loop+2))
	jend := c.emit(OpJump, -1, 0)
	c.loops = append(c.loops, loopCtx{start: loop})
	switch t := n.Left.(type) {
	case *VarDecl:
		if len(t.Patterns) > 0 && t.Patterns[0] != nil {
			c.destructure(t.Patterns[0])
		} else {
			slot := c.allocLocal(t.Names[0])
			c.emit(OpStoreLocal, slot, 0)
		}
	case *ExprStmt:
		if id, ok := t.X.(*Ident); ok {
			c.storeIdent(id.Name)
		}
	}
	c.stmt(n.Body)
	lc := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	for _, p := range lc.continuePatches {
		c.patch(p, loop)
	}
	c.emit(OpJump, int32(loop), 0)
	c.patch(jend, len(c.cur.Code))
	for _, p := range lc.breakPatches {
		c.patch(p, len(c.cur.Code))
	}
	c.popScope()
}

// forOfAsync compiles `for await (const x of expr) body` on its own. The
// iterable resolves to an iterator object, and each iteration awaits the
// iterator's `next()` result, so the loop body runs after the microtask that
// settles it.
func (c *Compiler) forOfAsync(n *ForOfStmt) {
	c.pushScope()
	iterSlot := c.allocLocal("")
	valSlot := c.allocLocal("")
	c.expr(n.Right)
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
	switch t := n.Left.(type) {
	case *VarDecl:
		if len(t.Patterns) > 0 && t.Patterns[0] != nil {
			c.destructure(t.Patterns[0])
		} else {
			slot := c.allocLocal(t.Names[0])
			c.emit(OpStoreLocal, slot, 0)
		}
	case *ExprStmt:
		if id, ok := t.X.(*Ident); ok {
			c.storeIdent(id.Name)
		}
	}
	c.stmt(n.Body)
	lc := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	for _, p := range lc.continuePatches {
		c.patch(p, loop)
	}
	c.emit(OpJump, int32(loop), 0)
	end := len(c.cur.Code)
	c.emit(OpPop, 0, 0)
	c.patch(jend, end)
	for _, p := range lc.breakPatches {
		c.patch(p, end)
	}
	c.popScope()
}

// ---- switch ----

func (c *Compiler) switchStmt(n *SwitchStmt) {
	c.expr(n.Disc)
	c.loops = append(c.loops, loopCtx{})
	var jumps []int
	for _, cl := range n.Cases {
		c.emit(OpDup, 0, 0)
		c.expr(cl.Test)
		c.emit(OpStrictEq, 0, 0)
		jumps = append(jumps, c.emit(OpJumpIfTrue, -1, 0))
	}
	jd := c.emit(OpJump, -1, 0) // skip to the default body
	var endJumps []int
	// A case with an empty body falls through to the next case, so its test
	// jump targets the first following case that actually has a body.
	var pending []int
	for i, cl := range n.Cases {
		if len(cl.Body) == 0 {
			pending = append(pending, jumps[i])
			continue
		}
		for _, p := range pending {
			c.patch(p, len(c.cur.Code))
		}
		pending = nil
		c.patch(jumps[i], len(c.cur.Code))
		c.emit(OpPop, 0, 0)
		c.pushScope()
		for _, s := range cl.Body {
			c.stmt(s)
		}
		c.popScope()
		endJumps = append(endJumps, c.emit(OpJump, -1, 0))
	}
	for _, p := range pending {
		c.patch(p, len(c.cur.Code))
	}
	c.patch(jd, len(c.cur.Code))
	c.emit(OpPop, 0, 0)
	if n.Default != nil {
		c.pushScope()
		for _, s := range n.Default {
			c.stmt(s)
		}
		c.popScope()
	}
	end := len(c.cur.Code)
	for _, j := range endJumps {
		c.patch(j, end)
	}
	lc := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	for _, p := range lc.breakPatches {
		c.patch(p, end)
	}
}

// ---- try / catch / finally ----

func (c *Compiler) tryStmt(n *TryStmt) {
	jt := c.emit(OpTryStart, -1, 0)
	c.pushScope()
	for _, s := range n.Body {
		c.stmt(s)
	}
	c.popScope()
	c.emit(OpTryEnd, 0, 0)
	jend := c.emit(OpJump, -1, 0)
	catchPC := len(c.cur.Code)
	c.patch(jt, catchPC)
	if len(n.CatchBody) > 0 || n.CatchParam != "" {
		c.pushScope()
		if n.CatchParam != "" {
			slot := c.allocLocal(n.CatchParam)
			c.emit(OpCatchStart, slot, 0)
		} else {
			c.emit(OpCatchStart, -1, 0)
		}
		for _, s := range n.CatchBody {
			c.stmt(s)
		}
		c.popScope()
	}
	if n.Finally != nil {
		c.pushScope()
		for _, s := range n.Finally {
			c.stmt(s)
		}
		c.popScope()
	}
	if len(n.CatchBody) == 0 && n.CatchParam == "" {
		c.emit(OpThrow, 0, 0)
	}
	c.patch(jend, len(c.cur.Code))
}

// ---- object literals ----

func (c *Compiler) objectLit(n *ObjectLit) {
	simple := true
	for _, pr := range n.Props {
		if pr.Spread || pr.Computed || pr.Method {
			simple = false
			break
		}
		if pr.Kind == "get" || pr.Kind == "set" {
			simple = false
			break
		}
	}
	if simple {
		for _, pr := range n.Props {
			c.emit(OpConst, c.constIndex(Str(pr.Key)), 0)
			c.expr(pr.Value)
		}
		c.emit(OpMakeObject, int32(len(n.Props)), 0)
		return
	}
	c.emit(OpNewObject, 0, 0)
	for _, pr := range n.Props {
		switch {
		case pr.Spread:
			c.expr(pr.Value)
			c.emit(OpObjAssign, 0, 0)
		case pr.Computed && pr.Method:
			fn := pr.Value.(*FuncExpr)
			idx := c.addFunc(fn)
			c.expr(pr.KeyExpr)
			c.emit(OpMakeClosure, idx, 0)
			c.emit(OpObjSetIdx, 0, 0)
		case pr.Computed:
			c.expr(pr.KeyExpr)
			c.expr(pr.Value)
			c.emit(OpObjSetIdx, 0, 0)
		case pr.Method:
			fn := pr.Value.(*FuncExpr)
			idx := c.addFunc(fn)
			c.emit(OpMakeClosure, idx, 0)
			if pr.Kind == "get" || pr.Kind == "set" {
				// B: 1 for setter so the runtime stores it on the setter slot.
				setter := int32(0)
				if pr.Kind == "set" {
					setter = 1
				}
				c.emit(OpSetAccessor, c.constIndex(Str(pr.Key)), setter)
			} else {
				c.emit(OpObjSet, c.constIndex(Str(pr.Key)), 0)
			}
		default:
			c.expr(pr.Value)
			c.emit(OpObjSet, c.constIndex(Str(pr.Key)), 0)
		}
	}
}

// ---- template literals ----

func (c *Compiler) templateLit(n *TemplateLit) {
	if len(n.Parts) > 0 {
		c.emit(OpConst, c.constIndex(Str(n.Parts[0])), 0)
	}
	for i, e := range n.Exprs {
		c.expr(e)
		c.emit(OpAdd, 0, 0)
		if i+1 < len(n.Parts) {
			c.emit(OpConst, c.constIndex(Str(n.Parts[i+1])), 0)
			c.emit(OpAdd, 0, 0)
		}
	}
}

// taggedTemplate compiles “tag`a${x}b` “: build the cooked strings array,
// push the substitutions, and call the tag with (strings, ...values).
func (c *Compiler) taggedTemplate(n *TaggedTemplate) {
	c.expr(n.Tag)
	c.emit(OpNewArray, 0, 0)
	for _, part := range n.Tpl.Parts {
		c.expr(&StringLit{Value: part})
		c.emit(OpArrayPush, 0, 0)
	}
	for _, e := range n.Tpl.Exprs {
		c.expr(e)
	}
	c.emit(OpCall, c.constIndex(Str("taggedTemplate")), int32(1+len(n.Tpl.Exprs)))
}

// ---- optional chaining ----

func (c *Compiler) optMember(n *OptMemberExpr) {
	c.expr(n.Obj)
	c.emit(OpDup, 0, 0)
	c.emit(OpIsNull, 0, 0)
	jt := c.emit(OpJumpIfTrue, -1, 0)
	if n.Computed {
		c.expr(n.Prop)
		c.emit(OpGetIndex, 0, 0)
	} else {
		c.emit(OpGetField, c.constIndex(Str(n.Prop.(*StringLit).Value)), 0)
	}
	jend := c.emit(OpJump, -1, 0)
	c.patch(jt, len(c.cur.Code))
	c.emit(OpPop, 0, 0)
	c.emit(OpConst, c.constIndex(Undef()), 0)
	c.patch(jend, len(c.cur.Code))
}

func (c *Compiler) optCall(n *OptCallExpr) {
	c.expr(n.Obj)
	c.emit(OpDup, 0, 0)
	c.emit(OpIsNull, 0, 0)
	jt := c.emit(OpJumpIfTrue, -1, 0)
	for _, a := range n.Args {
		c.expr(a)
	}
	c.emit(OpCall, c.constIndex(Str(calleeDesc(n.Obj))), int32(len(n.Args)))
	jend := c.emit(OpJump, -1, 0)
	c.patch(jt, len(c.cur.Code))
	c.emit(OpPop, 0, 0)
	c.emit(OpConst, c.constIndex(Undef()), 0)
	c.patch(jend, len(c.cur.Code))
}

// ---- new ----

func (c *Compiler) newExpr(n *NewExpr) {
	c.expr(n.Callee)
	for _, a := range n.Args {
		c.expr(a)
	}
	c.emit(OpNew, 0, int32(len(n.Args)))
}

// ---- destructuring ----

func (c *Compiler) destructure(pat Pat) {
	switch p := pat.(type) {
	case *PatIdent:
		slot := c.allocLocal(p.Name)
		c.emit(OpStoreLocal, slot, 0)
	case *PatArray:
		tmp := c.allocLocal("")
		c.emit(OpStoreLocal, tmp, 0)
		for i, el := range p.Elems {
			if el.Hole {
				continue
			}
			c.emit(OpLoadLocal, tmp, 0)
			c.emit(OpConst, c.constIndex(Num(float64(i))), 0)
			c.emit(OpGetIndex, 0, 0)
			if el.Default != nil {
				c.emit(OpDup, 0, 0)
				c.emit(OpIsUndef, 0, 0)
				jt := c.emit(OpJumpIfFalse, -1, 0)
				c.emit(OpPop, 0, 0)
				c.expr(el.Default)
				c.patch(jt, len(c.cur.Code))
			}
			c.destructure(el.Pat)
		}
		if p.Rest != "" {
			c.emit(OpLoadLocal, tmp, 0)
			c.emit(OpSliceFrom, c.constIndex(Num(float64(len(p.Elems)))), 0)
			slot := c.allocLocal(p.Rest)
			c.emit(OpStoreLocal, slot, 0)
		}
	case *PatObject:
		tmp := c.allocLocal("")
		c.emit(OpStoreLocal, tmp, 0)
		for _, pr := range p.Props {
			c.emit(OpLoadLocal, tmp, 0)
			c.emit(OpGetField, c.constIndex(Str(pr.Key)), 0)
			if pr.Default != nil {
				c.emit(OpDup, 0, 0)
				c.emit(OpIsUndef, 0, 0)
				jt := c.emit(OpJumpIfFalse, -1, 0)
				c.emit(OpPop, 0, 0)
				c.expr(pr.Default)
				c.patch(jt, len(c.cur.Code))
			}
			c.destructure(pr.Pat)
		}
	}
}

// labeled compiles `label: body`. Only loops take a label, since that is the
// only construct a labeled break/continue may name.
func (c *Compiler) labeled(n *LabeledStmt) {
	switch n.Body.(type) {
	case *ForStmt, *WhileStmt, *DoWhileStmt, *ForOfStmt:
		c.labels = append(c.labels, labelFrame{name: n.Label, loop: len(c.loops)})
		c.stmt(n.Body)
		c.labels = c.labels[:len(c.labels)-1]
		return
	}
	c.stmt(n.Body)
}

// ---- classes ----

func (c *Compiler) classDecl(n *ClassDecl) {
	cls := &Class{Name: n.Name, Proto: NewObj(), Static: NewObj()}
	// Bind the class name before compiling the methods: a method that names
	// its own class then captures the bound value of this declaration rather
	// than reading the shared, unbound class prototype.
	c.allocLocal(n.Name)
	outerClass := c.curClass
	c.curClass = cls
	defer func() { c.curClass = outerClass }()

	var ctor *FuncExpr
	var fieldInits []Stmt
	var staticInits []Stmt
	for _, m := range n.Methods {
		if m.Field {
			if m.Static {
				if m.Value != nil {
					// Static fields are data properties of the class value itself,
					// initialized in the class declaration before it is constructed.
					staticInits = append(staticInits, &ExprStmt{X: &AssignExpr{Op: "=", Target: &MemberExpr{Obj: &Ident{Name: n.Name}, Prop: &StringLit{Value: m.Name}}, Value: m.Value}})
				}
				continue
			}
			if !m.Computed {
				fieldInits = append(fieldInits, &ExprStmt{X: &AssignExpr{Op: "=", Target: &MemberExpr{Obj: &ThisExpr{}, Prop: &StringLit{Value: m.Name}}, Value: m.Value}})
			}
			continue
		}
		if m.Computed || m.Gen {
			// Computed and generator method names cannot be dispatched by
			// name; the interpreter has no protocol lookup for them yet.
			continue
		}
		if m.Name == "constructor" && !m.Static && m.Kind == "" {
			ctor = &m.Fn
			continue
		}
		fn := c.addMethodProto(&m.Fn)
		cls.Methods = append(cls.Methods, MethodSpec{Name: m.Name, Proto: fn, Static: m.Static, Kind: m.Kind})
	}
	if ctor != nil {
		// Instance fields initialize at the top of the constructor, matching
		// the order ES evaluates them in.
		ctor.Body = append(fieldInits, ctor.Body...)
		cls.Ctor = Func(c.addMethodProto(ctor))
	} else {
		synthetic := &FuncExpr{Name: n.Name, Body: fieldInits}
		cls.Ctor = Func(c.addMethodProto(synthetic))
	}

	if n.Super != nil {
		c.expr(n.Super)
	} else {
		c.emit(OpConst, c.constIndex(Undef()), 0)
	}
	c.emit(OpClass, c.constIndex(ClassVal(cls)), 0)
	c.assignIdent(n.Name)
	for _, si := range staticInits {
		c.stmt(si)
	}
}

// addMethodProto compiles a method into a prototype with `this` at slot 0 and
// the superclass value at slot 1.
func (c *Compiler) addMethodProto(f *FuncExpr) *FuncProto {
	outer := c.cur
	outerScopes := c.scopes
	outerSelf := c.selfName
	outerThis := c.thisSlot
	outerSuper := c.superSlot
	np := &FuncProto{Name: f.Name, SuperSlot: -1}
	c.funcs = append(c.funcs, np)
	c.outerFns = append(c.outerFns, outerFn{scopes: c.scopes, proto: outer})
	c.cur = np
	c.scopes = nil
	c.selfName = f.Name
	c.pushScope()
	c.thisSlot = c.allocLocal("this")
	c.superSlot = c.allocLocal("super")
	np.SuperSlot = c.superSlot
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
	c.superSlot = outerSuper
	return np
}

// ---- helpers ----

func (c *Compiler) storeIdent(name string) { c.assignIdent(name) }
