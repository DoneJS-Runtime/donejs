package vm

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/dop251/goja"
)

// execPolicy decides whether a child process may be spawned.
//
// donejs blocks child_process by default: spawning an executable is a
// privilege boundary, so it requires an explicit opt-in (--allow-exec or
// DONEJS_ALLOW_EXEC=1).
type execPolicy struct {
	allowed bool
	forced  bool
}

var errExecDenied = errors.New("child_process is disabled in donejs: spawn executables require --allow-exec")

// denyExec builds a JS error carrying the ERR_EXEC_DENIED code.
func (rt *Runtime) denyExec() goja.Value {
	ex := rt.vm.NewGoError(errExecDenied)
	ex.Set("code", "ERR_EXEC_DENIED")
	ex.Set("errno", -1)
	ex.Set("syscall", "exec")
	return ex
}

func (rt *Runtime) registerChildProcess() {
	mod := rt.vm.NewObject()

	mod.Set("exec", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		cmdStr := call.Argument(0).String()
		opts := argObj([]goja.Value{call.Argument(1)}, 0)
		cb, hasCB := splitCB(call)
		if !hasCB {
			panic(rt.vm.NewTypeError("callback required"))
		}
		return rt.spawnShell(cmdStr, opts, cb, true)
	})

	mod.Set("execFile", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := call.Argument(0).String()
		args, opts := splitArgsOpts(call.Arguments[1:])
		cb, hasCB := splitCB(call)
		if !hasCB {
			panic(rt.vm.NewTypeError("callback required"))
		}
		return rt.spawnDirect(file, args, opts, cb, true)
	})

	mod.Set("spawn", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := call.Argument(0).String()
		args, opts := splitArgsOpts(call.Arguments[1:])
		return rt.spawnDirect(file, args, opts, nil, false)
	})

	mod.Set("execSync", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		cmdStr := call.Argument(0).String()
		opts := argObj([]goja.Value{call.Argument(1)}, 0)
		out, err := rt.runShellSync(cmdStr, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	})

	mod.Set("execFileSync", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := call.Argument(0).String()
		args, opts := splitArgsOpts(call.Arguments[1:])
		out, err := rt.runDirectSync(file, args, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	})

	mod.Set("spawnSync", func(call goja.FunctionCall) goja.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := call.Argument(0).String()
		args, opts := splitArgsOpts(call.Arguments[1:])
		out, err := rt.runDirectSync(file, args, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	})

	mod.Set("allowed", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(rt.allowExec)
	})

	rt.natives["__child_process"] = mod
}

func (rt *Runtime) checkExecAllowed() error {
	if rt.allowExec {
		return nil
	}
	return errExecDenied
}

func stringArgs(vals []goja.Value) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.String())
	}
	return out
}

// splitArgsOpts separates positional args from a trailing options object.
// Node allows execFile(file, [args], [options]).
func splitArgsOpts(vals []goja.Value) ([]string, *goja.Object) {
	var args []string
	var opts *goja.Object
	if len(vals) > 0 {
		if o, ok := vals[len(vals)-1].(*goja.Object); ok && o.ClassName() == "Object" {
			opts = o
			vals = vals[:len(vals)-1]
		}
	}
	for _, v := range vals {
		if _, ok := v.Export().(string); ok {
			args = append(args, v.String())
		} else {
			args = append(args, v.String())
		}
	}
	return args, opts
}

// spawnShell runs a shell command and delivers stdout/stderr to the callback.
func (rt *Runtime) spawnShell(cmdStr string, opts *goja.Object, cb goja.Value, capture bool) goja.Value {
	shell := "/bin/sh"
	arg := "-c"
	if rt.execPolicy != nil && rt.execPolicy.forced {
		arg = "-c"
	}
	cmd := exec.Command(shell, arg, cmdStr)
	rt.applySpawnOpts(cmd, opts)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	if err := cmd.Start(); err != nil {
		panic(rt.vm.NewGoError(err))
	}
	rt.ref()
	go func() {
		outBytes, _ := readAllLines(stdout)
		errBytes, _ := readAllLines(stderr)
		werr := cmd.Wait()
		rt.unref()
		rt.Enqueue(func() {
			if f, ok := goja.AssertFunction(cb); ok {
				code := 0
				if werr != nil {
					var ee *exec.ExitError
					if errors.As(werr, &ee) {
						code = ee.ExitCode()
					} else {
						code = 1
					}
				}
				errObj := goja.Null()
				if werr != nil {
					errObj = rt.jsError(werr)
				}
				f(goja.Undefined(), errObj, rt.toJS(outBytes), rt.toJS(errBytes), rt.vm.ToValue(code))
			}
		})
	}()
	return goja.Undefined()
}

