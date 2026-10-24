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
		"print(1 + 2 * 3);":               "7\n",
		"print((1 + 2) * 3);":             "9\n",
		"print(10 / 4);":                  "2.5\n",
		"print(10 % 3);":                  "1\n",
		"print(2 ** 10);":                 "1024\n",
		"print(-5 + 3);":                  "-2\n",
		"print(7 > 3);":                   "true\n",
		"print(7 < 3);":                   "false\n",
		"print(1 == 1);":                  "true\n",
		"print('a' + 'b');":               "ab\n",
		"print(1 < 2 && 3 > 2);":          "true\n",
		"print(1 < 2 || 3 < 2);":          "true\n",
		"let x = 5; x = x + 1; print(x);": "6\n",
		"let x = 10; x += 5; print(x);":   "15\n",
		"print(0.1 + 0.2);":               "0.30000000000000004\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestControlFlow(t *testing.T) {
	cases := map[string]string{
		"let s = 0; for (let i = 0; i < 5; i++) s += i; print(s);":                                "10\n",
		"let s = 0; let i = 0; while (i < 5) { s += i; i++; } print(s);":                          "10\n",
		"let s = 0; let i = 0; do { s += i; i++; } while (i < 5); print(s);":                      "10\n",
		"let s = 0; for (let i = 0; i < 10; i++) { if (i == 5) break; s += i; } print(s);":        "10\n",
		"let s = 0; for (let i = 0; i < 10; i++) { if (i % 2 == 0) continue; s += i; } print(s);": "25\n",
		"let x = 0; if (3 > 2) { x = 1; } else { x = 2; } print(x);":                              "1\n",
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
		"let a = [1, 2, 3]; print(a[1]);":                                                       "2\n",
		"let a = [1, 2, 3]; print(a.length);":                                                   "3\n",
		"let a = [1, 2, 3]; a[0] = 9; print(a[0]);":                                             "9\n",
		"let o = { x: 1, y: 2 }; print(o.x + o.y);":                                             "3\n",
		"let o = { x: 1 }; o.y = 5; print(o.y);":                                                "5\n",
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

func TestArrowsAndTemplates(t *testing.T) {
	cases := map[string]string{
		"print((() => 6 * 7)());":                                              "42\n",
		"const double = x => x * 2; print(double(21));":                        "42\n",
		"const add = (a, b) => a + b; print(add(2, 3));":                       "5\n",
		"const greet = name => `hello ${name} world`; print(greet('donejs'));": "hello donejs world\n",
		"const t = `a${1 + 1}b${2}`.replace('2', 'x'); print(t);":              "axb2\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestDestructuring(t *testing.T) {
	cases := map[string]string{
		"let [a, b, c] = [10, 20, 30]; print(a + b + c);":            "60\n",
		"let [a, b = 5] = [1]; print(a); print(b);":                  "1\n5\n",
		"let { x, y: z, w = 9 } = { x: 1, y: 2 }; print(x + z + w);": "12\n",
		"function f({ q }) { return q * 2; } print(f({ q: 21 }));":   "42\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestForOf(t *testing.T) {
	cases := map[string]string{
		"let s = 0; for (let x of [1, 2, 3]) s += x; print(s);":           "6\n",
		"let s = 0; for (let x of 'abc') s += x.charCodeAt(0); print(s);": "294\n",
		"let n = 0; for (const k in { a: 1, b: 2 }) n++; print(n);":       "2\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestSwitchTryThrow(t *testing.T) {
	cases := map[string]string{
		"let r = 'c'; switch (r) { case 'a': print(1); break; case 'b': print(2); break; default: print(3); }": "3\n",
		"let r = 'b'; switch (r) { case 'a': print(1); break; case 'b': print(2); break; }":                    "2\n",
		"try { throw new Error('boom'); } catch (e) { print(e.message); }":                                     "boom\n",
		"try { throw 'x'; } catch { print('caught'); } finally { print('fin'); }":                              "caught\nfin\n",
		"try { try { throw 1; } catch (e) { throw e; } } catch (e) { print('caught'); }":                       "caught\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestClasses(t *testing.T) {
	src := `
class Point { constructor(x, y) { this.x = x; this.y = y; } dist() { return this.x + this.y; } static make(x, y) { return new Point(x, y); } }
const p = new Point(3, 4);
print(p.dist());
print(Point.make(5, 6).dist());
`
	want := "7\n11\n"
	if got := run(t, src); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNewTarget(t *testing.T) {
	src := `
function Ctor() { this.t = new.target; return this; }
class A { constructor() { this.t = new.target; return this; } }
class B extends A { constructor() { super(); this.d = new.target; return this; } }
function G() { return new.target; }
print(new Ctor().t === Ctor);
print(new A().t === A);
print(new B().t === B);
print(typeof new G());
print(typeof new.target);
class M { m() { return 1 } }
const OM = { m() { return 1 } };
try { new OM.m(); } catch (e) { print(e.name); }
if (new M().m() !== 1) throw new Error('method call');
function make() { return function inner(a) { this.a = a; }; }
const f1 = make();
const f2 = make();
if (f1.prototype === f2.prototype) throw new Error('shared prototype');
print(new f1(1).a);
`
	want := "true\ntrue\ntrue\nfunction\nundefined\nTypeError\n1\n"
	if got := run(t, src); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOptionalChaining(t *testing.T) {
	cases := map[string]string{
		"const o = {}; print(o?.x ?? 42);":            "42\n",
		"const o = { x: 7 }; print(o?.x ?? 42);":      "7\n",
		"const arr = [5]; print(arr?.[0] ?? 99);":     "5\n",
		"const f = (x) => x + 1; print(f?.(1) ?? 0);": "2\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestSpread(t *testing.T) {
	cases := map[string]string{
		"const a = [1, 2]; const b = [...a, 3]; print(b.length);":    "3\n",
		"function f(...xs) { return xs.length; } print(f(1, 2, 3));": "3\n",
		"const o = { x: 1 }; const p = { ...o, y: 2 }; print(p.y);":  "2\n",
		"const a = [1]; const b = [...a, ...[2]]; print(b.length);":  "2\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestClosures(t *testing.T) {
	cases := map[string]string{
		// Counters keep independent state across invocations.
		"function make() { let n = 0; return function() { n = n + 1; return n; }; }\n" +
			"var a = make(), b = make(); print(a()); print(a()); print(b());": "1\n2\n1\n",
		// A grandchild reaches a grandparent's local.
		"function outer() { let x = 41; function mid() { return function() { x = x + 1; return x; }; } return mid(); }\n" +
			"var f = outer(); print(f()); print(f());": "42\n43\n",
		// Capturing a parameter.
		"function adder(base) { return function(n) { return base + n; }; }\n" +
			"var add5 = adder(5); print(add5(3)); print(add5(10));": "8\n15\n",
		// Nested closures of different depths.
		"function a(x) { return function(y) { return function(z) { return x + y + z; }; }; }\n" +
			"print(a(1)(2)(3));": "6\n",
		// Arrows read the enclosing `this`.
		"var o = { v: 7, get: function() { var f = () => this.v; return f(); } }; print(o.get());": "7\n",
		"var p = { n: 3, m: function() { var f = () => this.n * 2; return f(); } }; print(p.m());": "6\n",
		// Class methods and the constructor capture the declaring scope.
		"function mk(base) { class C { constructor(x) { this.x = x + base; } plus() { var b = base; return this.x + b; } } return new C(5); }\n" +
			"var c = mk(10); print(c.x); print(c.plus());": "15\n25\n",
		// A method naming its own class sees the bound class.
		"function mkd() { class D { constructor() { this.ok = true; } static make() { return new D(); } } return D.make(); }\n" +
			"print(mkd().ok);": "true\n",
		// `super` inside an arrow in a method.
		"class A { hi() { return 'A'; } }\nclass B extends A { hi() { var f = () => super.hi() + '!'; return f(); } }\n" +
			"print(new B().hi());": "A!\n",
		// A rest parameter can be captured.
		"function collect(first) { return function(...rest) { return first + ':' + rest.length; }; }\n" +
			"print(collect('a')(1, 2, 3));": "a:3\n",
		// A catch binding can be captured.
		"function t() { try { throw new Error('boom'); } catch (e) { return function() { return e.message; }; } }\n" +
			"print(t()());": "boom\n",
		// Two closures over one slot share the value.
		"function pair() { let n = 1; return [function() { return n; }, function() { n = 99; }]; }\n" +
			"var q = pair(); print(q[0]()); q[1](); print(q[0]());": "1\n99\n",
		// A write in the outer scope is visible to the closure.
		"function c1() { var v = 1; var get = function() { return v; }; v = 2; return get(); }\nprint(c1());": "2\n",
		// Closures created in a loop keep their own arguments, even though the
		// frame is recycled between iterations.
		"var fs = []; for (var i = 0; i < 3; i++) { (function(k) { fs.push(function() { return k; }); })(i); }\n" +
			"print(fs[0]() + '' + fs[1]() + fs[2]());": "012\n",
		// A closure over a rest + object property state.
		"function counter() { let n = 0; return { inc: function() { n = n + 1; return n; }, get: function() { return n; } }; }\n" +
			"var r = counter(); r.inc(); r.inc(); print(r.get());": "2\n",
		// Recursive named function still resolves through the capture path.
		"function fact(n) { if (n <= 1) return 1; return n * fact(n - 1); } print(fact(5));": "120\n",
	}
	for src, want := range cases {
		if got := run(t, src); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

func TestAsync(t *testing.T) {
	cases := map[string]string{
		// await a non-promise passes the value straight through.
		"async function f() { return await 7; } f().then(v => print(v));": "7\n",
		// await a fulfilled promise yields its value.
		"async function f() { return await Promise.resolve(9); } f().then(print);": "9\n",
		// await a rejected promise throws into catch.
		"async function f() { try { await Promise.reject(new Error('nope')); } catch (e) { print('caught ' + e.message); } } f();": "caught nope\n",
		// an async function returns a promise; execution suspends and resumes.
		"async function f() { print('a'); await 0; print('b'); } f(); print('c');": "a\nc\nb\n",
		// rejection with no catch reaches the returned promise.
		"async function f() { throw new Error('boom'); } f().then(() => print('ok'), e => print('rej ' + e.message));": "rej boom\n",
		// chained thens run in order on the microtask queue.
		"Promise.resolve(1).then(v => v + 1).then(v => print(v));": "2\n",
		// async arrow with await.
		"const g = async () => { const v = await Promise.resolve(5); print(v); }; g();": "5\n",
		// Promise.all resolves every element.
		"Promise.all([1, Promise.resolve(2), 3]).then(a => print(a.join(',')));": "1,2,3\n",
	}
	for src, want := range cases {
		got := runJobs(t, src)
		if got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
}

// runJobs runs src then drains the microtask queue, as the host event loop does.
func runJobs(t *testing.T, src string) string {
	t.Helper()
	vm := NewVM()
	var buf strings.Builder
	vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
	if _, err := vm.Run(src); err != nil {
		t.Fatalf("run error: %v", err)
	}
	vm.RunJobs()
	return buf.String()
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

// TestHostThrow verifies a native panic (*ThrowError) surfaces as a JS
// catchable exception — the channel builtin modules throw through.
func TestHostThrow(t *testing.T) {
	vm := NewVM()
	var buf strings.Builder
	vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
	vm.SetGlobal("boom", builtinFn("boom", func([]Value) Value {
		panic(&ThrowError{Value: strErr("TypeError", "native boom")})
	}))
	src := "try { boom(); } catch (e) { print(e.name + ': ' + e.message); } print('after');"
	if _, err := vm.Run(src); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := buf.String(), "TypeError: native boom\nafter\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
