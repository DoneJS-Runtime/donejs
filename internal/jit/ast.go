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
	Obj     Expr
	Prop    Expr
	Computed bool
}

type ArrayLit struct{ Elems []Expr }

type Prop struct {
	Key   string
	Value Expr
}

type ObjectLit struct{ Props []Prop }

type FuncExpr struct {
	Name   string
	Params []string
	Body   []Stmt
}

// ---- statements ----

type ExprStmt struct{ X Expr }

type VarDecl struct {
	Names []string
	Inits []Expr
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

type BreakStmt struct{}
type ContinueStmt struct{}

type FuncDecl struct{ Fn FuncExpr }

type Program struct{ Body []Stmt }

// ---- node markers ----

func (*NumberLit) node()  {}
func (*StringLit) node()  {}
func (*BoolLit) node()    {}
func (*NullLit) node()    {}
func (*UndefLit) node()   {}
func (*Ident) node()      {}
func (*ThisExpr) node()   {}
func (*UnaryExpr) node()  {}
func (*BinaryExpr) node() {}
func (*LogicalExpr) node(){}
func (*AssignExpr) node() {}
func (*CondExpr) node()   {}
func (*CallExpr) node()   {}
func (*NewExpr) node()    {}
func (*MemberExpr) node() {}
func (*ArrayLit) node()   {}
func (*ObjectLit) node()  {}
func (*FuncExpr) node()   {}

func (*ExprStmt) node()   {}
func (*VarDecl) node()    {}
func (*BlockStmt) node()  {}
func (*IfStmt) node()     {}
func (*WhileStmt) node()  {}
func (*DoWhileStmt) node(){}
func (*ForStmt) node()    {}
func (*ReturnStmt) node() {}
func (*BreakStmt) node()  {}
func (*ContinueStmt) node(){}
func (*FuncDecl) node()   {}
func (*Program) node()    {}

func (*NumberLit) expr()  {}
func (*StringLit) expr()  {}
func (*BoolLit) expr()    {}
func (*NullLit) expr()    {}
func (*UndefLit) expr()   {}
func (*Ident) expr()      {}
func (*ThisExpr) expr()   {}
func (*UnaryExpr) expr()  {}
func (*BinaryExpr) expr() {}
func (*LogicalExpr) expr(){}
func (*AssignExpr) expr() {}
func (*CondExpr) expr()   {}
func (*CallExpr) expr()   {}
func (*NewExpr) expr()    {}
func (*MemberExpr) expr() {}
func (*ArrayLit) expr()   {}
func (*ObjectLit) expr()  {}
func (*FuncExpr) expr()   {}

func (*ExprStmt) stmt()   {}
func (*VarDecl) stmt()    {}
func (*BlockStmt) stmt()  {}
func (*IfStmt) stmt()     {}
func (*WhileStmt) stmt()  {}
func (*DoWhileStmt) stmt(){}
func (*ForStmt) stmt()    {}
func (*ReturnStmt) stmt() {}
func (*BreakStmt) stmt()  {}
func (*ContinueStmt) stmt(){}
func (*FuncDecl) stmt()   {}
func (*Program) stmt()    {}
