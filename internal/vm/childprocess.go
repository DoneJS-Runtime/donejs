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

	"donejs/internal/jit"
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
func (rt *Runtime) denyExec() *jit.ThrowError {
	ex := rt.jsThrow(errExecDenied)
	ex.Value.Set("code", "ERR_EXEC_DENIED")
	ex.Value.Set("errno", int64(-1))
	ex.Value.Set("syscall", "exec")
	return ex
}

func (rt *Runtime) registerChildProcess() {
	mod := jit.Object()

	mod.Set("exec", jit.BuiltinFn("exec", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		cmdStr := argStr(args, 0)
		opts := argObj(args, 1)
		cb, hasCB := splitCB(args)
		if !hasCB {
			panic(jit.Err("TypeError", "callback required"))
		}
		return rt.spawnShell(cmdStr, opts, cb, true)
	}))

	mod.Set("execFile", jit.BuiltinFn("execFile", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := argStr(args, 0)
		fileArgs, opts := splitArgsOpts(args[1:])
		cb, hasCB := splitCB(args)
		if !hasCB {
			panic(jit.Err("TypeError", "callback required"))
		}
		return rt.spawnDirect(file, fileArgs, opts, cb, true)
	}))

	mod.Set("spawn", jit.BuiltinFn("spawn", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := argStr(args, 0)
		fileArgs, opts := splitArgsOpts(args[1:])
		return rt.spawnDirect(file, fileArgs, opts, jit.Undef(), false)
	}))

	mod.Set("execSync", jit.BuiltinFn("execSync", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		cmdStr := argStr(args, 0)
		opts := argObj(args, 1)
		out, err := rt.runShellSync(cmdStr, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	}))

	mod.Set("execFileSync", jit.BuiltinFn("execFileSync", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := argStr(args, 0)
		fileArgs, opts := splitArgsOpts(args[1:])
		out, err := rt.runDirectSync(file, fileArgs, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	}))

	mod.Set("spawnSync", jit.BuiltinFn("spawnSync", func(args []jit.Value) jit.Value {
		if err := rt.checkExecAllowed(); err != nil {
			panic(rt.denyExec())
		}
		file := argStr(args, 0)
		fileArgs, opts := splitArgsOpts(args[1:])
		out, err := rt.runDirectSync(file, fileArgs, opts)
		if err != nil {
			panic(rt.denyExec())
		}
		return rt.toJS(out)
	}))

	mod.Set("allowed", jit.BuiltinFn("allowed", func(args []jit.Value) jit.Value {
		return jit.Bool(rt.allowExec)
	}))

	rt.natives["__child_process"] = mod
}

func (rt *Runtime) checkExecAllowed() error {
	if rt.allowExec {
		return nil
	}
	return errExecDenied
}

func stringArgs(vals []jit.Value) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.String())
	}
	return out
}

// splitArgsOpts separates positional args from a trailing options object.
// Node allows execFile(file, [args], [options]).
func splitArgsOpts(vals []jit.Value) ([]string, jit.Value) {
	var args []string
	var opts jit.Value = jit.Undef()
	if len(vals) > 0 {
		if last := vals[len(vals)-1]; last.IsObject() {
			opts = last
			vals = vals[:len(vals)-1]
		}
	}
	for _, v := range vals {
		args = append(args, v.String())
	}
	return args, opts
}

