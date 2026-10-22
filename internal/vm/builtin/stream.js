'use strict';
// stream — Node.js stream module (Readable/Writable/Transform core).

const { EventEmitter } = require('events');

// defer schedules work on the next tick, like Node's stream resume.
function defer(fn) {
  if (typeof process !== 'undefined' && process && typeof process.nextTick === 'function') {
    process.nextTick(fn);
    return;
  }
  setTimeout(fn, 0);
}

class Readable extends EventEmitter {
  constructor(options) {
    super();
    options = options || {};
    this.readable = true;
    this.readableEnded = false;
    this.destroyed = false;
    this._readableState = { flowing: false, ended: false, buffer: [], length: 0, reading: false, endEmitted: false, resumeScheduled: false, highWaterMark: options.highWaterMark || 16384 };
    if (options.objectMode) this._readableState.objectMode = true;
    if (options.encoding) this._readableState.encoding = options.encoding;
    if (typeof options.read === 'function') this._read = options.read;
    if (typeof options.destroy === 'function') this._destroy = options.destroy;
  }

  get readableLength() { return this._readableState.length; }

  // Attaching a 'data' listener switches the stream into flowing mode.
  on(event, listener) {
    const r = super.on(event, listener);
    if (event === 'data') this._resumeSoon();
    return r;
  }

  read(n) {
    const st = this._readableState;
    if (n === undefined) n = null;
    if (st.buffer.length === 0) {
      if (st.ended) return null;
      this._read(n);
      if (st.buffer.length === 0) return null;
    }
    if (n === null) {
      const chunks = st.buffer;
      st.buffer = [];
      st.length = 0;
      if (chunks.length === 1) return chunks[0];
      return Buffer.concat(chunks);
    }
    let out = Buffer.alloc(0);
    while (st.buffer.length && out.length < n) {
      const chunk = st.buffer.shift();
      st.length -= chunk.length;
      out = out.length === 0 ? chunk : Buffer.concat([out, chunk]);
    }
    return out;
  }

  _read() {}

  push(chunk, encoding) {
    const st = this._readableState;
    if (chunk === null) {
      if (st.ended) return false;
      st.ended = true;
      this.readableEnded = true;
      if (st.flowing && st.buffer.length === 0) this._emitEnd();
      return false;
    }
    const buf = toBuf(chunk, encoding);
    st.buffer.push(buf);
    st.length += buf.length;
    if (st.flowing) this._flow();
    return true;
  }

  unshift(chunk) {
    const st = this._readableState;
    const buf = toBuf(chunk);
    st.buffer.unshift(buf);
    st.length += buf.length;
    return true;
  }

  pipe(dest, options) {
    options = options || {};
    const onData = (chunk) => {
      if (dest.write(chunk) === false && options.end !== false) {
        this.pause();
        dest.once('drain', () => this.resume());
      }
    };
    this.on('data', onData);
    this.on('end', () => {
      if (options.end !== false) dest.end();
    });
    if (dest.emit) dest.emit('pipe', this);
    return dest;
  }

  resume() {
    this._resumeSoon();
    return this;
  }

  pause() {
    this._readableState.flowing = false;
    return this;
  }

  // _resumeSoon starts flowing on the next tick so that listeners attached
  // right after on('data') still observe the 'end' event.
  _resumeSoon() {
    const st = this._readableState;
    st.flowing = true;
    if (st.resumeScheduled) return this;
    st.resumeScheduled = true;
    defer(() => {
      st.resumeScheduled = false;
      if (this.destroyed) return;
      if (st.buffer.length === 0) this._read(null);
      this._flow();
    });
    return this;
  }

  _emitEnd() {
    const st = this._readableState;
    if (st.endEmitted) return;
    st.endEmitted = true;
    this.emit('end');
    this.emit('close');
  }

  _flow() {
    const st = this._readableState;
    while (st.flowing && st.buffer.length) {
      const chunk = st.buffer.shift();
      st.length -= chunk.length;
      this.emit('data', chunk);
    }
    if (st.flowing && st.ended && st.buffer.length === 0) this._emitEnd();
  }

  [Symbol.asyncIterator]() {
    const self = this;
    return {
      next() {
        const st = self._readableState;
        if (st.buffer.length) {
          const chunk = st.buffer.shift();
          st.length -= chunk.length;
          return Promise.resolve({ value: chunk, done: false });
        }
        if (st.ended) return Promise.resolve({ value: undefined, done: true });
        return new Promise((resolve) => {
          const onReadable = () => {
            self.removeListener('readable', onReadable);
            const chunk = st.buffer.shift();
            if (chunk) st.length -= chunk.length;
            resolve({ value: chunk, done: false });
          };
          self.once('readable', onReadable);
        });
      },
      return() {
        return Promise.resolve({ value: undefined, done: true });
      },
    };
  }

  destroy(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    this.readable = false;
    if (typeof this._destroy === 'function') {
      try { this._destroy(err, () => {}); } catch (_) { /* ignore */ }
    }
    this.emit('close');
    return this;
  }

  setEncoding(enc) { this._readableState.encoding = enc; return this; }
}

class Writable extends EventEmitter {
  constructor(options) {
    super();
    options = options || {};
    this.writable = true;
    this.writableEnded = false;
    this.writableFinished = false;
    this.destroyed = false;
    this._writableState = { length: 0, highWaterMark: options.highWaterMark || 16384, objectMode: !!options.objectMode, corked: 0, buffered: [] };
    if (typeof options.write === 'function') this._write = options.write;
    if (typeof options.final === 'function') this._final = options.final;
    if (typeof options.destroy === 'function') this._destroy = options.destroy;
  }

