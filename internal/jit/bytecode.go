package jit

// Instruction set. Stack-based, with a few fused (super)instructions to cut
// dispatch count on the hot paths, the way V8's Ignition uses short forms.

type Op uint8

const (
	OpConst       Op = iota // A: const index
	OpLoadLocal             // A: slot
	OpStoreLocal            // A: slot
	OpLoadGlobal            // A: const index (name)
	OpStoreGlobal           // A: const index (name)
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
	OpJump // A: target pc
	OpJumpIfFalse
	OpJumpIfTrue
	OpPop
	OpDup
	OpCall       // A: func index, B: nargs
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

	// Extended instruction set (language features).
	OpIterInit      // A: iter slot; pop iterable -> locals[A], locals[A+1]=0
	OpIterNext      // A: iter slot, B: body target; push next element or fall through
	OpThrow         // pop value, raise
	OpTryStart      // A: catch target
	OpTryEnd        // pop the active try handler
	OpCatchStart    // A: param slot; pop thrown value -> locals[A]
	OpNew           // B: nargs; construct
	OpNewArray      // push an empty array
	OpArrayPush     // pop value, append to array on top
	OpArrayPushAll  // pop array, append its elements to array on top
	OpNewObject     // push an empty object
	OpObjSet        // A: key const; pop value, set on object on top
	OpObjAssign     // pop object, copy its properties onto object on top
	OpCallDynamic   // pop arg array, call function below it
	OpIsNull        // pop, push true if null/undefined
	OpClass         // A: class const
	OpSuperCall     // B: nargs; call the super constructor
	OpSuperCallMeth // A: name const, B: nargs
	OpSliceFrom     // A: start index const; pop array, push array[start:]
	OpRest          // A: dest slot, B: first arg index; locals[A] = args[B:]
	OpObjSetIdx     // stack [obj, key, value] -> obj[key] = value
	OpIsUndef       // pop, push true if undefined
	OpLoadHeap      // A: heap slot (a captured local)
	OpStoreHeap     // A: heap slot
	OpLoadUpval     // A: upvalue index
	OpStoreUpval    // A: upvalue index
	OpMakeClosure   // A: const index of a function prototype
	OpSelfFunc      // push the running function value, captures included
	OpAwait         // suspend until the promise on the stack settles
	OpSetAccessor   // A: key const, B: 1 for setter; pop fn, store as accessor on obj on top
	OpInstanceOf    // pop rhs, lhs; push lhs instanceof rhs
	OpIn            // pop rhs, lhs; push "lhs in rhs"
	OpRegExp        // A: pattern const; B: flags const; push regexp instance
	OpDeleteIndex   // stack [obj, key] -> delete obj[key], push bool
	OpBitAnd        // pop b, a; push ToInt32(a) & ToInt32(b)
	OpBitOr         // pop b, a; push ToInt32(a) | ToInt32(b)
	OpBitXor        // pop b, a; push ToInt32(a) ^ ToInt32(b)
	OpShl           // pop b, a; push ToInt32(a) << ToInt32(b)
	OpShr           // pop b, a; push ToInt32(a) >> ToInt32(b)
	OpYield         // pop v; suspend the generator frame, resume with the sent value
	OpIterGet       // pop iterable -> push the Symbol.iterator object
	OpNewTarget     // push the constructor the running function was called with
	OpSuperCallDyn  // stack [this, super, argsArray]: call the super constructor
)

type Instr struct {
	Op Op
	A  int32
	B  int32
}

var opNames = []string{
	"Const", "LoadLocal", "StoreLocal", "LoadGlobal", "StoreGlobal", "Add",
	"Sub", "Mul", "Div", "Mod", "Pow", "Neg", "Not", "BitNot", "Eq", "Ne",
	"StrictEq", "StrictNe", "Lt", "Gt", "Le", "Ge", "Jump", "JumpIfFalse",
	"JumpIfTrue", "Pop", "Dup", "Call", "CallMethod", "Return", "MakeArray",
	"MakeObject", "GetField", "SetField", "GetIndex", "SetIndex", "Typeof",
	"Print", "Nop", "IterInit", "IterNext", "Throw", "TryStart", "TryEnd",
	"CatchStart", "New", "NewArray", "ArrayPush", "ArrayPushAll", "NewObject",
	"ObjSet", "ObjAssign", "CallDynamic", "IsNull", "Class", "SuperCall",
	"SuperCallMeth", "SliceFrom", "Rest", "ObjSetIdx", "IsUndef",
	"LoadHeap", "StoreHeap", "LoadUpval", "StoreUpval",
	"MakeClosure", "SelfFunc", "Await", "SetAccessor", "InstanceOf", "In",
	"RegExp", "DeleteIndex", "BitAnd", "BitOr", "BitXor", "Shl", "Shr", "Yield",
	"IterGet", "NewTarget", "SuperCallDyn",
}

func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return "?"
}

// FuncProto is a compiled function: its own code, constants and nested
// functions. Locals are resolved to slots at compile time.
type FuncProto struct {
	Name      string
	Params    []string
	NumLocals int
	SuperSlot int32 // local slot holding the superclass value (-1 when none)
	ParamBase int32 // local slot of the first parameter
	Code      []Instr
	Consts    []Value
	Funcs     []*FuncProto
	Free      []*frame // recycled activation frames for this prototype

	// Closure support. A local captured by a nested function lives in the
	// activation's heap slice instead of locals, so the closure can keep a
	// pointer to it after the frame returns. Upvals is read when the function
	// value is created, against the frame that created it. HeapSlots lists the
	// locals the frame fills in without executing a store (parameters, `this`,
	// the superclass) so pushFrame can mirror them into the heap slice.
	HeapCount int
	HeapSlots []HeapSlot
	Upvals    []UpvalSpec
	NoThis    bool // arrow function: the frame has no `this` in slot 0
	// NoCtor marks a function value that cannot be used with `new`: methods,
	// accessors, generators and async functions.
	NoCtor bool
	// ProtoObj is the lazily created `prototype` object of a function used as
	// a constructor. Stays on the bare prototype for the fallback path; each
	// *Closure that wraps it keeps its own copy.
	ProtoObj *Obj
	Async    bool // async function: returns a promise, may await
	Gen      bool // generator function: yields, returns a generator object
}

// HeapSlot maps a captured local slot to its index in the heap slice.
type HeapSlot struct {
	Slot int32
	Heap int32
}

// UpvalSpec describes one captured variable of a function, relative to the
// frame that creates the function value.
type UpvalSpec struct {
	HeapIdx         int32 // >=0: the creating frame's heap slot
	FromParentUpval int32 // >=0: the creating frame's closure cap (else HeapIdx)
}
