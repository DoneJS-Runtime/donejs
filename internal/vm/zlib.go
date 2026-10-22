package vm

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"hash"
	"io"

	"github.com/dop251/goja"
)

func md5New() hash.Hash { return md5.New() }

// pbkdf2Key implements PBKDF2 (RFC 2898) on top of crypto/hmac.
func pbkdf2Key(password, salt []byte, iter, keyLen int, digest string, out []byte) error {
	var h func() hash.Hash
	switch digest {
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	case "sha512":
		h = sha512.New
	case "sha224":
		h = sha256.New224
	case "md5":
		h = md5.New
	default:
		return errors.New("unsupported digest: " + digest)
	}
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		copy(out[(block-1)*hashLen:], t)
	}
	return nil
}

func (rt *Runtime) registerZlib() {
	mod := rt.vm.NewObject()

	sync := opTable{
		"gzip": func(a []goja.Value) (any, error) {
			var out bytes.Buffer
			w := gzip.NewWriter(&out)
			if _, err := w.Write(rt.argBytes(a, 0)); err != nil {
				return nil, err
			}
			if err := w.Close(); err != nil {
				return nil, err
			}
			return binResult(out.Bytes()), nil
		},
		"gunzip": func(a []goja.Value) (any, error) {
			r, err := gzip.NewReader(bytes.NewReader(rt.argBytes(a, 0)))
			if err != nil {
				return nil, err
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			return binResult(b), nil
		},
		"deflate": func(a []goja.Value) (any, error) {
			var out bytes.Buffer
			w, err := zlib.NewWriterLevel(&out, zlib.DefaultCompression)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(rt.argBytes(a, 0)); err != nil {
				return nil, err
			}
			if err := w.Close(); err != nil {
				return nil, err
			}
			return binResult(out.Bytes()), nil
		},
		"inflate": func(a []goja.Value) (any, error) {
			r, err := zlib.NewReader(bytes.NewReader(rt.argBytes(a, 0)))
			if err != nil {
				return nil, err
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			return binResult(b), nil
		},
		"deflateRaw": func(a []goja.Value) (any, error) {
			var out bytes.Buffer
			w, err := flate.NewWriter(&out, flate.DefaultCompression)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(rt.argBytes(a, 0)); err != nil {
				return nil, err
			}
			if err := w.Close(); err != nil {
				return nil, err
			}
			return binResult(out.Bytes()), nil
		},
		"inflateRaw": func(a []goja.Value) (any, error) {
			r := flate.NewReader(bytes.NewReader(rt.argBytes(a, 0)))
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			return binResult(b), nil
		},
		"crc32": func(a []goja.Value) (any, error) {
			return int64(crc32IEEE(rt.argBytes(a, 0))), nil
		},
	}

	mod.Set("sync", rt.syncDispatch(sync))
	mod.Set("async", rt.asyncDispatch(sync))
	rt.natives["__zlib"] = mod
}

func crc32IEEE(b []byte) uint32 {
	return crcTableSum(b)
}

var crcTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		c := uint32(i)
		for k := 0; k < 8; k++ {
			if c&1 != 0 {
				c = 0xEDB88320 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}()

func crcTableSum(b []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, x := range b {
		crc = crcTable[(crc^uint32(x))&0xFF] ^ (crc >> 8)
	}
	return crc ^ 0xFFFFFFFF
}

var (
	_ = binary.BigEndian
	_ = hmac.New
)
