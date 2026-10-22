package vm

import (
	"bufio"
	"os"
	"os/user"
	"runtime"
	"strings"
	"unsafe"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerOS() {
	mod := rt.vm.NewObject()

	sync := opTable{
		"platform": func(args []goja.Value) (any, error) { return runtime.GOOS, nil },
		"arch":     func(args []goja.Value) (any, error) { return runtime.GOARCH, nil },
		"type":     func(args []goja.Value) (any, error) { return runtime.GOOS, nil },
		"endianness": func(args []goja.Value) (any, error) {
			x := uint16(1)
			b := (*[2]byte)(unsafe.Pointer(&x))
			if b[0] == 1 {
				return "LE", nil
			}
			return "BE", nil
		},
		"hostname": func(args []goja.Value) (any, error) {
			h, err := os.Hostname()
			return h, err
		},
		"homedir": func(args []goja.Value) (any, error) {
			if h, err := os.UserHomeDir(); err == nil {
				return h, nil
			}
			if u, err := user.Current(); err == nil {
				return u.HomeDir, nil
			}
			return "", nil
		},
		"tmpdir": func(args []goja.Value) (any, error) { return os.TempDir(), nil },
		"release": func(args []goja.Value) (any, error) {
			return strings.TrimSpace(hostRelease()), nil
		},
		"uptime": func(args []goja.Value) (any, error) {
			return nowSeconds() - rt.startSeconds(), nil
		},
		"freemem": func(args []goja.Value) (any, error) { return int64(memStats().Sys - memStats().HeapInuse), nil },
		"totalmem": func(args []goja.Value) (any, error) { return int64(memStats().Sys), nil },
		"cpus": func(args []goja.Value) (any, error) {
			out := make([]any, 0, runtime.NumCPU())
			for i := 0; i < runtime.NumCPU(); i++ {
				out = append(out, map[string]any{
					"model": "unknown", "speed": 0,
					"times": map[string]any{"user": 0, "nice": 0, "sys": 0, "idle": 0, "irq": 0},
				})
			}
			return out, nil
		},
		"userInfo": func(args []goja.Value) (any, error) {
			name := ""
			home := ""
			shell := ""
			if u, err := user.Current(); err == nil {
				name = u.Username
				home = u.HomeDir
			}
			return map[string]any{"username": name, "homedir": home, "shell": shell}, nil
		},
		"networkInterfaces": func(args []goja.Value) (any, error) { return map[string]any{}, nil },
		"loadavg": func(args []goja.Value) (any, error) {
			return []float64{0, 0, 0}, nil
		},
	}

	mod.Set("sync", rt.syncDispatch(sync))
	mod.Set("async", rt.asyncDispatch(sync))
	rt.natives["__os"] = mod
}

func hostRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return string(b)
}

var _ = bufio.NewReader
