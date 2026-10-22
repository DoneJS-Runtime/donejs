package vm

import (
	"encoding/json"
	"os"
	"runtime"
	"time"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerMisc() {
	m := rt.vm.NewObject()
	m.Set("memStats", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(memStats())
	})
	m.Set("gc", func(call goja.FunctionCall) goja.Value {
		runtime.GC()
		return goja.Undefined()
	})
	m.Set("sleep", func(call goja.FunctionCall) goja.Value {
		d := time.Duration(call.Argument(0).ToInteger()) * time.Millisecond
		time.Sleep(d)
		return goja.Undefined()
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
