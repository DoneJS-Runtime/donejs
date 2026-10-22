package vm

import (
	"reflect"
	"sort"
	"sync"

	"github.com/dop251/goja"
)

// goEmitter is a native EventEmitter. Native objects (sockets, requests,
// responses) are built on it so listener registration and dispatch stay
// consistent regardless of the JS `this` binding.
type goEmitter struct {
	mu        sync.Mutex
	listeners map[string][]*goListener
	max       int
}

type goListener struct {
	fn       goja.Callable
	once     bool
	removed  bool
}

func newGoEmitter() *goEmitter {
	return &goEmitter{listeners: map[string][]*goListener{}, max: 10}
}

func (e *goEmitter) on(event string, fn goja.Callable, once bool, prepend bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	l := &goListener{fn: fn, once: once}
	if prepend {
		e.listeners[event] = append([]*goListener{l}, e.listeners[event]...)
	} else {
		e.listeners[event] = append(e.listeners[event], l)
	}
}

func (e *goEmitter) off(event string, fn goja.Callable) {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.listeners[event]
	for i, l := range list {
		if sameCallable(l.fn, fn) {
			l.removed = true
			e.listeners[event] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

// sameCallable compares two Go function values by code pointer.
func sameCallable(a, b goja.Callable) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func (e *goEmitter) removeAll(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if event == "" {
		e.listeners = map[string][]*goListener{}
		return
	}
	delete(e.listeners, event)
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
func (e *goEmitter) emit(rt *Runtime, this goja.Value, event string, args ...goja.Value) bool {
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
			e.off(event, l.fn)
		}
		if _, err := l.fn(this, args...); err != nil {
			rt.reportUncaught(err)
		}
	}
	return true
}

// newEmitterObject builds a JS object exposing EventEmitter semantics,
// backed by the native emitter.
func (rt *Runtime) newEmitterObject() (*goja.Object, *goEmitter) {
	e := newGoEmitter()
	obj := rt.vm.NewObject()
	obj.Set("on", func(c goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(c.Argument(1))
		if !ok {
			panic(rt.vm.NewTypeError("listener must be a function"))
		}
		e.on(c.Argument(0).String(), fn, false, false)
		return obj
	})
	obj.Set("addListener", obj.Get("on"))
	obj.Set("prependListener", func(c goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(c.Argument(1))
		if !ok {
			panic(rt.vm.NewTypeError("listener must be a function"))
		}
		e.on(c.Argument(0).String(), fn, false, true)
		return obj
	})
	obj.Set("once", func(c goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(c.Argument(1))
		if !ok {
			panic(rt.vm.NewTypeError("listener must be a function"))
		}
		e.on(c.Argument(0).String(), fn, true, false)
		return obj
	})
	obj.Set("off", func(c goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(c.Argument(1))
		if !ok {
			panic(rt.vm.NewTypeError("listener must be a function"))
		}
		e.off(c.Argument(0).String(), fn)
		return obj
	})
	obj.Set("removeListener", obj.Get("off"))
	obj.Set("removeAllListeners", func(c goja.FunctionCall) goja.Value {
		e.removeAll(c.Argument(0).String())
		return obj
	})
	obj.Set("emit", func(c goja.FunctionCall) goja.Value {
		args := []goja.Value{}
		if len(c.Arguments) > 1 {
			args = c.Arguments[1:]
		}
		return rt.vm.ToValue(e.emit(rt, obj, c.Argument(0).String(), args...))
	})
	obj.Set("listenerCount", func(c goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(e.count(c.Argument(0).String()))
	})
	obj.Set("listeners", func(c goja.FunctionCall) goja.Value {
		return rt.vm.NewArray()
	})
	obj.Set("eventNames", func(c goja.FunctionCall) goja.Value {
		names := e.names()
		items := make([]interface{}, len(names))
		for i, n := range names {
			items[i] = n
		}
		return rt.vm.NewArray(items...)
	})
	obj.Set("setMaxListeners", func(c goja.FunctionCall) goja.Value {
		e.max = int(c.Argument(0).ToInteger())
		return obj
	})
	obj.Set("getMaxListeners", func(c goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(e.max)
	})
	return obj, e
}

// emitEvent dispatches an event on a native emitter object.
func (rt *Runtime) emitEvent(obj *goja.Object, e *goEmitter, event string, args ...goja.Value) {
	e.emit(rt, obj, event, args...)
}
