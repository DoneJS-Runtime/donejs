package vm

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"donejs/internal/jit"
)

// httpState keeps live servers and sockets alive for the event loop.
type httpState struct {
	mu      sync.Mutex
	servers map[int]*httpEntry
	nextID  int
	conns   map[int]net.Conn
	connID  int
}

// httpEntry pairs a server with the address it is listening on.
type httpEntry struct {
	srv    *http.Server
	addr   net.Addr
	tlsCfg *tls.Config
}

func (rt *Runtime) httpState() *httpState {
	if rt.http == nil {
		rt.http = &httpState{servers: map[int]*httpEntry{}, conns: map[int]net.Conn{}}
	}
	return rt.http
}

func (rt *Runtime) registerHTTP() {
	mod := jit.Object()

	setFn(mod, "createServer", func(args []jit.Value) jit.Value {
		handler := argsAt(args, 0)
		if !handler.IsCallable() {
			panic(jit.Err("TypeError", "requestListener must be a function"))
		}
		opts := argObj(args, 1)
		return rt.newServerObject(handler, opts, nil)
	})

	setFn(mod, "createSecureServer", func(args []jit.Value) jit.Value {
		handler := argsAt(args, 0)
		if !handler.IsCallable() {
			panic(jit.Err("TypeError", "requestListener must be a function"))
		}
		opts := argObj(args, 1)
		certPath := optStr(opts, "cert", "")
		keyPath := optStr(opts, "key", "")
		if certPath == "" || keyPath == "" {
			panic(rt.jsThrow(errors.New("https.createServer requires 'cert' and 'key'")))
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			panic(rt.jsThrow(err))
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		if ca := optStr(opts, "ca", ""); ca != "" {
			if pem, err := readFileBytes(ca); err == nil {
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM(pem)
				tlsCfg.ClientCAs = pool
				tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
			}
		}
		return rt.newServerObject(handler, opts, tlsCfg)
	})

	setFn(mod, "request", func(args []jit.Value) jit.Value {
		opts := argObj(args, 0)
		cb, hasCB := splitCB(args)
		if !hasCB {
			panic(jit.Err("TypeError", "callback required"))
		}
		return rt.httpRequest(opts, cb, false)
	})

	setFn(mod, "get", func(args []jit.Value) jit.Value {
		opts := argObj(args, 0)
		cb, hasCB := splitCB(args)
		if !hasCB {
			panic(jit.Err("TypeError", "callback required"))
		}
		if opts.IsUndef() {
			opts = jit.Object()
		}
		opts.Set("method", "GET")
		return rt.httpRequest(opts, cb, false)
	})

	mod.Set("globalAgent", jit.Object())
	mod.Set("maxHeaderSize", 16384)

	rt.natives["__http"] = mod
	rt.natives["__https"] = mod
}

// newServerObject builds the native server handle exposed to JS.
func (rt *Runtime) newServerObject(handler jit.Value, opts jit.Value, tlsCfg *tls.Config) jit.Value {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt.serveHTTP(handler, w, r)
		}),
	}
	if !opts.IsUndef() {
		if to := optInt(opts, "timeout", 0); to > 0 {
			srv.ReadTimeout = time.Duration(to) * time.Millisecond
		}
		if to := optInt(opts, "keepAliveTimeout", 0); to > 0 {
			srv.IdleTimeout = time.Duration(to) * time.Millisecond
		}
	}
	id := rt.newHTTPServer(srv)
	st := rt.httpState()
	st.mu.Lock()
	st.servers[id].tlsCfg = tlsCfg
	st.mu.Unlock()
	obj := jit.Object()
	obj.Set("__id", id)
	obj.Set("listen", jit.BuiltinFn("listen", func(args []jit.Value) jit.Value {
		port := int(argsAt(args, 0).ToInteger())
		host := ""
		if len(args) > 1 && !args[1].IsUndef() && !args[1].IsNull() {
			host = args[1].String()
		}
		cb, hasCB := splitCB(args)
		rt.listenHTTP(id, port, host, cb, hasCB)
		return obj
	}))
	obj.Set("close", jit.BuiltinFn("close", func(args []jit.Value) jit.Value {
		cb, hasCB := splitCB(args)
		rt.closeHTTPServer(id, cb, hasCB)
		return obj
	}))
	obj.Set("address", jit.BuiltinFn("address", func(args []jit.Value) jit.Value {
		return rt.serverAddress(id)
	}))
	obj.Set("unref", jit.BuiltinFn("unref", func(args []jit.Value) jit.Value { return obj }))
	obj.Set("ref", jit.BuiltinFn("ref", func(args []jit.Value) jit.Value { return obj }))
	return obj
}

