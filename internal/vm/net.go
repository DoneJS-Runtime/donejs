package vm

import (
	"net"
	"sync"

	"github.com/dop251/goja"
)

func (rt *Runtime) registerNet() {
	mod := rt.vm.NewObject()

	mod.Set("createServer", func(call goja.FunctionCall) goja.Value {
		handler, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(rt.vm.NewTypeError("connectionListener must be a function"))
		}
		obj := rt.vm.NewObject()
		obj.Set("listen", func(c goja.FunctionCall) goja.Value {
			port := int(c.Argument(0).ToInteger())
			host := ""
			if len(c.Arguments) > 1 && !goja.IsUndefined(c.Argument(1)) && !goja.IsNull(c.Argument(1)) {
				host = c.Argument(1).String()
			}
			cb, hasCB := splitCB(c)
			ln, err := net.Listen("tcp", net.JoinHostPort(host, itoa(port)))
			if err != nil {
				panic(rt.vm.NewGoError(err))
			}
			rt.ref()
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					rt.Enqueue(func() {
						sock := rt.socketObject(conn)
						if _, err := handler(goja.Undefined(), sock); err != nil {
							rt.reportUncaught(err)
						}
					})
				}
			}()
			if hasCB {
				if f, ok := goja.AssertFunction(cb); ok {
					f(goja.Undefined(), goja.Null())
				}
			}
			return obj
		})
		obj.Set("close", func(c goja.FunctionCall) goja.Value {
			cb, hasCB := splitCB(c)
			if hasCB {
				if f, ok := goja.AssertFunction(cb); ok {
					f(goja.Undefined(), goja.Null())
				}
			}
			rt.unref()
			return obj
		})
		return obj
	})

	mod.Set("connect", func(call goja.FunctionCall) goja.Value {
		opts := argObj([]goja.Value{call.Argument(0)}, 0)
		cb, hasCB := splitCB(call)
		host := optStr(opts, "host", "127.0.0.1")
		port := optInt(opts, "port", 0)
		go func() {
			conn, err := net.Dial("tcp", net.JoinHostPort(host, itoa(port)))
			rt.Enqueue(func() {
				if err != nil {
					if hasCB {
						if f, ok := goja.AssertFunction(cb); ok {
							f(goja.Undefined(), rt.jsError(err))
						}
					}
					return
				}
				sock := rt.socketObject(conn)
				if hasCB {
					if f, ok := goja.AssertFunction(cb); ok {
						f(goja.Undefined(), goja.Null(), sock)
					}
				}
			})
		}()
		return goja.Undefined()
	})

	mod.Set("isIP", func(call goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(net.ParseIP(call.Argument(0).String()) != nil)
	})
	mod.Set("isIPv4", func(call goja.FunctionCall) goja.Value {
		ip := net.ParseIP(call.Argument(0).String())
		return rt.vm.ToValue(ip != nil && ip.To4() != nil)
	})
	mod.Set("isIPv6", func(call goja.FunctionCall) goja.Value {
		ip := net.ParseIP(call.Argument(0).String())
		return rt.vm.ToValue(ip != nil && ip.To4() == nil)
	})

	rt.natives["__net"] = mod
}

// socketObject wraps a net.Conn in a Node-like socket object.
func (rt *Runtime) socketObject(conn net.Conn) goja.Value {
	st := rt.httpState()
	st.mu.Lock()
	st.connID++
	id := st.connID
	st.conns[id] = conn
	st.mu.Unlock()
	rt.ref()

	sock, sockEmitter := rt.newEmitterObject()
	sock.Set("write", func(c goja.FunctionCall) goja.Value {
		data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
		go func() {
			_, _ = conn.Write(data)
		}()
		return rt.vm.ToValue(true)
	})
	sock.Set("end", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) > 0 {
			data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
			_, _ = conn.Write(data)
		}
		_ = conn.Close()
		rt.unref()
		return sock
	})
	sock.Set("destroy", func(c goja.FunctionCall) goja.Value {
		_ = conn.Close()
		rt.unref()
		return sock
	})
	sock.Set("setTimeout", func(c goja.FunctionCall) goja.Value { return sock })
	sock.Set("setKeepAlive", func(c goja.FunctionCall) goja.Value { return sock })
	if addr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		sock.Set("localAddress", addr.IP.String())
		sock.Set("localPort", addr.Port)
	}
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		sock.Set("remoteAddress", addr.IP.String())
		sock.Set("remotePort", addr.Port)
	}

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				rt.Enqueue(func() {
					rt.emitEvent(sock, sockEmitter, "data", rt.toJS(binResult(chunk)))
				})
			}
			if err != nil {
				rt.Enqueue(func() { rt.emitEvent(sock, sockEmitter, "end") })
				return
			}
		}
	}()
	return sock
}

var _ sync.Mutex
