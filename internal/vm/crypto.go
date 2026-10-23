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
	"sync"

	"donejs/internal/jit"
)

func (rt *Runtime) registerCrypto() {
	mod := jit.Object()

	hashes := jit.Object()
	setFn(hashes, "md5", rt.newHashOp("md5"))
	setFn(hashes, "sha1", rt.newHashOp("sha1"))
	setFn(hashes, "sha256", rt.newHashOp("sha256"))
	setFn(hashes, "sha512", rt.newHashOp("sha512"))
	setFn(hashes, "sha384", rt.newHashOp("sha384"))
	setFn(hashes, "sha224", rt.newHashOp("sha224"))
	mod.Set("hashes", hashes)

	hmacs := jit.Object()
	setFn(hmacs, "md5", rt.newHmacOp("md5"))
	setFn(hmacs, "sha1", rt.newHmacOp("sha1"))
	setFn(hmacs, "sha256", rt.newHmacOp("sha256"))
	setFn(hmacs, "sha512", rt.newHmacOp("sha512"))
	setFn(hmacs, "sha384", rt.newHmacOp("sha384"))
	setFn(hmacs, "sha224", rt.newHmacOp("sha224"))
	mod.Set("hmacs", hmacs)

	setFn(mod, "randomBytes", func(args []jit.Value) jit.Value {
		n := int(argInt(args, 0))
		if n < 0 {
			panic(rt.jsThrow(errors.New("size must be non-negative")))
		}
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			panic(rt.jsThrow(err))
		}
		return rt.newUint8Array(b)
	})

	setFn(mod, "randomUUID", func(args []jit.Value) jit.Value {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			panic(rt.jsThrow(err))
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return rt.toJS(formatUUID(b))
	})

	setFn(mod, "timingSafeEqual", func(args []jit.Value) jit.Value {
		a := rt.argBytes(args, 0)
		b := rt.argBytes(args, 1)
		return rt.toJS(hmac.Equal(a, b))
	})

	setFn(mod, "getHashes", func(args []jit.Value) jit.Value {
		return rt.toJS([]string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512"})
	})

	setFn(mod, "pbkdf2", rt.pbkdf2Func())
	setFn(mod, "pbkdf2Sync", func(args []jit.Value) jit.Value {
		password := rt.argBytes(args, 0)
		salt := rt.argBytes(args, 1)
		iterations := int(argInt(args, 2))
		keylen := int(argInt(args, 3))
		digest := "sha1"
		if len(args) > 4 && !args[4].IsUndef() {
			digest = args[4].String()
		}
		out := make([]byte, keylen)
		if err := pbkdf2Key(password, salt, iterations, keylen, digest, out); err != nil {
			panic(rt.jsThrow(err))
		}
		return rt.toJS(binResult(out))
	})
	setFn(mod, "randomInt", rt.randomIntFunc())

	sub := jit.Object()
	setFn(sub, "digest", func(args []jit.Value) jit.Value {
		algo := argStr(args, 0)
		data := rt.argBytes(args, 1)
		return rt.subtlePromise(func() ([]byte, error) {
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
	})
	setFn(sub, "importKey", func(args []jit.Value) jit.Value {
		return rt.toJS(map[string]any{"type": "secret"})
	})
	mod.Set("subtle", sub)
	rt.natives["__crypto"] = mod
}

func (rt *Runtime) subtlePromise(fn func() ([]byte, error)) jit.Value {
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

func (rt *Runtime) newHashOp(algo string) func(args []jit.Value) jit.Value {
	return func(args []jit.Value) jit.Value {
		h, err := newHash(algo)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		op := &hashOp{h: h}
		obj := jit.Object()
		setFn(obj, "update", func(args []jit.Value) jit.Value {
			data := rt.argBytes(args, 0)
			op.mu.Lock()
			op.h.Write(data)
			op.mu.Unlock()
			return obj
		})
		setFn(obj, "digest", func(args []jit.Value) jit.Value {
			enc := "hex"
			if len(args) > 0 && !args[0].IsUndef() {
				enc = args[0].String()
			}
			op.mu.Lock()
			sum := op.h.Sum(nil)
			op.mu.Unlock()
			return rt.toJS(encodeDigest(sum, enc))
		})
		return obj
	}
}

func (rt *Runtime) newHmacOp(algo string) func(args []jit.Value) jit.Value {
	return func(args []jit.Value) jit.Value {
		key := rt.argBytes(args, 0)
		newH, err := newHashFunc(algo)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		mac := hmac.New(newH, key)
		obj := jit.Object()
		setFn(obj, "update", func(args []jit.Value) jit.Value {
			data := rt.argBytes(args, 0)
			mac.Write(data)
			return obj
		})
		setFn(obj, "digest", func(args []jit.Value) jit.Value {
			enc := "hex"
			if len(args) > 0 && !args[0].IsUndef() {
				enc = args[0].String()
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

func (rt *Runtime) pbkdf2Func() func(args []jit.Value) jit.Value {
	return func(args []jit.Value) jit.Value {
		password := rt.argBytes(args, 0)
		salt := rt.argBytes(args, 1)
		iterations := int(argInt(args, 2))
		keylen := int(argInt(args, 3))
		digest := "sha1"
		if len(args) > 4 && !args[4].IsUndef() {
			digest = args[4].String()
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

func (rt *Runtime) randomIntFunc() func(args []jit.Value) jit.Value {
	return func(args []jit.Value) jit.Value {
		min := int64(0)
		max := int64(0)
		switch len(args) {
		case 1:
			max = args[0].ToInteger()
		case 2:
			min = args[0].ToInteger()
			max = args[1].ToInteger()
		}
		if max <= min {
			panic(rt.jsThrow(errors.New("max must be greater than min")))
		}
		span := uint64(max - min)
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(rt.jsThrow(err))
		}
		return rt.toJS(min + int64(uint64(binaryLE(b[:]))%span))
	}
}

func binaryLE(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}
