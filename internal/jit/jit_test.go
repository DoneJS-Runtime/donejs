package jit

import (
	"strings"
	"testing"
)

func run(t *testing.T, src string) string {
	t.Helper()
	vm := NewVM()
	var buf strings.Builder
	vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
	if _, err := vm.Run(src); err != nil {
		t.Fatalf("run error: %v", err)
	}
	return buf.String()
}

func TestArith(t *testing.T) {
	cases := map[string]string{
		"print(1 + 2 * 3);":            "7\n",
		"print((1 + 2) * 3);":          "9\n",
		"print(10 / 4);":               "2.5\n",
		"print(10 % 3);":               "1\n",
		"print(2 ** 10);":              "1024\n",
		"print(-5 + 3);":               "-2\n",
		"print(7 > 3);":                "true\n",
		"print(7 < 3);":                "false\n",
		"print(1 == 1);":               "true\n",
		"print('a' + 'b');":            "ab\n",
		"print(1 < 2 && 3 > 2);":       "true\n",
		"print(1 < 2 || 3 < 2);":       "true\n",
		"let x = 5; x = x + 1; print(x);": "6\n",
		"let x = 10; x += 5; print(x);":   "15\n",
		"print(0.1 + 0.2);":            "0.30000000000000004\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestControlFlow(t *testing.T) {
	cases := map[string]string{
		"let s = 0; for (let i = 0; i < 5; i++) s += i; print(s);": "10\n",
		"let s = 0; let i = 0; while (i < 5) { s += i; i++; } print(s);": "10\n",
		"let s = 0; let i = 0; do { s += i; i++; } while (i < 5); print(s);": "10\n",
		"let s = 0; for (let i = 0; i < 10; i++) { if (i == 5) break; s += i; } print(s);": "10\n",
		"let s = 0; for (let i = 0; i < 10; i++) { if (i % 2 == 0) continue; s += i; } print(s);": "25\n",
		"let x = 0; if (3 > 2) { x = 1; } else { x = 2; } print(x);": "1\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestFunctions(t *testing.T) {
	src := `
function fib(n) { if (n < 2) return n; return fib(n-1) + fib(n-2); }
print(fib(20));
function add(a, b) { return a + b; }
print(add(3, 4));
`
	want := "6765\n7\n"
	if got := run(t, src); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestArraysAndObjects(t *testing.T) {
	cases := map[string]string{
		"let a = [1, 2, 3]; print(a[1]);":                          "2\n",
		"let a = [1, 2, 3]; print(a.length);":                      "3\n",
		"let a = [1, 2, 3]; a[0] = 9; print(a[0]);":                "9\n",
		"let o = { x: 1, y: 2 }; print(o.x + o.y);":                "3\n",
		"let o = { x: 1 }; o.y = 5; print(o.y);":                    "5\n",
		"let s = 0; let a = [1,2,3,4]; for (let i = 0; i < a.length; i++) s += a[i]; print(s);": "10\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestBuiltins(t *testing.T) {
	cases := map[string]string{
		"print(Math.floor(3.7));": "3\n",
		"print(Math.abs(-4));":    "4\n",
		"print(Math.sqrt(16));":   "4\n",
		"print(Math.max(3, 9));":  "9\n",
		"console.log('hi');":      "hi\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func BenchmarkFib25(b *testing.B) {
	src := `function fib(n) { if (n < 2) return n; return fib(n-1) + fib(n-2); } print(fib(25));`
	for i := 0; i < b.N; i++ {
		vm := NewVM()
		vm.out = func(string) {}
		vm.Run(src)
	}
}

func BenchmarkLoop1M(b *testing.B) {
	src := `let s = 0; for (let i = 0; i < 1000000; i++) s += i; print(s);`
	for i := 0; i < b.N; i++ {
		vm := NewVM()
		vm.out = func(string) {}
		vm.Run(src)
	}
}
