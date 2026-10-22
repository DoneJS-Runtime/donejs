package vm

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerProcess() {
	p := rt.vm.NewObject()

	argv := make([]any, 0, len(rt.argv))
	for _, a := range rt.argv {
		argv = append(argv, a)
	}
	p.Set("argv", argv)
	p.Set("argv0", "donejs")
	p.Set("execPath", rt.execPath())
	p.Set("title", "donejs")
	p.Set("platform", runtime.GOOS)
	p.Set("arch", nodeArch(runtime.GOARCH))
	p.Set("version", "v22.0.0-donejs")
	p.Set("pid", os.Getpid())
	p.Set("ppid", os.Getppid())

	env := map[string]any{}
	for _, kv := range os.Environ() {
		if i := strings.Index(kv, "="); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	p.Set("env", env)

	versions := map[string]any{
		"node":     "22.0.0",
		"donejs":   "0.1.0",
		"goja":     "0.0.0",
		"v8":       "",
		"uv":       "",
		"modules":  "26",
		"napi":     "9",
		"acorn":    "8.11.3",
		"openssl":  "3.0.0",
	}
	p.Set("versions", versions)

	p.Set("cwd", func(call goja.FunctionCall) goja.Value {
		d, err := os.Getwd()
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.vm.ToValue(d)
	})
	p.Set("chdir", func(call goja.FunctionCall) goja.Value {
		if err := os.Chdir(call.Argument(0).String()); err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	p.Set("exit", func(call goja.FunctionCall) goja.Value {
		code := 0
		if len(call.Arguments) > 0 {
			code = int(call.Argument(0).ToInteger())
		}
		rt.exit(code)
		return goja.Undefined()
	})
	p.Set("exitCode", goja.Undefined())
	p.Set("nextTick", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(rt.vm.NewTypeError("callback must be a function"))
		}
		var args []goja.Value
		if len(call.Arguments) > 1 {
			args = call.Arguments[1:]
		}
		rt.pushTick(func() {
			if _, err := fn(goja.Undefined(), args...); err != nil {
				rt.reportUncaught(err)
			}
		})
		return goja.Undefined()
	})
	p.Set("hrtime", func(call goja.FunctionCall) goja.Value {
		d := time.Since(rt.start)
		return rt.vm.ToValue([]int64{d.Nanoseconds() / 1e9, int64(d.Nanoseconds() % 1e9)})
	})
	p.Set("uptime", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(nowSeconds() - rt.startSeconds())
	})
	p.Set("memoryUsage", func(call goja.FunctionCall) goja.Value {
		m := memStats()
		return rt.toJS(map[string]any{
			"rss":     int64(m.Sys),
			"heapTotal": int64(m.HeapSys),
			"heapUsed":  int64(m.HeapAlloc),
			"external":  int64(m.Sys - m.HeapSys),
			"arrayBuffers": int64(0),
		})
	})
	p.Set("umask", func(call goja.FunctionCall) goja.Value { return rt.vm.ToValue(0o022) })
	p.Set("kill", func(call goja.FunctionCall) goja.Value {
		pid := int(call.Argument(0).ToInteger())
		sig := "SIGTERM"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			sig = call.Argument(1).String()
		}
		s, err := strconv.Atoi(strings.TrimPrefix(sig, "SIG"))
		if err != nil {
			s = 15
		}
		if pid == os.Getpid() {
			rt.exit(0)
			return rt.vm.ToValue(true)
		}
		if err := syscall.Kill(pid, syscall.Signal(s)); err != nil {
			return rt.vm.ToValue(false)
		}
		return rt.vm.ToValue(true)
	})
	p.Set("emitWarning", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	p.Set("binding", func(call goja.FunctionCall) goja.Value {
		panic(rt.vm.NewGoError(errUnsupported("process.binding")))
	})
	p.Set("dlopen", func(call goja.FunctionCall) goja.Value {
		panic(rt.vm.NewGoError(errUnsupported("process.dlopen")))
	})

	// stdio
	stdout := rt.vm.NewObject()
	stdout.Set("write", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		os.Stdout.WriteString(s)
		if cb, ok := splitCB(call); ok {
			f, _ := goja.AssertFunction(cb)
			f(goja.Undefined(), goja.Null(), rt.vm.ToValue(len(s)))
		}
		return rt.vm.ToValue(true)
	})
	stdout.Set("isTTY", isTTY(os.Stdout))
	stdout.Set("fd", 1)
	stdout.Set("writable", true)
	stdout.Set("end", func(call goja.FunctionCall) goja.Value { return rt.vm.ToValue(true) })
	stderr := rt.vm.NewObject()
	stderr.Set("write", func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		os.Stderr.WriteString(s)
		return rt.vm.ToValue(true)
	})
	stderr.Set("isTTY", isTTY(os.Stderr))
	stderr.Set("fd", 2)
	stderr.Set("writable", true)
	stderr.Set("end", func(call goja.FunctionCall) goja.Value { return rt.vm.ToValue(true) })
	stdin := rt.vm.NewObject()
	stdin.Set("isTTY", isTTY(os.Stdin))
	stdin.Set("fd", 0)
	stdin.Set("read", func(call goja.FunctionCall) goja.Value { return goja.Null() })
	stdin.Set("setEncoding", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	stdin.Set("resume", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	stdin.Set("pause", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	p.Set("stdout", stdout)
	p.Set("stderr", stderr)
	p.Set("stdin", stdin)

	rt.natives["__process"] = p
	rt.processObj = p
}

type unsupportedError string

func (e unsupportedError) Error() string { return string(e) + " is not supported in donejs" }

func errUnsupported(name string) error { return unsupportedError(name) }

// execPath returns the path of the running donejs binary.
func (rt *Runtime) execPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "donejs"
}

// nodeArch maps a Go architecture to the name Node uses.
func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm":
		return "arm"
	case "arm64":
		return "arm64"
	case "mips64le":
		return "mips64le"
	case "ppc64le":
		return "ppc64"
	case "s390x":
		return "s390x"
	case "riscv64":
		return "riscv64"
	case "loong64":
		return "loong64"
	}
	return goarch
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// --- tick queue (process.nextTick) -------------------------------------

func (rt *Runtime) pushTick(fn func()) {
	rt.mu.Lock()
	rt.ticks = append(rt.ticks, fn)
	rt.mu.Unlock()
	rt.Enqueue(func() {}) // wake the loop
}

func (rt *Runtime) drainTicks() {
	for {
		rt.mu.Lock()
		if len(rt.ticks) == 0 {
			rt.mu.Unlock()
			return
		}
		fn := rt.ticks[0]
		rt.ticks = rt.ticks[1:]
		rt.mu.Unlock()
		fn()
	}
}

// pushMicrotask queues a queueMicrotask callback.
func (rt *Runtime) pushMicrotask(fn func()) {
	rt.mu.Lock()
	rt.microtasks = append(rt.microtasks, fn)
	rt.mu.Unlock()
	rt.Enqueue(func() {}) // wake the loop
}

// drainMicrotasks runs queued queueMicrotask callbacks.
func (rt *Runtime) drainMicrotasks() {
	for {
		rt.mu.Lock()
		if len(rt.microtasks) == 0 {
			rt.mu.Unlock()
			return
		}
		fn := rt.microtasks[0]
		rt.microtasks = rt.microtasks[1:]
		rt.mu.Unlock()
		fn()
	}
}

var _ = exec.Command
