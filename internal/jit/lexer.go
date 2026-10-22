// Package jit is a self-contained bytecode compiler and interpreter for a
// JavaScript subset, accelerated the way V8 accelerates a bytecode VM without
// native codegen: dense switch dispatch, superinstructions (fused opcode
// pairs), stack caching in locals, inline caches for property access and
// hidden classes for object layout.
package jit

import "strconv"

type tokKind int

const (
	tEOF tokKind = iota
	tNumber
	tString
	tIdent
	tPunct
)

type token struct {
	kind tokKind
	val  string
	num  float64
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
	toks []token
}

func lex(src string) []token {
	l := &lexer{src: src}
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
			l.pos++
		case c == '/' && l.peek(1) == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.peek(1) == '*':
			l.pos += 2
			for l.pos < len(l.src) && !(l.src[l.pos] == '*' && l.peek(1) == '/') {
				l.pos++
			}
			if l.pos < len(l.src) {
				l.pos += 2
			}
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
	l.toks = append(l.toks, token{kind: tEOF})
}

func (l *lexer) number() {
	start := l.pos
	if l.src[l.pos] == '0' && (l.peek(1) == 'x' || l.peek(1) == 'X') {
		l.pos += 2
		for l.pos < len(l.src) && isHex(l.src[l.pos]) {
			l.pos++
		}
		v, _ := strconv.ParseInt(l.src[start+2:l.pos], 16, 64)
		l.toks = append(l.toks, token{kind: tNumber, num: float64(v), val: l.src[start:l.pos]})
		return
	}
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.pos++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
	}
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
	}
	v, _ := strconv.ParseFloat(l.src[start:l.pos], 64)
	l.toks = append(l.toks, token{kind: tNumber, num: v, val: l.src[start:l.pos]})
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (l *lexer) str(quote byte) {
	l.pos++
	start := l.pos
	var out []byte
	for l.pos < len(l.src) && l.src[l.pos] != quote {
		if l.src[l.pos] == '\\' {
			l.pos++
			if l.pos >= len(l.src) {
				break
			}
			out = append(out, unescape(l.src[l.pos])...)
			l.pos++
			continue
		}
		out = append(out, l.src[l.pos])
		l.pos++
	}
	_ = start
	l.pos++ // closing quote
	l.toks = append(l.toks, token{kind: tString, val: string(out)})
}

func (l *lexer) tmpl() {
	// Template literal: treat the whole thing as a string, no interpolation.
	l.pos++
	start := l.pos
	for l.pos < len(l.src) && l.src[l.pos] != '`' {
		if l.src[l.pos] == '\\' {
			l.pos++
		}
		l.pos++
	}
	s := l.src[start:l.pos]
	l.pos++
	l.toks = append(l.toks, token{kind: tString, val: s})
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
	l.toks = append(l.toks, token{kind: tIdent, val: l.src[start:l.pos]})
}

var puncts3 = []string{"===", "!==", ">>>", "**=", "...", "<<=", ">>=", "&&=", "||=", "??="}
var puncts2 = []string{"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "**"}

func (l *lexer) punct() {
	rest := l.src[l.pos:]
	for _, p := range puncts3 {
		if hasPrefix(rest, p) {
			l.toks = append(l.toks, token{kind: tPunct, val: p})
			l.pos += len(p)
			return
		}
	}
	for _, p := range puncts2 {
		if hasPrefix(rest, p) {
			l.toks = append(l.toks, token{kind: tPunct, val: p})
			l.pos += len(p)
			return
		}
	}
	l.toks = append(l.toks, token{kind: tPunct, val: string(l.src[l.pos])})
	l.pos++
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
