'use strict';
// Buffer — Node.js Buffer, implemented on top of Uint8Array.

const enc = require('__encoding');

class Buffer extends Uint8Array {
  constructor(arg, encodingOrOffset, length) {
    if (typeof arg === 'number') {
      super(arg);
      if (encodingOrOffset) this.fill(encodingOrOffset);
    } else if (arg instanceof ArrayBuffer) {
      super(arg);
    } else if (ArrayBuffer.isView(arg)) {
      super(arg.buffer, arg.byteOffset, arg.byteLength);
    } else if (arg === undefined || arg === null) {
      super(0);
    } else if (typeof arg === 'object' && arg.type === 'Buffer' && arg.data) {
      const b = new Uint8Array(arg.data);
      super(b.buffer, b.byteOffset, b.byteLength);
    } else {
      super(0);
    }
  }

  static alloc(size, fill, encoding) {
    const buf = new Buffer(size);
    if (fill !== undefined && fill !== 0) {
      if (typeof fill === 'string') buf.fill(fill, encoding || 'utf8');
      else buf.fill(fill);
    }
    return buf;
  }

  static allocUnsafe(size) { return new Buffer(size); }

  static from(data, encodingOrOffset, length) {
    if (typeof data === 'string') {
      return new Buffer(enc.toBytes(data, encodingOrOffset || 'utf8'));
    }
    if (Buffer.isBuffer(data)) return Buffer.from(data);
    if (data instanceof ArrayBuffer) return new Buffer(data);
    if (ArrayBuffer.isView(data)) {
      return new Buffer(data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength));
    }
    if (Array.isArray(data)) return Buffer.from(data);
    if (data && data.type === 'Buffer' && Array.isArray(data.data)) return Buffer.from(data.data);
    throw new TypeError('The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object.');
  }

  static of(...items) { return Buffer.from(items); }

  static concat(list, totalLength) {
    const buffers = list.map((b) => Buffer.isBuffer(b) ? b : Buffer.from(b));
    let len = totalLength;
    if (len === undefined) {
      len = 0;
      for (const b of buffers) len += b.length;
    }
    const out = Buffer.alloc(len);
    let off = 0;
    for (const b of buffers) {
      if (off >= len) break;
      const n = Math.min(b.length, len - off);
      out.set(b.subarray(0, n), off);
      off += n;
    }
    return out;
  }

  static isBuffer(obj) {
    return obj instanceof Buffer ||
      (obj && obj.constructor && obj.constructor.isBuffer && obj.constructor.isBuffer(obj)) ||
      (obj && obj.type === 'Buffer' && Array.isArray(obj.data));
  }

  static isEncoding(encoding) {
    return ['utf8', 'utf-8', 'ascii', 'latin1', 'binary', 'ucs2', 'ucs-2', 'utf16le', 'utf-16le', 'hex', 'base64', 'base64url'].includes(String(encoding).toLowerCase());
  }

  static byteLength(string, encoding) {
    return enc.byteLength(String(string), encoding || 'utf8');
  }

  static compare(buf1, buf2) {
    const a = Buffer.isBuffer(buf1) ? buf1 : Buffer.from(buf1);
    const b = Buffer.isBuffer(buf2) ? buf2 : Buffer.from(buf2);
    const len = Math.min(a.length, b.length);
    for (let i = 0; i < len; i++) {
      if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
    }
    if (a.length === b.length) return 0;
    return a.length < b.length ? -1 : 1;
  }

  static isUtf8(buf) {
    try { new TextDecoder('utf-8', { fatal: true }).decode(buf); return true; } catch (_) { return false; }
  }

  toString(encoding, start, end) {
    const e = (encoding || 'utf8').toLowerCase();
    const s = start === undefined ? 0 : start;
    const t = end === undefined ? this.length : end;
    const slice = this.subarray(s, t);
    if (e === 'utf8' || e === 'utf-8') return new TextDecoder('utf-8').decode(slice);
    return enc.fromBytes(slice, e);
  }

  toJSON() {
    return { type: 'Buffer', data: Array.from(this) };
  }

  write(string, offset, length, encoding) {
    if (typeof offset === 'string') { encoding = offset; offset = 0; }
    if (typeof length === 'string') { encoding = length; length = undefined; }
    offset = offset || 0;
    length = length === undefined ? this.length - offset : length;
    const bytes = enc.toBytes(String(string), encoding || 'utf8');
    const n = Math.min(bytes.length, length, this.length - offset);
    if (n <= 0) return 0;
    this.set(bytes.subarray(0, n), offset);
    return n;
  }

  copy(target, targetStart, sourceStart, sourceEnd) {
    targetStart = targetStart || 0;
    sourceStart = sourceStart || 0;
    sourceEnd = sourceEnd === undefined ? this.length : sourceEnd;
    const slice = this.subarray(sourceStart, sourceEnd);
    const n = Math.min(slice.length, target.length - targetStart);
    if (n <= 0) return 0;
    target.set(slice.subarray(0, n), targetStart);
    return n;
  }

  slice(start, end) { return this.subarray(start, end); }

  fill(value, offset, end, encoding) {
    if (typeof offset === 'string') { encoding = offset; offset = 0; }
    if (typeof end === 'string') { encoding = end; end = undefined; }
    offset = offset || 0;
    end = end === undefined ? this.length : end;
    if (typeof value === 'string') {
      const bytes = enc.toBytes(value, encoding || 'utf8');
      for (let i = offset; i < end; i++) this[i] = bytes[0] || 0;
    } else {
      for (let i = offset; i < end; i++) this[i] = value;
    }
    return this;
  }

  includes(value, byteOffset, encoding) {
    if (typeof value === 'string') {
      const needle = enc.toBytes(value, encoding || 'utf8');
      return indexOfBytes(this, needle, byteOffset || 0) !== -1;
    }
    return this.indexOf(value, byteOffset || 0) !== -1;
  }

  indexOf(value, byteOffset) {
    if (typeof value === 'string') value = enc.toBytes(value, 'utf8');
    return indexOfBytes(this, value, byteOffset || 0);
  }

  lastIndexOf(value, byteOffset) {
    if (typeof value === 'string') value = enc.toBytes(value, 'utf8');
    for (let i = this.length - value.length; i >= (byteOffset || 0); i--) {
      let match = true;
      for (let j = 0; j < value.length; j++) {
        if (this[i + j] !== value[j]) { match = false; break; }
      }
      if (match) return i;
    }
    return -1;
  }

  swap16() { const b = Buffer.from(this); for (let i = 0; i + 1 < b.length; i += 2) { const t = b[i]; b[i] = b[i + 1]; b[i + 1] = t; } return b; }
  swap32() { const b = Buffer.from(this); for (let i = 0; i + 3 < b.length; i += 4) { const t = b[i]; b[i] = b[i + 3]; b[i + 3] = t; b[i + 1] = b[i + 2]; b[i + 2] = b[i + 1]; } return b; }
  swap64() { return Buffer.from(this); }

  readUInt8(offset) { return this.readUInt(offset, 1); }
  readInt8(offset) { const v = this.readUInt8(offset); return v > 127 ? v - 256 : v; }
  readUInt16BE(offset) { return this.readUInt(offset, 2, false); }
  readUInt16LE(offset) { return this.readUInt(offset, 2, true); }
  readInt16BE(offset) { const v = this.readUInt16BE(offset); return v > 32767 ? v - 65536 : v; }
  readInt16LE(offset) { const v = this.readUInt16LE(offset); return v > 32767 ? v - 65536 : v; }
  readUInt32BE(offset) { return this.readUInt(offset, 4, false); }
  readUInt32LE(offset) { return this.readUInt(offset, 4, true); }
  readInt32BE(offset) { const v = this.readUInt32BE(offset); return v > 2147483647 ? v - 4294967296 : v; }
  readInt32LE(offset) { const v = this.readUInt32LE(offset); return v > 2147483647 ? v - 4294967296 : v; }
  readFloatBE(offset) { const b = Buffer.from(this.subarray(offset, offset + 4)); b.swap32(); return b.readFloatLE(0); }
  readFloatLE(offset) { const v = new DataView(this.buffer, this.byteOffset + offset, 4).getFloat32(0, true); return v; }
  readDoubleBE(offset) { const v = new DataView(this.buffer, this.byteOffset + offset, 8).getFloat64(0, false); return v; }
  readDoubleLE(offset) { const v = new DataView(this.buffer, this.byteOffset + offset, 8).getFloat64(0, true); return v; }

  readUInt(offset, byteLength, littleEndian) {
    let val = 0;
    if (littleEndian) {
      for (let i = byteLength - 1; i >= 0; i--) val = val * 256 + this[offset + i];
    } else {
      for (let i = 0; i < byteLength; i++) val = val * 256 + this[offset + i];
    }
    return val;
  }

  writeUInt8(value, offset) { this[offset] = value & 0xff; return offset + 1; }
  writeUInt16BE(value, offset) { return this.writeUInt(value, offset, 2, false); }
  writeUInt16LE(value, offset) { return this.writeUInt(value, offset, 2, true); }
  writeInt8(value, offset) { return this.writeUInt8(value < 0 ? value + 256 : value, offset); }
  writeInt16BE(value, offset) { return this.writeUInt16BE(value < 0 ? value + 65536 : value, offset); }
  writeInt16LE(value, offset) { return this.writeUInt16LE(value < 0 ? value + 65536 : value, offset); }
  writeUInt32BE(value, offset) { return this.writeUInt(value, offset, 4, false); }
  writeUInt32LE(value, offset) { return this.writeUInt(value, offset, 4, true); }
  writeInt32BE(value, offset) { return this.writeUInt32BE(value < 0 ? value + 4294967296 : value, offset); }
  writeInt32LE(value, offset) { return this.writeUInt32LE(value < 0 ? value + 4294967296 : value, offset); }
  writeFloatBE(value, offset) { const b = Buffer.alloc(4); b.writeFloatLE(value, 0); b.swap32(); this.set(b, offset); return offset + 4; }
  writeFloatLE(value, offset) { new DataView(this.buffer, this.byteOffset + offset, 4).setFloat32(0, value, true); return offset + 4; }
  writeDoubleBE(value, offset) { new DataView(this.buffer, this.byteOffset + offset, 8).setFloat64(0, value, false); return offset + 8; }
  writeDoubleLE(value, offset) { new DataView(this.buffer, this.byteOffset + offset, 8).setFloat64(0, value, true); return offset + 8; }

  writeUInt(value, offset, byteLength, littleEndian) {
    let v = value;
    for (let i = 0; i < byteLength; i++) {
      const byte = v % 256;
      this[offset + (littleEndian ? i : byteLength - 1 - i)] = byte;
      v = Math.floor(v / 256);
    }
    return offset + byteLength;
  }

  readBigInt64BE(offset) { return BigInt(this.readUInt32BE(offset)) * 4294967296n + BigInt(this.readUInt32BE(offset + 4)); }
  readBigInt64LE(offset) { return BigInt(this.readUInt32LE(offset + 4)) * 4294967296n + BigInt(this.readUInt32LE(offset)); }
  writeBigInt64BE(value, offset) { this.writeUInt32BE(Number((value >> 32n) & 0xffffffffn), offset); this.writeUInt32BE(Number(value & 0xffffffffn), offset + 4); return offset + 8; }
  writeBigInt64LE(value, offset) { this.writeUInt32LE(Number(value & 0xffffffffn), offset); this.writeUInt32LE(Number((value >> 32n) & 0xffffffffn), offset + 4); return offset + 8; }

  equals(other) { return Buffer.compare(this, other) === 0; }
  compare(target) { return Buffer.compare(this, target); }

  *[Symbol.iterator]() {
    for (let i = 0; i < this.length; i++) yield this[i];
  }
}

function indexOfBytes(haystack, needle, start) {
  if (needle.length === 0) return start;
  outer: for (let i = start; i + needle.length <= haystack.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (haystack[i + j] !== needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

Buffer.poolSize = 8192;
Buffer.prototype.parent = undefined;
Buffer.prototype.offset = undefined;

module.exports = Buffer;
