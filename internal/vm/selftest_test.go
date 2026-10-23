package vm

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestSelftest runs the runtime self test, which exercises the public
// Node.js API surface.
func TestSelftest(t *testing.T) {
	var buf bytes.Buffer
	if err := Selftest(&buf); err != nil {
		t.Fatalf("selftest failed: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "38/38 checks passed") {
		t.Fatalf("unexpected selftest summary:\n%s", buf.String())
	}
}

// TestRunScript runs a CommonJS script end to end.
func TestRunScript(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/main.js"
	src := `
const fs = require('fs');
const path = require('path');
const out = path.join(__dirname, 'out.txt');
fs.writeFileSync(out, 'ok');
if (fs.readFileSync(out, 'utf8') !== 'ok') throw new Error('bad content');
if (require('path').basename(__filename) !== 'main.js') throw new Error('bad __filename');
globalThis.__done = true;
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{Script: path})
	if _, err := rt.loadModule(path, true); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	rt.Loop()
	if !rt.readGlobalBool("__done") {
		t.Fatal("script did not run to completion")
	}
}