func (rt *Runtime) newHTTPServer(srv *http.Server) int {
	st := rt.httpState()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.nextID++
	id := st.nextID
	st.servers[id] = &httpEntry{srv: srv}
	rt.ref()
	return id
}

func (rt *Runtime) listenHTTP(id, port int, host string, cb jit.Value, hasCB bool) {
	st := rt.httpState()
	st.mu.Lock()
	entry := st.servers[id]
	st.mu.Unlock()
	if entry == nil {
		return
	}
	addr := net.JoinHostPort(host, itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		rt.Enqueue(func() {
			if hasCB {
				if f := cb; f.IsCallable() {
					if _, err := rt.jitCall(f, jit.Undef(), rt.jsThrow(err).Value); err != nil {
						rt.reportUncaught(err)
					}
				}
			}
		})
		return
	}
	if entry.tlsCfg != nil {
		ln = tls.NewListener(ln, entry.tlsCfg)
	}
	st.mu.Lock()
	entry.addr = ln.Addr()
	st.mu.Unlock()
	go func() {
		_ = entry.srv.Serve(ln)
	}()
	rt.Enqueue(func() {
		if hasCB {
			if f := cb; f.IsCallable() {
				if _, err := rt.jitCall(f, jit.Undef(), jit.Null()); err != nil {
					rt.reportUncaught(err)
				}
			}
		}
	})
}

func (rt *Runtime) closeHTTPServer(id int, cb jit.Value, hasCB bool) {
	st := rt.httpState()
	st.mu.Lock()
	entry := st.servers[id]
	delete(st.servers, id)
	st.mu.Unlock()
	if entry != nil {
		_ = entry.srv.Close()
		rt.unref()
	}
	if hasCB {
		rt.Enqueue(func() {
			if f := cb; f.IsCallable() {
				if _, err := rt.jitCall(f, jit.Undef(), jit.Null()); err != nil {
					rt.reportUncaught(err)
				}
			}
		})
	}
}

func (rt *Runtime) serverAddress(id int) jit.Value {
	st := rt.httpState()
	st.mu.Lock()
	entry := st.servers[id]
	st.mu.Unlock()
	if entry == nil || entry.addr == nil {
		return jit.Null()
	}
	addr := entry.addr.String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return rt.toJS(map[string]any{"address": addr, "family": "IPv4", "port": 0})
	}
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	return rt.toJS(map[string]any{"address": host, "family": "IPv4", "port": p})
}

