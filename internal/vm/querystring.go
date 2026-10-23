package vm

import (
	"sort"
	"strings"

	"donejs/internal/jit"
)

func (rt *Runtime) registerQuerystring() {
	mod := jit.Object()

	setFn(mod, "parse", func(args []jit.Value) jit.Value {
		s := argsAt(args, 0).String()
		sep := "&"
		if len(args) > 1 && !args[1].IsUndef() {
			sep = args[1].String()
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

	setFn(mod, "stringify", func(args []jit.Value) jit.Value {
		obj := argObj(args, 0)
		if obj.IsUndef() {
			return jit.Str("")
		}
		sep := "&"
		if len(args) > 1 && !args[1].IsUndef() {
			sep = args[1].String()
		}
		eq := "="
		if len(args) > 2 && !args[2].IsUndef() {
			eq = args[2].String()
		}
		keys := obj.Keys()
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			v := obj.Get(k)
			if v.IsUndef() || v.IsNull() {
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
		return jit.Str(strings.Join(parts, sep))
	})

	setFn(mod, "escape", func(args []jit.Value) jit.Value {
		return jit.Str(queryEscape(argsAt(args, 0).String()))
	})
	setFn(mod, "unescape", func(args []jit.Value) jit.Value {
		return jit.Str(queryUnescape(argsAt(args, 0).String()))
	})
	setFn(mod, "decode", func(args []jit.Value) jit.Value {
		return jit.Str(queryDecode(argsAt(args, 0).String()))
	})
	setFn(mod, "encode", func(args []jit.Value) jit.Value {
		return jit.Str(queryEscape(argsAt(args, 0).String()))
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
