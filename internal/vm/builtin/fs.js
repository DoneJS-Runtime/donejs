'use strict';
// fs — Node.js fs module.

const native = require('__fs');
const enc = require('__encoding');

function toBuffer(data) {
  if (data === undefined || data === null) return Buffer.alloc(0);
  if (typeof data === 'string') return Buffer.from(data, 'utf8');
  if (Buffer.isBuffer(data)) return data;
  if (data instanceof ArrayBuffer) return Buffer.from(new Uint8Array(data));
  if (ArrayBuffer.isView(data)) return Buffer.from(new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
  return Buffer.from(String(data), 'utf8');
}

function decode(buf, encoding) {
  if (!encoding || encoding === 'buffer') return buf;
  return enc.fromBytes(buf, encoding);
}

function optsEncoding(options) {
  if (typeof options === 'string') return options;
  if (options && typeof options === 'object' && options.encoding) return options.encoding;
  return null;
}

function normalizeArgs(args) {
  // returns {path, options, encoding}
  const path = args[0];
  let options = args[1];
  if (typeof options === 'function') options = {};
  const encoding = optsEncoding(options);
  return { path, options: options || {}, encoding };
}

const fs = module.exports;

// --- sync API ---
fs.readFileSync = (p, options) => {
  const enc0 = optsEncoding(options);
  const buf = native.sync('readFile', [p]);
  return decode(Buffer.from(new Uint8Array(buf)), enc0);
};
fs.writeFileSync = (p, data, options) => {
  native.sync('writeFile', [p, toBuffer(data)]);
};
fs.appendFileSync = (p, data, options) => {
  native.sync('appendFile', [p, toBuffer(data)]);
};
fs.existsSync = (p) => native.sync('exists', [p]);
fs.statSync = (p, options) => native.sync('stat', [p]);
fs.lstatSync = (p, options) => native.sync('lstat', [p]);
fs.fstatSync = (fd, options) => native.sync('statFd', [fd]);
fs.readdirSync = (p, options) => {
  if (options && options.withFileTypes) return native.sync('readdirEntries', [p]);
  return native.sync('readdir', [p]);
};
fs.mkdirSync = (p, options) => native.sync('mkdir', [p, options || {}]);
fs.rmdirSync = (p, options) => native.sync('rmdir', [p]);
fs.rmSync = (p, options) => native.sync('rm', [p, options || {}]);
fs.unlinkSync = (p) => native.sync('unlink', [p]);
fs.renameSync = (from, to) => native.sync('rename', [from, to]);
fs.copyFileSync = (from, to, mode) => native.sync('copyFile', [from, to]);
fs.realpathSync = (p, options) => native.sync('realpath', [p]);
fs.accessSync = (p, mode) => native.sync('access', [p]);
fs.chmodSync = (p, mode) => native.sync('chmod', [p, mode]);
fs.truncateSync = (p, len) => native.sync('truncate', [p, len]);
fs.mkdtempSync = (prefix) => native.sync('mkdtemp', [prefix]);
fs.readlinkSync = (p) => native.sync('readlink', [p]);
fs.symlinkSync = (target, path) => native.sync('symlink', [target, path]);
fs.openSync = (p, flags, mode) => native.sync('open', [p, { flag: flags || 'r' }]);
fs.readSync = (fd, buf, offset, length, position) => {
  const r = native.sync('read', [fd, length || 4096]);
  return r && r.length !== undefined ? r.length : 0;
};
fs.writeSync = (fd, buffer, offset, length, position) => {
  const r = native.sync('write', [fd, toBuffer(buffer)]);
  return r || 0;
};
fs.closeSync = (fd) => native.sync('close', [fd]);

// --- async API (callback style) ---
function asyncFn(name, argCount) {
  return function (...args) {
    const cb = args.pop();
    if (typeof cb !== 'function') throw new TypeError('callback must be a function');
    const p = native.async(name, args);
    p.then((v) => cb(null, v), (e) => cb(e));
  };
}

fs.readFile = function (p, options, cb) {
  if (typeof options === 'function') { cb = options; options = {}; }
  const encoding = optsEncoding(options);
  native.async('readFile', [p]).then(
    (buf) => cb(null, decode(Buffer.from(new Uint8Array(buf)), encoding)),
    (e) => cb(e)
  );
};
fs.writeFile = function (p, data, options, cb) {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('writeFile', [p, toBuffer(data)]).then(() => cb(null), (e) => cb(e));
};
fs.appendFile = function (p, data, options, cb) {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('appendFile', [p, toBuffer(data)]).then(() => cb(null), (e) => cb(e));
};
fs.stat = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('stat', [p]).then((v) => cb(null, v), (e) => cb(e));
};
fs.lstat = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('lstat', [p]).then((v) => cb(null, v), (e) => cb(e));
};
fs.fstat = (fd, cb) => native.async('statFd', [fd]).then((v) => cb(null, v), (e) => cb(e));
fs.readdir = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  const name = options && options.withFileTypes ? 'readdirEntries' : 'readdir';
  native.async(name, [p]).then((v) => cb(null, v), (e) => cb(e));
};
fs.mkdir = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('mkdir', [p, options || {}]).then(() => cb(null), (e) => cb(e));
};
fs.rmdir = (p, options, cb) => native.async('rmdir', [p]).then(() => cb(null), (e) => cb(e));
fs.rm = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('rm', [p, options || {}]).then(() => cb(null), (e) => cb(e));
};
fs.unlink = (p, cb) => native.async('unlink', [p]).then(() => cb(null), (e) => cb(e));
fs.rename = (from, to, cb) => native.async('rename', [from, to]).then(() => cb(null), (e) => cb(e));
fs.copyFile = (from, to, mode, cb) => {
  if (typeof mode === 'function') { cb = mode; mode = 0; }
  native.async('copyFile', [from, to]).then(() => cb(null), (e) => cb(e));
};
fs.realpath = (p, options, cb) => {
  if (typeof options === 'function') { cb = options; options = {}; }
  native.async('realpath', [p]).then((v) => cb(null, v), (e) => cb(e));
};
fs.access = (p, mode, cb) => {
  if (typeof mode === 'function') { cb = mode; mode = 0; }
  native.async('access', [p]).then(() => cb(null), (e) => cb(e));
};
fs.chmod = (p, mode, cb) => native.async('chmod', [p, mode]).then(() => cb(null), (e) => cb(e));
fs.truncate = (p, len, cb) => native.async('truncate', [p, len]).then(() => cb(null), (e) => cb(e));
fs.mkdtemp = (prefix, cb) => native.async('mkdtemp', [prefix]).then((v) => cb(null, v), (e) => cb(e));
fs.readlink = (p, cb) => native.async('readlink', [p]).then((v) => cb(null, v), (e) => cb(e));
fs.symlink = (target, path, cb) => native.async('symlink', [target, path]).then(() => cb(null), (e) => cb(e));
fs.open = (p, flags, mode, cb) => {
  if (typeof mode === 'function') { cb = mode; mode = 0; }
  if (typeof flags === 'function') { cb = flags; flags = 'r'; }
  native.async('open', [p, { flag: flags || 'r' }]).then((v) => cb(null, v), (e) => cb(e));
};
fs.close = (fd, cb) => native.async('close', [fd]).then(() => cb(null), (e) => cb(e));