  write(chunk, encoding, cb) {
    if (typeof encoding === 'function') { cb = encoding; encoding = null; }
    if (!this.writable) {
      const err = new Error('Cannot write to a closed stream');
      err.code = 'ERR_STREAM_DESTROYED';
      if (cb) cb(err);
      else this.emit('error', err);
      return false;
    }
    const st = this._writableState;
    const buf = toBuf(chunk, encoding);
    st.length += buf.length;
    const ok = st.length < st.highWaterMark;
    if (typeof this._write === 'function') {
      this._write(buf, encoding, (err) => {
        if (err) this.emit('error', err);
        if (cb) cb(err);
      });
    } else {
      if (cb) cb();
    }
    if (!ok) this.emit('drain');
    return ok;
  }

  end(chunk, encoding, cb) {
    if (typeof chunk === 'function') { cb = chunk; chunk = null; }
    if (typeof encoding === 'function') { cb = encoding; encoding = null; }
    if (chunk !== null && chunk !== undefined) this.write(chunk, encoding);
    this.writableEnded = true;
    if (typeof this._final === 'function') {
      this._final((err) => {
        if (err) this.emit('error', err);
        this.writableFinished = true;
        this.emit('finish');
        if (cb) cb();
      });
    } else {
      this.writableFinished = true;
      this.emit('finish');
      if (cb) cb();
    }
    return this;
  }

  destroy(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    this.writable = false;
    if (typeof this._destroy === 'function') {
      try { this._destroy(err, () => {}); } catch (_) { /* ignore */ }
    }
    this.emit('close');
    return this;
  }
}

class Duplex extends Readable {
  constructor(options) {
    super(options);
    this.writable = true;
    this.writableEnded = false;
    this.writableFinished = false;
    this._writableState = { length: 0, highWaterMark: (options && options.highWaterMark) || 16384, objectMode: !!(options && options.objectMode), corked: 0, buffered: [] };
    if (options && typeof options.write === 'function') this._write = options.write;
    if (options && typeof options.final === 'function') this._final = options.final;
  }

  write(chunk, encoding, cb) { return Writable.prototype.write.call(this, chunk, encoding, cb); }
  end(chunk, encoding, cb) { return Writable.prototype.end.call(this, chunk, encoding, cb); }
}

class Transform extends Duplex {
  constructor(options) {
    super(options);
    this._transforms = [];
    if (options && typeof options.transform === 'function') this._transforms.push(options.transform);
  }

  _transform(chunk, enc, cb) {
    const t = this._transforms.shift();
    if (!t) return cb(null, chunk);
    const res = t.call(this, chunk, enc, cb);
    if (res !== undefined && cb) cb(null, res);
  }

  _write(chunk, enc, cb) {
    this._transform(chunk, enc, (err, out) => {
      if (err) return cb(err);
      if (out !== undefined && out !== null) this.push(out);
      cb();
    });
  }
}

class PassThrough extends Transform {
  constructor(options) {
    super(options);
    this._transforms = [function (chunk, enc, cb) { cb(null, chunk); }];
  }
}

function toBuf(chunk, encoding) {
  if (chunk === null || chunk === undefined) return Buffer.alloc(0);
  if (Buffer.isBuffer(chunk)) return chunk;
  if (typeof chunk === 'string') return Buffer.from(chunk, encoding || 'utf8');
  if (chunk instanceof ArrayBuffer) return Buffer.from(new Uint8Array(chunk));
  if (ArrayBuffer.isView(chunk)) return Buffer.from(new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength));
  return Buffer.from(String(chunk), 'utf8');
}

function pipeline(...streams) {
  const cb = typeof streams[streams.length - 1] === 'function' ? streams.pop() : null;
  const done = (err) => { if (cb) cb(err); };
  if (streams.length === 0) return done();
  let current = streams[0];
  for (let i = 1; i < streams.length; i++) {
    const next = streams[i];
    current.on('error', done);
    next.on('error', done);
    if (typeof current.pipe === 'function') current.pipe(next);
    current = next;
  }
  current.on('finish', () => done());
  current.on('end', () => done());
  return current;
}

function finished(stream, cb) {
  const onFinish = () => { cb(); };
  const onEnd = () => { cb(); };
  stream.on('finish', onFinish);
  stream.on('end', onEnd);
  stream.on('close', onFinish);
  return stream;
}

const stream = module.exports;
stream.Readable = Readable;
stream.Writable = Writable;
stream.Duplex = Duplex;
stream.Transform = Transform;
stream.PassThrough = PassThrough;
stream.pipeline = pipeline;
stream.finished = finished;
stream.getDefaultHighWaterMark = () => 16384;
stream.isReadable = (s) => s && typeof s.pipe === 'function';
stream.isWritable = (s) => s && typeof s.write === 'function';
stream.isDuplex = (s) => stream.isReadable(s) && stream.isWritable(s);
stream.isTransform = (s) => s instanceof Transform;
stream.addAbortSignal = (signal, stream) => {
  signal.addEventListener('abort', () => stream.destroy());
  return stream;
};
stream.Readable.from = function (iterable, options) {
  const rs = new Readable(options);
  (async () => {
    for await (const chunk of iterable) {
      if (!rs.push(chunk)) break;
    }
    rs.push(null);
  })();
  return rs;
};
stream.destroy = (stream, err) => { if (stream && stream.destroy) stream.destroy(err); };
stream.pipeline = pipeline;
