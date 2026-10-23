package jit

// host.go is the boundary between the interpreter and values it does not
// own. A host value is opaque to jit: it can be called and read through
// HostValue, and jit hands it back to the host unchanged.

// HostValue is a foreign object or function.
// Implementations are expected to marshal arguments to and from Value.
type HostValue interface {
	// Call invokes the value as a function.
	Call(args []Value) Value
	// Get reads a property.
	Get(name string) Value
	// Set writes a property.
	Set(name string, v Value)
	// Str renders the value the way String(v) would.
	Str() string
	// Type reports the typeof result: "object", "function", "undefined"...
	Type() string
}

// Host wraps a foreign value so the interpreter can call and inspect it.
func Host(h HostValue) Value { return Value{tag: tHost, ref: h} }

// AsHost returns the foreign value, or nil when v is not a host value.
func (v Value) AsHost() HostValue {
	if h, ok := v.ref.(HostValue); ok {
		return h
	}
	return nil
}

func (v Value) IsHost() bool { return v.tag == tHost }

// ArrayLen reports the length of an array value (used by the host bridge).
func ArrayLen(a *Arr) int { return len(a.elems) }

// ArrayGet reads index i of an array value (used by the host bridge).
func ArrayGet(a *Arr, i int) Value { return a.get(i) }

// ObjectKeys lists an object's own property names (used by the host bridge).
func ObjectKeys(o *Obj) []string { return o.keys() }

// ObjectGet reads an object property (used by the host bridge).
func ObjectGet(o *Obj, name string) Value { return o.get(name) }

// CallProto runs a bare prototype to completion with args. Host callbacks (a
// foreign function invoked with a jit function as an argument) enter the
// interpreter here. Functions that carry captures need CallValue instead.
func (vm *VM) CallProto(fn *FuncProto, args []Value) Value { return vm.invoke(Func(fn), args) }

// CallValue runs any callable — prototype or closure — to completion.
func (vm *VM) CallValue(fn Value, args []Value) Value { return vm.invoke(fn, args) }

// SetGlobal installs a value as a global name, e.g. a host-provided builtin.
func (vm *VM) SetGlobal(name string, v Value) { vm.globals[name] = v }

// GetGlobal reads a global by name.
func (vm *VM) GetGlobal(name string) Value { return vm.globals[name] }

// SetOutput wires the interpreter's output sink (console.log / print).
func (vm *VM) SetOutput(f func(string)) { vm.out = f }
