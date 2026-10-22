'use strict';
// url — Node.js url module (WHATWG URL + legacy helpers).

const native = require('__url');
const querystring = require('querystring');

class URLSearchParams {
  constructor(init) {
    this._list = [];
    if (typeof init === 'string') {
      const s = init.startsWith('?') ? init.slice(1) : init;
      for (const part of s.split('&')) {
        if (!part) continue;
        const eq = part.indexOf('=');
        const k = eq < 0 ? part : part.slice(0, eq);
        const v = eq < 0 ? '' : part.slice(eq + 1);
        this._list.push([decodeURIComponent(k.replace(/\+/g, ' ')), decodeURIComponent(v.replace(/\+/g, ' '))]);
      }
    } else if (init && typeof init === 'object') {
      if (Array.isArray(init)) {
        for (const pair of init) this._list.push([String(pair[0]), String(pair[1])]);
      } else {
        for (const k of Object.keys(init)) this._list.push([k, String(init[k])]);
      }
    }
  }

  append(key, value) { this._list.push([String(key), String(value)]); }
  delete(key) { const k = String(key); this._list = this._list.filter((p) => p[0] !== k); }
  get(key) { const k = String(key); const hit = this._list.find((p) => p[0] === k); return hit ? hit[1] : null; }
  getAll(key) { const k = String(key); return this._list.filter((p) => p[0] === k).map((p) => p[1]); }
  has(key) { const k = String(key); return this._list.some((p) => p[0] === k); }
  set(key, value) {
    const k = String(key);
    const idx = this._list.findIndex((p) => p[0] === k);
    if (idx === -1) this._list.push([k, String(value)]);
    else {
      this._list[idx] = [k, String(value)];
      this._list = this._list.filter((p, i) => p[0] !== k || i === idx);
    }
  }
  sort() { this._list.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0)); }
  forEach(fn, thisArg) { for (const [k, v] of this._list) fn.call(thisArg, v, k, this); }
  *entries() { for (const p of this._list) yield [p[0], p[1]]; }
  *keys() { for (const p of this._list) yield p[0]; }
  *values() { for (const p of this._list) yield p[1]; }
  [Symbol.iterator]() { return this.entries(); }
  toString() {
    return this._list.map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v)).join('&');
  }
  get size() { return this._list.length; }
}

class URL {
  constructor(input, base) {
    const parsed = native.parse(String(input), base === undefined ? null : String(base));
    this._p = parsed;
  }

  get href() { return this._p.href; }
  set href(v) { this._p = native.parse(String(v), null); }
  get protocol() { return this._p.protocol; }
  set protocol(v) { this._p = native.parse(String(v) + '//' + this._p.host + this._p.pathname + this._p.search + this._p.hash, null); }
  get host() { return this._p.host; }
  get hostname() { return this._p.hostname; }
  get port() { return this._p.port; }
  get pathname() { return this._p.pathname; }
  get search() { return this._p.search; }
  set search(v) {
    const s = String(v);
    this._p.search = s ? (s.startsWith('?') ? s : '?' + s) : '';
    this._p.href = rebuild(this._p);
  }
  get hash() { return this._p.hash; }
  set hash(v) {
    const s = String(v);
    this._p.hash = s ? (s.startsWith('#') ? s : '#' + s) : '';
    this._p.href = rebuild(this._p);
  }
  get origin() { return this._p.origin; }
  get username() { return this._p.username; }
  get password() { return this._p.password; }
  get searchParams() {
    if (!this._searchParams) {
      this._searchParams = new URLSearchParams(this._p.search);
    }
    return this._searchParams;
  }
  toString() { return this._p.href; }
  toJSON() { return this._p.href; }
}

function rebuild(p) {
  return p.protocol + '//' + p.host + p.pathname + p.search + p.hash;
}

const url = module.exports;
url.URL = URL;
url.URLSearchParams = URLSearchParams;
url.parse = function (urlStr, parseQueryString, slashesDenoteHost) {
  const p = native.parse(String(urlStr), null);
  const out = {
    protocol: p.protocol,
    slashes: p.host ? true : null,
    auth: p.username ? (p.password ? p.username + ':' + p.password : p.username) : null,
    host: p.host,
    hostname: p.hostname,
    port: p.port,
    pathname: p.pathname,
    search: p.search,
    query: p.search ? querystring.parse(p.search.slice(1)) : {},
    hash: p.hash,
    href: p.href,
  };
  if (!parseQueryString) out.query = p.search ? p.search.slice(1) : null;
  return out;
};
url.format = function (urlObj) {
  if (typeof urlObj === 'string') return urlObj;
  if (!urlObj) return '';
  let result = '';
  if (urlObj.protocol) result += urlObj.protocol.endsWith(':') ? urlObj.protocol + '//' : urlObj.protocol + '://';
  if (urlObj.host) result += urlObj.host;
  else if (urlObj.hostname) {
    result += urlObj.hostname;
    if (urlObj.port) result += ':' + urlObj.port;
  }
  if (urlObj.pathname) result += urlObj.pathname;
  if (urlObj.search) result += urlObj.search.startsWith('?') ? urlObj.search : '?' + urlObj.search;
  if (urlObj.hash) result += urlObj.hash.startsWith('#') ? urlObj.hash : '#' + urlObj.hash;
  return result;
};
url.resolve = (from, to) => native.resolve(String(to), String(from));
url.resolveObject = (from, to) => url.parse(url.resolve(from, to));
url.domainToASCII = (d) => native.domainToASCII(String(d));
url.domainToUnicode = (d) => native.domainToUnicode(String(d));
url.fileURLToPath = (u) => native.fileURLToPath(String(u));
url.pathToFileURL = (p) => native.pathToFileURL(String(p));
url.URL = URL;
