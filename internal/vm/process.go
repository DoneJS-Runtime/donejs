package vm

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"donejs/internal/jit"
)

func (rt *Runtime) registerProcess() {
	p := jit.Object()

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
		"node":    "22.0.0",
		"donejs":  "0.1.0",
		"jit":     EngineVersion(),
		"v8":      "",
		"uv":      "",
		"modules": "26",
		"napi":    "9",
		"acorn":   "8.11.3",
		"openssl": "3.0.0",
	}
	p.Set("versions", versions)

	setFn(p, "cwd", func(args []jit.Value) jit.Value {
		d, err := os.Getwd()
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return jit.Str(d)
	})
	setFn(p, "chdir", func(args []jit.Value) jit.Value {
		if err := os.Chdir(argStr(args, 0)); err != nil {
			panic(rt.jsThrow(err))
		}
		return jit.Undef()
	})
	setFn(p, "exit", func(args []jit.Value) jit.Value {
		code := 0
		if len(args) > 0 {
			code = int(argInt(args, 0))
		}
		rt.exit(code)
		return jit.Undef()
	})
	p.Set("exitCode", jit.Undef())
	setFn(p, "nextTick", func(args []jit.Value) jit.Value {
		fn := argsAt(args, 0)
		if !fn.IsCallable() {
			panic(jit.Err("TypeError", "callback must be a function"))
		}
		var cbArgs []jit.Value
		if len(args) > 1 {
			cbArgs = args[1:]
		}
		id := rt.regCB(fn)
		rt.pushTick(func() { rt.invokeCB(id, cbArgs...) })
		return jit.Undef()
	})
	setFn(p, "hrtime", func(args []jit.Value) jit.Value {
		d := time.Since(rt.start)
		return jit.ArrOf(
			jit.Num(float64(d.Nanoseconds()/1e9)),
			jit.Num(float64(d.Nanoseconds()%1e9)),
		)
	})
	setFn(p, "uptime", func(args []jit.Value) jit.Value {
		return jit.Num(nowSeconds() - rt.startSeconds())
	})
	setFn(p, "memoryUsage", func(args []jit.Value) jit.Value {
		m := memStats()
		return rt.toJS(map[string]any{
			"rss":          int64(m.Sys),
			"heapTotal":    int64(m.HeapSys),
			"heapUsed":     int64(m.HeapAlloc),
			"external":     int64(m.Sys - m.HeapSys),
			"arrayBuffers": int64(0),
		})
	})
	setFn(p, "umask", func(args []jit.Value) jit.Value { return jit.Num(0o022) })
	setFn(p, "kill", func(args []jit.Value) jit.Value {
		pid := int(argInt(args, 0))
		sig := "SIGTERM"
		if len(args) > 1 && !args[1].IsUndef() {
			sig = argStr(args, 1)
		}
		s, err := strconv.Atoi(strings.TrimPrefix(sig, "SIG"))
		if err != nil {
			s = 15
		}
		if pid == os.Getpid() {
			rt.exit(0)
			return jit.Bool(true)
		}
		if err := syscall.Kill(pid, syscall.Signal(s)); err != nil {
			return jit.Bool(false)
		}
		return jit.Bool(true)
	})
	setFn(p, "emitWarning", func(args []jit.Value) jit.Value { return jit.Undef() })
	setFn(p, "binding", func(args []jit.Value) jit.Value {
		panic(rt.jsThrow(errUnsupported("process.binding")))
	})
	setFn(p, "dlopen", func(args []jit.Value) jit.Value {
		panic(rt.jsThrow(errUnsupported("process.dlopen")))
	})

	// stdio
	stdout := jit.Object()
	setFn(stdout, "write", func(args []jit.Value) jit.Value {
		s := argStr(args, 0)
		os.Stdout.WriteString(s)
		if cb, ok := splitCB(args); ok {
			rt.jitCall(cb, jit.Undef(), jit.Null(), jit.Num(float64(len(s))))
		}
		return jit.Bool(true)
	})
	stdout.Set("isTTY", isTTY(os.Stdout))
	stdout.Set("fd", 1)
	stdout.Set("writable", true)
	setFn(stdout, "end", func(args []jit.Value) jit.Value { return jit.Bool(true) })
	stderr := jit.Object()
	setFn(stderr, "write", func(args []jit.Value) jit.Value {
		s := argStr(args, 0)
		os.Stderr.WriteString(s)
		return jit.Bool(true)
	})
	stderr.Set("isTTY", isTTY(os.Stderr))
	stderr.Set("fd", 2)
	stderr.Set("writable", true)
	stdin := jit.Object()
	stdin.Set("isTTY", isTTY(os.Stdin))
	stdin.Set("fd", 0)
	setFn(stdin, "read", func(args []jit.Value) jit.Value { return jit.Null() })
	setFn(stdin, "setEncoding", func(args []jit.Value) jit.Value { return jit.Undef() })
	setFn(stdin, "resume", func(args []jit.Value) jit.Value { return jit.Undef() })
	setFn(stdin, "pause", func(args []jit.Value) jit.Value { return jit.Undef() })
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

// drainMicrotasks runs the engine's microtask queue (promise reactions) and
// the runtime's queueMicrotask callbacks, promise jobs first as Node does.
func (rt *Runtime) drainMicrotasks() {
	for {
		if rt.jit.HasJobs() {
			rt.jit.RunJobs()
			continue
		}
		rt.mu.Lock()
		more := len(rt.microtasks) > 0
		if more {
			fn := rt.microtasks[0]
			rt.microtasks = rt.microtasks[1:]
			rt.mu.Unlock()
			fn()
			continue
		}
		rt.mu.Unlock()
		return
	}
}

var _ = exec.Command
