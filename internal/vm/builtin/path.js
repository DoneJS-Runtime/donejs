'use strict';
// path — Node.js path module (posix + win32).

const native = require('__path');

// parse is implemented here so that the key order of the result matches
// Node's ({root, dir, base, ext, name}).
function parseRoot(p, win) {
  if (!win) return p.charAt(0) === '/' ? '/' : '';
  const drive = /^([A-Za-z]:)([\\/]?)/.exec(p);
  if (drive) return drive[2] ? drive[1] + drive[2] : drive[1];
  const first = p.charAt(0);
  if (first === '\\' || first === '/') {
    const second = p.charAt(1);
    if (second === '\\' || second === '/') {
      const unc = /^([^\\/]+)[\\/]+([^\\/]+)/.exec(p.slice(2));
      if (unc) {
        const cut = 2 + unc[0].length;
        if (cut < p.length) return p.slice(0, cut) + p.charAt(cut);
        return p.slice(0, cut);
      }
      return p.slice(0, 2);
    }
    return first;
  }
  return '';
}

function splitExt(base) {
  const dot = base.lastIndexOf('.');
  if (dot <= 0) return ['', base];
  // "." and ".." are names, not extensions.
  if (base.length <= 2 && /^\.+$/.test(base)) return ['', base];
  return [base.slice(dot), base.slice(0, dot)];
}

function parseImpl(input, win) {
  const p = String(input);
  const root = parseRoot(p, win);
  const isSep = win
    ? (ch) => ch === '\\' || ch === '/'
    : (ch) => ch === '/';
  const rest = p.slice(root.length);
  let end = p.length;
  while (end > root.length && isSep(p.charAt(end - 1))) end--;
  const trimmed = p.slice(0, end);
  let cut = -1;
  for (let i = trimmed.length - 1; i >= 0; i--) {
    if (isSep(trimmed.charAt(i))) {
      cut = i;
      break;
    }
  }
  let base;
  let dir;
  if (rest === '') {
    base = '';
    dir = root;
  } else {
    base = cut >= 0 ? trimmed.slice(cut + 1) : trimmed.slice(root.length);
    dir = cut >= 0 ? trimmed.slice(0, cut) : root;
    if (dir.length < root.length) dir = root;
  }
  const [ext, name] = splitExt(base);
  return { root, dir, base, ext, name };
}

function wrap(table, sep, delimiter, win) {
  const api = {
    sep,
    delimiter,
    normalize: (p) => table('normalize', [p]),
    join: (...parts) => table('join', parts),
    resolve: (...parts) => table('resolve', parts),
    dirname: (p) => table('dirname', [p]),
    basename: (p, ext) => {
      const b = table('basename', [p]);
      if (ext && b.endsWith(ext) && b !== ext) return b.slice(0, -ext.length);
      return b;
    },
    extname: (p) => table('extname', [p]),
    relative: (from, to) => table('relative', [from, to]),
    isAbsolute: (p) => table('isAbsolute', [p]),
    parse: (p) => parseImpl(p, win),
    format: (o) => table('format', [o]),
    toNamespacedPath: (p) => table('toNamespacedPath', [p]),
  };
  return api;
}

const posix = wrap(native.posix, '/', ':', false);
const win32 = wrap(native.win32, '\\', ';', true);

const path = posix;
path.posix = posix;
path.win32 = win32;
path.sep = posix.sep;
path.delimiter = posix.delimiter;
path.normalize = posix.normalize;
path.join = posix.join;
path.resolve = posix.resolve;
path.dirname = posix.dirname;
path.basename = posix.basename;
path.extname = posix.extname;
path.relative = posix.relative;
path.isAbsolute = posix.isAbsolute;
path.parse = posix.parse;
path.format = posix.format;
path.toNamespacedPath = posix.toNamespacedPath;

module.exports = path;
module.exports.default = path;
