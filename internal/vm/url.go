package vm

import (
	"net/url"
	"strings"

	"donejs/internal/jit"
)

func (rt *Runtime) registerURL() {
	mod := jit.Object()

	// parse returns the WHATWG-ish components of a URL.
	setFn(mod, "parse", func(args []jit.Value) jit.Value {
		raw := argsAt(args, 0).String()
		base := ""
		if len(args) > 1 && !args[1].IsUndef() {
			base = args[1].String()
		}
		key := raw + "\x00" + base
		if v, ok := rt.urlCache[key]; ok {
			return v
		}
		u, err := url.Parse(raw)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		if base != "" {
			b, err := url.Parse(base)
			if err != nil {
				panic(rt.jsThrow(err))
			}
			u = b.ResolveReference(u)
		}
		v := rt.toJS(map[string]any{
			"href":     u.String(),
			"protocol": u.Scheme + ":",
			"host":     u.Host,
			"hostname": u.Hostname(),
			"port":     u.Port(),
			"pathname": u.Path,
			"search":   searchOf(u),
			"hash":     hashOf(u),
			"username": u.User.Username(),
			"password": passwordOf(u),
			"origin":   originOf(u),
			"hostName": u.Hostname(),
			"path":     u.Path,
		})
		rt.urlCache[key] = v
		return v
	})

	// resolve implements new URL(relative, base) semantics.
	setFn(mod, "resolve", func(args []jit.Value) jit.Value {
		rel := argsAt(args, 0).String()
		base := ""
		if len(args) > 1 && !args[1].IsUndef() {
			base = args[1].String()
		}
		b, err := url.Parse(base)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		r, err := url.Parse(rel)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		return jit.Str(b.ResolveReference(r).String())
	})

	setFn(mod, "domainToASCII", func(args []jit.Value) jit.Value {
		return jit.Str(argsAt(args, 0).String())
	})
	setFn(mod, "domainToUnicode", func(args []jit.Value) jit.Value {
		return jit.Str(argsAt(args, 0).String())
	})
	setFn(mod, "fileURLToPath", func(args []jit.Value) jit.Value {
		u, err := url.Parse(argsAt(args, 0).String())
		if err != nil {
			panic(rt.jsThrow(err))
		}
		if u.Scheme != "file" {
			panic(rt.jsThrow(&url.Error{Op: "fileURLToPath", URL: u.String(), Err: errNotFileScheme}))
		}
		return jit.Str(u.Path)
	})
	setFn(mod, "pathToFileURL", func(args []jit.Value) jit.Value {
		p := argsAt(args, 0).String()
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		u := &url.URL{Scheme: "file", Path: p}
		return jit.Str(u.String())
	})

	rt.natives["__url"] = mod
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const errNotFileScheme = simpleError("The URL must be of scheme file")

func searchOf(u *url.URL) string {
	if u.RawQuery == "" && u.ForceQuery {
		return "?"
	}
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

func hashOf(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	return "#" + u.EscapedFragment()
}

func passwordOf(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	p, _ := u.User.Password()
	return p
}

func originOf(u *url.URL) string {
	switch u.Scheme {
	case "http", "https", "ws", "wss", "ftp":
		return u.Scheme + "://" + u.Host
	case "file":
		return "null"
	default:
		return "null"
	}
}
