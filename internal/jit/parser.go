package jit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type parser struct {
	toks []token
	pos  int
	// async counts the enclosing async function bodies. `await` is only an
	// operator inside them; elsewhere it stays an ordinary identifier.
	async int
	// pendingAsync is set when `async` is consumed and the function keyword
	// (or arrow) it modifies has not been parsed yet.
	pendingAsync bool
}

func Parse(src string) (*Program, error) {
	p := &parser{toks: lex(src)}
	body, err := p.block()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, p.errf("unexpected token %s", p.cur().val)
	}
	return &Program{Body: body}, nil
}

// restPrefix marks a rest parameter (`...xs`) in a parameter list so the
// compiler can emit the argument-collection prologue.
const restPrefix = "\x00"

func isRestParam(name string) (string, bool) {
	return strings.TrimPrefix(name, restPrefix), strings.HasPrefix(name, restPrefix)
}

func (p *parser) cur() token { return p.toks[p.pos] }
func (p *parser) peekTok(n int) token {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) at(val string) bool {
	t := p.cur()
	return (t.kind == tPunct || t.kind == tIdent) && t.val == val
}
func (p *parser) accept(val string) bool {
	if p.at(val) {
		p.pos++
		return true
	}
	return false
}

// errf builds a parse error carrying the line of the offending token.
func (p *parser) errf(format string, a ...any) error {
	return fmt.Errorf("jit: line %d: %s", p.cur().line, fmt.Sprintf(format, a...))
}

func (p *parser) expect(val string) error {
	if !p.accept(val) {
		return p.errf("expected %s got %s", val, p.cur().val)
	}
	return nil
}
func (p *parser) isKeyword(kw string) bool {
	t := p.cur()
	return t.kind == tIdent && t.val == kw
}

// propName reads a property name after `.`: a plain identifier or a private
// `#name` field reference.
func (p *parser) propName() (string, error) {
	if p.at("#") {
		p.next()
		if p.cur().kind != tIdent {
			return "", p.errf("expected private field name")
		}
		return "#" + p.next().val, nil
	}
	if p.cur().kind != tIdent {
		return "", p.errf("expected property name")
	}
	return p.next().val, nil
}

// accessorAhead reports whether the `get`/`set` at token offset n is an
// accessor prefix (`get x() {}`) rather than a method named get/set
// (`get(x) {}`): a property name followed by `(`.
func (p *parser) accessorAhead(n int) bool {
	t := p.peekTok(n)
	if t.kind == tIdent || t.kind == tString || t.kind == tNumber {
		nxt := p.peekTok(n + 1)
		return nxt.kind == tPunct && nxt.val == "("
	}
	return false
}

