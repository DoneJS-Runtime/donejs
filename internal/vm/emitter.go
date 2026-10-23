package vm

import (
	"sort"
	"sync"

	"donejs/internal/jit"
)

// goEmitter is a native EventEmitter. Native objects (sockets, requests,
// responses) are built on it so listener registration and dispatch stay
// consistent regardless of the JS `this` binding.
type goEmitter struct {
	mu        sync.Mutex
	listeners map[string][]*goListener
	max       int
	nextID    uint64
}

type goListener struct {
	id      uint64
	fn      jit.Value
	once    bool
	removed bool
}

func newGoEmitter() *goEmitter {
	return &goEmitter{listeners: map[string][]*goListener{}, max: 10}
}

func (e *goEmitter) on(event string, fn jit.Value, once bool, prepend bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	l := &goListener{id: e.nextID, fn: fn, once: once}
	if prepend {
		e.listeners[event] = append([]*goListener{l}, e.listeners[event]...)
	} else {
		e.listeners[event] = append(e.listeners[event], l)
	}
}

func (e *goEmitter) off(event string, fn jit.Value) {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.listeners[event]
	for i, l := range list {
		if sameFn(l.fn, fn) {
			l.removed = true
			e.listeners[event] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

// sameFn compares two jit function values by identity.
func sameFn(a, b jit.Value) bool { return a == b }

func (e *goEmitter) removeAll(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if event == "" {
		e.listeners = map[string][]*goListener{}
		return
	}
	delete(e.listeners, event)
}

func (e *goEmitter) removeID(event string, id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.listeners[event]
	for i, l := range list {
		if l.id == id {
			l.removed = true
			e.listeners[event] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

func (e *goEmitter) count(event string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.listeners[event])
}

func (e *goEmitter) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.listeners))
	for k := range e.listeners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// emit dispatches an event, returning true when a listener handled it.
func (e *goEmitter) emit(rt *Runtime, this jit.Value, event string, args ...jit.Value) bool {
	e.mu.Lock()
	list := append([]*goListener{}, e.listeners[event]...)
	e.mu.Unlock()
	if len(list) == 0 {
		return false
	}
	for _, l := range list {
		if l.removed {
			continue
		}
		if l.once {
			e.removeID(event, l.id)
		}
		if _, err := rt.jitCall(l.fn, this, args...); err != nil {
			rt.reportUncaught(err)
		}
	}
	return true
}

// newEmitterObject builds a JS object exposing EventEmitter semantics,
// backed by the native emitter.
func (rt *Runtime) newEmitterObject() (jit.Value, *goEmitter) {
	e := newGoEmitter()
	obj := jit.Object()
	setFn(obj, "on", func(args []jit.Value) jit.Value {
		fn := argsAt(args, 1)
		if !fn.IsCallable() {
			panic(jit.Err("TypeError", "listener must be a function"))
		}
		e.on(argStr(args, 0), fn, false, false)
		return obj
	})
	obj.Set("addListener", obj.Get("on"))
	obj.Set("prependListener", func(args []jit.Value) jit.Value {
		fn := argsAt(args, 1)
		if !fn.IsCallable() {
			panic(jit.Err("TypeError", "listener must be a function"))
		}
		e.on(argStr(args, 0), fn, false, true)
		return obj
	})
	obj.Set("once", func(args []jit.Value) jit.Value {
		fn := argsAt(args, 1)
		if !fn.IsCallable() {
			panic(jit.Err("TypeError", "listener must be a function"))
		}
		e.on(argStr(args, 0), fn, true, false)
		return obj
	})
	obj.Set("off", func(args []jit.Value) jit.Value {
		fn := argsAt(args, 1)
		if !fn.IsCallable() {
			panic(jit.Err("TypeError", "listener must be a function"))
		}
		e.off(argStr(args, 0), fn)
		return obj
	})
	obj.Set("removeListener", obj.Get("off"))
	obj.Set("removeAllListeners", func(args []jit.Value) jit.Value {
		e.removeAll(argStr(args, 0))
		return obj
	})
	obj.Set("emit", func(args []jit.Value) jit.Value {
		var evArgs []jit.Value
		if len(args) > 1 {
			evArgs = args[1:]
		}
		return jit.Bool(e.emit(rt, obj, argStr(args, 0), evArgs...))
	})
	obj.Set("listenerCount", func(args []jit.Value) jit.Value {
		return jit.Num(float64(e.count(argStr(args, 0))))
	})
	obj.Set("listeners", func(args []jit.Value) jit.Value { return jit.ArrOf() })
	obj.Set("eventNames", func(args []jit.Value) jit.Value { return jit.FromGo(e.names()) })
	obj.Set("setMaxListeners", func(args []jit.Value) jit.Value {
		e.max = argInt(args, 0)
		return obj
	})
	obj.Set("getMaxListeners", func(args []jit.Value) jit.Value {
		return jit.Num(float64(e.max))
	})
	return obj, e
}

// emitEvent dispatches an event on a native emitter object.
func (rt *Runtime) emitEvent(obj jit.Value, e *goEmitter, event string, args ...jit.Value) {
	e.emit(rt, obj, event, args...)
}
