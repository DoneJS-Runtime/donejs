package vm

import (
	"strings"

	"donejs/internal/jit"
)

// uri.go implements the global encodeURI(Component)/decodeURI(Component)
// functions with the ECMAScript reserved-character sets.

func uriUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

func uriEncode(s string, extra string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if uriUnreserved(c) || strings.IndexByte(extra, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	return b.String()
}

func uriDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) || !uriHex(s[i+1]) || !uriHex(s[i+2]) {
			panic(jit.Err("URIError", "URI malformed"))
		}
		b.WriteByte(uriByte(s[i+1])<<4 | uriByte(s[i+2]))
		i += 2
	}
	return b.String()
}

func uriHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func uriByte(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// registerURI installs the four URI globals.
func (rt *Runtime) registerURI() {
	set := func(name string, fn func(string) string) {
		rt.jit.SetGlobal(name, jit.BuiltinFn(name, func(a []jit.Value) jit.Value {
			s := ""
			if len(a) > 0 {
				s = a[0].String()
			}
			return jit.Str(fn(s))
		}))
	}
	set("encodeURI", func(s string) string { return uriEncode(s, ";,/?:@&=+$#") })
	set("encodeURIComponent", func(s string) string { return uriEncode(s, "") })
	set("decodeURI", uriDecode)
	set("decodeURIComponent", uriDecode)
}
