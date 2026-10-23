package vm

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"donejs/internal/jit"
)

const maxInspectDepth = 4

// registerInspect provides util.inspect / util.format in Go for speed and
// Node-compatible output.
func (rt *Runtime) registerInspect() {
	mod := jit.Object()

	setFn(mod, "inspect", func(args []jit.Value) jit.Value {
		v := argsAt(args, 0)
		depth := -1
		if len(args) > 1 {
			if o := argObj(args, 1); !o.IsUndef() {
				d := o.Get("depth")
				if !d.IsUndef() && !d.IsNull() {
					depth = int(d.ToInteger())
				}
			}
		}
		return rt.toJS(rt.inspectValue(v, depth))
	})

	setFn(mod, "format", func(args []jit.Value) jit.Value {
		return rt.toJS(rt.formatArgs(args))
	})

	setFn(mod, "replacer", func(args []jit.Value) jit.Value {
		return jit.Undef()
	})

	rt.natives["__inspect"] = mod
}

func (rt *Runtime) formatArgs(args []jit.Value) string {
	if len(args) == 0 {
		return ""
	}
	first := args[0]
	if s, ok := first.Export().(string); ok {
		if len(args) == 1 {
			return s
		}
		return rt.formatWithPlaceholders(s, args[1:])
	}
	var parts []string
	for _, a := range args {
		// Node prints bare strings raw and inspects everything else.
		if s, ok := a.Export().(string); ok {
			parts = append(parts, s)
			continue
		}
		parts = append(parts, rt.inspectValue(a, 2))
	}
	return strings.Join(parts, " ")
}

// formatWithPlaceholders implements %s/%d/%i/%f/%j/%o/%O/%% substitution.
func (rt *Runtime) formatWithPlaceholders(f string, args []jit.Value) string {
	var out strings.Builder
	argIdx := 0
	for i := 0; i < len(f); i++ {
		c := f[i]
		if c != '%' {
			out.WriteByte(c)
			continue
		}
		if i+1 >= len(f) {
			out.WriteByte('%')
			break
		}
		verb := f[i+1]
		i++
		switch verb {
		case '%':
			out.WriteByte('%')
		case 's', 'd', 'i', 'f', 'j', 'o', 'O', 'c':
			if argIdx >= len(args) {
				out.WriteString("%!" + string(verb) + "(undefined)")
				continue
			}
			a := args[argIdx]
			argIdx++
			switch verb {
			case 's':
				out.WriteString(rt.formatArgString(a))
			case 'd', 'i':
				if n, ok := a.Export().(int64); ok {
					out.WriteString(strconv.FormatInt(n, 10))
				} else if n, ok := a.Export().(float64); ok {
					out.WriteString(strconv.FormatInt(int64(n), 10))
				} else {
					out.WriteString("NaN")
				}
			case 'f':
				if n, ok := a.Export().(float64); ok {
					out.WriteString(strconv.FormatFloat(n, 'f', 6, 64))
				} else {
					out.WriteString("NaN")
				}
			case 'j':
				b, err := jsonMarshal(a.Export())
				if err != nil {
					out.WriteString("[Circular]")
				} else {
					out.Write(b)
				}
			case 'o', 'O':
				out.WriteString(rt.inspectValue(a, 4))
			case 'c':
				// no CSS support; ignore
			}
		default:
			out.WriteByte('%')
			out.WriteByte(verb)
		}
	}
	if argIdx < len(args) {
		rest := args[argIdx:]
		extra := make([]string, 0, len(rest)+1)
		extra = append(extra, rt.formatArgs(rest))
		out.WriteString(" " + strings.Join(extra, " "))
	}
	return out.String()
}

func (rt *Runtime) formatArgString(a jit.Value) string {
	if s, ok := a.Export().(string); ok {
		return s
	}
	return rt.inspectValue(a, 2)
}

