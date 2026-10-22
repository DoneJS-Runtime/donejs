'use strict';
// util — Node.js util module.

const { EventEmitter } = require('events');
const nativeEncoding = require('__encoding');

const inspect = require('__inspect');

const format = inspect.format;
const inspectCustom = Symbol.for('nodejs.util.inspect.custom');

const util = module.exports;

util.format = format;
util.inspect = inspect.inspect;
util.isArray = Array.isArray;
util.isBoolean = (v) => typeof v === 'boolean';
util.isBuffer = (v) => Buffer.isBuffer(v);
util.isDate = (v) => v instanceof Date;
util.isError = (v) => v instanceof Error;
util.isFunction = (v) => typeof v === 'function';
util.isNull = (v) => v === null;
util.isNullOrUndefined = (v) => v == null;
util.isNumber = (v) => typeof v === 'number';
util.isObject = (v) => v !== null && (typeof v === 'object' || typeof v === 'function');
util.isPrimitive = (v) => v === null || (typeof v !== 'object' && typeof v !== 'function');
util.isRegExp = (v) => v instanceof RegExp;
util.isMap = (v) => v instanceof Map;
util.isSet = (v) => v instanceof Set;
util.isPromise = (v) => v instanceof Promise;
util.isProxy = () => false;
util.isAsyncFunction = (v) => typeof v === 'function' && v.constructor && v.constructor.name === 'AsyncFunction';
util.isGeneratorFunction = (v) => typeof v === 'function' && v.constructor && v.constructor.name === 'GeneratorFunction';
util.isTypedArray = (v) => ArrayBuffer.isView(v) && !(v instanceof DataView);
util.isAnyArrayBuffer = (v) => v instanceof ArrayBuffer;
util.isArrayBufferView = (v) => ArrayBuffer.isView(v);
util.isDataView = (v) => v instanceof DataView;
util.isDeepStrictEqual = require('__util').isDeepStrictEqual;

util.inherits = function (ctor, superCtor) {
  if (typeof ctor !== 'function') throw new TypeError('The constructor to "inherits" must be a function');
  if (typeof superCtor !== 'function') throw new TypeError('The super constructor to "inherits" must be a function');
  Object.setPrototypeOf(ctor.prototype, superCtor.prototype);
  Object.defineProperty(ctor, 'super_', {
    value: superCtor,
    writable: true,
    configurable: true,
  });
};

util.promisify = function (fn, options) {
  if (typeof fn !== 'function') throw new TypeError('The "original" argument must be of type Function');
  const custom = options && options.custom;
  const context = options && options.context;
  const multiArgs = options && options.multiArgs;
  const reflect = custom ? Reflect : null;
  const fnName = fn.name || '';

  function fn(...args) {
    return new Promise((resolve, reject) => {
      args.push((err, ...values) => {
        if (err) return reject(err);
        if (custom) {
          const target = custom(resolve, reject, ...values);
          reflectApply(target, this, values);
        } else if (multiArgs) {
          resolve(values);
        } else {
          resolve(values[0]);
        }
      });
      const thisArg = context ? context : this;
      return Reflect.apply(fn, thisArg, args);
    });
  }

  Object.setPrototypeOf(fn, Object.getPrototypeOf(original_fn_ref(fn)));
  fn[util.promisify.custom] = fn;
  Object.defineProperty(fn, 'name', { value: fnName, configurable: true });
  return fn;
};
util.promisify.custom = Symbol('util.promisify.custom');

function original_fn_ref(fn) { return fn; }
function reflectApply(target, thisArg, args) {
  if (target && typeof target === 'function') {
    try { return Reflect.apply(target, thisArg, args); } catch (_) { /* ignore */ }
  }
  return undefined;
}

util.callbackify = function (fn) {
  if (typeof fn !== 'function') throw new TypeError('The "original" argument must be of type Function');
  function callbackified(...args) {
    const maybeCb = args.pop();
    if (typeof maybeCb !== 'function') throw new TypeError('The last argument must be of type Function');
    const self = this;
    args.push((...cbArgs) => {
      const err = cbArgs.shift();
      return maybeCb.apply(self, err ? [err] : [null, ...cbArgs]);
    });
    return Reflect.apply(fn, self, args);
  }
  Object.defineProperty(callbackified, 'name', { value: fn.name, configurable: true });
  return callbackified;
};

util.deprecate = function (fn, msg, code) {
  if (typeof fn !== 'function') throw new TypeError('The "fn" argument must be of type Function');
  let warned = false;
  function deprecated(...args) {
    if (!warned) {
      warned = true;
      process.emitWarning(msg || 'Deprecated', code || 'DeprecationWarning');
    }
    return Reflect.apply(fn, this, args);
  }
  return deprecated;
};

