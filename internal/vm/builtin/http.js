'use strict';
// http / https — Node.js http & https modules.

const { EventEmitter } = require('events');
const { Readable } = require('stream');

class IncomingMessage extends Readable {
  constructor(socket) {
    super();
    this.socket = socket;
    this.headers = {};
    this.rawHeaders = [];
    this.method = undefined;
    this.url = undefined;
    this.httpVersion = '1.1';
    this.statusCode = undefined;
  }
}

class ServerResponse extends EventEmitter {
  constructor() {
    super();
    this.statusCode = 200;
    this.statusMessage = '';
    this.headersSent = false;
    this.writableEnded = false;
    this.finished = false;
  }
}

class ClientRequest extends EventEmitter {
  constructor(handle) {
    super();
    this._handle = handle;
    this.writableEnded = false;
  }
  write(chunk, encoding, cb) {
    const r = this._handle.write(chunk, encoding);
    if (typeof cb === 'function') cb();
    return r;
  }
  end(chunk, encoding, cb) {
    if (chunk && typeof chunk !== 'function') this._handle.write(chunk, encoding);
    this._handle.end();
    this.writableEnded = true;
    this.emit('finish');
    if (typeof cb === 'function') cb();
    return this;
  }
  setHeader(name, value) { this._handle.setHeader(name, value); return this; }
  getHeader(name) { return this._handle.getHeader ? this._handle.getHeader(name) : undefined; }
  removeHeader(name) { if (this._handle.removeHeader) this._handle.removeHeader(name); return this; }
  abort() { if (this._handle.abort) this._handle.abort(); return this; }
  destroy() { if (this._handle.abort) this._handle.abort(); return this; }
  setTimeout() { return this; }
}

function createServer(native, handler, options, secure) {
  const srv = secure ? native.createSecureServer(handler, options) : native.createServer(handler, options);
  const server = new EventEmitter();
  server._handle = srv;
  server.listen = (...args) => {
    let port = 0, host = null, cb = null;
    for (const a of args) {
      if (typeof a === 'number') port = a;
      else if (typeof a === 'string') host = a;
      else if (typeof a === 'function') cb = a;
      else if (a && typeof a === 'object') { if (a.port !== undefined) port = a.port; if (a.host !== undefined) host = a.host; }
    }
    srv.listen(port, host || '', (err) => {
      if (err) { server.emit('error', err); return; }
      server.emit('listening');
      if (cb) cb();
    });
    return server;
  };
  server.close = (cb) => {
    srv.close(() => {
      server.emit('close');
      if (cb) cb();
    });
    return server;
  };
  server.address = () => srv.address();
  server.unref = () => srv.unref();
  server.ref = () => srv.ref();
  server[Symbol.asyncDispose] = () => server.close();
  return server;
}

function request(native, options, callback) {
  const opts = typeof options === 'string' || options instanceof URL
    ? normalizeUrlArgs(options)
    : Object.assign({}, options);
  if (typeof options === 'string' || options instanceof URL) {
    opts.path = opts.path || '/';
    opts.method = opts.method || 'GET';
  }
  const handle = native.request(opts, (res) => {
    const msg = new IncomingMessage(res.socket);
    msg.statusCode = res.statusCode;
    msg.statusMessage = res.statusMessage;
    msg.headers = res.headers;
    msg.rawHeaders = res.rawHeaders;
    msg.httpVersion = res.httpVersion;
    res.on('data', (chunk) => msg.push(chunk));
    res.on('end', () => msg.push(null));
    if (typeof callback === 'function') callback(msg);
    req.emit('response', msg);
  });
  const req = new ClientRequest(handle);
  if (typeof options === 'object' && options !== null && options.on) {
    if (typeof options.on === 'function') req.on('response', options.on);
  }
  return req;
}

function get(native, options, callback) {
  const opts = typeof options === 'string' || options instanceof URL ? normalizeUrlArgs(options) : Object.assign({}, options);
  opts.method = 'GET';
  const req = request(native, opts, callback);
  req.end();
  return req;
}

function normalizeUrlArgs(u) {
  const parsed = new URL(String(u));
  return {
    protocol: parsed.protocol,
    hostname: parsed.hostname,
    port: parsed.port ? Number(parsed.port) : undefined,
    path: parsed.pathname + parsed.search,
    href: parsed.href,
  };
}

// build assembles the module object for a native transport.
function build(native, defaultProtocol, secure) {
  const mod = {
    createServer: (handler, options) => createServer(native, handler, options),
    request: (options, callback) => request(native, withProtocol(options, defaultProtocol), callback),
    get: (options, callback) => get(native, withProtocol(options, defaultProtocol), callback),
    ServerResponse,
    IncomingMessage,
    ClientRequest,
    METHODS: ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'CONNECT', 'OPTIONS', 'TRACE', 'PATCH'],
    STATUS_CODES: {
      200: 'OK', 201: 'Created', 204: 'No Content', 301: 'Moved Permanently', 302: 'Found',
      304: 'Not Modified', 400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden',
      404: 'Not Found', 405: 'Method Not Allowed', 500: 'Internal Server Error', 502: 'Bad Gateway',
      503: 'Service Unavailable',
    },
    globalAgent: native.globalAgent,
    maxHeaderSize: native.maxHeaderSize,
    validateHeaderName: () => true,
    validateHeaderValue: () => true,
    setMaxIdleHTTPParsers: () => {},
  };
  if (secure) mod.createServer = (handler, options) => createServer(native, handler, options, true);
  mod.createServer.constructor = mod;
  mod.__build = build;
  return mod;
}

// withProtocol pins the scheme when the caller did not supply one.
function withProtocol(options, defaultProtocol) {
  if (typeof options === 'string') return options;
  if (options instanceof URL) return options.href;
  const opts = Object.assign({}, options);
  if (!opts.protocol) opts.protocol = defaultProtocol;
  return opts;
}

const http = build(require('__http'), 'http:');
module.exports = http;
module.exports.default = http;

