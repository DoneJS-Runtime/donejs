package jit

// AST for the supported JavaScript subset. Deliberately small: the compiler
// only needs shapes it can map to bytecode.

type Node interface{ node() }

type Expr interface {
	Node
	expr()
}

type Stmt interface {
	Node
	stmt()
}

// ---- expressions ----

type NumberLit struct{ Value float64 }
type StringLit struct{ Value string }
type RegExpLit struct{ Pattern, Flags string }
type BoolLit struct{ Value bool }
type NullLit struct{}
type UndefLit struct{}
type Ident struct{ Name string }
type ThisExpr struct{}

type UnaryExpr struct {
	Op string
	X  Expr
}

type BinaryExpr struct {
	Op   string
	X, Y Expr
}

type LogicalExpr struct {
	Op   string // "&&" or "||"
	X, Y Expr
}

type AssignExpr struct {
	Op     string // "=", "+=", ...
	Target Expr
	Value  Expr
}

type CondExpr struct{ Cond, Then, Else Expr }

type CallExpr struct {
	Callee Expr
	Args   []Expr
}

type NewExpr struct {
	Callee Expr
	Args   []Expr
}

type MemberExpr struct {
	Obj      Expr
	Prop     Expr
	Computed bool
}

// OptMemberExpr is `obj?.prop` / `obj?.[expr]`; OptCallExpr is `obj?.(args)`.
type OptMemberExpr struct {
	Obj      Expr
	Prop     Expr
	Computed bool
}

type OptCallExpr struct {
	Obj  Expr
	Args []Expr
}

type ArrayLit struct{ Elems []Expr }

// SpreadExpr is `...expr` in an array literal, object literal or call.
type SpreadExpr struct{ X Expr }

type Prop struct {
	Key      string
	Computed bool // [expr]: value
	KeyExpr  Expr // when Computed
	Value    Expr
	Spread   bool   // ...expr
	Method   bool   // foo() { ... } shorthand
	Kind     string // "", "get", or "set" for accessor shorthand
	Gen      bool   // *foo() { ... }
}

type ObjectLit struct{ Props []Prop }

type FuncExpr struct {
	Name   string
	Params []string
	Body   []Stmt
	Async  bool
	Gen    bool
}

// ArrowFunc is `(params) => body` / `x => expr`. Expression bodies are stored
// in Expr; block bodies in Body.
type ArrowFunc struct {
	Params []string
	Body   []Stmt
	Expr   Expr
	Async  bool
}

// AwaitExpr is `await x`.
type AwaitExpr struct{ X Expr }

// YieldExpr is `yield x` / `yield* x`. Delegate marks the `yield*` form.
type YieldExpr struct {
	X        Expr
	Delegate bool
}

func (*YieldExpr) expr() {}

// TemplateLit is “ `a${x}b` “. Parts holds the literal chunks; Exprs the
// substitutions, interleaved as Parts[0] Exprs[0] Parts[1] ...
type TemplateLit struct {
	Parts []string
	Exprs []Expr
}

// ArrayPat / ObjectPat are destructuring targets.
type ArrayPat struct {
	Elems []PatElem
	Rest  string
}

type ObjectPat struct {
	Props []PatProp
	Rest  string
}

type PatElem struct {
	Pat     Pat
	Default Expr
	Hole    bool
}

type PatProp struct {
	Key     string
	Pat     Pat
	Default Expr
}

// Pat is a destructuring pattern.
type Pat interface{ pat() }

type PatIdent struct{ Name string }
type PatArray struct {
	Elems []PatElem
	Rest  string
}
type PatObject struct {
	Props []PatProp
	Rest  string
}

// ForOfStmt is `for (x of y)` (Of=true) or `for (x in y)` (Of=false).
type ForOfStmt struct {
	Left  Stmt // VarDecl or ExprStmt
	Right Expr
	Body  Stmt
	Of    bool
}

// SwitchStmt is a switch statement.
type SwitchStmt struct {
	Disc    Expr
	Cases   []CaseClause
	Default []Stmt
}

type CaseClause struct {
	Test Expr
	Body []Stmt
}

// TryStmt is try/catch/finally.
type TryStmt struct {
	Body       []Stmt
	CatchParam string
	CatchBody  []Stmt
	Finally    []Stmt
}

type ThrowStmt struct{ Value Expr }

// ClassDecl is a class declaration with methods.
type ClassDecl struct {
	Name    string
	Super   Expr
	Methods []Method
}

type Method struct {
	Name     string
	Computed bool // [expr]() { ... }
	KeyExpr  Expr // when Computed
	Static   bool
	Kind     string // "", "get", "set"
	Async    bool
	Gen      bool
	Field    bool // class field initializer
	Value    Expr // when Field
	Fn       FuncExpr
}

