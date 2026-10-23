package vm

import (
	"encoding/json"
	"time"

	"donejs/internal/jit"
)

// registerTimers exposes the global timer functions.
func (rt *Runtime) registerTimers() {
	rt.natives["__timers"] = jit.Object()
}

// nSetTimeout implements global setTimeout.
func (rt *Runtime) nSetTimeout(args []jit.Value) jit.Value {
	fn := argsAt(args, 0)
	if !fn.IsCallable() {
		panic(jit.Err("TypeError", "callback must be a function"))
	}
	delay := time.Duration(0)
	if len(args) > 1 {
		delay = time.Duration(argInt(args, 1)) * time.Millisecond
	}
	var extra []jit.Value
	if len(args) > 2 {
		extra = args[2:]
	}
	id := rt.addTimer(delay, false, fn, jit.Undef(), extra)
	return jit.Num(float64(id))
}

// nSetInterval implements global setInterval.
func (rt *Runtime) nSetInterval(args []jit.Value) jit.Value {
	fn := argsAt(args, 0)
	if !fn.IsCallable() {
		panic(jit.Err("TypeError", "callback must be a function"))
	}
	delay := time.Duration(0)
	if len(args) > 1 {
		delay = time.Duration(argInt(args, 1)) * time.Millisecond
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	var extra []jit.Value
	if len(args) > 2 {
		extra = args[2:]
	}
	id := rt.addTimer(delay, true, fn, jit.Undef(), extra)
	return jit.Num(float64(id))
}

// nClearTimer implements clearTimeout/clearInterval/clearImmediate.
func (rt *Runtime) nClearTimer(args []jit.Value) jit.Value {
	if len(args) == 0 {
		return jit.Undef()
	}
	rt.clearTimer(uint64(argsAt(args, 0).ToInteger()))
	return jit.Undef()
}

// nSetImmediate implements global setImmediate.
func (rt *Runtime) nSetImmediate(args []jit.Value) jit.Value {
	fn := argsAt(args, 0)
	if !fn.IsCallable() {
		panic(jit.Err("TypeError", "callback must be a function"))
	}
	var extra []jit.Value
	if len(args) > 1 {
		extra = args[1:]
	}
	id := rt.addTimer(0, false, fn, jit.Undef(), extra)
	return jit.Num(float64(id))
}

// nQueueMicrotask implements global queueMicrotask.
func (rt *Runtime) nQueueMicrotask(args []jit.Value) jit.Value {
	fn := argsAt(args, 0)
	if !fn.IsCallable() {
		panic(jit.Err("TypeError", "callback must be a function"))
	}
	id := rt.regCB(fn)
	rt.pushMicrotask(func() { rt.invokeCB(id) })
	return jit.Undef()
}

// nStructuredClone implements global structuredClone.
func (rt *Runtime) nStructuredClone(args []jit.Value) jit.Value {
	v := argsAt(args, 0)
	raw, err := json.Marshal(v.Export())
	if err != nil {
		panic(rt.jsThrow(err))
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(rt.jsThrow(err))
	}
	return rt.toJS(out)
}
