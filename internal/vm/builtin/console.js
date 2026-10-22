'use strict';
// console — Node.js console.

const util = require('util');
const { format } = util;

const console = module.exports;

function writeOut(stream, ...args) {
  const text = format(...args) + '\n';
  stream.write(text);
}

console.log = (...args) => writeOut(process.stdout, ...args);
console.info = console.log;
console.debug = console.log;
console.dir = (obj, options) => {
  writeOut(process.stdout, util.inspect(obj, options));
};
console.warn = (...args) => writeOut(process.stderr, ...args);
console.error = (...args) => writeOut(process.stderr, ...args);
console.trace = (...args) => {
  const err = new Error();
  writeOut(process.stderr, 'Trace:', ...args, err.stack || err.message);
};
console.assert = (value, ...args) => {
  if (!value) writeOut(process.stderr, 'Assertion failed:', ...args);
};

console.group = (...args) => writeOut(process.stdout, ...args);
console.groupCollapsed = console.group;
console.groupEnd = () => {};
console.time = (label = 'default') => { console._timers[label] = Date.now(); };
console.timeEnd = (label = 'default') => {
  const start = console._timers[label];
  if (start === undefined) return;
  delete console._timers[label];
  writeOut(process.stdout, label + ': ' + (Date.now() - start) + 'ms');
};
console.timeLog = (label = 'default', ...args) => {
  const start = console._timers[label];
  if (start === undefined) return;
  writeOut(process.stdout, label + ': ' + (Date.now() - start) + 'ms', ...args);
};
console.count = (label = 'default') => {
  console._counts[label] = (console._counts[label] || 0) + 1;
  writeOut(process.stdout, label + ': ' + console._counts[label]);
};
console.countReset = (label = 'default') => { delete console._counts[label]; };
console.table = (data) => {
  if (data === null || typeof data !== 'object') {
    writeOut(process.stdout, util.inspect(data));
    return;
  }
  const rows = Array.isArray(data) ? data.map((v, i) => [String(i), v]) : Object.entries(data);
  const headers = new Set(['(index)']);
  for (const [, v] of rows) {
    if (v && typeof v === 'object') for (const k of Object.keys(v)) headers.add(k);
  }
  const cols = [...headers];
  const table = [cols];
  for (const [idx, v] of rows) {
    const row = [idx];
    for (let i = 1; i < cols.length; i++) {
      const key = cols[i];
      row.push(v && typeof v === 'object' ? util.inspect(v[key]) : v === undefined ? '' : util.inspect(v));
    }
    table.push(row);
  }
  const widths = cols.map((_, i) => Math.max(...table.map((r) => String(r[i] === undefined ? '' : r[i]).length)));
  for (const row of table) {
    writeOut(process.stdout, row.map((cell, i) => String(cell === undefined ? '' : cell).padEnd(widths[i])).join(' | '));
  }
};
console._timers = {};
console._counts = {};
console.Console = function Console(options) {
  const stdout = (options && options.stdout) || process.stdout;
  const stderr = (options && options.stderr) || process.stderr;
  const self = {
    log: (...args) => writeOut(stdout, ...args),
    error: (...args) => writeOut(stderr, ...args),
    warn: (...args) => writeOut(stderr, ...args),
  };
  return self;
};