// inspectValue renders a value the way Node's util.inspect does.
func (rt *Runtime) inspectValue(v jit.Value, depth int) string {
	if v.IsUndef() {
		return "undefined"
	}
	if v.IsNull() {
		return "null"
	}
	if a := v.AsArr(); a != nil {
		return rt.inspectArray(v, depth)
	}
	if o := v.AsObj(); o != nil {
		return rt.inspectObject(v, depth)
	}
	switch t := v.Export().(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case string:
		return quoteJS(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		if t == float64(int64(t)) && t < 1e21 && t > -1e21 {
			return strconv.FormatFloat(t, 'f', -1, 64)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []byte:
		return rt.inspectBytes(t)
	}
	return fmt.Sprintf("%v", v.Export())
}

func (rt *Runtime) inspectBytes(b []byte) string {
	if len(b) > 32 {
		return "Buffer[ " + strconv.Itoa(len(b)) + " bytes ]"
	}
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = strconv.Itoa(int(x))
	}
	return "Buffer[ " + strings.Join(parts, " ") + " ]"
}

func (rt *Runtime) inspectArray(a jit.Value, depth int) string {
	if depth == 0 {
		return "[Array]"
	}
	arr := a.AsArr()
	length := jit.ArrayLen(arr)
	parts := make([]string, 0, length)
	for i := 0; i < length; i++ {
		parts = append(parts, rt.inspectValue(jit.ArrayGet(arr, i), depth-1))
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

func (rt *Runtime) inspectObject(v jit.Value, depth int) string {
	if v.AsArr() != nil {
		return rt.inspectArray(v, depth)
	}
	// special cases first
	if fn := v.Get("inspect"); fn.IsCallable() {
		if res, err := rt.jitCall(fn, jit.Undef(), v); err == nil && !res.IsUndef() {
			if s, ok := res.Export().(string); ok {
				return s
			}
		}
	}

	ctorName := ""
	if c := v.Get("constructor"); !c.IsUndef() && !c.IsNull() && c.AsObj() != nil {
		if n := c.Get("name"); !n.IsUndef() {
			ctorName = n.String()
		}
	}

	switch ctorName {
	case "Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "EvalError", "URIError":
		return rt.inspectError(v)
	case "Date":
		return v.Get("toISOString").String()
	case "RegExp":
		return v.Get("toString").String()
	}

	if depth == 0 {
		if ctorName != "" {
			return ctorName + " { ... }"
		}
		return "{ ... }"
	}

	// Node keeps the insertion order of the keys and breaks long objects
	// over several lines.
	keys := v.Keys()
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := v.Get(k)
		parts = append(parts, k+": "+rt.inspectValue(val, depth-1))
	}
	var body string
	switch {
	case len(parts) == 0:
		body = "{}"
	case len(parts) > 6 || len("{ "+strings.Join(parts, ", ")+" }") > 128:
		lines := []string{}
		if len(parts) > 6 {
			// Node keeps the first five entries on one line.
			lines = append(lines, "  "+strings.Join(parts[:5], ", "))
			for _, part := range parts[5:] {
				lines = append(lines, "  "+part)
			}
		} else {
			for _, part := range parts {
				lines = append(lines, "  "+part)
			}
		}
		body = "{\n" + strings.Join(lines, ",\n") + "\n}"
	default:
		body = "{ " + strings.Join(parts, ", ") + " }"
	}
	if ctorName != "" && ctorName != "Object" {
		return ctorName + " " + body
	}
	return body
}

func (rt *Runtime) inspectError(v jit.Value) string {
	name := "Error"
	if n := v.Get("name"); !n.IsUndef() {
		name = n.String()
	}
	msg := ""
	if m := v.Get("message"); !m.IsUndef() {
		msg = m.String()
	}
	if msg == "" {
		return name
	}
	return name + ": " + msg
}

// quoteJS quotes a string the way Node's util.inspect does: the quote
// character that does not appear in the string wins, backticks when both
// single and double quotes are present, and long multi-line strings are
// broken into concatenated segments.
func quoteJS(s string) string {
	single := quoteOne(s)
	if !strings.Contains(s, "\n") || len(single) <= 128 {
		return single
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString(" +\n  ")
		}
		segment := line
		if i < len(lines)-1 {
			segment += "\n"
		}
		b.WriteString(quoteOne(segment))
	}
	return b.String()
}

func quoteOne(s string) string {
	hasSingle := strings.Contains(s, "'")
	hasDouble := strings.Contains(s, "\"")
	hasBacktick := strings.Contains(s, "`")
	quote := byte('\'')
	switch {
	case hasSingle && !hasDouble:
		quote = '"'
	case hasSingle && hasDouble && !hasBacktick:
		quote = '`'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch r {
		case rune(quote):
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '\b':
			b.WriteString("\\b")
		case '\f':
			b.WriteString("\\f")
		case '\v':
			b.WriteString("\\v")
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf("\\x%02x", r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// --- deepStrictEqual ----------------------------------------------------

// registerUtilDeep provides util.isDeepStrictEqual.
func (rt *Runtime) registerUtilDeep() {
	mod := jit.Object()
	setFn(mod, "isDeepStrictEqual", func(args []jit.Value) jit.Value {
		return rt.toJS(deepEqual(argsAt(args, 0), argsAt(args, 1), 0))
	})
	rt.natives["__util"] = mod
}

func deepEqual(a, b jit.Value, depth int) bool {	if depth > 100 {
		return false
	}
	if a.IsUndef() || b.IsUndef() {
		return a.IsUndef() && b.IsUndef()
	}
	if a.IsNull() || b.IsNull() {
		return a.IsNull() && b.IsNull()
	}
	ao := a.AsObj()
	bo := b.AsObj()
	if (ao == nil || bo == nil) && a.AsArr() == nil && b.AsArr() == nil {
		// Primitives (and values without a plain-object shape) compare without
		// touching Export: slices are not comparable with ==.
		return deepEqualExport(a, b)
	}
	// typed arrays / array buffers compare by bytes
	if ab, ok := a.Export().([]byte); ok {
		bb, ok2 := b.Export().([]byte)
		if !ok2 {
			return false
		}
		return reflect.DeepEqual(ab, bb)
	}
	// dates
	if !a.Get("toISOString").IsUndef() && !b.Get("toISOString").IsUndef() {
		aISO := a.Get("toISOString").String()
		bISO := b.Get("toISOString").String()
		if aISO != "" && bISO != "" && aISO == bISO {
			return true
		}
	}
	// arrays
	arrA := a.AsArr() != nil
	arrB := b.AsArr() != nil
	if arrA || arrB {
		if arrA != arrB {
			return false
		}
		arrAObj := a.AsArr()
		arrBObj := b.AsArr()
		al := jit.ArrayLen(arrAObj)
		bl := jit.ArrayLen(arrBObj)
		if al != bl {
			return false
		}
		for i := 0; i < al; i++ {
			if !deepEqual(jit.ArrayGet(arrAObj, i), jit.ArrayGet(arrBObj, i), depth+1) {
				return false
			}
		}
		return true
	}
	// plain objects
	ak := a.Keys()
	bk := b.Keys()
	if len(ak) != len(bk) {
		return false
	}
	sort.Strings(ak)
	sort.Strings(bk)
	for i := range ak {
		if ak[i] != bk[i] {
			return false
		}
		if !deepEqual(a.Get(ak[i]), b.Get(bk[i]), depth+1) {
			return false
		}
	}
	return true
}

// deepEqualExport compares values that have no plain-object shape — primitives,
// arrays, maps — without using Go's ==, which panics on slices.
func deepEqualExport(a, b jit.Value) bool {
	if a.IsUndef() || b.IsUndef() {
		return a.IsUndef() && b.IsUndef()
	}
	if a.IsNull() || b.IsNull() {
		return a.IsNull() && b.IsNull()
	}
	switch {
	case a.IsBool() && b.IsBool():
		return a.AsBool() == b.AsBool()
	case a.IsNum() && b.IsNum():
		return a.AsNum() == b.AsNum()
	case a.IsStr() && b.IsStr():
		return a.AsStr() == b.AsStr()
	case a.AsArr() != nil && b.AsArr() != nil:
		al := jit.ArrayLen(a.AsArr())
		bl := jit.ArrayLen(b.AsArr())
		if al != bl {
			return false
		}
		for i := 0; i < al; i++ {
			if !deepEqualExport(jit.ArrayGet(a.AsArr(), i), jit.ArrayGet(b.AsArr(), i)) {
				return false
			}
		}
		return true
	}
	return false
}
