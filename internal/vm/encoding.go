package vm

import (
	"encoding/base64"
	"encoding/hex"
	"unicode/utf16"
	"unicode/utf8"

	"donejs/internal/jit"
)

// registerEncoding exposes byte/string conversions used by Buffer, fs and crypto.
func (rt *Runtime) registerEncoding() {
	enc := jit.Object()

	setFn(enc, "toBytes", func(args []jit.Value) jit.Value {
		s := argsAt(args, 0).String()
		encName := "utf8"
		if len(args) > 1 && !args[1].IsUndef() {
			encName = args[1].String()
		}
		b, err := encodeString(s, encName)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return rt.newUint8Array(b)
	})

	setFn(enc, "fromBytes", func(args []jit.Value) jit.Value {
		ab := rt.arrayBuffer(argsAt(args, 0))
		encName := "utf8"
		if len(args) > 1 && !args[1].IsUndef() {
			encName = args[1].String()
		}
		s, err := decodeBytes(ab, encName)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return jit.Str(s)
	})

	setFn(enc, "byteLength", func(args []jit.Value) jit.Value {
		s := argsAt(args, 0).String()
		encName := "utf8"
		if len(args) > 1 && !args[1].IsUndef() {
			encName = args[1].String()
		}
		b, err := encodeString(s, encName)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return jit.Num(float64(len(b)))
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
		return string(b), nil
	case "ascii":
		sb := make([]byte, len(b))
		for i, c := range b {
			if c > 0x7f {
				c = '?'
			}
			sb[i] = c
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

func errUnknownEncoding(enc string) error { return unknownEncoding(enc) }

var errTruncatedUTF16 = errUnknownEncoding("truncated utf16le")

// arrayBuffer extracts the bytes behind a Buffer/ArrayBuffer/TypedArray argument.
func (rt *Runtime) arrayBuffer(v jit.Value) []byte {
	if v.IsUndef() || v.IsNull() {
		return nil
	}
	if o := v.AsObj(); o != nil {
		switch jit.KindOf(o) {
		case jit.KindArrayBuffer, jit.KindUint8Array, jit.KindDataView:
			return o.Bytes()
		}
	}
	switch t := v.Export().(type) {
	case string:
		return []byte(t)
	case []byte:
		return t
	}
	return nil
}

// utf8Valid reports whether b is valid UTF-8 (used by Buffer.toString).
func utf8Valid(b []byte) bool { return utf8.Valid(b) }
