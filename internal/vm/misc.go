package vm

import (
	"encoding/json"
	"os"
	"runtime"
	"time"

	"donejs/internal/jit"
)

func (rt *Runtime) registerMisc() {
	m := jit.Object()
	setFn(m, "memStats", func(args []jit.Value) jit.Value {
		return rt.toJS(memStats())
	})
	setFn(m, "gc", func(args []jit.Value) jit.Value {
		runtime.GC()
		return jit.Undef()
	})
	setFn(m, "sleep", func(args []jit.Value) jit.Value {
		d := time.Duration(argsAt(args, 0).ToInteger()) * time.Millisecond
		time.Sleep(d)
		return jit.Undef()
	})
	rt.natives["__misc"] = m
}

func nowSeconds() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func (rt *Runtime) startSeconds() float64 { return rt.startSecondsVal }

func memStats() *runtime.MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &m
}

func osGetwd() (string, error) { return os.Getwd() }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
