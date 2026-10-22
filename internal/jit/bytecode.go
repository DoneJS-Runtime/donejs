package jit

// Instruction set. Stack-based, with a few fused (super)instructions to cut
// dispatch count on the hot paths, the way V8's Ignition uses short forms.

type Op uint8

const (
	OpConst Op = iota // A: const index
	OpLoadLocal       // A: slot
	OpStoreLocal      // A: slot
	OpLoadGlobal      // A: const index (name)
	OpStoreGlobal     // A: const index (name)
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpNeg
	OpNot
	OpBitNot
	OpEq
	OpNe
	OpStrictEq
	OpStrictNe
	OpLt
	OpGt
	OpLe
	OpGe
	OpJump    // A: target pc
	OpJumpIfFalse
	OpJumpIfTrue
	OpPop
	OpDup
	OpCall      // A: func index, B: nargs
	OpCallMethod // A: name const, B: nargs (receiver below args)
	OpReturn
	OpMakeArray  // A: count
	OpMakeObject // A: count
	OpGetField   // A: name const
	OpSetField   // A: name const
	OpGetIndex
	OpSetIndex
	OpTypeof
	OpPrint
	OpNop
)

type Instr struct {
	Op Op
	A  int32
	B  int32
}

// FuncProto is a compiled function: its own code, constants and nested
// functions. Locals are resolved to slots at compile time.
type FuncProto struct {
	Name      string
	Params    []string
	NumLocals int
	Code      []Instr
	Consts    []Value
	Funcs     []*FuncProto
	Free      []*frame // recycled activation frames for this prototype
}
