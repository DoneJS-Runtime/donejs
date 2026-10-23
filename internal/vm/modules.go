package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"donejs/internal/jit"
)

type jsModule struct {
	exports jit.Value
	loaded  bool
}

var nodePrefix = func(s string) string { return strings.TrimPrefix(s, "node:") }

// requireFrom implements CommonJS resolution starting at dir.
func (rt *Runtime) requireFrom(dir string, spec string) (jit.Value, error) {
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
		return jit.Undef(), fmt.Errorf("Cannot find module '%s'", spec)
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
	return jit.Undef(), fmt.Errorf("Cannot find module '%s'", spec)
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

func (rt *Runtime) loadModule(path string, flush bool) (jit.Value, error) {
	path = filepath.Clean(path)
	rt.mu.Lock()
	if m, ok := rt.modCache[path]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return jit.Undef(), err
	}
	return rt.loadModuleText(path, string(data), filepath.Dir(path), flush)
}

func (rt *Runtime) loadModuleText(filename, src, dir string, flush bool) (jit.Value, error) {
	rt.mu.Lock()
	if m, ok := rt.modCache[filename]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()

	mod := &jsModule{exports: jit.Object()}
	rt.mu.Lock()
	rt.modCache[filename] = mod
	rt.mu.Unlock()

	if strings.HasSuffix(filename, ".json") {
		var v any
		if err := json.Unmarshal([]byte(src), &v); err != nil {
			return jit.Undef(), fmt.Errorf("%s: %w", filename, err)
		}
		mod.exports = jit.FromGo(v)
		mod.loaded = true
		return mod.exports, nil
	}

	wrapped := "(function (exports, require, module, __filename, __dirname) {" + src + "\n;__donejsFlushTicks();\n})"
	prg, err := rt.compile(filename, wrapped)
	if err != nil {
		return jit.Undef(), fmt.Errorf("%s: %w", filename, err)
	}

	moduleObj := jit.Object()
	moduleObj.Set("exports", mod.exports)
	moduleObj.Set("id", filename)
	if flush {
		moduleObj.Set("id", ".")
	}
	moduleObj.Set("filename", filename)
	moduleObj.Set("loaded", jit.Bool(false))
	moduleObj.Set("paths", jit.FromGo(rt.modulePaths(dir)))
	moduleObj.Set("children", jit.ArrOf())
	var parent jit.Value = jit.Null()
	if n := len(rt.modStack); n > 0 {
		parent = rt.modStack[n-1]
		if parent.AsObj() != nil {
			if kids := parent.Get("children"); kids.AsArr() != nil {
				kids.Set(strconv.Itoa(int(kids.Get("length").ToInteger())), moduleObj)
			}
		}
	}
	moduleObj.Set("parent", parent)
	if flush {
		rt.mainModule = moduleObj
	}

	// The module is the parent of anything it requires while its body runs.
	rt.modStack = append(rt.modStack, moduleObj)
	defer func() { rt.modStack = rt.modStack[:len(rt.modStack)-1] }()

	reqFn := func(args []jit.Value) jit.Value {
		if len(args) == 0 {
			panic(jit.Err("TypeError", "require specifier required"))
		}
		v, err := rt.requireFrom(dir, argsAt(args, 0).String())
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return v
	}
	reqVal := jit.Host(&hostFunc{name: "require", call: reqFn})
	rt.jit.SetGlobal("__donejsRequire", reqVal)
	moduleObj.Set("require", reqVal)
	reqVal.Set("main", rt.mainModule)

	fn, err := rt.runModuleFn(prg)
	if err != nil {
		delete(rt.modCache, filename)
		return jit.Undef(), err
	}
	ret, err := rt.jit.CallBind(fn, jit.ObjVal(jit.NewObj()), moduleObj, reqVal, moduleObj, jit.Str(filename), jit.Str(dir))
	if err != nil {
		delete(rt.modCache, filename)
		return jit.Undef(), err
	}
	_ = ret
	ne := moduleObj.Get("exports")
	mod.exports = ne
	mod.loaded = true
	moduleObj.Set("loaded", jit.Bool(true))
	return ne, nil
}

// runModuleFn executes a compiled module wrapper and returns the exported
// value.
func (rt *Runtime) runModuleFn(prg *jit.FuncProto) (jit.Value, error) {
	ret, err := rt.jit.CallProtoErr(prg)
	if err != nil {
		return jit.Undef(), errors.New(rt.formatErr(errToValue(err)))
	}
	if !ret.IsCallable() {
		return jit.Undef(), fmt.Errorf("module wrapper did not produce a function")
	}
	return ret, nil
}

func errors2(err error) error {
	return fmt.Errorf("%s", rtFormat(err))
}

func rtFormat(err error) string {
	if ex, ok := err.(*jit.ThrowError); ok {
		return ex.Error()
	}
	return err.Error()
}

// runBuiltin loads an embedded JS builtin module by name.
func (rt *Runtime) runBuiltin(name string) (jit.Value, error) {
	key := "@" + name
	rt.mu.Lock()
	if m, ok := rt.modCache[key]; ok {
		rt.mu.Unlock()
		return m.exports, nil
	}
	rt.mu.Unlock()
	src, err := readBuiltin(name)
	if err != nil {
		return jit.Undef(), err
	}
	return rt.loadModuleText(key, src, rt.mainDir, false)
}

func isBuiltin(name string) bool {
	_, ok := builtinNames()[name]
	return ok
}
