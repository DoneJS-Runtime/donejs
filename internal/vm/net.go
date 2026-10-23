package vm

import (
	"net"

	"donejs/internal/jit"
)

func (rt *Runtime) registerNet() {
	mod := jit.Object()

	setFn(mod, "createServer", func(args []jit.Value) jit.Value {
		handler := argsAt(args, 0)
		if !handler.IsCallable() {
			panic(jit.Err("TypeError", "connectionListener must be a function"))
		}
		obj := jit.Object()
		obj.Set("listen", jit.BuiltinFn("listen", func(args []jit.Value) jit.Value {
			port := int(argsAt(args, 0).ToInteger())
			host := ""
			if len(args) > 1 && !args[1].IsUndef() && !args[1].IsNull() {
				host = args[1].String()
			}
			cb, hasCB := splitCB(args)
			ln, err := net.Listen("tcp", net.JoinHostPort(host, itoa(port)))
			if err != nil {
				panic(rt.jsThrow(err))
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
						if _, err := rt.jitCall(handler, jit.Undef(), sock); err != nil {
							rt.reportUncaught(err)
						}
					})
				}
			}()
			if hasCB {
				if f := cb; f.IsCallable() {
					if _, err := rt.jitCall(f, jit.Undef(), jit.Null()); err != nil {
						rt.reportUncaught(err)
					}
				}
			}
			return obj
		}))
		obj.Set("close", jit.BuiltinFn("close", func(args []jit.Value) jit.Value {
			cb, hasCB := splitCB(args)
			if hasCB {
				if f := cb; f.IsCallable() {
					if _, err := rt.jitCall(f, jit.Undef(), jit.Null()); err != nil {
						rt.reportUncaught(err)
					}
				}
			}
			rt.unref()
			return obj
		}))
		return obj
	})

	setFn(mod, "connect", func(args []jit.Value) jit.Value {
		opts := argObj(args, 0)
		cb, hasCB := splitCB(args)
		host := optStr(opts, "host", "127.0.0.1")
		port := optInt(opts, "port", 0)
		go func() {
			conn, err := net.Dial("tcp", net.JoinHostPort(host, itoa(port)))
			rt.Enqueue(func() {
				if err != nil {
					if hasCB {
						if f := cb; f.IsCallable() {
							if _, e := rt.jitCall(f, jit.Undef(), rt.jsThrow(err).Value); e != nil {
								rt.reportUncaught(e)
							}
						}
					}
					return
				}
				sock := rt.socketObject(conn)
				if hasCB {
					if f := cb; f.IsCallable() {
						if _, e := rt.jitCall(f, jit.Undef(), jit.Null(), sock); e != nil {
							rt.reportUncaught(e)
						}
					}
				}
			})
		}()
		return jit.Undef()
	})

	setFn(mod, "isIP", func(args []jit.Value) jit.Value {
		return jit.Bool(net.ParseIP(argsAt(args, 0).String()) != nil)
	})
	setFn(mod, "isIPv4", func(args []jit.Value) jit.Value {
		ip := net.ParseIP(argsAt(args, 0).String())
		return jit.Bool(ip != nil && ip.To4() != nil)
	})
	setFn(mod, "isIPv6", func(args []jit.Value) jit.Value {
		ip := net.ParseIP(argsAt(args, 0).String())
		return jit.Bool(ip != nil && ip.To4() == nil)
	})

	rt.natives["__net"] = mod
}

// socketObject wraps a net.Conn in a Node-like socket object.
func (rt *Runtime) socketObject(conn net.Conn) jit.Value {
	st := rt.httpState()
	st.mu.Lock()
	st.connID++
	id := st.connID
	st.conns[id] = conn
	st.mu.Unlock()
	rt.ref()

	sock, sockEmitter := rt.newEmitterObject()
	sock.Set("write", jit.BuiltinFn("write", func(args []jit.Value) jit.Value {
		data := rt.argBytes(args, 0)
		go func() {
			_, _ = conn.Write(data)
		}()
		return jit.Bool(true)
	}))
	sock.Set("end", jit.BuiltinFn("end", func(args []jit.Value) jit.Value {
		if len(args) > 0 {
			data := rt.argBytes(args, 0)
			_, _ = conn.Write(data)
		}
		_ = conn.Close()
		rt.unref()
		return sock
	}))
	sock.Set("destroy", jit.BuiltinFn("destroy", func(args []jit.Value) jit.Value {
		_ = conn.Close()
		rt.unref()
		return sock
	}))
	sock.Set("setTimeout", jit.BuiltinFn("setTimeout", func(args []jit.Value) jit.Value { return sock }))
	sock.Set("setKeepAlive", jit.BuiltinFn("setKeepAlive", func(args []jit.Value) jit.Value { return sock }))
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
