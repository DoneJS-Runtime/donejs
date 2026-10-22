'use strict';
// crypto — Node.js crypto module.

const native = require('__crypto');

const crypto = module.exports;

crypto.createHash = (algorithm) => native.hashes[algorithm]();
crypto.createHmac = (algorithm, key) => native.hmacs[algorithm](key);
crypto.randomBytes = (size) => native.randomBytes(size);
crypto.randomUUID = () => native.randomUUID();
crypto.timingSafeEqual = (a, b) => native.timingSafeEqual(a, b);
crypto.getHashes = () => native.getHashes();
crypto.pbkdf2 = (password, salt, iterations, keylen, digest, cb) => {
  const p = native.pbkdf2(password, salt, iterations, keylen, digest || 'sha1');
  if (typeof cb === 'function') {
    p.then((buf) => cb(null, Buffer.from(new Uint8Array(buf))), (e) => cb(e));
    return;
  }
  return p.then((buf) => Buffer.from(new Uint8Array(buf)));
};
crypto.pbkdf2Sync = (password, salt, iterations, keylen, digest) =>
  Buffer.from(new Uint8Array(native.pbkdf2Sync(password, salt, iterations, keylen, digest || 'sha1')));
crypto.randomInt = (min, max) => native.randomInt(min === undefined ? 0 : min, max === undefined ? 0 : max);
crypto.webcrypto = { subtle: native.subtle };
crypto.subtle = native.subtle;
crypto.constants = {
  RSA_PKCS1_PADDING: 1,
  RSA_PSS_PADDING: 6,
  AES_128_CBC: 'aes-128-cbc',
  AES_256_GCM: 'aes-256-gcm',
};
crypto.createPrivateKey = (key) => ({ type: 'private', key });
crypto.createPublicKey = (key) => ({ type: 'public', key });
crypto.createSign = () => { throw new Error('crypto.createSign is not supported in donejs'); };
crypto.createVerify = () => { throw new Error('crypto.createVerify is not supported in donejs'); };
crypto.generateKeyPairSync = () => { throw new Error('crypto.generateKeyPairSync is not supported in donejs'); };
crypto.constants = crypto.constants;