// spawnShell runs a shell command and delivers stdout/stderr to the callback.
func (rt *Runtime) spawnShell(cmdStr string, opts jit.Value, cb jit.Value, capture bool) jit.Value {
	shell := "/bin/sh"
	arg := "-c"
	if rt.execPolicy != nil && rt.execPolicy.forced {
		arg = "-c"
	}
	cmd := exec.Command(shell, arg, cmdStr)
	rt.applySpawnOpts(cmd, opts)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(rt.jsThrow(err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		panic(rt.jsThrow(err))
	}
	if err := cmd.Start(); err != nil {
		panic(rt.jsThrow(err))
	}
	rt.ref()
	go func() {
		outBytes, _ := readAllLines(stdout)
		errBytes, _ := readAllLines(stderr)
		werr := cmd.Wait()
		rt.unref()
		rt.Enqueue(func() {
			if f := cb; f.IsCallable() {
				code := 0
				if werr != nil {
					var ee *exec.ExitError
					if errors.As(werr, &ee) {
						code = ee.ExitCode()
					} else {
						code = 1
					}
				}
				errObj := jit.Null()
				if werr != nil {
					errObj = rt.jsThrow(werr).Value
				}
				if _, e := rt.jitCall(f, jit.Undef(), errObj, rt.toJS(outBytes), rt.toJS(errBytes), rt.toJS(code)); e != nil {
					rt.reportUncaught(e)
				}
			}
		})
	}()
	return jit.Undef()
}

// spawnDirect runs an executable without a shell.
func (rt *Runtime) spawnDirect(file string, args []string, opts jit.Value, cb jit.Value, capture bool) jit.Value {
	cmd := exec.Command(file, args...)
	rt.applySpawnOpts(cmd, opts)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(rt.jsThrow(err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		panic(rt.jsThrow(err))
	}
	stdinPipe, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		panic(rt.jsThrow(err))
	}
	rt.ref()

	child, childEmitter := rt.newEmitterObject()
	child.Set("pid", cmd.Process.Pid)
	child.Set("stdin", rt.stdinObject(stdinPipe))
	child.Set("stdout", rt.stdoutObject(stdout))
	child.Set("stderr", rt.stderrObject(stderr))
	child.Set("kill", jit.BuiltinFn("kill", func(args []jit.Value) jit.Value {
		sig := syscall.SIGTERM
		if len(args) > 0 {
			if name, ok := argsAt(args, 0).Export().(string); ok {
				if n, err := strconv.Atoi(strings.TrimPrefix(name, "SIG")); err == nil {
					sig = syscall.Signal(n)
				}
			}
		}
		_ = cmd.Process.Signal(sig)
		return jit.Bool(true)
	}))

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
			rt.emitEvent(child, childEmitter, "close", rt.toJS(code))
			if cb.IsCallable() {
				errObj := jit.Null()
				if werr != nil {
					errObj = rt.jsThrow(werr).Value
				}
				if _, e := rt.jitCall(cb, jit.Undef(), errObj, rt.toJS(outBytes), rt.toJS(errBytes), rt.toJS(code)); e != nil {
					rt.reportUncaught(e)
				}
			}
		})
	}()
	return child
}

func (rt *Runtime) applySpawnOpts(cmd *exec.Cmd, opts jit.Value) {
	if opts.IsUndef() {
		return
	}
	if cwd := optStr(opts, "cwd", ""); cwd != "" {
		cmd.Dir = cwd
	}
	if envV := opts.Get("env"); envV.AsObj() != nil {
		envMap := map[string]string{}
		for _, k := range envV.Keys() {
			envMap[k] = envV.Get(k).String()
		}
		cmd.Env = make([]string, 0, len(envMap))
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
}

func (rt *Runtime) runShellSync(cmdStr string, opts jit.Value) (any, error) {
	cmd := exec.Command("/bin/sh", "-c", cmdStr)
	rt.applySpawnOpts(cmd, opts)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func (rt *Runtime) runDirectSync(file string, args []string, opts jit.Value) (any, error) {
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

func (rt *Runtime) stdinObject(w interface{ Write([]byte) (int, error) }) jit.Value {
	obj := jit.Object()
	obj.Set("write", jit.BuiltinFn("write", func(args []jit.Value) jit.Value {
		if w == nil {
			return jit.Bool(true)
		}
		data := rt.argBytes(args, 0)
		go func() { _, _ = w.Write(data) }()
		return jit.Bool(true)
	}))
	obj.Set("end", jit.BuiltinFn("end", func(args []jit.Value) jit.Value { return obj }))
	return obj
}

func (rt *Runtime) stdoutObject(r interface{ Read([]byte) (int, error) }) jit.Value {
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

func (rt *Runtime) stderrObject(r interface{ Read([]byte) (int, error) }) jit.Value {
	return rt.stdoutObject(r)
}

var (
	_ = bufio.NewReader
	_ = filepath.Base
	_ = os.Getenv
	_ = strings.TrimSpace
)
