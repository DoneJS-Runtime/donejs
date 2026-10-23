package vm

import (
	"donejs/internal/jit"
)

// registerNatives installs the low-level native modules. Each native module
// exposes a table-driven API so the JS layer can stay thin:
//
//	__fs.sync(name, argsArray)   -> result (throws on error)
//	__fs.async(name, argsArray)  -> Promise
//
// JS wrappers in builtin/*.js shape these into the Node.js API.
func (rt *Runtime) registerNatives() {
	rt.registerEncoding()
	rt.registerInspect()
	rt.registerUtilDeep()
	rt.registerFS()
	rt.registerOS()
	rt.registerPath()
	rt.registerCrypto()
	rt.registerZlib()
	rt.registerURL()
	rt.registerQuerystring()
	rt.registerProcess()
	rt.registerHTTP()
	rt.registerNet()
	rt.registerChildProcess()
	rt.registerTimers()
	rt.registerMisc()
	rt.registerIntl()
}

// opTable is a name -> implementation map for a native module. An op receives
// its parameters as values and returns a Go result (or an error to throw).
type opTable map[string]func(args []jit.Value) (any, error)

// syncDispatch exposes an op table as a single callable: sync(name, params).
func (rt *Runtime) syncDispatch(table opTable) jit.Value {
	return jit.BuiltinFn("native", func(args []jit.Value) jit.Value {
		name := argStr(args, 0)
		op, ok := table[name]
		if !ok {
			panic(jit.Err("Error", "unknown operation: "+name))
		}
		res, err := op(values(argsAt(args, 1)))
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return rt.toJS(res)
	})
}

// asyncDispatch runs an op off the JS thread and settles a Promise.
func (rt *Runtime) asyncDispatch(table opTable) jit.Value {
	return jit.BuiltinFn("native", func(args []jit.Value) jit.Value {
		name := argStr(args, 0)
		op, ok := table[name]
		if !ok {
			panic(jit.Err("Error", "unknown operation: "+name))
		}
		params := values(argsAt(args, 1))
		return rt.goPromise(func() (any, error) { return op(params) })
	})
}

// values converts a JS array value into its element slice.
func values(v jit.Value) []jit.Value {
	if a := v.AsArr(); a != nil {
		n := jit.ArrayLen(a)
		out := make([]jit.Value, n)
		for i := 0; i < n; i++ {
			out[i] = jit.ArrayGet(a, i)
		}
		return out
	}
	return nil
}

// argsAt safely fetches argument i.
func argsAt(args []jit.Value, i int) jit.Value {
	if i < len(args) {
		return args[i]
	}
	return jit.Null()
}

func argStr(args []jit.Value, i int) string {
	return argsAt(args, i).String()
}

func argInt(args []jit.Value, i int) int {
	return int(argsAt(args, i).ToInteger())
}

func argNum(args []jit.Value, i int) float64 {
	return argsAt(args, i).ToFloat()
}

func argBool(args []jit.Value, i int) bool {
	return argsAt(args, i).ToBoolean()
}

// argObj returns argument i when it is an object, otherwise undefined, so
// opt* helpers can read options objects without nil checks.
func argObj(args []jit.Value, i int) jit.Value {
	v := argsAt(args, i)
	if v.IsObject() {
		return v
	}
	return jit.Undef()
}

func optBool(o jit.Value, key string, def bool) bool {
	v := o.Get(key)
	if v.IsUndef() {
		return def
	}
	return v.ToBoolean()
}

func optStr(o jit.Value, key, def string) string {
	v := o.Get(key)
	if v.IsUndef() {
		return def
	}
	return v.String()
}

// setFn attaches a Go function as a JS-callable property of an object value.
func setFn(o jit.Value, name string, fn func(args []jit.Value) jit.Value) {
	o.Set(name, jit.BuiltinFn(name, fn))
}

func optInt(o jit.Value, key string, def int) int {
	v := o.Get(key)
	if v.IsUndef() {
		return def
	}
	return int(v.ToInteger())
}
