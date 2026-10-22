// Package vm implements the donejs runtime: a goja-powered,
// Node.js-API-compatible JavaScript runtime with a Go event loop.
package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// EngineVersion reports the engine version string.
func EngineVersion() string { return gojaVersion }

const gojaVersion = "goja"

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
	vm   *goja.Runtime
	jobs chan func()

	mu       sync.Mutex
	timers   map[uint64]*timerEntry
	nextID   uint64
	handles  int
	pending  int
	exiting  bool
	exitCode int
	ticks    []func()
	// microtasks holds queueMicrotask callbacks. They run after promise
	// jobs, matching the microtask queue semantics.
	microtasks []func()

	cbs    map[uint64]goja.Callable
	nextCB uint64

	modCache  map[string]*jsModule
	progCache map[string]*goja.Program

	fds   map[int]*os.File
	fdSeq int

	natives map[string]goja.Value

	argv     []string
	mainFile string
	mainDir  string

	// mainModule is the entry module object (require.main); modStack holds
	// the modules currently being evaluated so children can be linked.
	mainModule goja.Value
	modStack   []goja.Value

	allowExec  bool
	execPolicy *execPolicy

	http *httpState

	processObj goja.Value

	// JS-side helpers captured at bootstrap
	utilInspect goja.Callable
	emitterNew  goja.Callable
	uint8Maker  goja.Callable

	start          time.Time
	startSecondsVal float64
}

// New builds a bootstrapped runtime (JS globals installed, but no user code run).
func New(opts Options) *Runtime {
	rt := &Runtime{
		vm:        goja.New(),
		jobs:      make(chan func(), 1024),
		timers:    map[uint64]*timerEntry{},
		cbs:       map[uint64]goja.Callable{},
		modCache:  map[string]*jsModule{},
		progCache: map[string]*goja.Program{},
		fds:       map[int]*os.File{},
		natives:   map[string]goja.Value{},
		allowExec: opts.AllowExec || os.Getenv("DONEJS_ALLOW_EXEC") == "1",
		start:     time.Now(),
	}
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
		if _, err := rt.loadModule(abs, true); err != nil {
			return failCode(rt.currentExit()), err
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

// VMRaw exposes the underlying goja runtime for native modules.
func (rt *Runtime) VMRaw() *goja.Runtime { return rt.vm }

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
	rt.mu.Lock()
	rt.handles++
	rt.mu.Unlock()
}

func (rt *Runtime) unref() {
	rt.mu.Lock()
	rt.handles--
	if rt.handles < 0 {
		rt.handles = 0
	}
	rt.mu.Unlock()
}

func (rt *Runtime) exit(code int) {
	rt.mu.Lock()
	if !rt.exiting {
		rt.exitCode = code
		rt.exiting = true
	}
	rt.mu.Unlock()
}

func (rt *Runtime) bootstrapGlobals() {
	vm := rt.vm

	set := func(name string, fn func(goja.FunctionCall) goja.Value) {
		if err := vm.Set(name, fn); err != nil {
			panic(err)
		}
	}
	set("setTimeout", rt.nSetTimeout)
	set("setInterval", rt.nSetInterval)
	set("clearTimeout", rt.nClearTimer)
	set("clearInterval", rt.nClearTimer)
	set("setImmediate", rt.nSetImmediate)
	set("clearImmediate", rt.nClearTimer)
	set("queueMicrotask", rt.nQueueMicrotask)
	set("structuredClone", rt.nStructuredClone)
	// __donejsFlushTicks drains the process.nextTick queue. The entry module
	// calls it at the end of its synchronous body so nextTick callbacks run
	// before promise microtasks, like Node.
	set("__donejsFlushTicks", func(call goja.FunctionCall) goja.Value {
		rt.drainTicks()
		return goja.Undefined()
	})

	// Global require, so eval'd code and scripts can call it directly.
	reqFn := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(rt.vm.NewTypeError("require specifier required"))
		}
		v, err := rt.requireFrom(rt.mainDir, call.Argument(0).String())
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return v
	}
	reqVal := rt.vm.ToValue(reqFn)
	if o, ok := reqVal.(*goja.Object); ok {
		o.Set("resolve", func(call goja.FunctionCall) goja.Value {
			spec := nodePrefix(call.Argument(0).String())
			if isBuiltin(spec) || strings.HasPrefix(spec, "__") {
				return rt.vm.ToValue(spec)
			}
			if r, ok := rt.resolveFile(filepath.Join(rt.mainDir, spec)); ok {
				return rt.vm.ToValue(r)
			}
			w := rt.mainDir
			for {
				if r, ok := rt.resolveNodeModules(filepath.Join(w, "node_modules"), spec); ok {
					return rt.vm.ToValue(r)
				}
				parent := filepath.Dir(w)
				if parent == w {
					break
				}
				w = parent
			}
			panic(rt.vm.NewGoError(fmt.Errorf("Cannot find module '%s'", spec)))
		})
		o.Set("cache", rt.moduleCacheObj())
		o.Set("extensions", map[string]any{".js": ".js", ".json": ".json", ".node": ".node"})
	}
	vm.Set("require", reqVal)

	// Run _bootstrap.js: installs console, process, Buffer, global glue.
	if _, err := rt.runBuiltin("_bootstrap"); err != nil {
		panic(fmt.Errorf("donejs bootstrap failed: %w", err))
	}

	// util.inspect for Go-side formatting (console fallback)
	if u, err := vm.RunString("require('util')"); err == nil {
		obj := u.ToObject(vm)
		if f, ok := goja.AssertFunction(obj.Get("inspect")); ok {
			rt.utilInspect = f
		}
	}
	if e, err := vm.RunString("require('events')"); err == nil {
		ctor := e.ToObject(vm).Get("EventEmitter")
		if f, ok := goja.AssertFunction(ctor); ok {
			rt.emitterNew = f
		}
	}
	if u, err := vm.RunString("(function(ab){ return new Uint8Array(ab); })"); err == nil {
		if f, ok := goja.AssertFunction(u); ok {
			rt.uint8Maker = f
		}
	}
}