fs.exists = (p, cb) => native.async('exists', [p]).then((v) => cb(v), () => cb(false));

// --- promises API ---
const promises = {};
fs.promises = promises;

promises.readFile = (p, options) => native.async('readFile', [p]).then((buf) => {
  const encoding = optsEncoding(options);
  return decode(Buffer.from(new Uint8Array(buf)), encoding);
});
promises.writeFile = (p, data) => native.async('writeFile', [p, toBuffer(data)]);
promises.appendFile = (p, data) => native.async('appendFile', [p, toBuffer(data)]);
promises.stat = (p) => native.async('stat', [p]);
promises.lstat = (p) => native.async('lstat', [p]);
promises.fstat = (fd) => native.async('statFd', [fd]);
promises.readdir = (p, options) => {
  const name = options && options.withFileTypes ? 'readdirEntries' : 'readdir';
  return native.async(name, [p]);
};
promises.mkdir = (p, options) => native.async('mkdir', [p, options || {}]);
promises.rmdir = (p) => native.async('rmdir', [p]);
promises.rm = (p, options) => native.async('rm', [p, options || {}]);
promises.unlink = (p) => native.async('unlink', [p]);
promises.rename = (from, to) => native.async('rename', [from, to]);
promises.copyFile = (from, to) => native.async('copyFile', [from, to]);
promises.realpath = (p) => native.async('realpath', [p]);
promises.access = (p) => native.async('access', [p]);
promises.chmod = (p, mode) => native.async('chmod', [p, mode]);
promises.truncate = (p, len) => native.async('truncate', [p, len]);
promises.mkdtemp = (prefix) => native.async('mkdtemp', [prefix]);
promises.readlink = (p) => native.async('readlink', [p]);
promises.symlink = (target, path) => native.async('symlink', [target, path]);
promises.open = (p, flags) => native.async('open', [p, { flag: flags || 'r' }]);
promises.close = (fd) => native.async('close', [fd]);
promises.copyFile = (from, to) => native.async('copyFile', [from, to]);
promises.cp = (from, to, options) => native.async('rm', [to, { recursive: true, force: true }])
  .then(() => native.async('copyFile', [from, to]));

fs.constants = native.constants;
fs.ReadStream = require('stream').ReadStream;
fs.WriteStream = require('stream').WriteStream;

module.exports = fs;
module.exports.default = fs;
