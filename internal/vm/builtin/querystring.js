'use strict';
// querystring — Node.js querystring module.

const native = require('__querystring');

const qs = module.exports;
qs.parse = (str, sep, eq, options) => native.parse(String(str), sep === undefined ? undefined : sep);
qs.stringify = (obj, sep, eq, options) => native.stringify(obj, sep === undefined ? undefined : sep, eq === undefined ? undefined : eq);
qs.escape = (s) => native.escape(String(s));
qs.unescape = (s) => native.unescape(String(s));
qs.decode = (s) => native.decode(String(s));
qs.encode = (s) => native.encode(String(s));
