// Package jit is a self-contained bytecode compiler and interpreter for a
// JavaScript subset, accelerated the way V8 accelerates a bytecode VM without
// native codegen: dense switch dispatch, superinstructions (fused opcode
// pairs), stack caching in locals, inline caches for property access and
// hidden classes for object layout.
package jit

import (
	"strconv"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tNumber
	tString
	tIdent
	tPunct
	tTplStart
	tTplChunk
	tTplEnd
	tRegExp
	tBigInt
)

type token struct {
	kind  tokKind
	val   string
	flags string
	num   float64
	line  int
}

var keywords = map[string]bool{
	"var": true, "let": true, "const": true, "function": true, "return": true,
	"if": true, "else": true, "while": true, "do": true, "for": true, "break": true,
	"continue": true, "true": true, "false": true, "null": true, "undefined": true,
	"new": true, "typeof": true, "instanceof": true, "in": true, "of": true,
	"this": true, "delete": true, "void": true, "yield": true, "await": true,
	"class": true, "extends": true, "super": true, "switch": true, "case": true,
	"default": true, "try": true, "catch": true, "finally": true, "throw": true,
}

type lexer struct {
	src  string
	pos  int
	line int
	toks []token
	// last holds the most recent significant token so a `/` can be told
	// apart as a regex literal or a division operator.
	last token
}

// regexAllowed reports whether a `/` at the current position starts a regex
// literal rather than a division. Value-ending tokens (identifiers, literals,
// `)`, `]`, `}`, `++`, `--`) mean division; everything else means regex.
func (l *lexer) regexAllowed() bool {
	if l.last.kind == tEOF {
		return true
	}
	if l.last.kind == tIdent || l.last.kind == tNumber || l.last.kind == tString ||
		l.last.kind == tBigInt || l.last.kind == tTplEnd {
		return false
	}
	switch l.last.val {
	case ")", "]", "}", "++", "--", "true", "false", "null", "this", "super":
		return false
	}
	return true
}

// emit appends a token, tagging it with the current line.
func (l *lexer) emit(t token) { t.line = l.line; l.toks = append(l.toks, t); l.last = t }

func lex(src string) []token {
	l := &lexer{src: src, line: 1}
	l.run()
	return l.toks
}

func (l *lexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func (l *lexer) run() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			if c == '\n' {
				l.line++
			}
			l.pos++
		case c == '/' && l.peek(1) == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.peek(1) == '*':
			l.pos += 2
			for l.pos < len(l.src) && !(l.src[l.pos] == '*' && l.peek(1) == '/') {
				if l.src[l.pos] == '\n' {
					l.line++
				}
				l.pos++
			}
			if l.pos < len(l.src) {
				l.pos += 2
			}
		case c == '/' && l.peek(1) != '/' && l.peek(1) != '*' && l.regexAllowed():
			l.regex()
		case c >= '0' && c <= '9' || (c == '.' && l.peek(1) >= '0' && l.peek(1) <= '9'):
			l.number()
		case c == '"' || c == '\'':
			l.str(c)
		case c == '`':
			l.tmpl()
		case isIdentStart(c):
			l.ident()
		default:
			l.punct()
		}
	}
	l.emit(token{kind: tEOF})
}