// serveHTTP bridges an incoming Go request to a Node-style (req, res) pair.
func (rt *Runtime) serveHTTP(handler jit.Value, w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	rt.Enqueue(func() {
		req, reqEmitter := rt.newEmitterObject()
		req.Set("method", r.Method)
		req.Set("url", r.URL.RequestURI())
		req.Set("httpVersion", r.Proto)
		headers := map[string]any{}
		for k, v := range r.Header {
			if len(v) == 1 {
				headers[k] = v[0]
			} else {
				headers[k] = v
			}
		}
		req.Set("headers", headers)
		req.Set("rawHeaders", r.Header)
		sock := jit.Object()
		if r.RemoteAddr != "" {
			h, p, _ := net.SplitHostPort(r.RemoteAddr)
			sock.Set("remoteAddress", h)
			sock.Set("remotePort", atoi(p))
		}
		req.Set("socket", sock)

		res, resEmitter := rt.newEmitterObject()
		res.Set("statusCode", 200)
		res.Set("statusMessage", "")
		res.Set("headersSent", false)
		res.Set("writableEnded", false)
		res.Set("chunkedEncoding", false)

		resHeaders := map[string]any{}
		res.Set("setHeader", func(args []jit.Value) jit.Value {
			resHeaders[argsAt(args, 0).String()] = argsAt(args, 1).Export()
			return jit.Undef()
		})
		res.Set("getHeader", func(args []jit.Value) jit.Value {
			v, ok := resHeaders[argsAt(args, 0).String()]
			if !ok {
				return jit.Undef()
			}
			return rt.toJS(v)
		})
		res.Set("getHeaders", func(args []jit.Value) jit.Value {
			return rt.toJS(resHeaders)
		})
		res.Set("removeHeader", func(args []jit.Value) jit.Value {
			delete(resHeaders, argsAt(args, 0).String())
			return jit.Undef()
		})
		res.Set("hasHeader", func(args []jit.Value) jit.Value {
			_, ok := resHeaders[argsAt(args, 0).String()]
			return jit.Bool(ok)
		})
		res.Set("writeHead", func(args []jit.Value) jit.Value {
			code := int(argsAt(args, 0).ToInteger())
			res.Set("statusCode", code)
			if len(args) > 1 {
				if o := argObj(args, 1); o.IsObject() {
					for _, k := range o.Keys() {
						resHeaders[k] = o.Get(k).Export()
					}
				}
			}
			for k, v := range resHeaders {
				w.Header().Set(k, headerValue(v))
			}
			w.WriteHeader(code)
			res.Set("headersSent", true)
			return res
		})
		res.Set("write", func(args []jit.Value) jit.Value {
			if !res.Get("headersSent").ToBoolean() {
				for k, v := range resHeaders {
					w.Header().Set(k, headerValue(v))
				}
				w.WriteHeader(int(res.Get("statusCode").ToInteger()))
				res.Set("headersSent", true)
			}
			data := rt.argBytes(args, 0)
			if len(data) > 0 {
				_, _ = w.Write(data)
			}
			return jit.Bool(true)
		})
		res.Set("end", func(args []jit.Value) jit.Value {
			if len(args) > 0 {
				if f := res.Get("write"); f.IsCallable() {
					rt.jitCall(f, res, argsAt(args, 0))
				}
			}
			res.Set("writableEnded", true)
			finish()
			rt.emitEvent(res, resEmitter, "finish")
			return res
		})
		res.Set("flushHeaders", func(args []jit.Value) jit.Value {
			if !res.Get("headersSent").ToBoolean() {
				w.WriteHeader(int(res.Get("statusCode").ToInteger()))
				res.Set("headersSent", true)
			}
			return jit.Undef()
		})

		if len(body) > 0 {
			rt.emitEvent(req, reqEmitter, "data", rt.toJS(binResult(body)))
		}
		rt.emitEvent(req, reqEmitter, "end")

		if _, err := rt.jitCall(handler, jit.Undef(), req, res); err != nil {
			rt.reportUncaught(err)
		}
	})

	select {
	case <-done:
	case <-time.After(10 * time.Minute):
	}
}

func headerValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, toStr(x))
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

