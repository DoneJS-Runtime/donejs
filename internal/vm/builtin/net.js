'use strict';
// net — Node.js net module.

const native = require('__net');
const { EventEmitter } = require('events');

class Socket extends EventEmitter {
  constructor(handle) {
    super();
    this._handle = handle;
    this.readable = true;
    this.writable = true;
    this.destroyed = false;
    if (handle) {
      this.remoteAddress = handle.remoteAddress;
      this.remotePort = handle.remotePort;
      this.localAddress = handle.localAddress;
      this.localPort = handle.localPort;
    }
  }
  write(data, encoding, cb) {
    const r = this._handle.write(data, encoding);
    if (typeof cb === 'function') cb();
    return r;
  }
  end(data, encoding, cb) {
    if (data && typeof data !== 'function') this._handle.write(data, encoding);
    this._handle.end();
    this.writable = false;
    this.emit('finish');
    if (typeof cb === 'function') cb();
    return this;
  }
  destroy() { this._handle.destroy(); this.destroyed = true; this.emit('close'); return this; }
  setTimeout() { return this; }
  setKeepAlive() { return this; }
  setNoDelay() { return this; }
  setEncoding() { return this; }
  pause() { return this; }
  resume() { return this; }
  unref() { return this; }
  ref() { return this; }
  address() {
    return { address: this.localAddress, family: 'IPv4', port: this.localPort };
  }
}

const net = module.exports;

net.createServer = (handler) => {
  const server = new EventEmitter();
  const handle = native.createServer((sock) => {
    const s = new Socket(sock);
    server.emit('connection', s);
    if (handler) handler(s);
  });
  server.listen = (...args) => {
    let port = 0, host = null, cb = null;
    for (const a of args) {
      if (typeof a === 'number') port = a;
      else if (typeof a === 'string') host = a;
      else if (typeof a === 'function') cb = a;
      else if (a && typeof a === 'object') { if (a.port !== undefined) port = a.port; if (a.host !== undefined) host = a.host; }
    }
    handle.listen(port, host || '', () => {
      server.emit('listening');
      if (cb) cb();
    });
    return server;
  };
  server.close = (cb) => {
    handle.close(() => { server.emit('close'); if (cb) cb(); });
    return server;
  };
  server.unref = () => handle.unref();
  return server;
};

net.connect = (...args) => {
  let opts = {}, cb = null;
  for (const a of args) {
    if (typeof a === 'function') cb = a;
    else if (typeof a === 'string') opts.host = a;
    else if (typeof a === 'number') opts.port = a;
    else if (a && typeof a === 'object') opts = Object.assign({}, opts, a);
  }
  const socket = new Socket({ write: () => true, end: () => {}, destroy: () => {} });
  native.connect(opts, (err, sock) => {
    if (err) { socket.emit('error', err); return; }
    Object.assign(socket, { _handle: sock, remoteAddress: sock.remoteAddress, remotePort: sock.remotePort });
    socket.emit('connect');
    if (cb) cb(null, socket);
  });
  return socket;
};
net.createConnection = net.connect;
net.isIP = (v) => native.isIP(v);
net.isIPv4 = (v) => native.isIPv4(v);
net.isIPv6 = (v) => native.isIPv6(v);
net.Socket = Socket;
net.Server = EventEmitter;