func (l *lexer) number() {
	start := l.pos
	if l.src[l.pos] == '0' && (l.peek(1) == 'x' || l.peek(1) == 'X') {
		l.pos += 2
		for l.pos < len(l.src) && isHex(l.src[l.pos]) {
			l.pos++
		}
		v, _ := strconv.ParseInt(l.src[start+2:l.pos], 16, 64)
		if l.pos < len(l.src) && l.src[l.pos] == 'n' {
			l.pos++
			l.emit(token{kind: tBigInt, val: l.src[start+2 : l.pos-1]})
			return
		}
		l.emit(token{kind: tNumber, num: float64(v), val: l.src[start:l.pos]})
		return
	}
	if l.src[l.pos] == '0' && (l.peek(1) == 'b' || l.peek(1) == 'B') {
		l.pos += 2
		for l.pos < len(l.src) && isBinDigit(l.src[l.pos]) {
			l.pos++
		}
		v, _ := strconv.ParseInt(l.src[start+2:l.pos], 2, 64)
		l.emit(token{kind: tNumber, num: float64(v), val: l.src[start:l.pos]})
		return
	}
	if l.src[l.pos] == '0' && (l.peek(1) == 'o' || l.peek(1) == 'O') {
		l.pos += 2
		for l.pos < len(l.src) && isOctDigit(l.src[l.pos]) {
			l.pos++
		}
		v, _ := strconv.ParseInt(l.src[start+2:l.pos], 8, 64)
		l.emit(token{kind: tNumber, num: float64(v), val: l.src[start:l.pos]})
		return
	}
	digits := l.src[l.pos:]
	end := 0
	for end < len(digits) && (digits[end] == '_' || (digits[end] >= '0' && digits[end] <= '9')) {
		end++
	}
	if end < len(digits) && digits[end] == '.' {
		end++
		for end < len(digits) && (digits[end] == '_' || (digits[end] >= '0' && digits[end] <= '9')) {
			end++
		}
	}
	if end < len(digits) && (digits[end] == 'e' || digits[end] == 'E') {
		end++
		if end < len(digits) && (digits[end] == '+' || digits[end] == '-') {
			end++
		}
		for end < len(digits) && (digits[end] == '_' || (digits[end] >= '0' && digits[end] <= '9')) {
			end++
		}
	}
	if l.pos+end < len(l.src) && l.src[l.pos+end] == 'n' {
		l.pos += end + 1
		l.emit(token{kind: tBigInt, val: l.src[start : l.pos-1]})
		return
	}
	l.pos += end
	v, _ := strconv.ParseFloat(strings.ReplaceAll(l.src[start:l.pos], "_", ""), 64)
	l.emit(token{kind: tNumber, num: v, val: l.src[start:l.pos]})
}

func isBinDigit(c byte) bool { return c == '_' || (c >= '0' && c <= '1') }
func isOctDigit(c byte) bool { return c == '_' || (c >= '0' && c <= '7') }

// regex scans a `/pattern/flags` literal.
func (l *lexer) regex() {
	start := l.pos
	l.pos++ // opening '/'
	inClass := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\\' {
			l.pos += 2
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			break
		} else if c == '\n' {
			break // unterminated; the parser will report it
		}
		l.pos++
	}
	pattern := l.src[start+1 : l.pos]
	if l.pos < len(l.src) && l.src[l.pos] == '/' {
		l.pos++
	}
	fstart := l.pos
	for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) && isFlagChar(l.src[l.pos])) {
		l.pos++
	}
	l.emit(token{kind: tRegExp, val: pattern, flags: l.src[fstart:l.pos]})
}

func isFlagChar(c byte) bool {
	switch c {
	case 'd', 'g', 'i', 'm', 's', 'u', 'v', 'y':
		return true
	}
	return false
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (l *lexer) str(quote byte) {
	l.pos++
	var out []byte
	for l.pos < len(l.src) && l.src[l.pos] != quote {
		if l.src[l.pos] == '\\' {
			l.pos++
			if l.pos >= len(l.src) {
				break
			}
			b, n := readEscape(l.src, l.pos)
			out = append(out, b...)
			l.pos += n
			continue
		}
		out = append(out, l.src[l.pos])
		l.pos++
	}
	l.pos++ // closing quote
	l.emit(token{kind: tString, val: string(out)})
}

func (l *lexer) tmpl() {
	l.pos++ // opening backtick
	l.emit(token{kind: tTplStart})
	for {
		start := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '`' && l.src[l.pos] != '$' {
			if l.src[l.pos] == '\\' {
				l.pos++
			}
			if l.pos < len(l.src) {
				l.pos++
			}
		}
		l.emit(token{kind: tTplChunk, val: unescapeStr(l.src[start:l.pos])})
		if l.pos >= len(l.src) {
			break
		}
		if l.src[l.pos] == '`' {
			l.pos++
			break
		}
		// '${' - lex the substitution as a nested expression.
		l.pos += 2
		end := l.templateExprEnd(l.pos)
		inner := l.src[l.pos:end]
		l.pos = end + 1 // skip '}'
		for _, t := range lex(inner) {
			if t.kind != tEOF {
				l.toks = append(l.toks, t)
			}
		}
	}
	l.emit(token{kind: tTplEnd})
}

// templateExprEnd returns the index of the '}' closing a ${...} starting at i.
func (l *lexer) templateExprEnd(i int) int {
	depth := 1
	for i < len(l.src) {
		switch l.src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		case '\'', '"', '`':
			q := l.src[i]
			i++
			for i < len(l.src) && l.src[i] != q {
				if l.src[i] == '\\' {
					i++
				}
				i++
			}
		}
		i++
	}
	return len(l.src)
}

