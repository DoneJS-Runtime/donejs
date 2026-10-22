'use strict';
// zlib — Node.js zlib module.

const native = require('__zlib');

const zlib = module.exports;

function toBuf(data) {
  if (Buffer.isBuffer(data)) return data;
  if (typeof data === 'string') return Buffer.from(data, 'utf8');
  if (data instanceof ArrayBuffer) return Buffer.from(new Uint8Array(data));
  if (ArrayBuffer.isView(data)) return Buffer.from(new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
  return Buffer.from(String(data), 'utf8');
}

zlib.gzipSync = (buf) => Buffer.from(new Uint8Array(native.sync('gzip', [toBuf(buf)])));
zlib.gunzipSync = (buf) => Buffer.from(new Uint8Array(native.sync('gunzip', [toBuf(buf)])));
zlib.deflateSync = (buf) => Buffer.from(new Uint8Array(native.sync('deflate', [toBuf(buf)])));
zlib.inflateSync = (buf) => Buffer.from(new Uint8Array(native.sync('inflate', [toBuf(buf)])));
zlib.deflateRawSync = (buf) => Buffer.from(new Uint8Array(native.sync('deflateRaw', [toBuf(buf)])));
zlib.inflateRawSync = (buf) => Buffer.from(new Uint8Array(native.sync('inflateRaw', [toBuf(buf)])));
zlib.crc32 = (buf) => native.sync('crc32', [toBuf(buf)]);

zlib.gzip = (buf, cb) => native.async('gzip', [toBuf(buf)]).then((b) => cb(null, Buffer.from(new Uint8Array(b))), (e) => cb(e));
zlib.gunzip = (buf, cb) => native.async('gunzip', [toBuf(buf)]).then((b) => cb(null, Buffer.from(new Uint8Array(b))), (e) => cb(e));
zlib.deflate = (buf, cb) => native.async('deflate', [toBuf(buf)]).then((b) => cb(null, Buffer.from(new Uint8Array(b))), (e) => cb(e));
zlib.inflate = (buf, cb) => native.async('inflate', [toBuf(buf)]).then((b) => cb(null, Buffer.from(new Uint8Array(b))), (e) => cb(e));

zlib.constants = {
  Z_NO_COMPRESSION: 0,
  Z_BEST_SPEED: 1,
  Z_BEST_COMPRESSION: 9,
  Z_DEFAULT_COMPRESSION: -1,
  ZLIB_VERSION: '1.3',
  BROTLI_PARAM_QUALITY: 0,
};
