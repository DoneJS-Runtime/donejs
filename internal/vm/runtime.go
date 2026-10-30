// Package vm implements the donejs runtime: a Node.js-API-compatible
// JavaScript runtime built on the jit bytecode engine, with a Go event loop.
package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"donejs/internal/jit"
)

// EngineVersion reports the engine version string.
func EngineVersion() string { return engineName }

const engineName = "jit"

// Options configure a runtime instance.
type Options struct {
	Script    string   // script file to run
	Eval      string   // inline code to run (used when Script is empty)
	Args      []string // user arguments after the script
	AllowExec bool     // permit child_process spawning
}

// Runtime owns the JS VM and the event loop. All JS executes on the
// goroutine that calls Loop/Run; other goroutines hand work over
// through Enqueue.
type Runtime struct {
	jit  *jit.VM
	jobs chan func()

	mu     sync.Mutex
	timers map[uint64]*timerEntry
	nextID uint64
	// handles counts live handles; atomic because ref/unref run on socket
	// goroutines while the loop reads it from the JS thread.
	handles  atomic.Int64
	pending  int
	exiting  bool
	exitCode int
	ticks    []func()
	// microtasks holds queueMicrotask callbacks. They run after promise
	// jobs, matching the microtask queue semantics.
	microtasks []func()

	cbs    map[uint64]jit.Value
	nextCB uint64

	modCache  map[string]*jsModule
	progCache map[string]*jit.FuncProto
	// resCache memoizes successful module resolutions keyed by
	// "dir\x00specifier", so repeated require/import calls never re-stat the
	// filesystem. Only hits are stored: a failed resolution stays retryable
	// when the file appears later.
	resCache map[string]string

	// esModules holds ES module namespaces keyed by resolved file path.
	esModules map[string]*esModule
	// esStack holds the ES modules currently being evaluated, for cycles.
	esStack []*esModule
	// currentModDir is the directory an ES `import` specifier resolves from.
	currentModDir string

	fds   map[int]*os.File
	fdSeq int

	natives map[string]jit.Value

	// urlCache memoizes successful URL parses keyed by "raw\x00base" so
	// repeated parses of the same URL skip the WHATWG component walk.
	urlCache map[string]jit.Value

	argv     []string
	mainFile string
	mainDir  string

	// mainModule is the entry module object (require.main); modStack holds
	// the modules currently being evaluated so children can be linked.
	mainModule jit.Value
	modStack   []jit.Value

	allowExec  bool
	execPolicy *execPolicy

	http *httpState

	processObj jit.Value

	// JS-side helpers captured at bootstrap
	utilInspect jit.Value
	emitterNew  jit.Value

	start           time.Time
	startSecondsVal float64
}

// New builds a bootstrapped runtime (JS globals installed, but no user code run).
func New(opts Options) *Runtime {
	rt := &Runtime{
		jit:       jit.NewVM(),
		jobs:      make(chan func(), 1024),
		timers:    map[uint64]*timerEntry{},
		cbs:       map[uint64]jit.Value{},
		modCache:  map[string]*jsModule{},
		progCache: map[string]*jit.FuncProto{},
		resCache:  map[string]string{},
		urlCache:  map[string]jit.Value{},
		esModules: map[string]*esModule{},
		fds:       map[int]*os.File{},
		natives:   map[string]jit.Value{},
		allowExec: opts.AllowExec || os.Getenv("DONEJS_ALLOW_EXEC") == "1",
		start:     time.Now(),
	}
	rt.jit.SetOutput(func(s string) { os.Stdout.WriteString(s + "\n") })
	rt.startSecondsVal = nowSeconds()
	rt.execPolicy = &execPolicy{allowed: rt.allowExec, forced: os.Getenv("DONEJS_FORCE_EXEC") == "1"}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	rt.mainDir = wd
	// process.argv follows Node: the executable, then the script (only when
	// a file is run), then the user arguments.
	rt.argv = []string{"donejs"}
	if opts.Eval == "" && opts.Script != "" {
		if abs, err := filepath.Abs(opts.Script); err == nil {
			rt.argv = append(rt.argv, abs)
		} else {
			rt.argv = append(rt.argv, opts.Script)
		}
	}
	rt.argv = append(rt.argv, opts.Args...)

	rt.registerNatives()
	rt.jit.SetGlobal("__donejsImport", rt.importHost())
	rt.jit.SetGlobal("__donejsImportDyn", rt.dynamicImportHost())
	rt.bootstrapGlobals()
	return rt
}

