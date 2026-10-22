package vm

import (
	"encoding/base64"
	"encoding/hex"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// registerEncoding exposes byte/string conversions used by Buffer, fs and crypto.
func (rt *Runtime) registerEncoding() {
	enc := rt.vm.NewObject()

	enc.Set("toBytes", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		encName := "utf8"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			encName = call.Argument(1).String()
		}
		b, err := encodeString(s, encName)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.newUint8Array(b)
	})

	enc.Set("fromBytes", func(call goja.FunctionCall) goja.Value {
		ab := rt.arrayBuffer(call.Argument(0))
		encName := "utf8"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			encName = call.Argument(1).String()
		}
		s, err := decodeBytes(ab, encName)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.vm.ToValue(s)
	})

	enc.Set("byteLength", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		encName := "utf8"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			encName = call.Argument(1).String()
		}
		b, err := encodeString(s, encName)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.vm.ToValue(len(b))
	})

	rt.natives["__encoding"] = enc
}

func encodeString(s, enc string) ([]byte, error) {
	switch enc {
	case "", "utf8", "utf-8":
		return []byte(s), nil
	case "latin1", "binary", "ascii":
		b := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			b[i] = s[i]
		}
		return b, nil
	case "utf16le", "ucs2", "ucs-2":
		u := utf16.Encode([]rune(s))
		b := make([]byte, 0, len(u)*2)
		for _, c := range u {
			b = append(b, byte(c), byte(c>>8))
		}
		return b, nil
	case "hex":
		b := make([]byte, hex.DecodedLen(len(s)))
		if _, err := hex.Decode(b, []byte(s)); err != nil {
			return nil, err
		}
		return b, nil
	case "base64":
		return base64.StdEncoding.DecodeString(s)
	case "base64url":
		return base64.URLEncoding.DecodeString(s)
	default:
		return nil, errUnknownEncoding(enc)
	}
}

func decodeBytes(b []byte, enc string) (string, error) {
	switch enc {
	case "", "utf8", "utf-8":
		return string(b), nil
	case "latin1", "binary":
		var sb []byte
		sb = append(sb, b...)
		return string(sb), nil
	case "ascii":
		var sb []byte
		for _, c := range b {
			if c > 0x7f {
				c = '?'
			}
			sb = append(sb, c)
		}
		return string(sb), nil
	case "utf16le", "ucs2", "ucs-2":
		if len(b)%2 != 0 {
			return "", errTruncatedUTF16
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u)), nil
	case "hex":
		return hex.EncodeToString(b), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "base64url":
		return base64.URLEncoding.EncodeToString(b), nil
	default:
		return "", errUnknownEncoding(enc)
	}
}

type unknownEncoding string

func (u unknownEncoding) Error() string { return "unknown encoding: " + string(u) }

var errUnknownEncoding = func(enc string) error { return unknownEncoding(enc) }

var errTruncatedUTF16 = errUnknownEncoding("truncated utf16le")

// arrayBuffer extracts the bytes behind a Buffer/ArrayBuffer/TypedArray argument.
func (rt *Runtime) arrayBuffer(v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	switch t := v.Export().(type) {
	case goja.ArrayBuffer:
		return t.Bytes()
	case []byte:
		return t
	}
	if o, ok := v.(*goja.Object); ok {
		if buf := o.Get("buffer"); buf != nil && !goja.IsUndefined(buf) {
			if ab, ok := buf.Export().(goja.ArrayBuffer); ok {
				off := int64(0)
				if b := o.Get("byteOffset"); b != nil && !goja.IsUndefined(b) {
					off = b.ToInteger()
				}
				length := int64(len(ab.Bytes()))
				if l := o.Get("byteLength"); l != nil && !goja.IsUndefined(l) {
					length = l.ToInteger()
				}
				if off < 0 {
					off = 0
				}
				if off+length > int64(len(ab.Bytes())) {
					length = int64(len(ab.Bytes())) - off
				}
				if length <= 0 {
					return nil
				}
				return ab.Bytes()[off : off+length]
			}
		}
	}
	return nil
}

// utf8Valid reports whether b is valid UTF-8 (used by Buffer.toString).
func utf8Valid(b []byte) bool { return utf8.Valid(b) }
