package vm

import (
	"net/url"
	"strings"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerURL() {
	mod := rt.vm.NewObject()

	// parse returns the WHATWG-ish components of a URL.
	mod.Set("parse", func(call goja.FunctionCall) goja.Value {
		raw := call.Argument(0).String()
		base := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			base = call.Argument(1).String()
		}
		u, err := url.Parse(raw)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		if base != "" {
			b, err := url.Parse(base)
			if err != nil {
				panic(rt.vm.NewGoError(err))
			}
			u = b.ResolveReference(u)
		}
		return rt.toJS(map[string]any{
			"href":      u.String(),
			"protocol":  u.Scheme + ":",
			"host":      u.Host,
			"hostname":  u.Hostname(),
			"port":      u.Port(),
			"pathname":  u.Path,
			"search":    searchOf(u),
			"hash":      hashOf(u),
			"username":  u.User.Username(),
			"password":  passwordOf(u),
			"origin":    originOf(u),
			"hostName":  u.Hostname(),
			"path":      u.Path,
		})
	})

	// resolve implements new URL(relative, base) semantics.
	mod.Set("resolve", func(call goja.FunctionCall) goja.Value {
		rel := call.Argument(0).String()
		base := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			base = call.Argument(1).String()
		}
		b, err := url.Parse(base)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		r, err := url.Parse(rel)
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		return rt.vm.ToValue(b.ResolveReference(r).String())
	})

	mod.Set("domainToASCII", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(call.Argument(0).String())
	})
	mod.Set("domainToUnicode", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(call.Argument(0).String())
	})
	mod.Set("fileURLToPath", func(call goja.FunctionCall) goja.Value {
		u, err := url.Parse(call.Argument(0).String())
		if err != nil {
			panic(rt.vm.NewGoError(err))
		}
		if u.Scheme != "file" {
			panic(rt.vm.NewGoError(&url.Error{Op: "fileURLToPath", URL: u.String(), Err: errNotFileScheme}))
		}
		return rt.vm.ToValue(u.Path)
	})
	mod.Set("pathToFileURL", func(call goja.FunctionCall) goja.Value {
		p := call.Argument(0).String()
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		u := &url.URL{Scheme: "file", Path: p}
		return rt.vm.ToValue(u.String())
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