util.debuglog = function (section) {
  if (process.env.DONEJS_DEBUG === section || process.env.NODE_DEBUG === section) {
    return (...args) => console.error(...args);
  }
  return () => {};
};

util.text = {
  encode: (input) => nativeEncoding.toBytes(String(input), 'utf8'),
  decode: (input) => nativeEncoding.fromBytes(input, 'utf8'),
  encodeInto: (input, dest) => {
    const bytes = nativeEncoding.toBytes(String(input), 'utf8');
    const view = new Uint8Array(dest.buffer || dest, dest.byteOffset || 0, dest.byteLength || dest.length);
    const n = Math.min(bytes.length, view.length);
    view.set(bytes.subarray(0, n));
    return { read: n, written: n };
  },
};

class TextEncoder {
  get encoding() { return 'utf-8'; }
  encode(input = '') { return util.text.encode(input); }
  encodeInto(src, dest) { return util.text.encodeInto(src, dest); }
}
class TextDecoder {
  constructor(label = 'utf-8', options = {}) {
    this._encoding = String(label).toLowerCase();
    this._fatal = !!options.fatal;
    this._ignoreBOM = !!options.ignoreBOM;
  }
  get encoding() { return this._encoding === 'utf8' ? 'utf-8' : this._encoding; }
  decode(input) {
    if (input === undefined) return '';
    return nativeEncoding.fromBytes(input, this._encoding);
  }
}

util.TextEncoder = TextEncoder;
util.TextDecoder = TextDecoder;
util.types = {
  isDate: util.isDate,
  isRegExp: util.isRegExp,
  isMap: util.isMap,
  isSet: util.isSet,
  isPromise: util.isPromise,
  isProxy: util.isProxy,
  isTypedArray: util.isTypedArray,
  isAnyArrayBuffer: util.isAnyArrayBuffer,
  isArrayBufferView: util.isArrayBufferView,
  isDataView: util.isDataView,
};
util.inspect.custom = inspectCustom;
util.inspect.replacer = inspect.replacer;

util.stripVTControlCharacters = function (str) {
  if (typeof str !== 'string') throw new TypeError('The "str" argument must be of type string');
  // eslint-disable-next-line no-control-regex
  return str.replace(/\x1B\[[0-9;]*m/g, '');
};

util.parseArgs = function (args, options) {
  const out = { values: {}, positionals: [] };
  const opts = options || {};
  const strict = opts.strict !== false;
  const allowPositionals = opts.allowPositionals !== false;
  let i = 0;
  while (i < args.length) {
    const a = args[i];
    if (a === '--') { i++; break; }
    if (a.startsWith('--')) {
      const eq = a.indexOf('=');
      let name, value;
      if (eq >= 0) { name = a.slice(2, eq); value = a.slice(eq + 1); }
      else { name = a.slice(2); value = true; }
      let type = 'string';
      if (opts.string && opts.string.includes(name)) type = 'string';
      else if (opts.boolean && opts.boolean.includes(name)) type = 'boolean';
      else if (opts.number && opts.number.includes(name)) type = 'number';
      if (type === 'string' && value === true) {
        if (i + 1 < args.length && !args[i + 1].startsWith('-')) { value = args[++i]; }
        else if (strict) throw new Error(`Option '--${name}' argument missing`);
      }
      if (type === 'number' && value !== true) value = Number(value);
      if (type === 'boolean' && value !== true) value = value !== 'false';
      out.values[name] = value;
    } else if (a.startsWith('-') && a.length > 1) {
      const name = a.slice(1);
      let value = true;
      if (i + 1 < args.length && !args[i + 1].startsWith('-')) value = args[++i];
      else if (strict) throw new Error(`Option '-${name}' argument missing`);
      out.values[name] = value;
    } else {
      if (!allowPositionals) throw new Error(`Unexpected argument '${a}'`);
      out.positionals.push(a);
    }
    i++;
  }
  for (; i < args.length; i++) out.positionals.push(args[i]);
  return out;
};

util.inspect = inspect.inspect;
util.format = format;
util.formatWithOptions = function (opts, ...args) { return format(...args); };
util.getSystemErrorName = function (err) { return err && err.code ? err.code : 'UNKNOWN'; };
util.getSystemErrorCode = function (err) { return err && err.errno ? err.errno : -1; };

module.exports = util;
module.exports.default = util;
