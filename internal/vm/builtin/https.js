'use strict';
// https — Node.js https module (TLS transport over the shared http core).

const http = require('http');

const https = http.__build(require('__https'), 'https:', true);

module.exports = https;
module.exports.default = https;
