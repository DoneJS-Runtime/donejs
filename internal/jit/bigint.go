package jit

import (
	"math"
	"math/big"
	"strings"
)

// bigIntVal is a minimal BigInt host value: enough for `typeof`, string
// coercion and BigInt(x) construction. Arithmetic is not implemented yet.
type bigIntVal struct{ v *big.Int }

func (b *bigIntVal) Call([]Value) Value { return Undef() }
func (b *bigIntVal) Get(string) Value   { return Undef() }
func (b *bigIntVal) Set(string, Value)  {}
func (b *bigIntVal) Str() string        { return b.v.String() }
func (b *bigIntVal) Type() string       { return "bigint" }
func (b *bigIntVal) New([]Value) Value  { return Host(&bigIntVal{v: big.NewInt(0)}) }

// installBigInt registers the global BigInt constructor.
func (vm *VM) installBigInt() {
	ctor := builtin{name: "BigInt"}
	ctor.fn = func(a []Value) Value {
		if len(a) == 0 {
			return Host(&bigIntVal{v: big.NewInt(0)})
		}
		v := a[0]
		if v.IsNum() {
			f := v.AsNum()
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
				panic(&ThrowError{Value: ErrObj("RangeError",
					"The number "+v.toStr()+" cannot be converted to a BigInt because it is not an integer")})
			}
			return Host(&bigIntVal{v: big.NewInt(int64(f))})
		}
		s := strings.TrimSpace(v.toStr())
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			panic(&ThrowError{Value: ErrObj("SyntaxError", "Cannot convert "+s+" to a BigInt")})
		}
		return Host(&bigIntVal{v: n})
	}
	vm.SetGlobal("BigInt", Value{tag: tFunc, ref: &ctor})
}