// callMethod invokes obj.method(args...), returning (value, error).
func callMethod(rt *Runtime, obj goja.Value, name string, args ...goja.Value) (goja.Value, error) {
	o := obj.ToObject(rt.vm)
	f, ok := goja.AssertFunction(o.Get(name))
	if !ok {
		return goja.Undefined(), fmt.Errorf("not a function: %s", name)
	}
	return f(obj, args...)
}

// newUint8Array wraps bytes in a JS Uint8Array so JS code can index them.
func (rt *Runtime) newUint8Array(b []byte) goja.Value {
	if rt.uint8Maker == nil {
		return rt.vm.ToValue(rt.vm.NewArrayBuffer(b))
	}
	ab := rt.vm.ToValue(rt.vm.NewArrayBuffer(b))
	v, err := rt.uint8Maker(goja.Undefined(), ab)
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	return v
}

// formatErr renders an uncaught JS value like Node does.
func (rt *Runtime) formatErr(v any) string {
	switch t := v.(type) {
	case nil:
		return "undefined"
	case error:
		return t.Error()
	case goja.Value:
		if goja.IsUndefined(t) {
			return "undefined"
		}
		if o, ok := t.(*goja.Object); ok {
			if s, ok := goja.AssertFunction(o.Get("stack")); ok {
				if sv, err := s(t); err == nil {
					return sv.String()
				}
			}
		}
		return t.String()
	}
	return fmt.Sprintf("%v", v)
}

// makeRequire builds the CommonJS require function with Node-compatible
// helper properties (resolve, cache, extensions).
func (rt *Runtime) makeRequire(reqFn, resolveFn goja.Value) goja.Value {
	f := rt.vm.ToValue(reqFn)
	obj, ok := f.(*goja.Object)
	if !ok {
		return f
	}
	obj.Set("resolve", resolveFn)
	obj.Set("cache", rt.moduleCacheObj())
	obj.Set("extensions", map[string]any{".js": ".js", ".json": ".json", ".node": ".node"})
	if rt.mainModule != nil {
		obj.Set("main", rt.mainModule)
	} else {
		obj.Set("main", goja.Undefined())
	}
	return f
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
func (rt *Runtime) moduleCacheObj() *goja.Object {
	o := rt.vm.NewObject()
	rt.mu.Lock()
	for k, m := range rt.modCache {
		if m.exports != nil {
			o.Set(k, m.exports)
		}
	}
	rt.mu.Unlock()
	return o
}

func (rt *Runtime) compile(name, src string) (*goja.Program, error) {
	rt.mu.Lock()
	if p, ok := rt.progCache[name]; ok {
		rt.mu.Unlock()
		return p, nil
	}
	rt.mu.Unlock()
	p, err := goja.Compile(name, src, false)
	if err != nil {
		return nil, err
	}
	rt.mu.Lock()
	rt.progCache[name] = p
	rt.mu.Unlock()
	return p, nil
}

// RunString executes source on the JS thread (used by tests/tools).
func (rt *Runtime) RunString(name, src string) (goja.Value, error) {
	p, err := rt.compile(name, src)
	if err != nil {
		return nil, err
	}
	var out goja.Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = errors.New(rt.formatErr(toValue(rt.vm, r)))
			}
		}()
		out, err = rt.vm.RunProgram(p)
	}()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func toValue(vm *goja.Runtime, v any) goja.Value {
	if val, ok := v.(goja.Value); ok {
		return val
	}
	return vm.ToValue(v)
}