// spawnDirect runs an executable without a shell.
func (rt *Runtime) spawnDirect(file string, args []string, opts *goja.Object, cb goja.Value, capture bool) goja.Value {
	cmd := exec.Command(file, args...)
	rt.applySpawnOpts(cmd, opts)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		panic(rt.vm.NewGoError(err))
	}
	stdinPipe, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		panic(rt.vm.NewGoError(err))
	}
	rt.ref()

	child, childEmitter := rt.newEmitterObject()
	child.Set("pid", cmd.Process.Pid)
	child.Set("stdin", rt.stdinObject(stdinPipe))
	child.Set("stdout", rt.stdoutObject(stdout))
	child.Set("stderr", rt.stderrObject(stderr))
	child.Set("kill", func(c goja.FunctionCall) goja.Value {
		sig := syscall.SIGTERM
		if len(c.Arguments) > 0 {
			if name, ok := c.Argument(0).Export().(string); ok {
				if n, err := strconv.Atoi(strings.TrimPrefix(name, "SIG")); err == nil {
					sig = syscall.Signal(n)
				}
			}
		}
		_ = cmd.Process.Signal(sig)
		return rt.vm.ToValue(true)
	})

	go func() {
		outBytes, _ := readAllLines(stdout)
		errBytes, _ := readAllLines(stderr)
		werr := cmd.Wait()
		rt.unref()
		code := 0
		if werr != nil {
			var ee *exec.ExitError
			if errors.As(werr, &ee) {
				code = ee.ExitCode()
			} else {
				code = 1
			}
		}
		rt.Enqueue(func() {
			rt.emitEvent(child, childEmitter, "close", rt.vm.ToValue(code))
			if cb != nil {
				if f, ok := goja.AssertFunction(cb); ok {
					errObj := goja.Null()
					if werr != nil {
						errObj = rt.jsError(werr)
					}
					f(goja.Undefined(), errObj, rt.toJS(outBytes), rt.toJS(errBytes), rt.vm.ToValue(code))
				}
			}
		})
	}()
	return child
}

func (rt *Runtime) applySpawnOpts(cmd *exec.Cmd, opts *goja.Object) {
	if opts == nil {
		return
	}
	if cwd := optStr(opts, "cwd", ""); cwd != "" {
		cmd.Dir = cwd
	}
	if env := opts.Get("env"); env != nil {
		if eo, ok := env.(*goja.Object); ok {
			envMap := map[string]string{}
			for _, k := range eo.Keys() {
				envMap[k] = eo.Get(k).String()
			}
			cmd.Env = make([]string, 0, len(envMap))
			for k, v := range envMap {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}
}

func (rt *Runtime) runShellSync(cmdStr string, opts *goja.Object) (any, error) {
	cmd := exec.Command("/bin/sh", "-c", cmdStr)
	rt.applySpawnOpts(cmd, opts)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func (rt *Runtime) runDirectSync(file string, args []string, opts *goja.Object) (any, error) {
	cmd := exec.Command(file, args...)
	rt.applySpawnOpts(cmd, opts)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func readAllLines(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, nil
		}
	}
}

func (rt *Runtime) stdinObject(w interface{ Write([]byte) (int, error) }) goja.Value {
	obj := rt.vm.NewObject()
	obj.Set("write", func(c goja.FunctionCall) goja.Value {
		if w == nil {
			return rt.vm.ToValue(true)
		}
		data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
		go func() { _, _ = w.Write(data) }()
		return rt.vm.ToValue(true)
	})
	obj.Set("end", func(c goja.FunctionCall) goja.Value { return obj })
	return obj
}

func (rt *Runtime) stdoutObject(r interface{ Read([]byte) (int, error) }) goja.Value {
	obj, emitter := rt.newEmitterObject()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				rt.Enqueue(func() { rt.emitEvent(obj, emitter, "data", rt.toJS(binResult(chunk))) })
			}
			if err != nil {
				rt.Enqueue(func() { rt.emitEvent(obj, emitter, "end") })
				return
			}
		}
	}()
	return obj
}

func (rt *Runtime) stderrObject(r interface{ Read([]byte) (int, error) }) goja.Value {
	return rt.stdoutObject(r)
}

var (
	_ = bufio.NewReader
	_ = filepath.Base
	_ = os.Getenv
	_ = strings.TrimSpace
)
