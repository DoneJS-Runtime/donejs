package vm

import (
	"encoding/json"
	"time"

	"github.com/dop251/goja"
)

// registerTimers exposes the global timer functions.
func (rt *Runtime) registerTimers() {
	rt.natives["__timers"] = rt.vm.NewObject()
}

// nSetTimeout implements global setTimeout.
func (rt *Runtime) nSetTimeout(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(rt.vm.NewTypeError("callback must be a function"))
	}
	delay := time.Duration(0)
	if len(call.Arguments) > 1 {
		delay = time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
	}
	var args []goja.Value
	if len(call.Arguments) > 2 {
		args = call.Arguments[2:]
	}
	id := rt.addTimer(delay, false, fn, goja.Undefined(), args)
	return rt.vm.ToValue(id)
}

// nSetInterval implements global setInterval.
func (rt *Runtime) nSetInterval(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(rt.vm.NewTypeError("callback must be a function"))
	}
	delay := time.Duration(0)
	if len(call.Arguments) > 1 {
		delay = time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
	}
	var args []goja.Value
	if len(call.Arguments) > 2 {
		args = call.Arguments[2:]
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	id := rt.addTimer(delay, true, fn, goja.Undefined(), args)
	return rt.vm.ToValue(id)
}

// nClearTimer implements clearTimeout/clearInterval/clearImmediate.
func (rt *Runtime) nClearTimer(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) == 0 {
		return goja.Undefined()
	}
	rt.clearTimer(uint64(call.Argument(0).ToInteger()))
	return goja.Undefined()
}

// nSetImmediate implements setImmediate.
func (rt *Runtime) nSetImmediate(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(rt.vm.NewTypeError("callback must be a function"))
	}
	var args []goja.Value
	if len(call.Arguments) > 1 {
		args = call.Arguments[1:]
	}
	id := rt.addTimer(0, false, fn, goja.Undefined(), args)
	return rt.vm.ToValue(id)
}

// nQueueMicrotask implements queueMicrotask.
func (rt *Runtime) nQueueMicrotask(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(rt.vm.NewTypeError("callback must be a function"))
	}
	rt.pushMicrotask(func() {
		if _, err := fn(goja.Undefined()); err != nil {
			rt.reportUncaught(err)
		}
	})
	return goja.Undefined()
}

// nStructuredClone implements global structuredClone.
func (rt *Runtime) nStructuredClone(call goja.FunctionCall) goja.Value {
	v := call.Argument(0)
	raw, err := json.Marshal(v.Export())
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(rt.vm.NewGoError(err))
	}
	return rt.toJS(out)
}