// ---- statements ----

type ExprStmt struct{ X Expr }

type VarDecl struct {
	Names    []string
	Inits    []Expr
	Patterns []Pat // destructuring patterns, parallel to Names when non-nil
}

type BlockStmt struct{ Body []Stmt }

type IfStmt struct {
	Cond Expr
	Then Stmt
	Else Stmt
}

type WhileStmt struct {
	Cond Expr
	Body Stmt
}

type DoWhileStmt struct {
	Body Stmt
	Cond Expr
}

type ForStmt struct {
	Init Stmt // VarDecl or ExprStmt or nil
	Cond Expr
	Post Expr
	Body Stmt
}

type ReturnStmt struct{ Value Expr }

type BreakStmt struct{ Label string }
type ContinueStmt struct{ Label string }

type LabeledStmt struct {
	Label string
	Body  Stmt
}

type FuncDecl struct{ Fn FuncExpr }

type Program struct{ Body []Stmt }

// ---- node markers ----

func (*NumberLit) node()     {}
func (*StringLit) node()     {}
func (*RegExpLit) node()     {}
func (*BoolLit) node()       {}
func (*NullLit) node()       {}
func (*UndefLit) node()      {}
func (*Ident) node()         {}
func (*ThisExpr) node()      {}
func (*UnaryExpr) node()     {}
func (*BinaryExpr) node()    {}
func (*LogicalExpr) node()   {}
func (*AssignExpr) node()    {}
func (*CondExpr) node()      {}
func (*CallExpr) node()      {}
func (*NewExpr) node()       {}
func (*MemberExpr) node()    {}
func (*OptMemberExpr) node() {}
func (*OptCallExpr) node()   {}
func (*ArrayLit) node()      {}
func (*ObjectLit) node()     {}
func (*FuncExpr) node()      {}
func (*AwaitExpr) node()     {}
func (*YieldExpr) node()     {}
func (*ArrowFunc) node()     {}
func (*TemplateLit) node()   {}
func (*ClassDecl) node()     {}

func (*ExprStmt) node()     {}
func (*VarDecl) node()      {}
func (*BlockStmt) node()    {}
func (*IfStmt) node()       {}
func (*WhileStmt) node()    {}
func (*DoWhileStmt) node()  {}
func (*ForStmt) node()      {}
func (*ReturnStmt) node()   {}
func (*BreakStmt) node()    {}
func (*ContinueStmt) node() {}
func (*LabeledStmt) node()  {}
func (*LabeledStmt) stmt()  {}
func (*FuncDecl) node()     {}
func (*SpreadExpr) node()   {}
func (*Program) node()      {}
func (*ForOfStmt) node()    {}
func (*SwitchStmt) node()   {}
func (*TryStmt) node()      {}
func (*ThrowStmt) node()    {}

func (*NumberLit) expr()     {}
func (*StringLit) expr()     {}
func (*RegExpLit) expr()     {}
func (*BoolLit) expr()       {}
func (*NullLit) expr()       {}
func (*UndefLit) expr()      {}
func (*Ident) expr()         {}
func (*ThisExpr) expr()      {}
func (*UnaryExpr) expr()     {}
func (*BinaryExpr) expr()    {}
func (*LogicalExpr) expr()   {}
func (*AssignExpr) expr()    {}
func (*CondExpr) expr()      {}
func (*CallExpr) expr()      {}
func (*NewExpr) expr()       {}
func (*MemberExpr) expr()    {}
func (*OptMemberExpr) expr() {}
func (*OptCallExpr) expr()   {}
func (*ArrayLit) expr()      {}
func (*SpreadExpr) expr()    {}
func (*ObjectLit) expr()     {}
func (*ArrowFunc) expr()     {}
func (*TemplateLit) expr()   {}
func (*FuncExpr) expr()      {}
func (*AwaitExpr) expr()     {}

func (*ForOfStmt) stmt()    {}
func (*SwitchStmt) stmt()   {}
func (*TryStmt) stmt()      {}
func (*ThrowStmt) stmt()    {}
func (*ClassDecl) stmt()    {}
func (*ExprStmt) stmt()     {}
func (*VarDecl) stmt()      {}
func (*BlockStmt) stmt()    {}
func (*IfStmt) stmt()       {}
func (*WhileStmt) stmt()    {}
func (*DoWhileStmt) stmt()  {}
func (*ForStmt) stmt()      {}
func (*ReturnStmt) stmt()   {}
func (*BreakStmt) stmt()    {}
func (*ContinueStmt) stmt() {}
func (*FuncDecl) stmt()     {}
func (*Program) stmt()      {}

// ---- pattern markers ----

func (*PatIdent) pat()  {}
func (*PatArray) pat()  {}
func (*PatObject) pat() {}
