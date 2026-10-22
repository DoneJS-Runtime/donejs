package vm

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

const maxInspectDepth = 4

// registerInspect provides util.inspect / util.format in Go for speed and
// Node-compatible output.
func (rt *Runtime) registerInspect() {
	mod := rt.vm.NewObject()

	mod.Set("inspect", func(call goja.FunctionCall) goja.Value {
		v := call.Argument(0)
		depth := -1
		if len(call.Arguments) > 1 {
			if o, ok := call.Argument(1).(*goja.Object); ok {
				d := o.Get("depth")
				if d != nil && !goja.IsUndefined(d) && !goja.IsNull(d) {
					depth = int(d.ToInteger())
				}
			}
		}
		return rt.vm.ToValue(rt.inspectValue(v, depth, make(map[uintptr]bool)))
	})

	mod.Set("format", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(rt.formatArgs(call.Arguments))
	})

	mod.Set("replacer", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})

	rt.natives["__inspect"] = mod
}

// formatArgs implements util.format.
func (rt *Runtime) formatArgs(args []goja.Value) string {
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
		parts = append(parts, rt.inspectValue(a, 2, make(map[uintptr]bool)))
	}
	return strings.Join(parts, " ")
}

// formatWithPlaceholders implements %s/%d/%i/%f/%j/%o/%O/%% substitution.
func (rt *Runtime) formatWithPlaceholders(f string, args []goja.Value) string {
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
				out.WriteString(rt.inspectValue(a, 4, make(map[uintptr]bool)))
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

func (rt *Runtime) formatArgString(a goja.Value) string {
	if s, ok := a.Export().(string); ok {
		return s
	}
	return rt.inspectValue(a, 2, make(map[uintptr]bool))
}

// inspectValue renders a value the way Node's util.inspect does.
func (rt *Runtime) inspectValue(v goja.Value, depth int, seen map[uintptr]bool) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "null"
	}
	if o, ok := v.(*goja.Object); ok {
		return rt.inspectObject(o, depth, seen)
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

func (rt *Runtime) inspectArray(a *goja.Object, depth int, seen map[uintptr]bool) string {
	if depth == 0 {
		return "[Array]"
	}
	length := int(a.Get("length").ToInteger())
	parts := make([]string, 0, length)
	for i := 0; i < length; i++ {
		parts = append(parts, rt.inspectValue(a.Get(strconv.Itoa(i)), depth-1, seen))
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

func (rt *Runtime) inspectObject(o *goja.Object, depth int, seen map[uintptr]bool) string {
	if o.ClassName() == "Array" {
		return rt.inspectArray(o, depth, seen)
	}
	// special cases first
	if fn, ok := goja.AssertFunction(o.Get("inspect")); ok {
		if v, err := fn(o); err == nil && v != nil {
			if s, ok := v.Export().(string); ok {
				return s
			}
		}
	}

	ctorName := ""
	if c := o.Get("constructor"); c != nil && !goja.IsUndefined(c) && !goja.IsNull(c) {
		if co, ok := c.(*goja.Object); ok {
			if n := co.Get("name"); n != nil && !goja.IsUndefined(n) {
				ctorName = n.String()
			}
		}
	}

	switch ctorName {
	case "Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "EvalError", "URIError":
		return rt.inspectError(o)
	case "Date":
		return o.Get("toISOString").String()
	case "RegExp":
		return o.Get("toString").String()
	}

	if depth == 0 {
		if ctorName != "" {
			return ctorName + " { ... }"
		}
		return "{ ... }"
	}

	// Node keeps the insertion order of the keys and breaks long objects
	// over several lines.
	keys := o.Keys()
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := o.Get(k)
		parts = append(parts, k+": "+rt.inspectValue(val, depth-1, seen))
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

func (rt *Runtime) inspectError(o *goja.Object) string {
	name := "Error"
	if n := o.Get("name"); n != nil && !goja.IsUndefined(n) {
		name = n.String()
	}
	msg := ""
	if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
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
	mod := rt.vm.NewObject()
	mod.Set("isDeepStrictEqual", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(deepEqual(call.Argument(0), call.Argument(1), 0))
	})
	rt.natives["__util"] = mod
}

func deepEqual(a, b goja.Value, depth int) bool {
	if depth > 100 {
		return false
	}
	if a == nil || b == nil {
		return goja.IsUndefined(a) && goja.IsUndefined(b)
	}
	if goja.IsUndefined(a) || goja.IsUndefined(b) {
		return goja.IsUndefined(a) && goja.IsUndefined(b)
	}
	if goja.IsNull(a) || goja.IsNull(b) {
		return goja.IsNull(a) && goja.IsNull(b)
	}
	ao, aIsObj := a.(*goja.Object)
	bo, bIsObj := b.(*goja.Object)
	if !aIsObj || !bIsObj {
		return a.Export() == b.Export()
	}
	// typed arrays / array buffers compare by bytes
	if ab, ok := a.Export().(goja.ArrayBuffer); ok {
		bb, ok2 := b.Export().(goja.ArrayBuffer)
		if !ok2 {
			return false
		}
		return reflect.DeepEqual(ab.Bytes(), bb.Bytes())
	}
	if au, ok := a.Export().([]byte); ok {
		bu, ok2 := b.Export().([]byte)
		if !ok2 {
			return false
		}
		return reflect.DeepEqual(au, bu)
	}
	// dates
	if ao.Get("toISOString") != nil && bo.Get("toISOString") != nil {
		aISO := ao.Get("toISOString").String()
		bISO := bo.Get("toISOString").String()
		if aISO != "" && bISO != "" && aISO == bISO {
			return true
		}
	}
	// arrays
	if ao.ClassName() == "Array" || bo.ClassName() == "Array" {
		if ao.ClassName() != bo.ClassName() {
			return false
		}
		al := int(ao.Get("length").ToInteger())
		bl := int(bo.Get("length").ToInteger())
		if al != bl {
			return false
		}
		for i := 0; i < al; i++ {
			if !deepEqual(ao.Get(strconv.Itoa(i)), bo.Get(strconv.Itoa(i)), depth+1) {
				return false
			}
		}
		return true
	}
	// plain objects
	ak := ao.Keys()
	bk := bo.Keys()
	if len(ak) != len(bk) {
		return false
	}
	sort.Strings(ak)
	sort.Strings(bk)
	for i := range ak {
		if ak[i] != bk[i] {
			return false
		}
		if !deepEqual(ao.Get(ak[i]), bo.Get(bk[i]), depth+1) {
			return false
		}
	}
	return true
}
