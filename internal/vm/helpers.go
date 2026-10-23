package vm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"donejs/internal/jit"
)

// binResult marks byte payloads that must be handed to JS as an ArrayBuffer.
type binResult []byte

// toJS converts a Go result into a JS value on the JS thread.
func (rt *Runtime) toJS(v any) jit.Value {
	switch t := v.(type) {
	case nil:
		return jit.Null()
	case jit.Value:
		return t
	case binResult:
		return jit.ArrayBufferValue([]byte(t))
	case []byte:
		return jit.ArrayBufferValue(t)
	default:
		return jit.FromGo(v)
	}
}

func printlnStderr(s string) {
	os.Stderr.WriteString(s + "\n")
}

// splitCB returns the trailing callback argument when it is a function.
func splitCB(args []jit.Value) (jit.Value, bool) {
	if len(args) == 0 {
		return jit.Undef(), false
	}
	last := args[len(args)-1]
	if last.IsCallable() {
		return last, true
	}
	return jit.Undef(), false
}

// encodingFromOptions extracts a string encoding from fs-style options.
func encodingFromOptions(args []jit.Value, argIdx int) string {
	if len(args) <= argIdx {
		return ""
	}
	v := argsAt(args, argIdx)
	if s, ok := v.Export().(string); ok {
		return s
	}
	if o := argObj(args, argIdx); !o.IsUndef() && !o.Get("encoding").IsUndef() {
		return o.Get("encoding").String()
	}
	return ""
}

// jsThrow converts a Go error into a JS ThrowError carrying name, message and
// Node-style error properties.
func (rt *Runtime) jsThrow(err error) *jit.ThrowError {
	if err == nil {
		return nil
	}
	code := ""
	sysName := ""
	pth := ""
	var errno syscall.Errno
	var msg string

	var pe *fs.PathError
	var le *os.LinkError
	var se *os.SyscallError
	switch {
	case errors.As(err, &pe):
		errno = asErrno(pe.Err)
		sysName = pe.Op
		pth = pe.Path
	case errors.As(err, &le):
		errno = asErrno(le.Err)
		sysName = le.Op
		pth = le.New
	case errors.As(err, &se):
		errno = asErrno(se.Err)
		sysName = se.Syscall
	}
	if errno != 0 {
		code = errnoName(errno)
		switch {
		case pth != "" && sysName != "":
			msg = fmt.Sprintf("%s: %s, %s '%s'", code, errno.Error(), sysName, pth)
		case sysName != "":
			msg = fmt.Sprintf("%s: %s, %s", code, errno.Error(), sysName)
		default:
			msg = fmt.Sprintf("%s: %s", code, errno.Error())
		}
	} else {
		msg = err.Error()
	}

	ev := jit.ErrObj("Error", msg)
	ev.Set("errno", int64(errno))
	if code != "" {
		ev.Set("code", code)
	}
	if sysName != "" {
		ev.Set("syscall", sysName)
	}
	if pth != "" {
		ev.Set("path", pth)
	}
	return &jit.ThrowError{Value: ev}
}

func asErrno(err error) syscall.Errno {
	var e syscall.Errno
	if errors.As(err, &e) {
		return e
	}
	return 0
}

func errnoName(e syscall.Errno) string {
	switch e {
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EEXIST:
		return "EEXIST"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.EISDIR:
		return "EISDIR"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.EBADF:
		return "EBADF"
	case syscall.EBUSY:
		return "EBUSY"
	case syscall.EMFILE:
		return "EMFILE"
	case syscall.ENFILE:
		return "ENFILE"
	case syscall.ELOOP:
		return "ELOOP"
	case syscall.ENAMETOOLONG:
		return "ENAMETOOLONG"
	case syscall.ENOTEMPTY:
		return "ENOTEMPTY"
	case syscall.EPIPE:
		return "EPIPE"
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.EADDRINUSE:
		return "EADDRINUSE"
	case syscall.EINVAL:
		return "EINVAL"
	case syscall.ERANGE:
		return "ERANGE"
	case syscall.ESPIPE:
		return "ESPIPE"
	case syscall.EXDEV:
		return "EXDEV"
	case syscall.EMLINK:
		return "EMLINK"
	case syscall.EAGAIN:
		return "EAGAIN"
	case syscall.ENOSPC:
		return "ENOSPC"
	case syscall.EFBIG:
		return "EFBIG"
	case syscall.ENODATA:
		return "ENODATA"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ENETUNREACH:
		return "ENETUNREACH"
	case syscall.ENOTCONN:
		return "ENOTCONN"
	case syscall.EROFS:
		return "EROFS"
	default:
		return "EUNKNOWN"
	}
}

