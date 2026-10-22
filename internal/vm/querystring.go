package vm

import (
	"sort"
	"strings"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerQuerystring() {
	mod := rt.vm.NewObject()

	mod.Set("parse", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		sep := "&"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			sep = call.Argument(1).String()
		}
		out := map[string]any{}
		for _, part := range strings.Split(s, sep) {
			if part == "" {
				continue
			}
			k, v := part, ""
			if i := strings.Index(part, "="); i >= 0 {
				k, v = part[:i], part[i+1:]
			}
			dk := queryDecode(k)
			dv := queryDecode(v)
			if existing, ok := out[dk]; ok {
				switch e := existing.(type) {
				case []any:
					out[dk] = append(e, dv)
				default:
					out[dk] = []any{e, dv}
				}
			} else {
				out[dk] = dv
			}
		}
		return rt.toJS(out)
	})

	mod.Set("stringify", func(call goja.FunctionCall) goja.Value {
		obj := argObj([]goja.Value{call.Argument(0)}, 0)
		if obj == nil {
			return rt.vm.ToValue("")
		}
		sep := "&"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			sep = call.Argument(1).String()
		}
		eq := "="
		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Argument(2)) {
			eq = call.Argument(2).String()
		}
		keys := obj.Keys()
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			v := obj.Get(k)
			if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
				continue
			}
			switch val := v.Export().(type) {
			case []any:
				for _, item := range val {
					parts = append(parts, queryEscape(k)+eq+queryEscape(toStr(item)))
				}
			default:
				parts = append(parts, queryEscape(k)+eq+queryEscape(v.String()))
			}
		}
		return rt.vm.ToValue(strings.Join(parts, sep))
	})

	mod.Set("escape", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(queryEscape(call.Argument(0).String()))
	})
	mod.Set("unescape", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(queryUnescape(call.Argument(0).String()))
	})
	mod.Set("decode", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(queryDecode(call.Argument(0).String()))
	})
	mod.Set("encode", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(queryEscape(call.Argument(0).String()))
	})

	rt.natives["__querystring"] = mod
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

func queryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isQuerySafe(c) {
			b.WriteByte(c)
			continue
		}
		if c == ' ' {
			b.WriteByte('+')
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func isQuerySafe(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '(', ')':
		return true
	}
	return false
}

func queryUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			b.WriteByte(' ')
		case '%':
			if i+2 < len(s) {
				hi, ok1 := hexVal(s[i+1])
				lo, ok2 := hexVal(s[i+2])
				if ok1 && ok2 {
					b.WriteByte(hi<<4 | lo)
					i += 2
					continue
				}
			}
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func queryDecode(s string) string {
	if i := strings.Index(s, "="); i >= 0 {
		s = s[:i]
	}
	return queryUnescape(s)
}