// readEscape decodes the escape sequence starting at src[i] (the character
// just after the backslash). It returns the decoded bytes and how many source
// bytes were consumed.
func readEscape(src string, i int) ([]byte, int) {
	c := src[i]
	switch c {
	case 'n':
		return []byte{'\n'}, 1
	case 't':
		return []byte{'\t'}, 1
	case 'r':
		return []byte{'\r'}, 1
	case 'b':
		return []byte{'\b'}, 1
	case 'f':
		return []byte{'\f'}, 1
	case 'v':
		return []byte{'\v'}, 1
	case '0':
		return []byte{0}, 1
	case 'x':
		if i+3 <= len(src) {
			if v, err := strconv.ParseUint(src[i+1:i+3], 16, 8); err == nil {
				return []byte{byte(v)}, 3
			}
		}
		return []byte{'x'}, 1
	case 'u':
		if i+1 < len(src) && src[i+1] == '{' {
			j := i + 2
			for j < len(src) && src[j] != '}' {
				j++
			}
			if j < len(src) {
				if v, err := strconv.ParseUint(src[i+2:j], 16, 32); err == nil {
					return []byte(string(rune(v))), j - i + 1
				}
			}
			return []byte{'u'}, 1
		}
		if i+5 <= len(src) {
			if v, err := strconv.ParseUint(src[i+1:i+5], 16, 32); err == nil {
				r := rune(v)
				// A surrogate pair written as two \uXXXX escapes joins into
				// the single code point it denotes.
				if r >= 0xD800 && r <= 0xDBFF && i+11 <= len(src) &&
					src[i+5] == '\\' && src[i+6] == 'u' {
					if lo, err := strconv.ParseUint(src[i+7:i+11], 16, 32); err == nil &&
						lo >= 0xDC00 && lo <= 0xDFFF {
						cp := rune(0x10000 + (int64(r)-0xD800)<<10 + (int64(lo) - 0xDC00))
						return []byte(string(cp)), 11
					}
				}
				return []byte(string(r)), 5
			}
		}
		return []byte{'u'}, 1
	case '\n':
		return nil, 1
	case '\r':
		if i+1 < len(src) && src[i+1] == '\n' {
			return nil, 2
		}
		return nil, 1
	default:
		return []byte{c}, 1
	}
}

// unescapeStr expands backslash escapes in a template-literal chunk.
func unescapeStr(s string) string {
	var out []byte
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			b, n := readEscape(s, i+1)
			out = append(out, b...)
			i += 1 + n
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

func unescape(c byte) []byte {
	switch c {
	case 'n':
		return []byte{'\n'}
	case 't':
		return []byte{'\t'}
	case 'r':
		return []byte{'\r'}
	case '0':
		return []byte{0}
	}
	return []byte{c}
}

func (l *lexer) ident() {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.pos++
	}
	l.emit(token{kind: tIdent, val: l.src[start:l.pos]})
}

var puncts3 = []string{"===", "!==", ">>>", "**=", "...", "<<=", ">>=", "&&=", "||=", "??="}
var puncts2 = []string{"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "**"}

func (l *lexer) punct() {
	rest := l.src[l.pos:]
	for _, p := range puncts3 {
		if hasPrefix(rest, p) {
			l.emit(token{kind: tPunct, val: p})
			l.pos += len(p)
			return
		}
	}
	for _, p := range puncts2 {
		if hasPrefix(rest, p) {
			l.emit(token{kind: tPunct, val: p})
			l.pos += len(p)
			return
		}
	}
	l.emit(token{kind: tPunct, val: string(l.src[l.pos])})
	l.pos++
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
