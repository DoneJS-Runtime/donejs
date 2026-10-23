package vm

import (
	"path"
	"strings"

	"donejs/internal/jit"
)

// registerPath exposes POSIX and Windows path semantics natively.
func (rt *Runtime) registerPath() {
	mod := jit.Object()

	posix := opTable{
		"normalize":        func(a []jit.Value) (any, error) { return path.Clean(argStr(a, 0)), nil },
		"join":             func(a []jit.Value) (any, error) { return path.Join(stringsOf(a, 0)...), nil },
		"resolve":          func(a []jit.Value) (any, error) { return pathResolve(a), nil },
		"dirname":          func(a []jit.Value) (any, error) { return path.Dir(argStr(a, 0)), nil },
		"basename":         func(a []jit.Value) (any, error) { return path.Base(argStr(a, 0)), nil },
		"extname":          func(a []jit.Value) (any, error) { return path.Ext(argStr(a, 0)), nil },
		"relative":         func(a []jit.Value) (any, error) { return pathRelative(a), nil },
		"isAbsolute":       func(a []jit.Value) (any, error) { return path.IsAbs(argStr(a, 0)), nil },
		"format":           func(a []jit.Value) (any, error) { return pathFormat(argObj(a, 0)), nil },
		"toNamespacedPath": func(a []jit.Value) (any, error) { return argStr(a, 0), nil },
	}

	win := opTable{
		"normalize":        func(a []jit.Value) (any, error) { return winNormalize(argStr(a, 0)), nil },
		"join":             func(a []jit.Value) (any, error) { return winJoin(stringsOf(a, 0)), nil },
		"resolve":          func(a []jit.Value) (any, error) { return winResolve(stringsOf(a, 0)), nil },
		"dirname":          func(a []jit.Value) (any, error) { return winDirname(argStr(a, 0)), nil },
		"basename":         func(a []jit.Value) (any, error) { return winBase(argStr(a, 0)), nil },
		"extname":          func(a []jit.Value) (any, error) { return winExtname(argStr(a, 0)), nil },
		"relative":         func(a []jit.Value) (any, error) { return winRelative(argStr(a, 0), argStr(a, 1)), nil },
		"isAbsolute":       func(a []jit.Value) (any, error) { return winIsAbs(argStr(a, 0)), nil },
		"format":           func(a []jit.Value) (any, error) { return pathFormat(argObj(a, 0)), nil },
		"toNamespacedPath": func(a []jit.Value) (any, error) { return argStr(a, 0), nil },
	}

	mod.Set("posix", rt.syncDispatch(posix))
	mod.Set("win32", rt.syncDispatch(win))
	rt.natives["__path"] = mod
}

func stringsOf(args []jit.Value, from int) []string {
	out := make([]string, 0, len(args)-from)
	for i := from; i < len(args); i++ {
		out = append(out, args[i].String())
	}
	return out
}

func pathResolve(args []jit.Value) string {
	if len(args) == 0 {
		return ""
	}
	parts := stringsOf(args, 0)
	cwd, _ := osGetwd()
	joined := path.Join(parts...)
	if path.IsAbs(joined) {
		return path.Clean(joined)
	}
	return path.Clean(path.Join(cwd, joined))
}

func pathRelative(args []jit.Value) string {
	from := argStr(args, 0)
	to := argStr(args, 1)
	if from == "" {
		from = "."
	}
	if to == "" {
		to = "."
	}
	cwd, _ := osGetwd()
	fromAbs := from
	if !path.IsAbs(fromAbs) {
		fromAbs = path.Clean(path.Join(cwd, from))
	}
	toAbs := to
	if !path.IsAbs(toAbs) {
		toAbs = path.Clean(path.Join(cwd, to))
	}
	return pathRelativeSlash(fromAbs, toAbs)
}

func pathRelativeSlash(from, to string) string {
	fromParts := strings.Split(path.Clean(from), "/")
	toParts := strings.Split(path.Clean(to), "/")
	i := 0
	for i < len(fromParts) && i < len(toParts) && fromParts[i] == toParts[i] {
		i++
	}
	ups := make([]string, 0, len(fromParts)-i)
	for j := i; j < len(fromParts); j++ {
		if fromParts[j] != "" {
			ups = append(ups, "..")
		}
	}
	rest := toParts[i:]
	for _, r := range rest {
		if r != "" {
			ups = append(ups, r)
		}
	}
	if len(ups) == 0 {
		return ""
	}
	return strings.Join(ups, "/")
}

// --- win32 path semantics ------------------------------------------------
// These are implemented independently of the host OS so path.win32 behaves
// the same on Linux, macOS and Windows.

