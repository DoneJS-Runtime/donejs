#!/usr/bin/env python3
"""Capture ICU's time zone canonical names from Node.

Reads the ICU 78.3 zone name list (cached at /tmp/cldr-cache/icu-zoneinfo64.txt)
and asks Node to resolve each name, writing tools/intl_zones.json so the
runtime canonicalizes legacy zone spellings exactly like Node does.
"""
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TEXT = os.environ.get("ICU_ZONEINFO", "/tmp/cldr-cache/icu-zoneinfo64.txt")
OUT = os.path.join(HERE, "intl_zones.json")


def names():
    with open(TEXT, encoding="utf-8") as fh:
        text = fh.read()
    start = text.index("Names {")
    body = text[start + len("Names {"):]
    end = body.index("}")
    body = body[:end]
    found = re.findall(r'"([^"]+)"', body)
    return found


def main():
    cands = names()
    script = (
        "const l=%s;const o={};for(const z of l){try{"
        "o[z]=new Intl.DateTimeFormat('en',{timeZone:z,hour:'numeric'})"
        ".resolvedOptions().timeZone}catch(e){o[z]=null}}"
        "process.stdout.write(JSON.stringify(o))"
    ) % json.dumps(cands)
    raw = subprocess.run([os.environ.get("NODE", "node"), "-e", script],
                         capture_output=True, text=True, timeout=600)
    if raw.returncode != 0:
        print("node failed:", raw.stderr, file=sys.stderr)
        return 1
    resolved = json.loads(raw.stdout)
    table = {}
    for name, canon in resolved.items():
        if canon is None:
            print("rejected by node:", name, file=sys.stderr)
            continue
        table[name.lower()] = canon
    with open(OUT, "w", encoding="utf-8") as fh:
        json.dump(table, fh, sort_keys=True, separators=(",", ":"))
    print("wrote", OUT, len(table), "names", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
