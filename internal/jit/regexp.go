package jit

// regexp.go implements the RegExp built-in on top of host values: regex-literal
// instances plus the global constructor, with instance methods served through
// HostValue.Get (the interpreter drops the receiver on host calls, so method
// invocations arrive via a bound wrapper).

import (
	"regexp"
	"strconv"
	"sync"
)

// regExpVal is one RegExp instance: pattern, flags, execution state and the
// compiled regexp.
type regExpVal struct {
	mu         sync.Mutex
	src        string
	flags      string
	global     bool
	ignoreCase bool
	multiline  bool
	dotAll     bool
	lastIndex  int
	re         *regexp.Regexp
	compErr    error
}

// NewRegExp builds a RegExp instance for a pattern and flags.
func (vm *VM) NewRegExp(pattern, flags string) Value {
	key := pattern + "\x00" + flags
	if re, ok := vm.regCache[key]; ok {
		return Host(&regExpVal{src: pattern, flags: flags, re: re})
	}
	h := &regExpVal{src: pattern, flags: flags}
	h.mu.Lock()
	h.re, h.compErr = regexp.Compile(jsPattern(pattern))
	h.mu.Unlock()
	vm.regCache[key] = h.re
	return Host(h)
}

// New implements the RegExp constructor used by `new RegExp(...)`.
func (h *regExpVal) New(_ []Value) Value { return Host(&regExpVal{src: "", flags: ""}) }

func (h *regExpVal) Call(args []Value) Value {
	if len(args) == 0 {
		return Str(h.src)
	}
	return h.callMethod(methodName(args), args)
}

func methodName(args []Value) string {
	if len(args) > 0 {
		return args[0].AsStr()
	}
	return ""
}

func (h *regExpVal) Get(name string) Value { return Host(&boundRegExp{inst: h, name: name}) }
func (h *regExpVal) Set(name string, v Value) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case "lastIndex":
		h.lastIndex = int(v.ToInteger())
	case "global", "ignoreCase", "multiline", "dotAll":
		b := v.AsBool()
		switch name {
		case "global":
			h.global = b
		case "ignoreCase":
			h.ignoreCase = b
		case "multiline":
			h.multiline = b
		case "dotAll":
			h.dotAll = b
		}
	}
}
func (h *regExpVal) Str() string {
	return "/" + h.src + "/" + h.flags
}
func (h *regExpVal) Type() string { return "object" }

// boundRegExp wraps a RegExp instance for one method call.
type boundRegExp struct {
	inst *regExpVal
	name string
}

func (b *boundRegExp) Call(args []Value) Value  { return b.inst.callMethod(b.name, args) }
func (b *boundRegExp) Get(name string) Value    { return Host(&boundRegExp{inst: b.inst, name: name}) }
func (b *boundRegExp) Set(name string, v Value) { b.inst.Set(name, v) }
func (b *boundRegExp) Str() string              { return b.inst.Str() }
func (b *boundRegExp) Type() string             { return "object" }

