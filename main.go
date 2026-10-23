package main

import (
	"fmt"
	"os"
	"strings"

	"donejs/internal/vm"
)

const version = "0.1.0"

func usage() {
	fmt.Fprint(os.Stderr, `donejs - a fast Node.js-compatible runtime written in Go

Usage:
  donejs [options] <script.js> [arguments...]
  donejs [options] -e "<code>" [arguments...]

Options:
  -e, --eval <code>     Evaluate JavaScript code
  --allow-exec          Permit child_process to spawn executables (denied by default)
  --selftest            Run the built-in runtime self test
  -v, --version         Print version
  -h, --help            Show this help
`)
}

func main() {
	var (
		evalCode  string
		allowExec bool
		selftest  bool
		showVer   bool
	)
	args := os.Args[1:]
	var script string
	var scriptArgs []string
	var evalMode bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--eval":
			i++
			if i < len(args) {
				evalCode = args[i]
			}
			evalMode = true
			scriptArgs = args[i+1:]
			i = len(args)
		case strings.HasPrefix(a, "-e") && len(a) > 2:
			evalCode = a[2:]
			evalMode = true
			scriptArgs = args[i+1:]
			i = len(args)
		case a == "--allow-exec":
			allowExec = true
		case a == "--selftest":
			selftest = true
		case a == "-v" || a == "--version":
			showVer = true
		case a == "-h" || a == "--help":
			usage()
			return
		case !evalMode && script == "" && !strings.HasPrefix(a, "-"):
			script = a
			scriptArgs = args[i+1:]
			i = len(args)
		default:
			fmt.Fprintf(os.Stderr, "donejs: unknown option %q\n", a)
			os.Exit(2)
		}
	}

	if showVer {
		fmt.Printf("donejs v%s (%s)\n", version, vm.EngineVersion())
		return
	}
	if selftest {
		if err := vm.Selftest(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "selftest FAILED:", err)
			os.Exit(1)
		}
		return
	}
	if evalCode == "" && script == "" {
		usage()
		os.Exit(2)
	}

	code, err := vm.Run(vm.Options{
		Script:    script,
		Eval:      evalCode,
		Args:      scriptArgs,
		AllowExec: allowExec,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
