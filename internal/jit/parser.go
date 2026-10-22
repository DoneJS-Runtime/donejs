package jit

import "errors"

type parser struct {
	toks []token
	pos  int
}

func Parse(src string) (*Program, error) {
	p := &parser{toks: lex(src)}
	body, err := p.block()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, errors.New("jit: unexpected token " + p.cur().val)
	}
	return &Program{Body: body}, nil
}

func (p *parser) cur() token  { return p.toks[p.pos] }
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
func (p *parser) expect(val string) error {
	if !p.accept(val) {
		return errors.New("jit: expected " + val + " got " + p.cur().val)
	}
	return nil
}
func (p *parser) isKeyword(kw string) bool {
	t := p.cur()
	return t.kind == tIdent && t.val == kw
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
	if t.kind == tIdent {
		switch t.val {
		case "var", "let", "const":
			return p.varDecl()
		case "function":
			return p.funcDecl()
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
			p.accept(";")
			return &BreakStmt{}, nil
		case "continue":
			p.next()
			p.accept(";")
			return &ContinueStmt{}, nil
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
	for {
		if p.cur().kind != tIdent {
			return nil, errors.New("jit: expected identifier in declaration")
		}
		names = append(names, p.next().val)
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
	return &VarDecl{Names: names, Inits: inits}, nil
}

func (p *parser) funcDecl() (Stmt, error) {
	p.next() // function
	name := ""
	if p.cur().kind == tIdent && !keywords[p.cur().val] {
		name = p.next().val
	}
	params, body, err := p.funcRest()
	if err != nil {
		return nil, err
	}
	return &FuncDecl{Fn: FuncExpr{Name: name, Params: params, Body: body}}, nil
}

func (p *parser) funcRest() ([]string, []Stmt, error) {
	if err := p.expect("("); err != nil {
		return nil, nil, err
	}
	var params []string
	for !p.at(")") && p.cur().kind != tEOF {
		if p.cur().kind != tIdent {
			return nil, nil, errors.New("jit: expected parameter name")
		}
		params = append(params, p.next().val)
		if !p.accept(",") {
			break
		}
	}
	if err := p.expect(")"); err != nil {
		return nil, nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, nil, err
	}
	body, err := p.block()
	if err != nil {
		return nil, nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, nil, err
	}
	return params, body, nil
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
		return nil, errors.New("jit: expected while")
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
	if err := p.expect("("); err != nil {
		return nil, err
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
		case p.accept("("):
			var args []Expr
			for !p.at(")") && p.cur().kind != tEOF {
				a, err := p.assign()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if !p.accept(",") {
					break
				}
			}
			if err := p.expect(")"); err != nil {
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
			if p.cur().kind != tIdent {
				return nil, errors.New("jit: expected property name")
			}
			name := p.next().val
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
	t := p.next()
	switch t.kind {
	case tNumber:
		return &NumberLit{Value: t.num}, nil
	case tString:
		return &StringLit{Value: t.val}, nil
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
			params, body, err := p.funcRest()
			if err != nil {
				return nil, err
			}
			return &FuncExpr{Params: params, Body: body}, nil
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
					elems = append(elems, x)
					continue
				}
				x, err := p.assign()
				if err != nil {
					return nil, err
				}
				elems = append(elems, x)
				if !p.accept(",") {
					break
				}
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			return &ArrayLit{Elems: elems}, nil
		case "{":
			var props []Prop
			for !p.at("}") && p.cur().kind != tEOF {
				var key string
				if p.cur().kind == tIdent || p.cur().kind == tString {
					key = p.next().val
				} else if p.cur().kind == tNumber {
					key = p.next().val
				} else {
					return nil, errors.New("jit: expected property key")
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
			return &ObjectLit{Props: props}, nil
		case "new":
			callee, err := p.primary()
			if err != nil {
				return nil, err
			}
			var args []Expr
			if p.accept("(") {
				for !p.at(")") && p.cur().kind != tEOF {
					a, err := p.assign()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if !p.accept(",") {
						break
					}
				}
				if err := p.expect(")"); err != nil {
					return nil, err
				}
			}
			return &NewExpr{Callee: callee, Args: args}, nil
		}
	}
	return nil, errors.New("jit: unexpected token " + t.val)
}