// callMethod resolves and runs one instance method; throws surface as
// *ThrowError panics.
func (h *regExpVal) callMethod(name string, args []Value) Value {
	switch name {
	case "source":
		return Str(h.src)
	case "flags":
		return Str(h.flags)
	case "global":
		return Bool(h.global)
	case "ignoreCase":
		return Bool(h.ignoreCase)
	case "multiline":
		return Bool(h.multiline)
	case "dotAll":
		return Bool(h.dotAll)
	case "lastIndex":
		return Num(float64(h.lastIndex))
	case "toString", "valueOf":
		return Str(h.src)
	case "constructor":
		return Host(&regexpCtor{})
	}
	h.mu.Lock()
	if h.compErr != nil {
		err := h.compErr
		h.mu.Unlock()
		panic(&ThrowError{Value: ErrObj("SyntaxError", "Invalid regular expression: "+h.src+": "+err.Error())})
	}
	re := h.re
	h.mu.Unlock()
	switch name {
	case "test":
		if len(args) < 1 {
			panic(&ThrowError{Value: ErrObj("TypeError", "The \"this\" value of RegExp.prototype.test must be a RegExp")})
		}
		ok := re.MatchString(subject(args))
		if h.global {
			h.mu.Lock()
			h.lastIndex = 0
			h.mu.Unlock()
		}
		return Bool(ok)
	case "exec":
		if len(args) < 1 {
			return Undef()
		}
		re := h.re
		m := re.FindStringSubmatchIndex(subject(args))
		h.mu.Lock()
		if h.global && m != nil {
			h.lastIndex = m[1] - m[0]
		}
		h.mu.Unlock()
		if m == nil {
			return Null()
		}
		text := subject(args)
		full := text[m[0]:m[1]]
		groups := map[string]string{}
		names := re.SubexpNames()
		subs := make([]string, 0, len(names)+1)
		subs = append(subs, full)
		for gi := 1; gi < len(names); gi++ {
			if m[2*gi] >= 0 && m[2*gi+1] >= 0 {
				subs = append(subs, text[m[2*gi]:m[2*gi+1]])
				if names[gi] != "" {
					groups[names[gi]] = text[m[2*gi]:m[2*gi+1]]
				}
			} else {
				subs = append(subs, "")
			}
		}
		return Host(&regExpMatch{full: full, subs: subs, groups: groups, index: m[0]})
	}
	panic(&ThrowError{Value: ErrObj("TypeError", "RegExp.prototype."+name+" is not a function")})
}

// subject returns the string a regex method matches against.
func subject(args []Value) string {
	if len(args) < 1 {
		return ""
	}
	return args[0].AsStr()
}

// jsPattern translates JavaScript-only regex syntax into the Go dialect:
// named groups `(?<name>...)` become Go's `(?P<name>...)`.
func jsPattern(src string) string {
	return namedGroupRe.ReplaceAllString(src, "(?P<$1>")
}

var namedGroupRe = regexp.MustCompile(`\(\?<([A-Za-z_][A-Za-z0-9_]*)>`)

func (h *regExpVal) recompile() (*regexp.Regexp, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	re, err := regexp.Compile(jsPattern(h.src))
	if err != nil {
		h.compErr = err
		return nil, err
	}
	h.re = re
	h.compErr = nil
	if h.global {
		h.lastIndex = 0
	}
	return re, nil
}

// regExpMatch is the host value returned by RegExp.prototype.exec: the full
// match, the capture sequence, the named groups and the match start index.
type regExpMatch struct {
	full   string
	subs   []string
	groups map[string]string
	index  int
}

func (m *regExpMatch) Call([]Value) Value { return Undef() }
func (m *regExpMatch) Get(name string) Value {
	switch name {
	case "0":
		return Str(m.full)
	case "length":
		return Num(float64(len(m.subs)))
	case "index":
		return Num(float64(m.index))
	case "groups":
		if len(m.groups) == 0 {
			return Null()
		}
		o := NewObj()
		for k, v := range m.groups {
			o.set(k, Str(v))
		}
		return ObjVal(o)
	}
	if i, err := strconv.Atoi(name); err == nil && i >= 0 && i < len(m.subs) {
		return Str(m.subs[i])
	}
	return Undef()
}
func (m *regExpMatch) Set(string, Value) {}
func (m *regExpMatch) Str() string { return m.full }
func (m *regExpMatch) Type() string { return "object" }
func (m *regExpMatch) New([]Value) Value { return Host(&regExpMatch{}) }

// regexpCtor is the value installed as the global `RegExp`.
type regexpCtor struct{}

func (regexpCtor) New(args []Value) Value   { return Host(&regExpVal{src: "", flags: ""}) }
func (regexpCtor) Call(args []Value) Value  { return Undef() }
func (regexpCtor) Get(name string) Value    { return Host(&boundRegExp{inst: &regExpVal{}, name: name}) }
func (regexpCtor) Set(name string, v Value) {}
func (regexpCtor) Str() string              { return "RegExp" }
func (regexpCtor) Type() string             { return "function" }