// Run executes the requested program and drives the event loop to completion.
func Run(opts Options) (int, error) {
	rt := New(opts)
	if opts.Eval != "" {
		if _, err := rt.loadModuleText("[eval]", opts.Eval, rt.mainDir, true); err != nil {
			return failCode(rt.currentExit()), err
		}
	} else {
		abs, err := filepath.Abs(opts.Script)
		if err != nil {
			return 1, err
		}
		if _, err := os.Stat(abs); err != nil {
			return 1, fmt.Errorf("Cannot find module '%s'", opts.Script)
		}
		rt.mainFile = abs
		rt.mainDir = filepath.Dir(abs)
		var loadErr error
		if data, err := os.ReadFile(abs); err == nil && hasESModuleSyntax(string(data)) {
			_, loadErr = rt.loadESModule(abs)
		} else {
			_, loadErr = rt.loadModule(abs, true)
		}
		if loadErr != nil {
			return failCode(rt.currentExit()), loadErr
		}
	}
	return rt.Loop(), nil
}

func failCode(c int) int {
	if c == 0 {
		return 1
	}
	return c
}

// VMRaw exposes the underlying engine VM for native modules.
func (rt *Runtime) VMRaw() *jit.VM { return rt.jit }

// Enqueue schedules fn to run on the JS thread.
func (rt *Runtime) Enqueue(fn func()) {
	rt.mu.Lock()
	if rt.exiting {
		rt.mu.Unlock()
		return
	}
	rt.mu.Unlock()
	rt.jobs <- fn
}

func (rt *Runtime) ref() {
	rt.handles.Add(1)
}

func (rt *Runtime) unref() {
	if v := rt.handles.Add(-1); v < 0 {
		rt.handles.Store(0)
	}
}

func (rt *Runtime) exit(code int) {
	rt.mu.Lock()
	if !rt.exiting {
		rt.exitCode = code
		rt.exiting = true
	}
	rt.mu.Unlock()
}

// hostFunc is a callable host value that can also carry properties — the
// shape require needs (require.resolve, require.cache).
type hostFunc struct {
	name  string
	call  func(args []jit.Value) jit.Value
	props map[string]jit.Value
}

func (h *hostFunc) Call(args []jit.Value) jit.Value { return h.call(args) }

func (h *hostFunc) Get(name string) jit.Value {
	if v, ok := h.props[name]; ok {
		return v
	}
	return jit.Undef()
}

func (h *hostFunc) Set(name string, v jit.Value) {
	if h.props == nil {
		h.props = map[string]jit.Value{}
	}
	h.props[name] = v
}

func (h *hostFunc) Str() string  { return "function " + h.name + "() { [native code] }" }
func (h *hostFunc) Type() string { return "function" }

func (rt *Runtime) bootstrapGlobals() {
	set := func(name string, fn func(args []jit.Value) jit.Value) {
		rt.jit.SetGlobal(name, jit.BuiltinFn(name, fn))
	}
	set("setTimeout", rt.nSetTimeout)
	set("setInterval", rt.nSetInterval)
	set("clearTimeout", rt.nClearTimer)
	set("clearInterval", rt.nClearTimer)
	set("setImmediate", rt.nSetImmediate)
	set("clearImmediate", rt.nClearTimer)
	set("queueMicrotask", rt.nQueueMicrotask)
	set("structuredClone", rt.nStructuredClone)
	rt.registerURI()
	// __donejsFlushTicks drains the process.nextTick queue. The entry module
	// calls it at the end of its synchronous body so nextTick callbacks run
	// before promise microtasks, like Node.
	set("__donejsFlushTicks", func(args []jit.Value) jit.Value {
		rt.drainTicks()
		return jit.Undef()
	})

	// Global require, so eval'd code and scripts can call it directly.
	req := &hostFunc{name: "require"}
	req.call = func(args []jit.Value) jit.Value {
		if len(args) == 0 {
			panic(jit.Err("TypeError", "require specifier required"))
		}
		v, err := rt.requireFrom(rt.mainDir, argsAt(args, 0).String())
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return v
	}
	req.Set("resolve", jit.BuiltinFn("resolve", func(args []jit.Value) jit.Value {
		spec := nodePrefix(argsAt(args, 0).String())
		if isBuiltin(spec) || strings.HasPrefix(spec, "__") {
			return jit.Str(spec)
		}
		if r, ok := rt.resolveFile(filepath.Join(rt.mainDir, spec)); ok {
			return jit.Str(r)
		}
		w := rt.mainDir
		for {
			if r, ok := rt.resolveNodeModules(filepath.Join(w, "node_modules"), spec); ok {
				return jit.Str(r)
			}
			parent := filepath.Dir(w)
			if parent == w {
				break
			}
			w = parent
		}
		panic(jit.Err("Error", fmt.Sprintf("Cannot find module '%s'", spec)))
	}))
	req.Set("cache", rt.moduleCacheObj())
	req.Set("extensions", jit.FromGo(map[string]any{".js": ".js", ".json": ".json", ".node": ".node"}))
	rt.jit.SetGlobal("require", jit.Host(req))

	// Run _bootstrap.js: installs console, process, Buffer, global glue.
	if _, err := rt.runBuiltin("_bootstrap"); err != nil {
		panic(fmt.Errorf("donejs bootstrap failed: %w", err))
	}

	// util.inspect for Go-side formatting (console fallback)
	if u, err := rt.RunString("util-inspect", "require('util')"); err == nil {
		if f := u.Get("inspect"); f.IsCallable() {
			rt.utilInspect = f
		}
	}
	if e, err := rt.RunString("events", "require('events')"); err == nil {
		if ctor := e.Get("EventEmitter"); ctor.IsCallable() {
			rt.emitterNew = ctor
		}
	}
}

