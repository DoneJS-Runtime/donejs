package vm

import (
	"strconv"

	"github.com/dop251/goja"
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

// opTable is a name -> implementation map for a native module.
type opTable map[string]func(args []goja.Value) (any, error)

// syncDispatch exposes an op table as a single Go function.
func (rt *Runtime) syncDispatch(table opTable) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		args := arrayValues(call.Argument(1))
		op, ok := table[name]
		if !ok {
			panic(rt.vm.NewGoError(unknownOp(name)))
		}
		res, err := op(args)
		if err != nil {
			panic(rt.jsError(err))
		}
		return rt.toJS(res)
	}
}

// asyncDispatch runs an op off the JS thread and settles a Promise.
func (rt *Runtime) asyncDispatch(table opTable) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		args := arrayValues(call.Argument(1))
		op, ok := table[name]
		if !ok {
			panic(rt.vm.NewGoError(unknownOp(name)))
		}
		return rt.goPromise(func() (any, error) { return op(args) })
	}
}

// arrayValues converts a JS array argument into a Go value slice.
func arrayValues(v goja.Value) []goja.Value {
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	n := int(obj.Get("length").ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, obj.Get(strconv.Itoa(i)))
	}
	return out
}

type unknownOp string

func (u unknownOp) Error() string { return "unknown operation: " + string(u) }

// argsAt safely fetches argument i.
func argsAt(args []goja.Value, i int) goja.Value {
	if i < len(args) {
		return args[i]
	}
	return goja.Null()
}

func argStr(args []goja.Value, i int) string {
	v := argsAt(args, i)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func argInt(args []goja.Value, i int) int {
	v := argsAt(args, i)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	return int(v.ToInteger())
}

func argNum(args []goja.Value, i int) float64 {
	v := argsAt(args, i)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	return v.ToFloat()
}

func argBool(args []goja.Value, i int) bool {
	v := argsAt(args, i)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	return v.ToBoolean()
}

func argObj(args []goja.Value, i int) *goja.Object {
	if o, ok := argsAt(args, i).(*goja.Object); ok {
		return o
	}
	return nil
}

func optBool(o *goja.Object, key string, def bool) bool {
	if o == nil {
		return def
	}
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	return v.ToBoolean()
}

func optStr(o *goja.Object, key, def string) string {
	if o == nil {
		return def
	}
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	return v.String()
}

func optInt(o *goja.Object, key string, def int) int {
	if o == nil {
		return def
	}
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	return int(v.ToInteger())
}
