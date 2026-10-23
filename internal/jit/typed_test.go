package jit

import (
	"strings"
	"testing"
)

// TestTypedArrays pins the ArrayBuffer/Uint8Array/DataView semantics verified
// against Node 26.
func TestTypedArrays(t *testing.T) {
	run := func(src string) string {
		t.Helper()
		vm := NewVM()
		var buf strings.Builder
		vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
		if _, err := vm.Run(src); err != nil {
			t.Fatalf("run %q: %v", src, err)
		}
		return buf.String()
	}
	cases := []struct{ src, want string }{
		{"var ab = new ArrayBuffer(4); var u8 = new Uint8Array(ab); u8[0] = 255; u8[1] = 300; print(ab.byteLength, u8.length, u8.byteOffset);", "4 4 0\n"},
		{"var u = new Uint8Array([1,2,3]); var s = u.subarray(1); s[0] = 9; print(u.join(','));", "1,9,3\n"},
		{"var ab = new ArrayBuffer(8); var dv = new DataView(ab); dv.setFloat64(0, 1.5, true); print(dv.getFloat64(0, true)); dv.setUint32(0, 42, false); print(dv.getUint32(0, false));", "1.5\n42\n"},
		{"print(ArrayBuffer.isView(new Uint8Array(1)), ArrayBuffer.isView(new ArrayBuffer(1)));", "true false\n"},
		{"var u = new Uint8Array([4,5]); var a = [...u]; print(a.length, a[1]);", "2 5\n"},
		{"class Buf extends Uint8Array { constructor(n) { super(n); this.fill(7); } } var b = new Buf(3); print(b.length, b[0], b instanceof Uint8Array, b instanceof Buf);", "3 7 true true\n"},
		{"try { new Uint8Array(-1); } catch (e) { print(e.name + ': ' + e.message); }", "RangeError: Invalid typed array length: -1\n"},
		{"try { new DataView(new ArrayBuffer(4), 8); } catch (e) { print(e.name + ': ' + e.message); }", "RangeError: Start offset 8 is outside the bounds of the buffer\n"},
		{"print(new Uint8Array('x').length, new Uint8Array({}).length, new Uint8Array(2.5).length);", "0 0 2\n"},
		{"var u = new Uint8Array([255,44,1,1]); u.copyWithin(0, 2); print(u[0], u[1]);", "1 1\n"},
		{"var ab = new ArrayBuffer(4); var u = new Uint8Array(ab); u[0] = 9; print(new Uint8Array(ab)[0]);", "9\n"},
		{"var ab = new ArrayBuffer(4); var dv = new DataView(ab); dv.setUint8(0, 7); dv.setInt16(1, -2, true); print(dv.getUint8(0), dv.getInt16(1, true));", "7 -2\n"},
		{"print(new Uint8Array(NaN).length, new Uint8Array(true).length);", "0 1\n"},
	}
	for _, c := range cases {
		if got := run(c.src); got != c.want {
			t.Errorf("src %q:\n got %q\nwant %q", c.src, got, c.want)
		}
	}
}

// TestInstanceofIn pins the operators that used to compile to OpNop.
func TestInstanceofIn(t *testing.T) {
	vm := NewVM()
	var buf strings.Builder
	vm.out = func(s string) { buf.WriteString(s); buf.WriteByte('\n') }
	src := `
class A {}
class B extends A {}
var b = new B();
print(b instanceof B, b instanceof A, (new A()) instanceof B);
print('x' in {x: 1}, 'y' in {x: 1}, 0 in [7], 'length' in [7]);
try { 1 instanceof 2; } catch (e) { print(e.name + ': ' + e.message); }
`
	if _, err := vm.Run(src); err != nil {
		t.Fatal(err)
	}
	want := "true true false\ntrue false true true\nTypeError: Right-hand side of 'instanceof' is not callable\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