// httpRequest performs an outgoing request and bridges the response to JS.
func (rt *Runtime) httpRequest(opts jit.Value, cb jit.Value, useTLS bool) jit.Value {
	if opts.IsUndef() {
		panic(jit.Err("TypeError", "options required"))
	}
	method := optStr(opts, "method", "GET")
	host := optStr(opts, "hostname", "")
	if host == "" {
		host = optStr(opts, "host", "")
	}
	port := optInt(opts, "port", 0)
	path := optStr(opts, "path", "/")
	proto := optStr(opts, "protocol", "")
	if proto == "" {
		if useTLS {
			proto = "https:"
		} else {
			proto = "http:"
		}
	}
	if host == "" {
		host = "localhost"
	}
	headers := map[string]string{}
	if h := opts.Get("headers"); !h.IsUndef() {
		if ho := h.AsObj(); ho != nil {
			for _, k := range h.Keys() {
				headers[k] = headerValue(h.Get(k).Export())
			}
		}
	}
	scheme := strings.TrimSuffix(proto, ":")
	hostHeader := host
	if port != 0 && !strings.Contains(host, ":") {
		hostHeader = host + ":" + itoa(port)
	}
	req := &http.Request{
		Method: method,
		URL:    mustURL(scheme, hostHeader, path),
		Header: http.Header{},
		Proto:  "HTTP/1.1",
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	bodyChunks := make(chan []byte, 16)
	reqDone := make(chan struct{})

	client := &http.Client{}
	if scheme == "https" {
		tlsCfg := &tls.Config{}
		if v := optBool(opts, "rejectUnauthorized", true); !v {
			tlsCfg.InsecureSkipVerify = true
		}
		if ca := optStr(opts, "ca", ""); ca != "" {
			pem, err := readFileBytes(ca)
			if err == nil {
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM(pem)
				tlsCfg.RootCAs = pool
			}
		}
		if cert := optStr(opts, "cert", ""); cert != "" {
			if key := optStr(opts, "key", ""); key != "" {
				if c, err := tls.LoadX509KeyPair(cert, key); err == nil {
					tlsCfg.Certificates = []tls.Certificate{c}
				}
			}
		}
		client.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}

	reqObj := jit.Object()
	reqObj.Set("method", method)
	reqObj.Set("path", path)

	rt.pending++
	go func() {
		defer close(reqDone)
		var bodyReader io.Reader
		if bodyChunks != nil {
			bodyReader = &chunkReader{chunks: bodyChunks, done: reqDone}
		}
		req.Body = io.NopCloser(bodyReader)
		resp, err := client.Do(req)
		if err != nil {
			rt.Enqueue(func() {
				rt.pending--
				if f := cb; f.IsCallable() {
					if _, err := rt.jitCall(f, jit.Undef(), rt.jsThrow(err).Value); err != nil {
						rt.reportUncaught(err)
					}
				}
			})
			return
		}
		respHeaders := map[string]any{}
		for k, v := range resp.Header {
			if len(v) == 1 {
				respHeaders[k] = v[0]
			} else {
				respHeaders[k] = v
			}
		}
		rt.Enqueue(func() {
			resObj, resEmitter := rt.newEmitterObject()
			resObj.Set("statusCode", resp.StatusCode)
			resObj.Set("statusMessage", resp.Status)
			resObj.Set("headers", respHeaders)
			resObj.Set("rawHeaders", resp.Header)
			resObj.Set("httpVersion", resp.Proto)
			resObj.Set("destroy", jit.BuiltinFn("destroy", func(args []jit.Value) jit.Value { return jit.Undef() }))

			if f := cb; f.IsCallable() {
				if _, err := rt.jitCall(f, jit.Undef(), resObj); err != nil {
					rt.reportUncaught(err)
				}
			}
			go func() {
				buf := make([]byte, 32*1024)
				var chunk []byte
				for {
					n, err := resp.Body.Read(buf)
					if n > 0 {
						if chunk == nil || cap(chunk) < n {
							chunk = make([]byte, n)
						}
						chunk = chunk[:n]
						copy(chunk, buf[:n])
						rt.Enqueue(func() {
							rt.emitEvent(resObj, resEmitter, "data", rt.toJS(binResult(chunk)))
						})
					}
					if err != nil {
						break
					}
				}
				resp.Body.Close()
				rt.Enqueue(func() {
					rt.pending--
					rt.emitEvent(resObj, resEmitter, "end")
				})
			}()
		})
	}()

	reqObj.Set("write", func(args []jit.Value) jit.Value {
		data := rt.argBytes(args, 0)
		if len(data) > 0 {
			bodyChunks <- data
		}
		return jit.Bool(true)
	})
	reqObj.Set("end", func(args []jit.Value) jit.Value {
		if len(args) > 0 {
			data := rt.argBytes(args, 0)
			if len(data) > 0 {
				bodyChunks <- data
			}
		}
		close(bodyChunks)
		return reqObj
	})
	reqObj.Set("setHeader", func(args []jit.Value) jit.Value {
		req.Header.Set(argsAt(args, 0).String(), headerValue(argsAt(args, 1).Export()))
		return jit.Undef()
	})
	reqObj.Set("on", func(args []jit.Value) jit.Value { return reqObj })
	reqObj.Set("once", func(args []jit.Value) jit.Value { return reqObj })
	reqObj.Set("abort", func(args []jit.Value) jit.Value { return reqObj })
	reqObj.Set("setTimeout", func(args []jit.Value) jit.Value { return reqObj })
	return reqObj
}

// chunkReader feeds an HTTP request body from a channel.
type chunkReader struct {
	chunks chan []byte
	done   chan struct{}
	buf    []byte
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		select {
		case chunk, ok := <-c.chunks:
			if !ok {
				return 0, io.EOF
			}
			c.buf = chunk
		case <-c.done:
			return 0, io.EOF
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func mustURL(scheme, host, path string) *url.URL {
	return &url.URL{Scheme: scheme, Host: host, Path: path}
}

// readFileBytes reads a file from disk.
func readFileBytes(p string) ([]byte, error) {
	return os.ReadFile(p)
}

func itoa(n int) string { return strconv.Itoa(n) }
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