// callMethod invokes obj.method(args...), returning (value, error).
func callMethod(rt *Runtime, obj jit.Value, name string, args ...jit.Value) (jit.Value, error) {
	f := obj.Get(name)
	if !f.IsCallable() {
		return jit.Undef(), fmt.Errorf("not a function: %s", name)
	}
	return rt.jitCall(f, obj, args...)
}

// jitCall runs a JS function with an explicit receiver.
func (rt *Runtime) jitCall(fn jit.Value, recv jit.Value, args ...jit.Value) (jit.Value, error) {
	return rt.jit.CallBind(fn, recv, args...)
}

// newUint8Array wraps bytes in a JS Uint8Array so JS code can index them.
func (rt *Runtime) newUint8Array(b []byte) jit.Value {
	return jit.Uint8ArrayValue(b)
}

// formatErr renders an uncaught JS value like Node does.
func (rt *Runtime) formatErr(v any) string {
	switch t := v.(type) {
	case nil:
		return "undefined"
	case error:
		return t.Error()
	case jit.Value:
		if t.IsUndef() {
			return "undefined"
		}
		if o := t.AsObj(); o != nil {
			if stack := t.Get("stack"); !stack.IsUndef() {
				return stack.String()
			}
			name := t.Get("name")
			if !name.IsUndef() && name.String() != "" {
				return name.String() + ": " + t.Get("message").String()
			}
		}
		return t.String()
	}
	return fmt.Sprintf("%v", v)
}

// makeRequire builds the CommonJS require function with Node-compatible
// helper properties (resolve, cache, extensions).
func (rt *Runtime) makeRequire(reqFn, resolveFn jit.Value) jit.Value {
	f := &hostFunc{name: "require"}
	f.call = func(args []jit.Value) jit.Value {
		return rt.jit.CallValue(reqFn, args)
	}
	f.Set("resolve", resolveFn)
	f.Set("cache", rt.moduleCacheObj())
	f.Set("extensions", jit.FromGo(map[string]any{".js": ".js", ".json": ".json", ".node": ".node"}))
	if !rt.mainModule.IsUndef() {
		f.Set("main", rt.mainModule)
	} else {
		f.Set("main", jit.Undef())
	}
	return jit.Host(f)
}

// modulePaths lists the node_modules directories visible from dir.
func (rt *Runtime) modulePaths(dir string) []any {
	out := []any{}
	for w := dir; ; {
		out = append(out, filepath.Join(w, "node_modules"))
		parent := filepath.Dir(w)
		if parent == w {
			break
		}
		w = parent
	}
	return out
}

// moduleCacheObj exposes the module cache as a plain JS object.
func (rt *Runtime) moduleCacheObj() jit.Value {
	o := jit.Object()
	rt.mu.Lock()
	for k, m := range rt.modCache {
		if !m.exports.IsUndef() {
			o.Set(k, m.exports)
		}
	}
	rt.mu.Unlock()
	return o
}

func (rt *Runtime) compile(name, src string) (*jit.FuncProto, error) {
	rt.mu.Lock()
	if p, ok := rt.progCache[name]; ok {
		rt.mu.Unlock()
		return p, nil
	}
	rt.mu.Unlock()
	p, err := jit.Compile(name, src)
	if err != nil {
		return nil, err
	}
	rt.mu.Lock()
	rt.progCache[name] = p
	rt.mu.Unlock()
	return p, nil
}

// RunString executes source on the JS thread (used by tests/tools).
func (rt *Runtime) RunString(name, src string) (jit.Value, error) {
	p, err := rt.compile(name, src)
	if err != nil {
		return jit.Undef(), err
	}
	out, err := rt.jit.CallProtoErr(p)
	if err != nil {
		return jit.Undef(), errors.New(rt.formatErr(errToValue(err)))
	}
	return out, nil
}

// errToValue unwraps a ThrowError into the JS value it carries.
func errToValue(err error) jit.Value {
	if te, ok := err.(*jit.ThrowError); ok {
		return te.Value
	}
	return jit.ErrObj("Error", err.Error())
}