// winSplitDrive splits "C:\foo" into ("C:", "\foo").
func winSplitDrive(p string) (drive, rest string) {
	if len(p) >= 2 && p[1] == ':' && isAlpha(p[0]) {
		return p[:2], p[2:]
	}
	return "", p
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// winNormalize implements path.win32.normalize.
func winNormalize(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	drive, rest := winSplitDrive(p)
	rooted := strings.HasPrefix(rest, `\`)
	out := make([]string, 0, 8)
	for _, seg := range strings.Split(rest, `\`) {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 && out[len(out)-1] != ".." {
				out = out[:len(out)-1]
			} else if !rooted {
				out = append(out, "..")
			}
		default:
			out = append(out, seg)
		}
	}
	body := strings.Join(out, `\`)
	switch {
	case drive != "" && rooted:
		return drive + `\` + body
	case drive != "":
		if body == "" {
			return drive + "."
		}
		return drive + `\` + body
	case rooted:
		return `\` + body
	case body == "":
		return "."
	}
	return body
}

// winJoin implements path.win32.join.
func winJoin(parts []string) string {
	joined := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if joined == "" {
			joined = p
			continue
		}
		joined += `\` + p
	}
	if joined == "" {
		return "."
	}
	return winNormalize(joined)
}

// winDirname implements path.win32.dirname.
func winDirname(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	drive, rest := winSplitDrive(p)
	i := strings.LastIndex(rest, `\`)
	if i < 0 {
		if drive != "" {
			return drive + `\`
		}
		return "."
	}
	dir := rest[:i+1]
	if drive == "" {
		if dir == `\` {
			return `\`
		}
		return dir
	}
	return drive + dir
}

// winBase implements path.win32.basename.
func winBase(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// winExtname implements path.win32.extname.
func winExtname(p string) string {
	base := winBase(p)
	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return ""
	}
	return base[i:]
}

// winIsAbs implements path.win32.isAbsolute.
func winIsAbs(p string) bool {
	p = strings.ReplaceAll(p, "/", `\`)
	drive, rest := winSplitDrive(p)
	return drive != "" || strings.HasPrefix(rest, `\`)
}

// winResolve implements path.win32.resolve.
func winResolve(parts []string) string {
	if len(parts) == 0 {
		cwd, _ := osGetwd()
		return winNormalize(cwd)
	}
	joined := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if joined == "" {
			joined = p
			continue
		}
		joined += `\` + p
	}
	if drive, _ := winSplitDrive(joined); drive == "" {
		cwd, _ := osGetwd()
		if strings.HasPrefix(strings.ReplaceAll(joined, "/", `\`), `\`) {
			if len(cwd) >= 2 && cwd[1] == ':' {
				joined = cwd[:2] + joined
			} else {
				joined = `\` + strings.TrimPrefix(winNormalize(cwd), `\`)
			}
		} else {
			joined = winNormalize(cwd) + `\` + joined
		}
	}
	return winNormalize(joined)
}

// winRelative implements path.win32.relative.
func winRelative(from, to string) string {
	if from == "" {
		from = "."
	}
	if to == "" {
		to = "."
	}
	fromAbs := winResolve([]string{from})
	toAbs := winResolve([]string{to})
	if strings.EqualFold(fromAbs, toAbs) {
		return ""
	}
	fromParts := strings.Split(strings.TrimPrefix(fromAbs, `\`), `\`)
	toParts := strings.Split(strings.TrimPrefix(toAbs, `\`), `\`)
	i := 0
	for i < len(fromParts) && i < len(toParts) && strings.EqualFold(fromParts[i], toParts[i]) {
		i++
	}
	out := make([]string, 0, len(fromParts)-i+1)
	for j := i; j < len(fromParts); j++ {
		if fromParts[j] != "" {
			out = append(out, "..")
		}
	}
	out = append(out, toParts[i:]...)
	return strings.Join(out, `\`)
}

func pathRoot(p string) string {
	if p == "" {
		return ""
	}
	if path.IsAbs(p) {
		return "/"
	}
	win := strings.Index(p, "\\")
	unix := strings.Index(p, "/")
	switch {
	case win >= 0 && (unix < 0 || win < unix):
		return p[:win+1]
	case unix >= 0:
		return p[:unix+1]
	}
	return ""
}

func pathFormat(o jit.Value) string {
	if o.IsUndef() {
		return ""
	}
	dir := optStr(o, "dir", "")
	root := optStr(o, "root", "")
	base := optStr(o, "base", "")
	ext := optStr(o, "ext", "")
	name := optStr(o, "name", "")
	if base == "" {
		if name != "" {
			base = name + ext
		} else {
			base = ext
		}
	}
	if root != "" && dir == "" {
		return root + base
	}
	if dir == "" {
		return base
	}
	return strings.TrimSuffix(dir, "/") + "/" + base
}
