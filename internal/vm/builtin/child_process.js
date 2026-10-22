'use strict';
// child_process — Node.js child_process module.
//
// Security: spawning executables is denied unless the runtime was started
// with --allow-exec (or DONEJS_ALLOW_EXEC=1).

const native = require('__child_process');
const { EventEmitter } = require('events');

const cp = module.exports;

function makeError(code, message) {
  const err = new Error(message);
  err.code = code;
  return err;
}

cp.exec = (command, options, callback) => {
  if (typeof options === 'function') { callback = options; options = {}; }
  try {
    return native.exec(command, options || {}, (err, stdout, stderr, code) => {
      if (err) { err.code = err.code || code; callback(err, stdout, stderr); return; }
      callback(null, stdout, stderr);
    });
  } catch (e) {
    callback(e, '', '');
  }
};

cp.execFile = (file, args, options, callback) => {
  if (typeof options === 'function') { callback = options; options = {}; }
  try {
    return native.execFile(file, args || [], options || {}, (err, stdout, stderr, code) => {
      if (err) { err.code = err.code || code; callback(err, stdout, stderr); return; }
      callback(null, stdout, stderr);
    });
  } catch (e) {
    callback(e, '', '');
  }
};

cp.spawn = (file, args, options) => {
  try {
    const child = native.spawn(file, args || [], options || {});
    return child;
  } catch (e) {
    const child = new EventEmitter();
    child.pid = undefined;
    child.stdout = null;
    child.stderr = null;
    child.stdin = null;
    child.kill = () => false;
    process.nextTick(() => child.emit('error', e));
    return child;
  }
};

cp.spawnSync = (file, args, options) => {
  try {
    const out = native.spawnSync(file, args || [], options || {});
    return { pid: undefined, stdout: out, stderr: '', status: 0, signal: null, error: undefined };
  } catch (e) {
    return { pid: undefined, stdout: '', stderr: '', status: 1, signal: null, error: e };
  }
};

cp.execSync = (command, options) => {
  try {
    return native.execSync(command, options || {});
  } catch (e) {
    if (e && e.code) throw e;
    const err = makeError('ERR_CHILD_PROCESS_EXEC_FAILED', e.message);
    err.stdout = e.stdout;
    err.stderr = e.stderr;
    err.status = e.status;
    throw err;
  }
};

cp.execFileSync = (file, args, options) => {
  try {
    return native.execFileSync(file, args || [], options || {});
  } catch (e) {
    if (e && e.code) throw e;
    const err = makeError('ERR_CHILD_PROCESS_EXEC_FAILED', e.message);
    throw err;
  }
};

cp.fork = () => { throw makeError('ERR_UNSUPPORTED', 'child_process.fork is not supported in donejs'); };
cp.allowed = native.allowed();
