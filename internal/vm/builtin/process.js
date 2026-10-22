'use strict';
// process — Node.js process module (global).

const native = require('__process');
const { EventEmitter } = require('events');

class Process extends EventEmitter {
  constructor() {
    super();
    Object.assign(this, {
      argv: native.argv,
      argv0: native.argv0,
      execPath: native.execPath,
      title: native.title,
      platform: native.platform,
      arch: native.arch,
      version: native.version,
      versions: native.versions,
      release: {
        name: 'node',
        sourceUrl: 'https://nodejs.org/download/release/v22.0.0/node-v22.0.0.tar.gz',
        headersUrl: 'https://nodejs.org/download/release/v22.0.0/node-v22.0.0-headers.tar.gz',
      },
      pid: native.pid,
      ppid: native.ppid,
      env: native.env,
      stdout: native.stdout,
      stderr: native.stderr,
      stdin: native.stdin,
    });
  }

  cwd() { return native.cwd(); }
  chdir(dir) { return native.chdir(dir); }
  exit(code) { return native.exit(code); }
  nextTick(fn, ...args) { return native.nextTick(fn, ...args); }
  hrtime() { return native.hrtime(); }
  uptime() { return native.uptime(); }
  memoryUsage() { return native.memoryUsage(); }
  umask() { return native.umask(); }
  kill(pid, signal) { return native.kill(pid, signal); }
  emitWarning(warning, type) { return native.emitWarning(warning, type); }
  get exitCode() { return native.exitCode; }
  set exitCode(v) { native.exitCode = v; }
  get uptime() { return native.uptime(); }
}

const proc = new Process();
proc.binding = native.binding;
proc.dlopen = native.dlopen;

module.exports = proc;
module.exports.default = proc;
module.exports.Process = Process;
