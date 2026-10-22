package vm

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sync"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerCrypto() {
	mod := rt.vm.NewObject()

	hashes := rt.vm.NewObject()
	hashes.Set("md5", rt.newHashOp("md5"))
	hashes.Set("sha1", rt.newHashOp("sha1"))
	hashes.Set("sha256", rt.newHashOp("sha256"))
	hashes.Set("sha512", rt.newHashOp("sha512"))
	hashes.Set("sha384", rt.newHashOp("sha384"))
	hashes.Set("sha224", rt.newHashOp("sha224"))
	mod.Set("hashes", hashes)

	hmacs := rt.vm.NewObject()
	hmacs.Set("md5", rt.newHmacOp("md5"))
	hmacs.Set("sha1", rt.newHmacOp("sha1"))
	hmacs.Set("sha256", rt.newHmacOp("sha256"))
	hmacs.Set("sha512", rt.newHmacOp("sha512"))
	hmacs.Set("sha384", rt.newHmacOp("sha384"))
	hmacs.Set("sha224", rt.newHmacOp("sha224"))
	mod.Set("hmacs", hmacs)

	mod.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		n := int(call.Argument(0).ToInteger())
		if n < 0 {
			panic(rt.vm.NewGoError(errors.New("size must be non-negative")))
		}
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.newUint8Array(b)
	})

	mod.Set("randomUUID", func(call goja.FunctionCall) goja.Value {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			panic(rt.vm.NewGoError(err))
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return rt.vm.ToValue(formatUUID(b))
	})

	mod.Set("timingSafeEqual", func(call goja.FunctionCall) goja.Value {
		a := rt.argBytes([]goja.Value{call.Argument(0)}, 0)
		b := rt.argBytes([]goja.Value{call.Argument(1)}, 0)
		return rt.vm.ToValue(hmac.Equal(a, b))
	})

	mod.Set("getHashes", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue([]string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512"})
	})

	mod.Set("pbkdf2", rt.pbkdf2Func())
	mod.Set("pbkdf2Sync", func(call goja.FunctionCall) goja.Value {
		password := rt.argBytes([]goja.Value{call.Argument(0)}, 0)
		salt := rt.argBytes([]goja.Value{call.Argument(1)}, 0)
		iterations := int(call.Argument(2).ToInteger())
		keylen := int(call.Argument(3).ToInteger())
		digest := "sha1"
		if len(call.Arguments) > 4 && !goja.IsUndefined(call.Argument(4)) {
			digest = call.Argument(4).String()
		}
		out := make([]byte, keylen)
		if err := pbkdf2Key(password, salt, iterations, keylen, digest, out); err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.toJS(binResult(out))
	})
	mod.Set("randomInt", rt.randomIntFunc())

	// crypto.subtle: expose the Go standard library implementations.
	sub := rt.vm.NewObject()
	sub.Set("digest", func(call goja.FunctionCall) goja.Value {
		algo := call.Argument(0).String()
		data := rt.argBytes([]goja.Value{call.Argument(1)}, 0)
		p := rt.subtlePromise(func() ([]byte, error) {
			switch algo {
			case "SHA-1", "SHA1":
				s := sha1.Sum(data)
				return s[:], nil
			case "SHA-256", "SHA256":
				s := sha256.Sum256(data)
				return s[:], nil
			case "SHA-384", "SHA384":
				s := sha512.Sum384(data)
				return s[:], nil
			case "SHA-512", "SHA512":
				s := sha512.Sum512(data)
				return s[:], nil
			}
			return nil, errors.New("unsupported digest algorithm: " + algo)
		})
		return p
	})
	sub.Set("importKey", func(call goja.FunctionCall) goja.Value {
		// Only raw-key imports are supported.
		return rt.vm.ToValue(map[string]any{"type": "secret"})
	})
	subtleCrypto := mod
	subtleCrypto.Set("subtle", sub)
	rt.natives["__crypto"] = mod
}

