package vm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Selftest exercises the runtime's public surface and reports the outcome to w.
// It returns an error when any check fails.
func Selftest(w io.Writer) error {
	rt := New(Options{})
	fails := 0
	total := 0

	check := func(name string, fn func() error) {
		total++
		if err := fn(); err != nil {
			fails++
			fmt.Fprintf(w, "FAIL %s: %v\n", name, err)
			return
		}
		fmt.Fprintf(w, "ok   %s\n", name)
	}

	// --- language core ----------------------------------------------------
	check("core: classes and generators", func() error {
		return evalOK(rt, `
			class A { #x = 1; get x() { return this.#x; } static make() { return new A(); } }
			function* g() { yield 1; yield 2; }
			const out = [...g()];
			if (A.make().x !== 1) throw new Error('private field');
			if (out.join(',') !== '1,2') throw new Error('generator spread');
		`)
	})

	check("core: modern syntax", func() error {
		return evalOK(rt, `
			const o = { a: 1, b: { c: 2 } };
			const v = o?.b?.c ?? 3;
			if (v !== 2) throw new Error('optional chaining/nullish');
			if ([1, [2, [3]]].flat(2).join('') !== '123') throw new Error('flat');
			if ('x'.padStart(3, '0') !== '00x') throw new Error('padStart');
			if (!/[a-z]+(?<y>\d)/.exec('ab12').groups.y) throw new Error('named groups');
			if (typeof BigInt(1) !== 'bigint') throw new Error('bigint');
			if (Object.entries({ a: 1 })[0][0] !== 'a') throw new Error('Object.entries');
		`)
	})

	check("core: promise ordering", func() error {
		// Uses the entry-module path so the nextTick queue is flushed at the
		// end of the synchronous body, like Node.
		if _, err := rt.loadModuleText("[selftest-order]", `
			const order = [];
			Promise.resolve().then(() => order.push('p'));
			process.nextTick(() => order.push('t'));
			queueMicrotask(() => order.push('m'));
			order.push('s');
			globalThis.__order = order;
		`, rt.mainDir, true); err != nil {
			return err
		}
		return nil
	})

	// --- module system ----------------------------------------------------
	check("module: require builtins", func() error {
		return evalOK(rt, `
			const fs = require('fs');
			const path = require('path');
			const crypto = require('crypto');
			const os = require('os');
			if (typeof fs.readFileSync !== 'function') throw new Error('fs');
			if (path.join('a', 'b') !== 'a/b') throw new Error('path');
			if (typeof crypto.createHash !== 'function') throw new Error('crypto');
			if (typeof os.platform !== 'function') throw new Error('os');
		`)
	})

	check("module: require.resolve and cache", func() error {
		return evalOK(rt, `
			if (typeof require.resolve !== 'function') throw new Error('no require.resolve');
			if (require.resolve('path') !== 'path') throw new Error('resolve builtin');
			if (typeof require.cache !== 'object') throw new Error('no require.cache');
			if (typeof require.extensions !== 'object') throw new Error('no require.extensions');
		`)
	})

	check("module: require.main and parent links", func() error {
		dir, err := os.MkdirTemp("", "donejs-module")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		child := filepath.Join(dir, "child.js")
		if err := os.WriteFile(child, []byte(`
			globalThis.__childParent = module.parent ? module.parent.filename : null;
			globalThis.__childMain = require.main ? require.main.filename : null;
			module.exports = 'child';
		`), 0o644); err != nil {
			return err
		}
		entry := filepath.Join(dir, "entry.js")
		_, err = rt.loadModuleText(entry, `
			const path = require('path');
			const child = require('./child.js');
			if (require.main !== module) throw new Error('require.main !== module');
			if (module.children.length !== 1) throw new Error('children');
			if (module.parent !== null) throw new Error('entry parent');
			if (globalThis.__childParent !== module.filename) throw new Error('child parent');
			if (globalThis.__childMain !== module.filename) throw new Error('child main');
			if (module.paths[0] !== path.join(__dirname, 'node_modules')) throw new Error('paths');
			if (child !== 'child') throw new Error('child exports');
		`, dir, true)
		return err
	})

	// --- fs ---------------------------------------------------------------
	dir, err := os.MkdirTemp("", "donejs-selftest")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "hello.txt")
	check("fs: write/read round trip", func() error {
		return evalOK(rt, fmt.Sprintf(`
			const fs = require('fs');
			fs.writeFileSync(%q, 'hello donejs');
			const data = fs.readFileSync(%q, 'utf8');
			if (data !== 'hello donejs') throw new Error('content mismatch: ' + data);
			if (!fs.existsSync(%q)) throw new Error('existsSync');
			const st = fs.statSync(%q);
			if (st.size !== 12) throw new Error('size: ' + st.size);
			if (!st.isFile()) throw new Error('not a file');
		`, file, file, file, file))
	})

	check("fs: errors carry ENOENT", func() error {
		return evalOK(rt, fmt.Sprintf(`
			const fs = require('fs');
			try {
				fs.readFileSync(%q);
				throw new Error('expected throw');
			} catch (e) {
				if (e.code !== 'ENOENT') throw new Error('code: ' + e.code);
				if (!e.path) throw new Error('missing path');
			}
		`, filepath.Join(dir, "missing.txt")))
	})

	check("fs: promises API", func() error {
		return evalOK(rt, fmt.Sprintf(`
			const fs = require('fs');
			fs.promises.writeFile(%q, 'async data').then(() => {
				return fs.promises.readFile(%q, 'utf8');
			}).then((d) => {
				if (d !== 'async data') throw new Error('mismatch: ' + d);
				globalThis.__asyncFsOK = true;
			}).catch((e) => { globalThis.__asyncFsErr = e; });
		`, file, file))
	})

	check("fs: readdir and mkdir", func() error {
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
		return evalOK(rt, fmt.Sprintf(`
			const fs = require('fs');
			const names = fs.readdirSync(%q);
			if (!names.includes('hello.txt')) throw new Error('missing entry: ' + names);
			if (!names.includes('sub')) throw new Error('missing dir');
		`, dir))
	})

	// --- path -------------------------------------------------------------
	check("path: posix and win32 semantics", func() error {
		return evalOK(rt, `
			const path = require('path');
			if (path.posix.join('/a', 'b', '..', 'c') !== '/a/c') throw new Error('posix.join');
			if (path.posix.normalize('/a//b/../c') !== '/a/c') throw new Error('posix.normalize');
			if (path.posix.extname('a/b.min.js') !== '.js') throw new Error('extname');
			if (path.win32.join('C:\\a', 'b') !== 'C:\\a\\b') throw new Error('win32.join');
			if (path.basename('/a/b/c.txt', '.txt') !== 'c') throw new Error('basename');
			if (path.dirname('/a/b/c.txt') !== '/a/b') throw new Error('dirname');
			const parsed = path.parse('/a/b/c.txt');
			if (Object.keys(parsed).join(',') !== 'root,dir,base,ext,name') {
				throw new Error('parse key order: ' + Object.keys(parsed).join(','));
			}
			if (parsed.ext !== '.txt' || parsed.name !== 'c') throw new Error('parse ext/name');
		`)
	})

	// --- intl --------------------------------------------------------------
	check("intl: number, date and plural formatting", func() error {
		return evalOK(rt, `
			if (typeof Intl !== 'object') throw new Error('Intl');
			const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' });
			if (money.format(1234.5) !== '$1,234.50') throw new Error('currency: ' + money.format(1234.5));
			if (new Intl.NumberFormat('en-US').format(1234567.891) !== '1,234,567.891') throw new Error('decimal');
			if (new Intl.PluralRules('en').select(1) !== 'one') throw new Error('plural one');
			if (new Intl.PluralRules('en').select(2) !== 'other') throw new Error('plural other');
			const dtf = new Intl.DateTimeFormat('en-US', { timeZone: 'UTC' });
			if (dtf.format(new Date(0)) !== '1/1/1970') throw new Error('date: ' + dtf.format(new Date(0)));
			if ((1234.5).toLocaleString('en-US') !== '1,234.5') throw new Error('toLocaleString');
			const d = new Date(0);
			if (d.toLocaleString('en-US', { timeZone: 'UTC' }) !== '1/1/1970, 12:00:00 AM') throw new Error('date+time');
			if (d.toLocaleString('en-US', { year: 'numeric', timeZone: 'UTC' }) !== '1970') throw new Error('year only');
			if (new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', dateStyle: 'short' }).format(d) !== '1/1/70') {
				throw new Error('dateStyle short');
			}
			let mix = false;
			try {
				new Intl.DateTimeFormat('en-US', { dateStyle: 'short', year: 'numeric' });
			} catch (_) { mix = true; }
			if (!mix) throw new Error('mixing a style with a field should throw');
		`)
	})

	// --- buffer -----------------------------------------------------------
	check("buffer: alloc/from/toString", func() error {
		return evalOK(rt, `
			const b = Buffer.from('hello');
			if (b.length !== 5) throw new Error('length');
			if (b.toString() !== 'hello') throw new Error('toString');
			if (b.toString('utf8') !== 'hello') throw new Error('utf8');
			if (!Buffer.isBuffer(b)) throw new Error('isBuffer');
			const h = Buffer.from('68656c6c6f', 'hex');
			if (h.toString() !== 'hello') throw new Error('hex');
			const b64 = Buffer.from('hello').toString('base64');
			if (b64 !== 'aGVsbG8=') throw new Error('base64: ' + b64);
			const c = Buffer.concat([Buffer.from('a'), Buffer.from('b')]);
			if (c.toString() !== 'ab') throw new Error('concat');
			if (Buffer.alloc(3, 'x').toString() !== 'xxx') throw new Error('alloc');
		`)
	})

	check("buffer: numeric reads and writes", func() error {
		return evalOK(rt, `
			const b = Buffer.alloc(4);
			b.writeUInt32BE(0x01020304, 0);
			if (b.readUInt32BE(0) !== 0x01020304) throw new Error('u32be');
			b.writeUInt16LE(0xbeef, 0);
			if (b.readUInt16LE(0) !== 0xbeef) throw new Error('u16le');
			if (b.readUInt8(0) !== 0xef) throw new Error('u8');
		`)
	})

	// --- crypto -----------------------------------------------------------
	check("crypto: hashes and hmac", func() error {
		return evalOK(rt, `
			const crypto = require('crypto');
			const h = crypto.createHash('sha256').update('abc').digest('hex');
			if (h !== 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad') {
				throw new Error('sha256: ' + h);
			}
			const m = crypto.createHmac('sha256', 'key').update('abc').digest('hex');
			if (m.length !== 64) throw new Error('hmac length');
			if (crypto.randomBytes(16).length !== 16) throw new Error('randomBytes');
			if (!/^[0-9a-f-]{36}$/.test(crypto.randomUUID())) throw new Error('uuid');
			if (crypto.timingSafeEqual(Buffer.from('a'), Buffer.from('a')) !== true) throw new Error('timingSafeEqual');
		`)
	})

	check("crypto: pbkdf2 matches RFC 6070 vector", func() error {
		return evalOK(rt, `
			const crypto = require('crypto');
			const key = crypto.pbkdf2Sync('password', 'salt', 1, 20, 'sha1').toString('hex');
			if (key !== '0c60c80f961f0e71f3a9b524af6012062fe037a6') throw new Error('pbkdf2: ' + key);
		`)
	})

	// --- zlib -------------------------------------------------------------
	check("zlib: gzip round trip", func() error {
		return evalOK(rt, `
			const zlib = require('zlib');
			const gz = zlib.gzipSync(Buffer.from('hello donejs'));
			const back = zlib.gunzipSync(gz).toString();
			if (back !== 'hello donejs') throw new Error('gzip: ' + back);
			const def = zlib.deflateSync(Buffer.from('hello donejs'));
			if (zlib.inflateSync(def).toString() !== 'hello donejs') throw new Error('deflate');
			if (zlib.crc32(Buffer.from('123456789')) !== 0xcbf43926) throw new Error('crc32');
		`)
	})

	// --- url / querystring ------------------------------------------------
	check("url: parsing and search params", func() error {
		return evalOK(rt, `
			const { URL, URLSearchParams } = require('url');
			const u = new URL('https://user:pw@example.com:8443/a/b?x=1&y=2#frag');
			if (u.protocol !== 'https:') throw new Error('protocol');
			if (u.hostname !== 'example.com') throw new Error('hostname');
			if (u.port !== '8443') throw new Error('port');
			if (u.pathname !== '/a/b') throw new Error('pathname');
			if (u.searchParams.get('x') !== '1') throw new Error('searchParams');
			if (u.hash !== '#frag') throw new Error('hash');
			const p = new URLSearchParams('a=1&b=2');
			if (p.get('b') !== '2') throw new Error('URLSearchParams');
			if (p.toString() !== 'a=1&b=2') throw new Error('toString');
		`)
	})

	check("querystring: parse and stringify", func() error {
		return evalOK(rt, `
			const qs = require('querystring');
			const o = qs.parse('a=1&b=2');
			if (o.a !== '1' || o.b !== '2') throw new Error('parse');
			if (qs.stringify({ a: 1 }) !== 'a=1') throw new Error('stringify');
		`)
	})

	// --- events -----------------------------------------------------------
	check("events: on/once/off/emit", func() error {
		return evalOK(rt, `
			const { EventEmitter } = require('events');
			const e = new EventEmitter();
			let n = 0;
			const h = () => { n++; };
			e.on('x', h);
			e.emit('x');
			e.off('x', h);
			e.emit('x');
			if (n !== 1) throw new Error('on/off count: ' + n);
			let once = 0;
			e.once('y', () => { once++; });
			e.emit('y');
			e.emit('y');
			if (once !== 1) throw new Error('once count: ' + once);
			e.on('z', () => {});
			if (e.listenerCount('x') !== 0) throw new Error('listenerCount');
			if (!e.eventNames().includes('z')) throw new Error('eventNames');
		`)
	})

	// --- streams / util ----------------------------------------------------
	check("stream: Readable/Writable basics", func() error {
		return evalOK(rt, `
			const { Readable, Writable } = require('stream');
			const r = new Readable({ read() { this.push('a'); this.push('b'); this.push(null); } });
			let out = '';
			r.on('data', (c) => { out += c.toString(); });
			r.on('end', () => { globalThis.__streamOut = out; });
			const w = new Writable({ write(chunk, enc, cb) { cb(); } });
			w.write('x');
			w.end();
		`)
	})

	check("util: inspect and format", func() error {
		return evalOK(rt, `
			const util = require('util');
			if (util.format('%s-%d', 'a', 5) !== 'a-5') throw new Error('format');
			if (util.inspect({ a: 1 }) !== '{ a: 1 }') throw new Error('inspect: ' + util.inspect({ a: 1 }));
			if (!util.isDeepStrictEqual({ a: [1, 2] }, { a: [1, 2] })) throw new Error('deepEqual');
		`)
	})

	// --- assert -----------------------------------------------------------
	check("assert: strict and loose", func() error {
		return evalOK(rt, `
			const assert = require('assert');
			assert.strictEqual(1, 1);
			assert.ok(true);
			assert.deepStrictEqual({ a: 1 }, { a: 1 });
			let threw = false;
			try { assert.strictEqual(1, 2); } catch (_) { threw = true; }
			if (!threw) throw new Error('assert did not throw');
		`)
	})

	// --- os / process -----------------------------------------------------
	check("os: platform and homedir", func() error {
		return evalOK(rt, `
			const os = require('os');
			if (typeof os.platform() !== 'string') throw new Error('platform');
			if (typeof os.homedir() !== 'string') throw new Error('homedir');
			if (typeof os.tmpdir() !== 'string') throw new Error('tmpdir');
			if (typeof os.cpus().length !== 'number') throw new Error('cpus');
		`)
	})

	check("process: argv, platform, exit code", func() error {
		return evalOK(rt, `
			if (!Array.isArray(process.argv)) throw new Error('argv');
			if (typeof process.platform !== 'string') throw new Error('platform');
			if (typeof process.pid !== 'number') throw new Error('pid');
			if (process.version === undefined) throw new Error('version');
			if (typeof process.env !== 'object') throw new Error('env');
			if (typeof process.cwd() !== 'string') throw new Error('cwd');
		`)
	})

	// --- timers -----------------------------------------------------------
	check("timers: setTimeout ordering", func() error {
		return evalOK(rt, `
			const order = [];
			setTimeout(() => order.push('b'), 20);
			setTimeout(() => order.push('a'), 0);
			setImmediate(() => order.push('i'));
			globalThis.__timers = order;
		`)
	})

	// --- child_process gating --------------------------------------------
	check("child_process: denied by default", func() error {
		return evalOK(rt, `
			const cp = require('child_process');
			let threw = false;
			try {
				cp.execSync('echo hi');
			} catch (e) {
				threw = true;
				if (e.code !== 'ERR_EXEC_DENIED') throw new Error('code: ' + e.code);
			}
			if (!threw) throw new Error('exec was not denied');
			if (cp.allowed !== false) throw new Error('allowed should be false');
		`)
	})

	// --- http --------------------------------------------------------------
	check("http: server + client round trip", func() error {
		return evalOK(rt, `
			const http = require('http');
			const server = http.createServer((req, res) => {
				res.writeHead(200, { 'Content-Type': 'text/plain' });
				res.end('pong');
			});
			server.listen(0, '127.0.0.1', () => {
				const port = server.address().port;
				http.get({ host: '127.0.0.1', port, path: '/' }, (res) => {
					let body = '';
					res.on('data', (c) => { body += c.toString(); });
					res.on('end', () => {
						globalThis.__http = JSON.stringify({ status: res.statusCode, body });
						server.close();
					});
				});
			});
		`)
	})

	// --- globals ----------------------------------------------------------
	check("globals: console, Buffer, URL, fetch present", func() error {
		return evalOK(rt, `
			if (typeof console.log !== 'function') throw new Error('console');
			if (typeof Buffer !== 'function') throw new Error('Buffer');
			if (typeof URL !== 'function') throw new Error('URL');
			if (typeof fetch !== 'function') throw new Error('fetch');
			if (typeof globalThis.setTimeout !== 'function') throw new Error('setTimeout');
			if (typeof structuredClone !== 'function') throw new Error('structuredClone');
			if (typeof TextEncoder !== 'function') throw new Error('TextEncoder');
		`)
	})

	// --- drain the event loop so async checks settle ----------------------
	rt.Loop()

	// async assertions that needed the loop
	total++
	if rt.readGlobalBool("__asyncFsOK") {
		fmt.Fprintf(w, "ok   fs: promises API (async)\n")
	} else {
		fails++
		fmt.Fprintf(w, "FAIL fs: promises API (async)\n")
	}

	total++
	if s := rt.readGlobalString("__streamOut"); s == "ab" {
		fmt.Fprintf(w, "ok   stream: data chunks received\n")
	} else {
		fails++
		fmt.Fprintf(w, "FAIL stream: data chunks received (got %q)\n", s)
	}

	total++
	if s := rt.readGlobalString("__http"); strings.Contains(s, "200") && strings.Contains(s, "pong") {
		fmt.Fprintf(w, "ok   http: response received\n")
	} else {
		fails++
		fmt.Fprintf(w, "FAIL http: response received (got %q)\n", s)
	}

	total++
	if s := rt.readGlobalString("__timers"); s == "a,i,b" {
		fmt.Fprintf(w, "ok   timers: ordering\n")
	} else {
		fails++
		fmt.Fprintf(w, "FAIL timers: ordering (got %q)\n", s)
	}

	total++
	if s := rt.readGlobalString("__order"); s == "s,t,p,m" {
		fmt.Fprintf(w, "ok   core: microtask ordering\n")
	} else {
		fails++
		fmt.Fprintf(w, "FAIL core: microtask ordering (got %q)\n", s)
	}

	fmt.Fprintf(w, "\n%d/%d checks passed\n", total-fails, total)
	if fails > 0 {
		return fmt.Errorf("%d of %d checks failed", fails, total)
	}
	return nil
}

var selftestSeq int

// evalOK runs JS in the runtime and reports the error text.
// The source is wrapped in a function so repeated runs do not collide
// on global lexical declarations, and gets a unique name because the
// program cache keys on the name.
func evalOK(rt *Runtime, src string) error {
	selftestSeq++
	name := "selftest-" + strconv.Itoa(selftestSeq)
	if _, err := rt.RunString(name, "(function(){\n"+src+"\n})();"); err != nil {
		return err
	}
	return nil
}

// readGlobalBool reads a boolean global.
func (rt *Runtime) readGlobalBool(name string) bool {
	v := rt.jit.GetGlobal(name)
	return !v.IsUndef() && v.ToBoolean()
}

// readGlobalString reads a string global.
func (rt *Runtime) readGlobalString(name string) string {
	v := rt.jit.GetGlobal(name)
	if v.IsUndef() || v.IsNull() {
		return ""
	}
	return v.String()
}
