package vm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/dop251/goja"
)

// binResult marks byte payloads that must be handed to JS as an ArrayBuffer.
type binResult []byte

// toJS converts a Go result into a JS value on the JS thread.
func (rt *Runtime) toJS(v any) goja.Value {
	switch t := v.(type) {
	case nil:
		return goja.Null()
	case goja.Value:
		return t
	case binResult:
		return rt.vm.ToValue(rt.vm.NewArrayBuffer([]byte(t)))
	case []byte:
		return rt.vm.ToValue(rt.vm.NewArrayBuffer(t))
	default:
		return rt.vm.ToValue(v)
	}
}

func printlnStderr(s string) {
	os.Stderr.WriteString(s + "\n")
}

// splitCB returns the trailing callback argument when it is a function.
func splitCB(call goja.FunctionCall) (goja.Value, bool) {
	n := len(call.Arguments)
	if n == 0 {
		return nil, false
	}
	last := call.Argument(n - 1)
	if _, ok := goja.AssertFunction(last); ok {
		return last, true
	}
	return nil, false
}

// encodingFromOptions extracts a string encoding from fs-style options.
func encodingFromOptions(call goja.FunctionCall, argIdx int) string {
	if len(call.Arguments) <= argIdx {
		return ""
	}
	v := call.Argument(argIdx)
	if s, ok := v.Export().(string); ok {
		return s
	}
	if o, ok := v.(*goja.Object); ok {
		if e := o.Get("encoding"); e != nil && !goja.IsUndefined(e) && !goja.IsNull(e) {
			return e.String()
		}
	}
	return ""
}

// jsError converts a Go error into a Node-style JS Error with code/errno/syscall/path.
func (rt *Runtime) jsError(err error) goja.Value {
	if err == nil {
		return goja.Null()
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

	ex := rt.vm.NewGoError(errors.New(msg))
	ex.Set("errno", int64(errno))
	if code != "" {
		ex.Set("code", code)
	}
	if sysName != "" {
		ex.Set("syscall", sysName)
	}
	if pth != "" {
		ex.Set("path", pth)
	}
	return ex
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
		"dev":         int64(0),
		"mode":        mode,
		"nlink":       int64(1),
		"uid":         int64(0),
		"gid":         int64(0),
		"rdev":        int64(0),
		"size":        st.Size(),
		"blksize":     int64(4096),
		"blocks":      (st.Size() + 511) / 512,
		"atimeMs":     float64(mtime.UnixMilli()),
		"mtimeMs":     float64(mtime.UnixMilli()),
		"ctimeMs":     float64(mtime.UnixMilli()),
		"birthtimeMs": float64(mtime.UnixMilli()),
		"atime":       mtime,
		"mtime":       mtime,
		"ctime":       mtime,
		"birthtime":   mtime,
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

func (rt *Runtime) regCB(cb goja.Value) uint64 {
	f, ok := goja.AssertFunction(cb)
	if !ok {
		return 0
	}
	rt.mu.Lock()
	rt.nextCB++
	id := rt.nextCB
	rt.cbs[id] = f
	rt.mu.Unlock()
	return id
}

func (rt *Runtime) takeCB(id uint64) goja.Callable {
	rt.mu.Lock()
	f := rt.cbs[id]
	delete(rt.cbs, id)
	rt.mu.Unlock()
	return f
}

func (rt *Runtime) invokeCB(id uint64, args ...goja.Value) {
	f := rt.takeCB(id)
	if f == nil {
		return
	}
	if _, err := f(goja.Undefined(), args...); err != nil {
		rt.reportUncaught(err)
	}
}

// asyncOp runs work off-thread and delivers (err, result) to cb on the JS thread.
// It keeps the process alive until the callback is delivered.
func (rt *Runtime) asyncOp(cb goja.Value, work func() (any, error)) {
	id := rt.regCB(cb)
	if id == 0 {
		panic(rt.vm.NewTypeError("callback required"))
	}
	rt.pending++
	go func() {
		v, err := work()
		rt.Enqueue(func() {
			rt.pending--
			if err != nil {
				rt.invokeCB(id, rt.jsError(err))
			} else {
				rt.invokeCB(id, goja.Null(), rt.toJS(v))
			}
		})
	}()
}

// goPromise runs work off-thread and settles a JS Promise.
func (rt *Runtime) goPromise(work func() (any, error)) goja.Value {
	p, resolve, reject := rt.vm.NewPromise()
	rt.pending++
	go func() {
		v, err := work()
		rt.Enqueue(func() {
			rt.pending--
			if err != nil {
				reject(rt.jsError(err))
			} else {
				resolve(rt.toJS(v))
			}
		})
	}()
	return rt.vm.ToValue(p)
}
