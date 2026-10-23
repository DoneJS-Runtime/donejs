package jit

// MethodSpec is one compiled class method. The class value keeps the specs so
// each execution of the declaration can rebuild its prototype with the method
// bound to the declaring frame's captures.
type MethodSpec struct {
	Name   string
	Proto  *FuncProto
	Static bool
	Kind   string // "", "get", or "set"
}

// Class is a compiled class: a constructor, an instance prototype object,
// a static-property object and an optional superclass.
type Class struct {
	Name    string
	Ctor    Value
	Proto   *Obj
	Static  *Obj
	Super   *Class
	Methods []MethodSpec
	// Typed marks binary view classes (kindU8, kindDV, kindAB); Native, when
	// set, initializes `this` in place instead of running a JS constructor.
	Typed  int
	Native func(o *Obj, args []Value) *ThrowError
}

func ClassVal(c *Class) Value { return Value{tag: tFunc, ref: c} }

func (v Value) AsClass() *Class {
	if c, ok := v.ref.(*Class); ok {
		return c
	}
	return nil
}

// bind returns the class for one execution of its declaration: fresh prototype
// objects whose methods and constructor are closures bound to the declaring
// frame, so they capture that activation's variables.
func (c *Class) bind(f *frame) *Class {
	n := &Class{
		Name:    c.Name,
		Methods: c.Methods,
		Proto:   NewObj(),
		Static:  NewObj(),
		Ctor:    f.closureFor(c.Ctor),
		Typed:   c.Typed,
		Native:  c.Native,
	}
	for _, m := range c.Methods {
		v := f.closureFor(Func(m.Proto))
		if m.Kind == "get" || m.Kind == "set" {
			// An accessor stores its function on the accessor slot so reads
			// and writes of the property invoke it instead of returning the
			// function value itself.
			if m.Static {
				n.Static.setAccessor(m.Name, m.Kind == "set", v)
			} else {
				n.Proto.setAccessor(m.Name, m.Kind == "set", v)
			}
			continue
		}
		if m.Static {
			n.Static.set(m.Name, v)
			continue
		}
		n.Proto.set(m.Name, v)
	}
	return n
}

// className reports the class name for error messages.
func (c *Class) String() string { return "class " + c.Name }