// block parses statements until '}' or EOF.
func (p *parser) block() ([]Stmt, error) {
	var body []Stmt
	for {
		t := p.cur()
		if t.kind == tEOF {
			break
		}
		if t.kind == tPunct && t.val == "}" {
			break
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		body = append(body, s)
	}
	return body, nil
}

func (p *parser) stmt() (Stmt, error) {
	t := p.cur()
	if t.kind == tPunct {
		switch t.val {
		case "{":
			p.next()
			body, err := p.block()
			if err != nil {
				return nil, err
			}
			if err := p.expect("}"); err != nil {
				return nil, err
			}
			return &BlockStmt{Body: body}, nil
		case ";":
			p.next()
			return &BlockStmt{}, nil
		}
	}
	if t.kind == tIdent && p.peekTok(1).kind == tPunct && p.peekTok(1).val == ":" {
		// labeled statement: `outer: for (...) { ... }`
		label := p.next().val
		p.next() // :
		body, err := p.stmt()
		if err != nil {
			return nil, err
		}
		return &LabeledStmt{Label: label, Body: body}, nil
	}
	if t.kind == tIdent {
		switch t.val {
		case "var", "let", "const":
			return p.varDecl()
		case "function":
			return p.funcDecl()
		case "async":
			if p.peekTok(1).kind == tIdent && p.peekTok(1).val == "function" {
				p.pendingAsync = true
				p.next() // consume `async`
				return p.funcDecl()
			}
		case "if":
			return p.ifStmt()
		case "while":
			return p.whileStmt()
		case "do":
			return p.doWhileStmt()
		case "for":
			return p.forStmt()
		case "return":
			p.next()
			if p.at(";") || p.at("}") || p.cur().kind == tEOF {
				p.accept(";")
				return &ReturnStmt{}, nil
			}
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			p.accept(";")
			return &ReturnStmt{Value: x}, nil
		case "break":
			p.next()
			label := ""
			if p.cur().kind == tIdent && p.peekTok(1).kind == tPunct && p.peekTok(1).val == ";" && !keywords[p.cur().val] {
				label = p.next().val
			}
			p.accept(";")
			return &BreakStmt{Label: label}, nil
		case "continue":
			p.next()
			label := ""
			if p.cur().kind == tIdent && p.peekTok(1).kind == tPunct && p.peekTok(1).val == ";" && !keywords[p.cur().val] {
				label = p.next().val
			}
			p.accept(";")
			return &ContinueStmt{Label: label}, nil
		case "switch":
			return p.switchStmt()
		case "try":
			return p.tryStmt()
		case "throw":
			p.next()
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			p.accept(";")
			return &ThrowStmt{Value: x}, nil
		case "class":
			return p.classDecl()
		}
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.accept(";")
	return &ExprStmt{X: x}, nil
}

func (p *parser) varDecl() (Stmt, error) {
	p.next() // var/let/const
	var names []string
	var inits []Expr
	var patterns []Pat
	for {
		if p.cur().kind != tIdent && !p.at("[") && !p.at("{") {
			return nil, p.errf("expected identifier in declaration")
		}
		if p.at("[") || p.at("{") {
			pat, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			patterns = append(patterns, pat)
			names = append(names, "")
		} else {
			names = append(names, p.next().val)
		}
		if p.accept("=") {
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			inits = append(inits, x)
		} else {
			inits = append(inits, nil)
		}
		if !p.accept(",") {
			break
		}
	}
	p.accept(";")
	return &VarDecl{Names: names, Inits: inits, Patterns: patterns}, nil
}

func (p *parser) funcDecl() (Stmt, error) {
	p.next() // function
	gen := p.accept("*")
	name := ""
	if p.cur().kind == tIdent && !keywords[p.cur().val] {
		name = p.next().val
	}
	async := p.pendingAsync
	p.pendingAsync = false
	params, body, err := p.funcRest(async)
	if err != nil {
		return nil, err
	}
	return &FuncDecl{Fn: FuncExpr{Name: name, Params: params, Body: body, Async: async, Gen: gen}}, nil
}

// funcRest parses a parameter list and a block body. When async it lets
// `await` appear inside the body.
func (p *parser) funcRest(async bool) ([]string, []Stmt, error) {
	names, patterns, err := p.parseParams()
	if err != nil {
		return nil, nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, nil, err
	}
	if async {
		p.async++
	}
	body, err := p.block()
	if err != nil {
		return nil, nil, err
	}
	if async {
		p.async--
	}
	if err := p.expect("}"); err != nil {
		return nil, nil, err
	}
	body = p.desugarParams(names, patterns, body)
	return names, body, nil
}

// parseParams parses a parameter list, returning a name per parameter (empty
// for pattern parameters) and the parallel pattern list.
func (p *parser) parseParams() ([]string, []Pat, error) {
	if err := p.expect("("); err != nil {
		return nil, nil, err
	}
	var names []string
	var patterns []Pat
	for !p.at(")") && p.cur().kind != tEOF {
		if p.at("...") {
			p.next()
			if p.cur().kind != tIdent {
				return nil, nil, errors.New("jit: expected parameter name")
			}
			names = append(names, restPrefix+p.next().val)
			patterns = append(patterns, nil)
		} else if p.at("[") || p.at("{") {
			pat, err := p.parsePattern()
			if err != nil {
				return nil, nil, err
			}
			names = append(names, "")
			patterns = append(patterns, pat)
		} else {
			if p.cur().kind != tIdent {
				return nil, nil, errors.New("jit: expected parameter name")
			}
			names = append(names, p.next().val)
			patterns = append(patterns, nil)
		}
		if p.accept("=") {
			if _, err := p.assign(); err != nil {
				return nil, nil, err
			}
		}
		if !p.accept(",") {
			break
		}
	}
	if err := p.expect(")"); err != nil {
		return nil, nil, err
	}
	return names, patterns, nil
}

// desugarParams rewrites pattern parameters into synthetic parameters plus
// destructuring declarations at the top of the body.
func (p *parser) desugarParams(names []string, patterns []Pat, body []Stmt) []Stmt {
	var decls []Stmt
	for i, pat := range patterns {
		if pat == nil {
			continue
		}
		if names[i] == "" {
			names[i] = fmt.Sprintf("__arg%d", i)
		}
		decls = append(decls, &VarDecl{
			Names:    []string{names[i]},
			Inits:    []Expr{&Ident{Name: names[i]}},
			Patterns: []Pat{pat},
		})
	}
	if len(decls) == 0 {
		return body
	}
	return append(decls, body...)
}

func (p *parser) ifStmt() (Stmt, error) {
	p.next()
	if err := p.expect("("); err != nil {
		return nil, err
	}
	cond, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	then, err := p.stmt()
	if err != nil {
		return nil, err
	}
	var els Stmt
	if p.accept("else") {
		els, err = p.stmt()
		if err != nil {
			return nil, err
		}
	}
	return &IfStmt{Cond: cond, Then: then, Else: els}, nil
}

func (p *parser) whileStmt() (Stmt, error) {
	p.next()
	if err := p.expect("("); err != nil {
		return nil, err
	}
	cond, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	body, err := p.stmt()
	if err != nil {
		return nil, err
	}
	return &WhileStmt{Cond: cond, Body: body}, nil
}

func (p *parser) doWhileStmt() (Stmt, error) {
	p.next()
	body, err := p.stmt()
	if err != nil {
		return nil, err
	}
	if !p.accept("while") {
		return nil, p.errf("expected while")
	}
	if err := p.expect("("); err != nil {
		return nil, err
	}
	cond, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	p.accept(";")
	return &DoWhileStmt{Body: body, Cond: cond}, nil
}

func (p *parser) forStmt() (Stmt, error) {
	p.next()
	// `for await (x of y)` is an async iteration; the interpreter runs it as
	// a plain for-of for now (await is a separate feature).
	p.accept("await")
	if err := p.expect("("); err != nil {
		return nil, err
	}
	if st, ok, err := p.tryForOf(); err != nil {
		return nil, err
	} else if ok {
		return st, nil
	}
	var init Stmt
	if p.at(";") {
		p.next()
	} else if p.isKeyword("var") || p.isKeyword("let") || p.isKeyword("const") {
		init, _ = p.varDecl()
	} else {
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		p.expect(";")
		init = &ExprStmt{X: x}
	}
	var cond Expr
	if !p.at(";") {
		var err error
		cond, err = p.expr()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expect(";"); err != nil {
		return nil, err
	}
	var post Expr
	if !p.at(")") {
		var err error
		post, err = p.expr()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	body, err := p.stmt()
	if err != nil {
		return nil, err
	}
	return &ForStmt{Init: init, Cond: cond, Post: post, Body: body}, nil
}

// tryForOf consumes and parses a for-of / for-in loop. It reports whether it
// matched, restoring the token position on no-match.
func (p *parser) tryForOf() (Stmt, bool, error) {
	save := p.pos
	var target Stmt
	if p.isKeyword("var") || p.isKeyword("let") || p.isKeyword("const") {
		p.next() // var/let/const
		pat, err := p.parsePattern()
		if err != nil {
			return nil, false, err
		}
		target = &VarDecl{Names: []string{""}, Inits: []Expr{nil}, Patterns: []Pat{pat}}
	} else if p.cur().kind == tIdent {
		nm := p.next().val
		target = &ExprStmt{X: &Ident{Name: nm}}
	} else {
		return nil, false, nil
	}
	of := false
	if p.accept("of") {
		of = true
	} else if !p.accept("in") {
		p.pos = save
		return nil, false, nil
	}
	right, err := p.expr()
	if err != nil {
		return nil, false, err
	}
	if err := p.expect(")"); err != nil {
		return nil, false, err
	}
	body, err := p.stmt()
	if err != nil {
		return nil, false, err
	}
	return &ForOfStmt{Left: target, Right: right, Body: body, Of: of}, true, nil
}

// parsePattern parses a destructuring pattern: identifier, [a, b], {x, y: z}.
func (p *parser) parsePattern() (Pat, error) {
	if p.accept("[") {
		var elems []PatElem
		rest := ""
		for !p.at("]") && p.cur().kind != tEOF {
			if p.at(",") {
				// hole: `[a, , b]`
				elems = append(elems, PatElem{Hole: true})
				p.next()
				continue
			}
			if p.accept("...") {
				rest = p.next().val
				break
			}
			pat, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			el := PatElem{Pat: pat}
			if p.accept("=") {
				d, err := p.assign()
				if err != nil {
					return nil, err
				}
				el.Default = d
			}
			elems = append(elems, el)
			if !p.accept(",") {
				break
			}
		}
		if err := p.expect("]"); err != nil {
			return nil, err
		}
		return &PatArray{Elems: elems, Rest: rest}, nil
	}
	if p.accept("{") {
		var props []PatProp
		rest := ""
		for !p.at("}") && p.cur().kind != tEOF {
			if p.accept("...") {
				rest = p.next().val
				break
			}
			var key string
			if p.cur().kind == tIdent || p.cur().kind == tString {
				key = p.next().val
			} else {
				return nil, p.errf("expected property key in pattern")
			}
			pp := PatProp{Key: key}
			if p.accept(":") {
				pat, err := p.parsePattern()
				if err != nil {
					return nil, err
				}
				pp.Pat = pat
			} else {
				pp.Pat = &PatIdent{Name: key}
			}
			if p.accept("=") {
				d, err := p.assign()
				if err != nil {
					return nil, err
				}
				pp.Default = d
			}
			props = append(props, pp)
			if !p.accept(",") {
				break
			}
		}
		if err := p.expect("}"); err != nil {
			return nil, err
		}
		return &PatObject{Props: props, Rest: rest}, nil
	}
	if p.cur().kind != tIdent {
		return nil, p.errf("expected identifier in pattern")
	}
	return &PatIdent{Name: p.next().val}, nil
}

func (p *parser) switchStmt() (Stmt, error) {
	p.next() // switch
	if err := p.expect("("); err != nil {
		return nil, err
	}
	disc, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	st := &SwitchStmt{Disc: disc}
	for !p.at("}") && p.cur().kind != tEOF {
		if p.accept("case") {
			test, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			body, err := p.blockUntilCase()
			if err != nil {
				return nil, err
			}
			st.Cases = append(st.Cases, CaseClause{Test: test, Body: body})
			continue
		}
		if p.accept("default") {
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			body, err := p.blockUntilCase()
			if err != nil {
				return nil, err
			}
			st.Default = body
			continue
		}
		return nil, p.errf("expected case or default")
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return st, nil
}

// blockUntilCase parses statements until a case/default/} terminator.
func (p *parser) blockUntilCase() ([]Stmt, error) {
	var body []Stmt
	for {
		if p.cur().kind == tEOF || p.at("}") || p.isKeyword("case") || p.isKeyword("default") {
			break
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		body = append(body, s)
	}
	return body, nil
}

func (p *parser) tryStmt() (Stmt, error) {
	p.next() // try
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	body, err := p.block()
	if err != nil {
		return nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	st := &TryStmt{Body: body}
	if p.accept("catch") {
		if p.accept("(") {
			if p.cur().kind != tIdent {
				return nil, p.errf("expected catch parameter")
			}
			st.CatchParam = p.next().val
			if err := p.expect(")"); err != nil {
				return nil, err
			}
		}
		if err := p.expect("{"); err != nil {
			return nil, err
		}
		cb, err := p.block()
		if err != nil {
			return nil, err
		}
		if err := p.expect("}"); err != nil {
			return nil, err
		}
		st.CatchBody = cb
	}
	if p.accept("finally") {
		if err := p.expect("{"); err != nil {
			return nil, err
		}
		fb, err := p.block()
		if err != nil {
			return nil, err
		}
		if err := p.expect("}"); err != nil {
			return nil, err
		}
		st.Finally = fb
	}
	return st, nil
}

func (p *parser) classDecl() (Stmt, error) {
	p.next() // class
	if p.cur().kind != tIdent {
		return nil, p.errf("expected class name")
	}
	cd := &ClassDecl{Name: p.next().val}
	if p.accept("extends") {
		super, err := p.primary()
		if err != nil {
			return nil, err
		}
		cd.Super = super
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	for !p.at("}") && p.cur().kind != tEOF {
		if p.accept(";") {
			continue
		}
		static := false
		if p.isKeyword("static") {
			p.next()
			static = true
		}
		gen := false
		if p.at("*") {
			gen = true
			p.next()
		}
		computed := false
		var keyExpr Expr
		if p.at("[") {
			p.next()
			k, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			computed = true
			keyExpr = k
		}
		kind := ""
		if (p.isKeyword("get") || p.isKeyword("set")) && p.accessorAhead(1) {
			kind = p.next().val
		}
		async := p.pendingAsync
		p.pendingAsync = false
		if !computed {
			if p.isKeyword("async") && p.peekTok(1).kind == tIdent && p.peekTok(2).kind == tPunct && p.peekTok(2).val == "(" {
				async = true
				p.next()
			}
			if p.cur().kind != tIdent && p.cur().kind != tString && !p.at("#") {
				return nil, p.errf("expected method name")
			}
			name := ""
			if p.at("#") {
				p.next()
				if p.cur().kind != tIdent {
					return nil, p.errf("expected private field name")
				}
				name = "#" + p.next().val
			} else {
				name = p.next().val
			}
			if p.at("(") {
				params, body, err := p.funcRest(async)
				if err != nil {
					return nil, err
				}
				cd.Methods = append(cd.Methods, Method{Name: name, Static: static, Kind: kind, Async: async, Gen: gen, Fn: FuncExpr{Name: name, Params: params, Body: body, Async: async, Gen: gen}})
				continue
			}
			if p.at("=") {
				p.next()
				v, err := p.assign()
				if err != nil {
					return nil, err
				}
				cd.Methods = append(cd.Methods, Method{Name: name, Static: static, Async: async, Field: true, Value: v})
				continue
			}
			return nil, p.errf("expected (")
		}
		if p.isKeyword("async") {
			async = true
			p.next()
		}
		if p.at("(") {
			params, body, err := p.funcRest(async)
			if err != nil {
				return nil, err
			}
			cd.Methods = append(cd.Methods, Method{Name: "", Computed: true, KeyExpr: keyExpr, Static: static, Kind: kind, Async: async, Gen: gen, Fn: FuncExpr{Name: "m", Params: params, Body: body, Async: async, Gen: gen}})
			continue
		}
		if p.at("=") {
			p.next()
			v, err := p.assign()
			if err != nil {
				return nil, err
			}
			cd.Methods = append(cd.Methods, Method{Name: "", Computed: true, KeyExpr: keyExpr, Static: static, Field: true, Value: v})
			continue
		}
		return nil, p.errf("expected (")
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return cd, nil
}

func (p *parser) objectProps() ([]Prop, error) {
	var props []Prop
	for !p.at("}") && p.cur().kind != tEOF {
		if p.accept("...") {
			x, err := p.assign()
			if err != nil {
				return nil, err
			}
			props = append(props, Prop{Spread: true, Value: x})
			if !p.accept(",") {
				break
			}
			continue
		}
		// computed key [expr]
		if p.accept("[") {
			k, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			if p.at("(") {
				// computed method name: [expr]() { ... }
				async := p.pendingAsync
				p.pendingAsync = false
				params, body, err := p.funcRest(async)
				if err != nil {
					return nil, err
				}
				props = append(props, Prop{Computed: true, KeyExpr: k, Method: true, Value: &FuncExpr{Name: "m", Params: params, Body: body, Async: async}})
			} else {
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				v, err := p.assign()
				if err != nil {
					return nil, err
				}
				props = append(props, Prop{Computed: true, KeyExpr: k, Value: v})
			}
			if !p.accept(",") {
				break
			}
			continue
		}
		// accessor shorthand: get foo() { ... } / set foo(v) { ... }
		if (p.isKeyword("get") || p.isKeyword("set")) && p.accessorAhead(1) {
			kind := p.next().val
			if p.cur().kind != tIdent && p.cur().kind != tString {
				return nil, p.errf("expected accessor name")
			}
			akey := p.next().val
			async := p.pendingAsync
			p.pendingAsync = false
			params, body, err := p.funcRest(async)
			if err != nil {
				return nil, err
			}
			props = append(props, Prop{Key: akey, Method: true, Kind: kind, Value: &FuncExpr{Name: akey, Params: params, Body: body, Async: async}})
			if !p.accept(",") {
				break
			}
			continue
		}
		var key string
		gen := false
		async := p.pendingAsync
		if p.isKeyword("async") && (p.peekTok(1).kind == tIdent || (p.peekTok(1).kind == tPunct && p.peekTok(1).val == "(")) {
			p.next() // consume `async`
			async = true
		}
		if p.at("*") {
			gen = true
			p.next() // consume `*`
		}
		if p.cur().kind == tIdent || p.cur().kind == tString {
			key = p.next().val
		} else if p.cur().kind == tNumber {
			key = p.next().val
		} else {
			return nil, p.errf("expected property key")
		}
		// shorthand method: foo(...) { ... }
		if p.at("(") {
			params, body, err := p.funcRest(async)
			if err != nil {
				return nil, err
			}
			props = append(props, Prop{Key: key, Method: true, Gen: gen, Value: &FuncExpr{Name: key, Params: params, Body: body, Async: async, Gen: gen}})
			if !p.accept(",") {
				break
			}
			continue
		}
		var val Expr
		if p.accept(":") {
			v, err := p.assign()
			if err != nil {
				return nil, err
			}
			val = v
		} else {
			val = &Ident{Name: key}
		}
		props = append(props, Prop{Key: key, Value: val})
		if !p.accept(",") {
			break
		}
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return props, nil
}

func (p *parser) looksLikeArrow() bool {
	depth := 0
	i := p.pos
	for i < len(p.toks) {
		t := p.toks[i]
		if t.kind == tPunct {
			switch t.val {
			case "(":
				depth++
			case ")":
				depth--
				if depth == 0 {
					return i+1 < len(p.toks) && p.toks[i+1].kind == tPunct && p.toks[i+1].val == "=>"
				}
			case ";":
				return false
			}
		} else if t.kind == tEOF {
			return false
		}
		i++
	}
	return false
}

func (p *parser) arrowFunc() (Expr, error) {
	names, patterns, err := p.parseParams()
	if err != nil {
		return nil, err
	}
	if err := p.expect("=>"); err != nil {
		return nil, err
	}
	async := p.pendingAsync
	p.pendingAsync = false
	body, err := p.arrowBody(async)
	if err != nil {
		return nil, err
	}
	if body.expr != nil {
		body.body = []Stmt{&ReturnStmt{Value: body.expr}}
	}
	body.body = p.desugarParams(names, patterns, body.body)
	return &ArrowFunc{Params: names, Body: body.body, Async: async}, nil
}

type arrowBodyT struct {
	body []Stmt
	expr Expr
}

func (p *parser) arrowBody(async bool) (arrowBodyT, error) {
	if async {
		p.async++
		defer func() { p.async-- }()
	}
	if p.at("{") {
		p.next()
		body, err := p.block()
		if err != nil {
			return arrowBodyT{}, err
		}
		if err := p.expect("}"); err != nil {
			return arrowBodyT{}, err
		}
		return arrowBodyT{body: body}, nil
	}
	x, err := p.assign()
	if err != nil {
		return arrowBodyT{}, err
	}
	return arrowBodyT{expr: x}, nil
}

func (p *parser) templateLit() (Expr, error) {
	// tTplStart was already consumed by primary.
	tl := &TemplateLit{}
	for {
		if p.cur().kind == tTplEnd {
			p.next()
			break
		}
		if p.cur().kind == tTplChunk {
			tl.Parts = append(tl.Parts, p.next().val)
			continue
		}
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		tl.Exprs = append(tl.Exprs, x)
	}
	return tl, nil
}

func (p *parser) callArgs() ([]Expr, error) {
	var args []Expr
	for !p.at(")") && p.cur().kind != tEOF {
		if p.accept("...") {
			x, err := p.assign()
			if err != nil {
				return nil, err
			}
			args = append(args, &SpreadExpr{X: x})
		} else {
			a, err := p.assign()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
		}
		if !p.accept(",") {
			break
		}
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	return args, nil
}

// ---- expressions (precedence climbing) ----

var binaryPrec = map[string]int{
	"??": 1, "||": 2, "&&": 3,
	"|": 4, "^": 5, "&": 6,
	"==": 7, "!=": 7, "===": 7, "!==": 7,
	"<": 8, ">": 8, "<=": 8, ">=": 8, "instanceof": 8, "in": 8,
	"<<": 9, ">>": 9, ">>>": 9,
	"+": 10, "-": 10,
	"*": 11, "/": 11, "%": 11,
	"**": 12,
}

func (p *parser) expr() (Expr, error) { return p.assign() }

func (p *parser) assign() (Expr, error) {
	x, err := p.cond()
	if err != nil {
		return nil, err
	}
	t := p.cur()
	if t.kind == tPunct && isAssignOp(t.val) {
		p.next()
		y, err := p.assign()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{Op: t.val, Target: x, Value: y}, nil
	}
	return x, nil
}

func isAssignOp(v string) bool {
	switch v {
	case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=", "**=":
		return true
	}
	return false
}

func (p *parser) cond() (Expr, error) {
	x, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if p.accept("?") {
		then, err := p.assign()
		if err != nil {
			return nil, err
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		els, err := p.assign()
		if err != nil {
			return nil, err
		}
		return &CondExpr{Cond: x, Then: then, Else: els}, nil
	}
	return x, nil
}

func (p *parser) binary(minPrec int) (Expr, error) {
	x, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		var op string
		if t.kind == tPunct {
			op = t.val
		} else if t.kind == tIdent {
			op = t.val // instanceof / in
		} else {
			break
		}
		prec, ok := binaryPrec[op]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		nextMin := prec + 1
		if op == "**" {
			nextMin = prec // right associative
		}
		y, err := p.binary(nextMin)
		if err != nil {
			return nil, err
		}
		if op == "&&" || op == "||" || op == "??" {
			x = &LogicalExpr{Op: op, X: x, Y: y}
		} else {
			x = &BinaryExpr{Op: op, X: x, Y: y}
		}
	}
	return x, nil
}

func (p *parser) unary() (Expr, error) {
	t := p.cur()
	if t.kind == tPunct {
		switch t.val {
		case "-", "+", "!", "~":
			p.next()
			x, err := p.unary()
			if err != nil {
				return nil, err
			}
			return &UnaryExpr{Op: t.val, X: x}, nil
		case "++", "--":
			p.next()
			x, err := p.unary()
			if err != nil {
				return nil, err
			}
			op := "+="
			if t.val == "--" {
				op = "-="
			}
			return &AssignExpr{Op: op, Target: x, Value: &NumberLit{Value: 1}}, nil
		}
	}
	if t.kind == tIdent && t.val == "await" && p.async > 0 {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &AwaitExpr{X: x}, nil
	}
	if t.kind == tIdent && (t.val == "typeof" || t.val == "delete" || t.val == "void") {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: t.val, X: x}, nil
	}
	return p.postfix()
}

func (p *parser) postfix() (Expr, error) {
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.at("?."):
			p.next()
			if p.accept("(") {
				args, err := p.callArgs()
				if err != nil {
					return nil, err
				}
				x = &OptCallExpr{Obj: x, Args: args}
			} else if p.accept("[") {
				prop, err := p.expr()
				if err != nil {
					return nil, err
				}
				if err := p.expect("]"); err != nil {
					return nil, err
				}
				x = &OptMemberExpr{Obj: x, Prop: prop, Computed: true}
			} else {
				if p.cur().kind != tIdent {
					return nil, p.errf("expected property name")
				}
				name := p.next().val
				x = &OptMemberExpr{Obj: x, Prop: &StringLit{Value: name}}
			}
		case p.accept("("):
			args, err := p.callArgs()
			if err != nil {
				return nil, err
			}
			x = &CallExpr{Callee: x, Args: args}
		case p.accept("["):
			prop, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			x = &MemberExpr{Obj: x, Prop: prop, Computed: true}
		case p.accept("."):
			name, err := p.propName()
			if err != nil {
				return nil, err
			}
			x = &MemberExpr{Obj: x, Prop: &StringLit{Value: name}}
		case p.at("++") || p.at("--"):
			op := "+="
			if p.cur().val == "--" {
				op = "-="
			}
			p.next()
			x = &AssignExpr{Op: op, Target: x, Value: &NumberLit{Value: 1}}
		default:
			return x, nil
		}
	}
}

func (p *parser) primary() (Expr, error) {
	// `async function`, `async () => …`, `async x => …`.
	if p.cur().kind == tIdent && p.cur().val == "async" {
		nxt := p.peekTok(1)
		if nxt.kind == tIdent && nxt.val == "function" {
			p.pendingAsync = true
			p.next() // consume `async`
			p.next() // consume `function`
			gen := p.accept("*")
			name := ""
			if p.cur().kind == tIdent && !keywords[p.cur().val] {
				name = p.next().val
			}
			async := p.pendingAsync
			p.pendingAsync = false
			params, body, err := p.funcRest(async)
			if err != nil {
				return nil, err
			}
			return &FuncExpr{Name: name, Params: params, Body: body, Async: async, Gen: gen}, nil
		}
		if (nxt.kind == tPunct && nxt.val == "(") ||
			(nxt.kind == tIdent && p.peekTok(2).kind == tPunct && p.peekTok(2).val == "=>") {
			p.pendingAsync = true
			p.next() // consume `async`
			x, err := p.primary()
			if err != nil {
				return nil, err
			}
			return x, nil
		}
	}
	// Arrow functions: `(a, b) => ...` and `x => ...`.
	if p.at("(") && p.looksLikeArrow() {
		return p.arrowFunc()
	}
	if p.cur().kind == tIdent && p.peekTok(1).kind == tPunct && p.peekTok(1).val == "=>" {
		name := p.next().val
		p.next() // =>
		async := p.pendingAsync
		p.pendingAsync = false
		body, err := p.arrowBody(async)
		if err != nil {
			return nil, err
		}
		return &ArrowFunc{Params: []string{name}, Body: body.body, Expr: body.expr, Async: async}, nil
	}
	t := p.next()
	switch t.kind {
	case tNumber:
		return &NumberLit{Value: t.num}, nil
	case tString:
		return &StringLit{Value: t.val}, nil
	case tRegExp:
		return &RegExpLit{Pattern: t.val, Flags: t.flags}, nil
	case tBigInt:
		v, _ := strconv.ParseFloat(t.val, 64)
		return &NumberLit{Value: v}, nil
	case tTplStart:
		return p.templateLit()
	case tIdent:
		switch t.val {
		case "true":
			return &BoolLit{Value: true}, nil
		case "false":
			return &BoolLit{Value: false}, nil
		case "null":
			return &NullLit{}, nil
		case "undefined":
			return &UndefLit{}, nil
		case "this":
			return &ThisExpr{}, nil
		case "function":
			async := p.pendingAsync
			p.pendingAsync = false
			gen := p.accept("*")
			name := ""
			if p.cur().kind == tIdent && !keywords[p.cur().val] {
				name = p.next().val
			}
			params, body, err := p.funcRest(async)
			if err != nil {
				return nil, err
			}
			return &FuncExpr{Name: name, Params: params, Body: body, Async: async, Gen: gen}, nil
		case "yield":
			delegate := false
			if p.at("*") {
				p.next()
				delegate = true
			}
			var x Expr = &UndefLit{}
			if !p.at(";") && !p.at(")") && !p.at("]") && !p.at("}") && !p.at(",") && p.cur().kind != tEOF {
				var err error
				x, err = p.assign()
				if err != nil {
					return nil, err
				}
			}
			return &YieldExpr{X: x, Delegate: delegate}, nil
		case "new":
			callee, err := p.primary()
			if err != nil {
				return nil, err
			}
			for p.accept(".") {
				if p.cur().kind != tIdent {
					return nil, p.errf("expected property name")
				}
				callee = &MemberExpr{Obj: callee, Prop: &StringLit{Value: p.next().val}}
			}
			var args []Expr
			if p.accept("(") {
				args, err = p.callArgs()
				if err != nil {
					return nil, err
				}
			}
			return &NewExpr{Callee: callee, Args: args}, nil
		}
		return &Ident{Name: t.val}, nil
	case tPunct:
		switch t.val {
		case "(":
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect(")"); err != nil {
				return nil, err
			}
			return x, nil
		case "[":
			var elems []Expr
			for !p.at("]") && p.cur().kind != tEOF {
				if p.accept("...") {
					x, err := p.assign()
					if err != nil {
						return nil, err
					}
					elems = append(elems, &SpreadExpr{X: x})
				} else {
					x, err := p.assign()
					if err != nil {
						return nil, err
					}
					elems = append(elems, x)
				}
				if !p.accept(",") {
					break
				}
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			return &ArrayLit{Elems: elems}, nil
		case "{":
			props, err := p.objectProps()
			if err != nil {
				return nil, err
			}
			return &ObjectLit{Props: props}, nil
		case "new":
			callee, err := p.primary()
			if err != nil {
				return nil, err
			}
			for p.accept(".") {
				if p.cur().kind != tIdent {
					return nil, p.errf("expected property name")
				}
				callee = &MemberExpr{Obj: callee, Prop: &StringLit{Value: p.next().val}}
			}
			var args []Expr
			if p.accept("(") {
				args, err = p.callArgs()
				if err != nil {
					return nil, err
				}
			}
			return &NewExpr{Callee: callee, Args: args}, nil
		}
	}
	return nil, p.errf("unexpected token %s", t.val)
}
