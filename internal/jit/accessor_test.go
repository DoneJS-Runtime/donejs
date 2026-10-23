package jit

import "strings"
import "testing"

func runAcc(t *testing.T, src string) string {
	t.Helper()
	vm := NewVM()
	var buf strings.Builder
	vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
	if _, err := vm.Run(src); err != nil {
		return "ERR: " + err.Error() + "\n"
	}
	vm.RunJobs()
	return buf.String()
}

func TestAccessors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"class P { get v() { return 42; } }\nprint(new P().v);", "42\n"},
		{"var o = { get x() { return 7; } };\nprint(o.x);", "7\n"},
		{"class P { get v() { return this._v; } set v(x) { this._v = x * 2; } }\nvar p = new P(); p.v = 5; print(p.v);", "10\n"},
		{"var n = 0; var o = { get x() { return n; }, set x(v) { n = v; } }; o.x = 9; print(o.x);", "9\n"},
		{"var o = { get x() { return 7; } };\nprint(o.x());", "ERR: TypeError: o.x is not a function\n"},
		{"var o = {}; o.nope();", "ERR: TypeError: o.nope is not a function\n"},
		{"var f = 7; f();", "ERR: TypeError: f is not a function\n"},
		{"null.x();", "ERR: TypeError: Cannot read properties of null (reading 'x')\n"},
		{"undefined.y();", "ERR: TypeError: Cannot read properties of undefined (reading 'y')\n"},
		{"class C { static get v() { return 3; } }\nprint(C.v);", "3\n"},
		{"var o = { get x() { return 7; } }; print(o.x); print(o.x);", "7\n7\n"},
	}
	for _, c := range cases {
		if got := runAcc(t, c.src); got != c.want {
			t.Errorf("SRC %q\n GOT %q\nWANT %q", c.src, got, c.want)
		}
	}
}
