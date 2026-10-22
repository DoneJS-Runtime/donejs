package vm

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

	"github.com/dop251/goja"
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
	mod := rt.vm.NewObject()

	mod.Set("createServer", func(call goja.FunctionCall) goja.Value {
		handler, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(rt.vm.NewTypeError("requestListener must be a function"))
		}
		opts := argObj([]goja.Value{call.Argument(1)}, 0)
		return rt.newServerObject(handler, opts, nil)
	})

	mod.Set("createSecureServer", func(call goja.FunctionCall) goja.Value {
		handler, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(rt.vm.NewTypeError("requestListener must be a function"))
		}
		opts := argObj([]goja.Value{call.Argument(1)}, 0)
		certPath := optStr(opts, "cert", "")
		keyPath := optStr(opts, "key", "")
		if certPath == "" || keyPath == "" {
			panic(rt.vm.NewGoError(errors.New("https.createServer requires 'cert' and 'key'")))
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			panic(rt.vm.NewGoError(err))
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

	mod.Set("request", func(call goja.FunctionCall) goja.Value {
		opts := argObj([]goja.Value{call.Argument(0)}, 0)
		cb, hasCB := splitCB(call)
		if !hasCB {
			panic(rt.vm.NewTypeError("callback required"))
		}
		return rt.httpRequest(opts, cb, false)
	})

	mod.Set("get", func(call goja.FunctionCall) goja.Value {
		opts := argObj([]goja.Value{call.Argument(0)}, 0)
		cb, hasCB := splitCB(call)
		if !hasCB {
			panic(rt.vm.NewTypeError("callback required"))
		}
		if opts == nil {
			opts = rt.vm.NewObject()
		}
		opts.Set("method", "GET")
		return rt.httpRequest(opts, cb, false)
	})

	mod.Set("globalAgent", rt.vm.NewObject())
	mod.Set("maxHeaderSize", 16384)

	rt.natives["__http"] = mod
	rt.natives["__https"] = mod
}

// newServerObject builds the native server handle exposed to JS.
func (rt *Runtime) newServerObject(handler goja.Callable, opts *goja.Object, tlsCfg *tls.Config) goja.Value {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt.serveHTTP(handler, w, r)
		}),
	}
	if opts != nil {
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
	obj := rt.vm.NewObject()
	obj.Set("__id", id)
	obj.Set("listen", func(c goja.FunctionCall) goja.Value {
		port := int(c.Argument(0).ToInteger())
		host := ""
		if len(c.Arguments) > 1 && !goja.IsUndefined(c.Argument(1)) && !goja.IsNull(c.Argument(1)) {
			host = c.Argument(1).String()
		}
		cb, hasCB := splitCB(c)
		rt.listenHTTP(id, port, host, cb, hasCB)
		return obj
	})
	obj.Set("close", func(c goja.FunctionCall) goja.Value {
		cb, hasCB := splitCB(c)
		rt.closeHTTPServer(id, cb, hasCB)
		return obj
	})
	obj.Set("address", func(c goja.FunctionCall) goja.Value {
		return rt.serverAddress(id)
	})
	obj.Set("unref", func(c goja.FunctionCall) goja.Value { return obj })
	obj.Set("ref", func(c goja.FunctionCall) goja.Value { return obj })
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

func (rt *Runtime) listenHTTP(id, port int, host string, cb goja.Value, hasCB bool) {
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
				if f, ok := goja.AssertFunction(cb); ok {
					f(goja.Undefined(), rt.jsError(err))
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
			if f, ok := goja.AssertFunction(cb); ok {
				f(goja.Undefined(), goja.Null())
			}
		}
	})
}

func (rt *Runtime) closeHTTPServer(id int, cb goja.Value, hasCB bool) {
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
			if f, ok := goja.AssertFunction(cb); ok {
				f(goja.Undefined(), goja.Null())
			}
		})
	}
}

