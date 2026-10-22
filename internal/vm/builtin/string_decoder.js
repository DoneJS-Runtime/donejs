'use strict';
// string_decoder — Node.js string_decoder module.

class StringDecoder {
  constructor(encoding = 'utf8') {
    this.encoding = encoding.toLowerCase();
    this._pending = Buffer.alloc(0);
  }

  write(buf) {
    let data = Buffer.isBuffer(buf) ? buf : Buffer.from(buf);
    if (this._pending.length) {
      data = Buffer.concat([this._pending, data]);
      this._pending = Buffer.alloc(0);
    }
    if (this.encoding === 'utf8' || this.encoding === 'utf-8') {
      // hold back an incomplete multi-byte sequence
      let end = data.length;
      for (let i = data.length - 1; i >= 0 && i >= data.length - 3; i--) {
        const b = data[i];
        if ((b & 0xc0) === 0xc0) {
          const need = (b & 0xf8) === 0xf0 ? 3 : (b & 0xf0) === 0xe0 ? 2 : 1;
          if (data.length - i < need) { end = i; }
          break;
        }
        if ((b & 0xc0) !== 0x80) break;
      }
      if (end < data.length) {
        this._pending = data.subarray(end);
        data = data.subarray(0, end);
      }
    }
    return data.toString(this.encoding === 'utf-8' ? 'utf8' : this.encoding);
  }

  end(buf) {
    if (buf) this.write(buf);
    const rest = this._pending;
    this._pending = Buffer.alloc(0);
    return rest.length ? rest.toString(this.encoding) : '';
  }

  text(buf, offset) {
    return this.write(buf);
  }
}

module.exports = { StringDecoder };
module.exports.StringDecoder = StringDecoder;
