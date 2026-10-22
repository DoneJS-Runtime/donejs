'use strict';
// module — Node.js module builtin.

const path = require('path');

class Module {
  constructor(id = '') {
    this.id = id;
    this.exports = {};
    this.parent = undefined;
    this.filename = undefined;
    this.loaded = false;
    this.children = [];
    this.paths = [];
  }
}

Module._cache = {};
Module._extensions = { '.js': '.js', '.json': '.json', '.node': '.node' };
Module._load = function (request, parent, isMain) {
  return require(request);
};
Module.prototype.require = function (path) { return require(path); };
Module.prototype.load = function (filename) { return require(filename); };

function createRequire(filename) {
  const dir = path.dirname(filename);
  const req = function (specifier) { return require(specifier); };
  req.resolve = function (specifier) { return require.resolve(specifier); };
  req.cache = Module._cache;
  req.main = undefined;
  req.extensions = Module._extensions;
  return req;
}

const module_ = module.exports;
module_.Module = Module;
module_.createRequire = createRequire;
module_.builtinModules = [
  'assert', 'buffer', 'child_process', 'crypto', 'events', 'fs', 'fs/promises',
  'http', 'https', 'net', 'os', 'path', 'querystring', 'stream', 'string_decoder',
  'timers', 'url', 'util', 'zlib', 'module', 'process', 'console', 'v8', 'vm',
];
module_.isBuiltin = (id) => module_.builtinModules.includes(id.replace(/^node:/, ''));
module_.stripTypeScriptTypes = (code) => code;