func (rt *Runtime) subtlePromise(fn func() ([]byte, error)) goja.Value {
	return rt.goPromise(func() (any, error) {
		b, err := fn()
		if err != nil {
			return nil, err
		}
		return binResult(b), nil
	})
}

func formatUUID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// hashOp holds state for a streaming hash.
type hashOp struct {
	mu sync.Mutex
	h  hash.Hash
}

func (rt *Runtime) newHashOp(algo string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		h, err := newHash(algo)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		op := &hashOp{h: h}
		obj := rt.vm.NewObject()
		obj.Set("update", func(c goja.FunctionCall) goja.Value {
			data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
			op.mu.Lock()
			op.h.Write(data)
			op.mu.Unlock()
			return obj
		})
		obj.Set("digest", func(c goja.FunctionCall) goja.Value {
			enc := "hex"
			if len(c.Arguments) > 0 && !goja.IsUndefined(c.Argument(0)) {
				enc = c.Argument(0).String()
			}
			op.mu.Lock()
			sum := op.h.Sum(nil)
			op.mu.Unlock()
			return rt.toJS(encodeDigest(sum, enc))
		})
		return obj
	}
}

func (rt *Runtime) newHmacOp(algo string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		key := rt.argBytes([]goja.Value{call.Argument(0)}, 0)
		newH, err := newHashFunc(algo)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		mac := hmac.New(newH, key)
		obj := rt.vm.NewObject()
		obj.Set("update", func(c goja.FunctionCall) goja.Value {
			data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
			mac.Write(data)
			return obj
		})
		obj.Set("digest", func(c goja.FunctionCall) goja.Value {
			enc := "hex"
			if len(c.Arguments) > 0 && !goja.IsUndefined(c.Argument(0)) {
				enc = c.Argument(0).String()
			}
			return rt.toJS(encodeDigest(mac.Sum(nil), enc))
		})
		return obj
	}
}

func newHash(algo string) (hash.Hash, error) {
	newH, err := newHashFunc(algo)
	if err != nil {
		return nil, err
	}
	return newH(), nil
}

// newHashFunc returns the constructor for a hash algorithm.
func newHashFunc(algo string) (func() hash.Hash, error) {
	switch algo {
	case "md5":
		return md5.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	case "sha384":
		return sha512.New384, nil
	case "sha512":
		return sha512.New, nil
	case "sha224":
		return sha256.New224, nil
	}
	return nil, errors.New("unsupported hash algorithm: " + algo)
}

func encodeDigest(sum []byte, enc string) any {
	switch enc {
	case "base64":
		return base64.StdEncoding.EncodeToString(sum)
	case "base64url":
		return base64.URLEncoding.EncodeToString(sum)
	default:
		return hex.EncodeToString(sum)
	}
}

func (rt *Runtime) pbkdf2Func() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		password := rt.argBytes([]goja.Value{call.Argument(0)}, 0)
		salt := rt.argBytes([]goja.Value{call.Argument(1)}, 0)
		iterations := int(call.Argument(2).ToInteger())
		keylen := int(call.Argument(3).ToInteger())
		digest := "sha1"
		if len(call.Arguments) > 4 && !goja.IsUndefined(call.Argument(4)) {
			digest = call.Argument(4).String()
		}
		return rt.goPromise(func() (any, error) {
			out := make([]byte, keylen)
			if err := pbkdf2Key(password, salt, iterations, keylen, digest, out); err != nil {
				return nil, err
			}
			return binResult(out), nil
		})
	}
}

func (rt *Runtime) randomIntFunc() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		min := int64(0)
		max := int64(0)
		switch len(call.Arguments) {
		case 1:
			max = call.Argument(0).ToInteger()
		case 2:
			min = call.Argument(0).ToInteger()
			max = call.Argument(1).ToInteger()
		}
		if max <= min {
			panic(rt.vm.NewGoError(errors.New("max must be greater than min")))
		}
		span := uint64(max - min)
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.vm.ToValue(min + int64(uint64(binaryLE(b[:]))%span))
	}
}

func binaryLE(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

var _ = io.Discard
