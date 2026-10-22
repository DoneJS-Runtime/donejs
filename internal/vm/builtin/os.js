'use strict';
// os — Node.js os module.

const native = require('__os');

const os = module.exports;

os.EOL = '\n';
os.devNull = '/dev/null';
os.constants = {
  O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2,
  SIGHUP: 1, SIGINT: 2, SIGQUIT: 3, SIGKILL: 9, SIGTERM: 15,
};

os.platform = () => native.sync('platform', []);
os.arch = () => native.sync('arch', []);
os.type = () => native.sync('type', []);
os.endianness = () => native.sync('endianness', []);
os.hostname = () => native.sync('hostname', []);
os.homedir = () => native.sync('homedir', []);
os.tmpdir = () => native.sync('tmpdir', []);
os.release = () => native.sync('release', []);
os.uptime = () => native.sync('uptime', []);
os.freemem = () => native.sync('freemem', []);
os.totalmem = () => native.sync('totalmem', []);
os.cpus = () => native.sync('cpus', []);
os.userInfo = () => native.sync('userInfo', []);
os.networkInterfaces = () => native.sync('networkInterfaces', []);
os.loadavg = () => native.sync('loadavg', []);
os.getPriority = () => 0;
os.setPriority = () => {};
os.machine = () => native.sync('arch', []);