func (rt *Runtime) serverAddress(id int) goja.Value {
	st := rt.httpState()
	st.mu.Lock()
	entry := st.servers[id]
	st.mu.Unlock()
	if entry == nil || entry.addr == nil {
		return goja.Null()
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
func (rt *Runtime) serveHTTP(handler goja.Callable, w http.ResponseWriter, r *http.Request) {
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
		sock := rt.vm.NewObject()
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
		res.Set("setHeader", func(c goja.FunctionCall) goja.Value {
			resHeaders[c.Argument(0).String()] = c.Argument(1).Export()
			return goja.Undefined()
		})
		res.Set("getHeader", func(c goja.FunctionCall) goja.Value {
			v, ok := resHeaders[c.Argument(0).String()]
			if !ok {
				return goja.Undefined()
			}
			return rt.toJS(v)
		})
		res.Set("getHeaders", func(c goja.FunctionCall) goja.Value {
			return rt.toJS(resHeaders)
		})
		res.Set("removeHeader", func(c goja.FunctionCall) goja.Value {
			delete(resHeaders, c.Argument(0).String())
			return goja.Undefined()
		})
		res.Set("hasHeader", func(c goja.FunctionCall) goja.Value {
			_, ok := resHeaders[c.Argument(0).String()]
			return rt.vm.ToValue(ok)
		})
		res.Set("writeHead", func(c goja.FunctionCall) goja.Value {
			code := int(c.Argument(0).ToInteger())
			res.Set("statusCode", code)
			if len(c.Arguments) > 1 {
				if o, ok := c.Argument(1).(*goja.Object); ok {
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
		res.Set("write", func(c goja.FunctionCall) goja.Value {
			if !res.Get("headersSent").ToBoolean() {
				for k, v := range resHeaders {
					w.Header().Set(k, headerValue(v))
				}
				w.WriteHeader(int(res.Get("statusCode").ToInteger()))
				res.Set("headersSent", true)
			}
			data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
			if len(data) > 0 {
				_, _ = w.Write(data)
			}
			return rt.vm.ToValue(true)
		})
		res.Set("end", func(c goja.FunctionCall) goja.Value {
			if len(c.Arguments) > 0 {
				if f, ok := goja.AssertFunction(res.Get("write")); ok {
					f(res, c.Argument(0))
				}
			}
			res.Set("writableEnded", true)
			finish()
			rt.emitEvent(res, resEmitter, "finish")
			return res
		})
		res.Set("flushHeaders", func(c goja.FunctionCall) goja.Value {
			if !res.Get("headersSent").ToBoolean() {
				w.WriteHeader(int(res.Get("statusCode").ToInteger()))
				res.Set("headersSent", true)
			}
			return goja.Undefined()
		})

		if len(body) > 0 {
			rt.emitEvent(req, reqEmitter, "data", rt.toJS(binResult(body)))
		}
		rt.emitEvent(req, reqEmitter, "end")

		if _, err := handler(goja.Undefined(), req, res); err != nil {
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
func (rt *Runtime) httpRequest(opts *goja.Object, cb goja.Value, useTLS bool) goja.Value {
	if opts == nil {
		panic(rt.vm.NewTypeError("options required"))
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
	if h := opts.Get("headers"); h != nil {
		if ho, ok := h.(*goja.Object); ok {
			for _, k := range ho.Keys() {
				headers[k] = headerValue(ho.Get(k).Export())
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

	reqObj := rt.vm.NewObject()
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
				if f, ok := goja.AssertFunction(cb); ok {
					f(goja.Undefined(), rt.jsError(err))
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
			resObj.Set("destroy", func(c goja.FunctionCall) goja.Value { return goja.Undefined() })

			if f, ok := goja.AssertFunction(cb); ok {
				if _, err := f(goja.Undefined(), resObj); err != nil {
					rt.reportUncaught(err)
				}
			}
			go func() {
				buf := make([]byte, 32*1024)
				for {
					n, err := resp.Body.Read(buf)
					if n > 0 {
						chunk := make([]byte, n)
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

	reqObj.Set("write", func(c goja.FunctionCall) goja.Value {
		data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
		if len(data) > 0 {
			bodyChunks <- data
		}
		return rt.vm.ToValue(true)
	})
	reqObj.Set("end", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) > 0 {
			data := rt.argBytes([]goja.Value{c.Argument(0)}, 0)
			if len(data) > 0 {
				bodyChunks <- data
			}
		}
		close(bodyChunks)
		return reqObj
	})
	reqObj.Set("setHeader", func(c goja.FunctionCall) goja.Value {
		req.Header.Set(c.Argument(0).String(), headerValue(c.Argument(1).Export()))
		return goja.Undefined()
	})
	reqObj.Set("on", func(c goja.FunctionCall) goja.Value { return reqObj })
	reqObj.Set("once", func(c goja.FunctionCall) goja.Value { return reqObj })
	reqObj.Set("abort", func(c goja.FunctionCall) goja.Value { return reqObj })
	reqObj.Set("setTimeout", func(c goja.FunctionCall) goja.Value { return reqObj })
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

var _ = json.Marshal
var _ = bytes.NewBuffer
