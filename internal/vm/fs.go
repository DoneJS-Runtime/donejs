package vm

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"donejs/internal/jit"
)

func (rt *Runtime) registerFS() {
	mod := jit.Object()

	sync := opTable{
		"readFile": func(args []jit.Value) (any, error) {
			b, err := os.ReadFile(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			return binResult(b), nil
		},
		"writeFile": func(args []jit.Value) (any, error) {
			data := rt.argBytes(args, 1)
			return nil, os.WriteFile(argStr(args, 0), data, 0o644)
		},
		"appendFile": func(args []jit.Value) (any, error) {
			data := rt.argBytes(args, 1)
			f, err := os.OpenFile(argStr(args, 0), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			_, err = f.Write(data)
			return nil, err
		},
		"exists": func(args []jit.Value) (any, error) {
			_, err := os.Stat(argStr(args, 0))
			return err == nil, nil
		},
		"stat": func(args []jit.Value) (any, error) {
			st, err := os.Stat(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			return rt.fileStat(st), nil
		},
		"lstat": func(args []jit.Value) (any, error) {
			st, err := os.Lstat(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			return rt.fileStat(st), nil
		},
		"readdir": func(args []jit.Value) (any, error) {
			entries, err := os.ReadDir(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			return names, nil
		},
		"mkdir": func(args []jit.Value) (any, error) {
			opts := argObj(args, 1)
			mode := os.FileMode(optInt(opts, "mode", 0o777))
			if optBool(opts, "recursive", false) {
				return nil, os.MkdirAll(argStr(args, 0), mode)
			}
			return nil, os.Mkdir(argStr(args, 0), mode)
		},
		"rmdir": func(args []jit.Value) (any, error) {
			return nil, os.Remove(argStr(args, 0))
		},
		"rm": func(args []jit.Value) (any, error) {
			opts := argObj(args, 1)
			p := argStr(args, 0)
			if optBool(opts, "recursive", false) {
				return nil, os.RemoveAll(p)
			}
			err := os.Remove(p)
			if err != nil && optBool(opts, "force", false) && os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		},
		"unlink": func(args []jit.Value) (any, error) {
			return nil, os.Remove(argStr(args, 0))
		},
		"rename": func(args []jit.Value) (any, error) {
			return nil, os.Rename(argStr(args, 0), argStr(args, 1))
		},
		"copyFile": func(args []jit.Value) (any, error) {
			return nil, copyFile(argStr(args, 0), argStr(args, 1))
		},
		"realpath": func(args []jit.Value) (any, error) {
			p, err := filepath.EvalSymlinks(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			return p, nil
		},
		"access": func(args []jit.Value) (any, error) {
			_, err := os.Stat(argStr(args, 0))
			return nil, err
		},
		"chmod": func(args []jit.Value) (any, error) {
			return nil, os.Chmod(argStr(args, 0), os.FileMode(argInt(args, 1)))
		},
		"truncate": func(args []jit.Value) (any, error) {
			return nil, os.Truncate(argStr(args, 0), int64(argInt(args, 1)))
		},
		"readdirEntries": func(args []jit.Value) (any, error) {
			entries, err := os.ReadDir(argStr(args, 0))
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(entries))
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				out = append(out, map[string]any{
					"name":        e.Name(),
					"isFile":      func() bool { return info.Mode().IsRegular() },
					"isDirectory": func() bool { return e.IsDir() },
					"isSymbolicLink": func() bool {
						return e.Type()&os.ModeSymlink != 0
					},
				})
			}
			return out, nil
		},
		"mkdtemp": func(args []jit.Value) (any, error) {
			return os.MkdirTemp("", filepath.Base(argStr(args, 0)))
		},
		"readlink": func(args []jit.Value) (any, error) {
			return os.Readlink(argStr(args, 0))
		},
		"symlink": func(args []jit.Value) (any, error) {
			return nil, os.Symlink(argStr(args, 0), argStr(args, 1))
		},
		"open": func(args []jit.Value) (any, error) {
			opts := argObj(args, 1)
			flag := os.O_RDONLY
			switch optStr(opts, "flag", "r") {
			case "r+":
				flag = os.O_RDWR
			case "w":
				flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			case "w+":
				flag = os.O_RDWR | os.O_CREATE | os.O_TRUNC
			case "a":
				flag = os.O_APPEND | os.O_CREATE | os.O_WRONLY
			case "a+":
				flag = os.O_APPEND | os.O_CREATE | os.O_RDWR
			}
			f, err := os.OpenFile(argStr(args, 0), flag, 0o644)
			if err != nil {
				return nil, err
			}
			return rt.registerFD(f), nil
		},
		"read": func(args []jit.Value) (any, error) {
			f := rt.lookupFD(argInt(args, 0))
			if f == nil {
				return nil, os.ErrInvalid
			}
			buf := make([]byte, argInt(args, 1))
			n, err := f.Read(buf)
			if n > 0 {
				return pair{b: binResult(buf[:n])}, err
			}
			return pair{}, err
		},
		"write": func(args []jit.Value) (any, error) {
			f := rt.lookupFD(argInt(args, 0))
			if f == nil {
				return nil, os.ErrInvalid
			}
			data := rt.argBytes(args, 1)
			n, err := f.Write(data)
			return pair{b: int64(n)}, err
		},
		"close": func(args []jit.Value) (any, error) {
			f := rt.lookupFD(argInt(args, 0))
			if f == nil {
				return nil, nil
			}
			return nil, f.Close()
		},
		"statFd": func(args []jit.Value) (any, error) {
			f := rt.lookupFD(argInt(args, 0))
			if f == nil {
				return nil, os.ErrInvalid
			}
			st, err := f.Stat()
			if err != nil {
				return nil, err
			}
			return rt.fileStat(st), nil
		},
		"createReadStream": func(args []jit.Value) (any, error) {
			return nil, nil
		},
	}

	mod.Set("sync", rt.syncDispatch(sync))
	mod.Set("async", rt.asyncDispatch(sync))

	consts := jit.Object()
	consts.Set("F_OK", 0)
	consts.Set("R_OK", 4)
	consts.Set("W_OK", 2)
	consts.Set("X_OK", 1)
	mod.Set("constants", consts)

	rt.natives["__fs"] = mod
}

// pair carries two result values (used by read/write fd ops).
type pair struct {
	a any
	b any
}

func (rt *Runtime) argBytes(args []jit.Value, i int) []byte {
	v := argsAt(args, i)
	if v.IsUndef() || v.IsNull() {
		return nil
	}
	// Binary views short-circuit straight to their backing bytes; the generic
	// path would run Export (allocating a map) just to throw it away.
	if o := v.AsObj(); o != nil {
		switch jit.KindOf(o) {
		case jit.KindArrayBuffer, jit.KindUint8Array, jit.KindDataView:
			return o.Bytes()
		}
	}
	if s, ok := v.Export().(string); ok {
		return []byte(s)
	}
	return rt.arrayBuffer(v)
}

// --- file descriptor table ---------------------------------------------

func (rt *Runtime) registerFD(f *os.File) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.fdSeq++
	id := rt.fdSeq
	if rt.fds == nil {
		rt.fds = map[int]*os.File{}
	}
	rt.fds[id] = f
	return id
}

func (rt *Runtime) lookupFD(id int) *os.File {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.fds[id]
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return &os.PathError{Op: "copyfile", Path: src, Err: errIsDir}
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const errIsDir = simpleErr("illegal operation on a directory")

// utimes is used by fs.utimes; kept for API completeness.
func utimes(p string, atime, mtime time.Time) error {
	return os.Chtimes(p, atime, mtime)
}