// posixMode converts a Go FileMode into POSIX st_mode bits.
func posixMode(m os.FileMode) uint32 {
	var typ uint32
	switch {
	case m&os.ModeDir != 0:
		typ = 0040000
	case m&os.ModeSymlink != 0:
		typ = 0120000
	case m&os.ModeNamedPipe != 0:
		typ = 0010000
	case m&os.ModeSocket != 0:
		typ = 0140000
	case m&os.ModeDevice != 0:
		if m&os.ModeCharDevice != 0 {
			typ = 0020000
		} else {
			typ = 0060000
		}
	default:
		typ = 0100000
	}
	return typ | uint32(m.Perm())
}

// fileStat builds a Node-compatible Stats object.
func (rt *Runtime) fileStat(st os.FileInfo) map[string]any {
	mtime := st.ModTime()
	mode := posixMode(st.Mode())
	isFile := st.Mode().IsRegular()
	isDir := st.Mode().IsDir()
	isLink := st.Mode()&os.ModeSymlink != 0
	return map[string]any{
		"dev":               int64(0),
		"mode":              mode,
		"nlink":             int64(1),
		"uid":               int64(0),
		"gid":               int64(0),
		"rdev":              int64(0),
		"size":              st.Size(),
		"blksize":           int64(4096),
		"blocks":            (st.Size() + 511) / 512,
		"atimeMs":           float64(mtime.UnixMilli()),
		"mtimeMs":           float64(mtime.UnixMilli()),
		"ctimeMs":           float64(mtime.UnixMilli()),
		"birthtimeMs":       float64(mtime.UnixMilli()),
		"atime":             mtime,
		"mtime":             mtime,
		"ctime":             mtime,
		"birthtime":         mtime,
		"isFile":            func() bool { return isFile },
		"isDirectory":       func() bool { return isDir },
		"isSymbolicLink":    func() bool { return isLink },
		"isBlockDevice":     func() bool { return false },
		"isCharacterDevice": func() bool { return st.Mode()&os.ModeCharDevice != 0 },
		"isFIFO":            func() bool { return st.Mode()&os.ModeNamedPipe != 0 },
		"isSocket":          func() bool { return st.Mode()&os.ModeSocket != 0 },
	}
}

// --- callback registry -------------------------------------------------

func (rt *Runtime) regCB(cb jit.Value) uint64 {
	if !cb.IsCallable() {
		return 0
	}
	rt.mu.Lock()
	rt.nextCB++
	id := rt.nextCB
	rt.cbs[id] = cb
	rt.mu.Unlock()
	return id
}

func (rt *Runtime) takeCB(id uint64) jit.Value {
	rt.mu.Lock()
	f := rt.cbs[id]
	delete(rt.cbs, id)
	rt.mu.Unlock()
	return f
}

func (rt *Runtime) invokeCB(id uint64, args ...jit.Value) {
	f := rt.takeCB(id)
	if !f.IsCallable() {
		return
	}
	if _, err := rt.jit.CallBind(f, jit.Undef(), args...); err != nil {
		rt.reportUncaught(err)
	}
}

// asyncOp runs work off-thread and delivers (err, result) to cb on the JS thread.
// It keeps the process alive until the callback is delivered.
func (rt *Runtime) asyncOp(cb jit.Value, work func() (any, error)) {
	id := rt.regCB(cb)
	if id == 0 {
		panic(jit.Err("TypeError", "callback required"))
	}
	rt.pending++
	go func() {
		v, err := work()
		rt.Enqueue(func() {
			rt.pending--
			if err != nil {
				rt.invokeCB(id, rt.jsThrow(err).Value)
			} else {
				rt.invokeCB(id, rt.toJS(v))
			}
		})
	}()
}

// goPromise runs work off-thread and settles a JS Promise.
func (rt *Runtime) goPromise(work func() (any, error)) jit.Value {
	p, resolve, reject := rt.jit.NewPromise()
	rt.pending++
	go func() {
		v, err := work()
		rt.Enqueue(func() {
			rt.pending--
			if err != nil {
				reject(rt.jsThrow(err).Value)
			} else {
				resolve(rt.toJS(v))
			}
		})
	}()
	return jit.PromiseVal(p)
}
