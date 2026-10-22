'use strict';
// bootstrap — installs Node-compatible globals.

const process_ = require('process');
const console_ = require('console');
const Buffer_ = require('buffer');
const util = require('util');
const { URL, URLSearchParams } = require('url');
const { TextEncoder, TextDecoder } = require('util');
const Intl_ = require('_intl');

globalThis.global = globalThis;
globalThis.Intl = Intl_;
globalThis.process = process_;
globalThis.console = console_;
globalThis.Buffer = Buffer_;
globalThis.URL = URL;
globalThis.URLSearchParams = URLSearchParams;
globalThis.TextEncoder = TextEncoder;
globalThis.TextDecoder = TextDecoder;
globalThis.setInterval = setInterval;
globalThis.clearInterval = clearInterval;
globalThis.setTimeout = setTimeout;
globalThis.clearTimeout = clearTimeout;
globalThis.setImmediate = setImmediate;
globalThis.clearImmediate = clearImmediate;

if (typeof globalThis.performance === 'undefined') {
  globalThis.performance = {
    now: () => Number(process_.hrtime()[0]) * 1000 + process_.hrtime()[1] / 1e6,
    timeOrigin: Date.now(),
    mark: () => {},
    measure: () => {},
  };
}

if (typeof globalThis.fetch === 'undefined') {
  const http = require('http');
  const https = require('https');
  globalThis.fetch = async (input, init) => {
    const url = input instanceof URL ? input : new URL(String(input));
    const mod = url.protocol === 'https:' ? https : http;
    const options = {
      method: (init && init.method) || 'GET',
      hostname: url.hostname,
      port: url.port ? Number(url.port) : undefined,
      path: url.pathname + url.search,
      headers: (init && init.headers) || {},
    };
    return new Promise((resolve, reject) => {
      const req = mod.request(options, (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          const body = Buffer.concat(chunks);
          const headers = {};
          for (const k of Object.keys(res.headers)) headers[k] = res.headers[k];
          resolve({
            ok: res.statusCode >= 200 && res.statusCode < 300,
            status: res.statusCode,
            statusText: res.statusMessage,
            headers: { get: (n) => headers[String(n).toLowerCase()] },
            url: url.href,
            text: async () => body.toString('utf8'),
            json: async () => JSON.parse(body.toString('utf8')),
            arrayBuffer: async () => body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength),
            textEncoding: 'utf-8',
          });
        });
      });
      req.on('error', reject);
      if (init && init.body) req.write(init.body);
      req.end();
    });
  };
}

if (typeof globalThis.crypto === 'undefined') {
  const crypto = require('crypto');
  globalThis.crypto = {
    getRandomValues: (arr) => {
      const bytes = crypto.randomBytes(arr.length);
      for (let i = 0; i < arr.length; i++) arr[i] = bytes[i];
      return arr;
    },
    randomUUID: () => crypto.randomUUID(),
    subtle: crypto.subtle,
  };
}
