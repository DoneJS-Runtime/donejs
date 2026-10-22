package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

type jsModule struct {
	exports goja.Value
	loaded  bool
}

var nodePrefix = func(s string) string { return strings.TrimPrefix(s, "node:") }

// requireFrom implements CommonJS resolution starting at dir.
func (rt *Runtime) requireFrom(dir string, spec string) (goja.Value, error) {
	spec = nodePrefix(spec)

	// native modules (e.g. __fs)
	if n, ok := rt.natives[spec]; ok {
		return n, nil
	}
	// builtin JS modules
	if isBuiltin(spec) {
		return rt.runBuiltin(spec)
	}
	// relative / absolute
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || filepath.IsAbs(spec) {
		p := spec
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, spec)
		}
		if resolved, ok := rt.resolveFile(p); ok {
			return rt.loadModule(resolved, false)
		}
		return nil, fmt.Errorf("Cannot find module '%s'", spec)
	}
	// bare specifier: walk node_modules upwards
	d := dir
	for {
		nm := filepath.Join(d, "node_modules")
		if resolved, ok := rt.resolveNodeModules(nm, spec); ok {
			return rt.loadModule(resolved, false)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return nil, fmt.Errorf("Cannot find module '%s'", spec)
}

func (rt *Runtime) resolveFile(p string) (string, bool) {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return rt.resolveDir(p)
	}
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	for _, ext := range []string{".js", ".json", ".node"} {
		if _, err := os.Stat(p + ext); err == nil {
			return p + ext, true
		}
	}
	return "", false
}

func (rt *Runtime) resolveDir(p string) (string, bool) {
	pj := filepath.Join(p, "package.json")
	if data, err := os.ReadFile(pj); err == nil {
		var pkg struct {
			Main string `json:"main"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Main != "" {
			m := filepath.Join(p, pkg.Main)
			if r, ok := rt.resolveFile(m); ok {
				return r, true
			}
		}
	}
	return rt.resolveFile(filepath.Join(p, "index"))
}

func (rt *Runtime) resolveNodeModules(nmDir, spec string) (string, bool) {
	// package name possibly with subpath: "@scope/pkg/sub" or "pkg/sub"
	pkgPart := spec
	sub := ""
	if i := strings.Index(spec, "/"); i >= 0 {
		if strings.HasPrefix(spec, "@") {
			if j := strings.Index(spec[i+1:], "/"); j >= 0 {
				pkgPart = spec[:i+1+j+1]
				sub = spec[i+1+j+1:]
			} else {
				pkgPart = spec
			}
		} else {
			pkgPart = spec[:i]
			sub = spec[i+1:]
		}
	}
	pkgDir := filepath.Join(nmDir, pkgPart)
	st, err := os.Stat(pkgDir)
	if err != nil || !st.IsDir() {
		return "", false
	}
	if sub == "" {
		return rt.resolveDir(pkgDir)
	}
	return rt.resolveFile(filepath.Join(pkgDir, sub))
}

func (rt *Runtime) loadModule(path string, flush bool) (goja.Value, error) {
	path = filepath.Clean(path)
	rt.mu.Lock()
	if m, ok := rt.modCache[path]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return rt.loadModuleText(path, string(data), filepath.Dir(path), flush)
}

func (rt *Runtime) loadModuleText(filename, src, dir string, flush bool) (goja.Value, error) {
	rt.mu.Lock()
	if m, ok := rt.modCache[filename]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()

	mod := &jsModule{exports: rt.vm.NewObject()}
	rt.mu.Lock()
	rt.modCache[filename] = mod
	rt.mu.Unlock()

	if strings.HasSuffix(filename, ".json") {
		var v any
		if err := json.Unmarshal([]byte(src), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", filename, err)
		}
		mod.exports = rt.toJS(v)
		mod.loaded = true
		return mod.exports, nil
	}

	wrapped := "(function (exports, require, module, __filename, __dirname) {" + src + "\n})"
	if flush {
		// Flush the nextTick queue at the end of the entry module so that
		// process.nextTick callbacks run before promise microtasks.
		wrapped = "(function (exports, require, module, __filename, __dirname) {" + src + "\n;__donejsFlushTicks();\n})"
	}
	prg, err := rt.compile(filename, wrapped)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	moduleObj := rt.vm.NewObject()
	moduleObj.Set("exports", mod.exports)
	moduleObj.Set("id", filename)
	if flush {
		moduleObj.Set("id", ".")
	}
	moduleObj.Set("filename", filename)
	moduleObj.Set("loaded", false)
	moduleObj.Set("paths", rt.modulePaths(dir))
	moduleObj.Set("children", rt.vm.NewArray())
	// Link to the requiring module, and remember the entry module so that
	// require.main works the way Node's does.
	var parent goja.Value = goja.Null()
	if n := len(rt.modStack); n > 0 {
		parent = rt.modStack[n-1]
		if pObj, ok := parent.(*goja.Object); ok {
			if kids, ok := pObj.Get("children").(*goja.Object); ok {
				kids.Set(strconv.Itoa(int(kids.Get("length").ToInteger())), moduleObj)
			}
		}
	}
	moduleObj.Set("parent", parent)
	if flush {
		rt.mainModule = moduleObj
	}

	reqFn := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(rt.vm.NewTypeError("require specifier required"))
		}
		v, err := rt.requireFrom(dir, call.Argument(0).String())
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return v
	}
	reqObj := rt.vm.NewObject()
	resolveFn := func(call goja.FunctionCall) goja.Value {
		spec := nodePrefix(call.Argument(0).String())
		if isBuiltin(spec) || strings.HasPrefix(spec, "__") {
			return rt.vm.ToValue(spec)
		}
		d := dir
		if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
			p := spec
			if !filepath.IsAbs(p) {
				p = filepath.Join(d, spec)
			}
			if r, ok := rt.resolveFile(p); ok {
				return rt.vm.ToValue(r)
			}
		} else {
			w := d
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
		}
		panic(rt.vm.NewGoError(fmt.Errorf("Cannot find module '%s'", spec)))
	}
	reqObj.Set("resolve", resolveFn)

	val, err := rt.vm.RunProgram(prg)
	if err != nil {
		delete(rt.modCache, filename)
		return nil, errors2(err)
	}
	_ = val
	fn, ok := goja.AssertFunction(val)
	if !ok {
		delete(rt.modCache, filename)
		return nil, fmt.Errorf("%s: module wrapper did not produce a function", filename)
	}
	this := rt.vm.NewObject()
	req := rt.makeRequire(rt.vm.ToValue(reqFn), rt.vm.ToValue(resolveFn))
	rt.modStack = append(rt.modStack, moduleObj)
	defer func() { rt.modStack = rt.modStack[:len(rt.modStack)-1] }()
	if _, err := fn(this, moduleObj.Get("exports"), req, moduleObj, rt.vm.ToValue(filename), rt.vm.ToValue(dir)); err != nil {
		delete(rt.modCache, filename)
		return nil, errors2(err)
	}
	ne := moduleObj.Get("exports")
	mod.exports = ne
	mod.loaded = true
	moduleObj.Set("loaded", true)
	return ne, nil
}

func errors2(err error) error {
	return fmt.Errorf("%s", rtFormat(err))
}

func rtFormat(err error) string {
	if ex, ok := err.(*goja.Exception); ok {
		return ex.String()
	}
	return err.Error()
}

// runBuiltin loads an embedded JS builtin module by name.
func (rt *Runtime) runBuiltin(name string) (goja.Value, error) {
	key := "@" + name
	rt.mu.Lock()
	if m, ok := rt.modCache[key]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()
	src, err := readBuiltin(name)
	if err != nil {
		return nil, err
	}
	return rt.loadModuleText(key, src, rt.mainDir, false)
}

func isBuiltin(name string) bool {
	_, ok := builtinNames()[name]
	return ok
}
