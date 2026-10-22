package vm

import (
	"embed"
	"strings"
)

//go:embed builtin/*.js
var builtinFS embed.FS

func builtinNames() map[string]string {
	m := map[string]string{}
	for _, f := range mustListBuiltin() {
		name := strings.TrimSuffix(f, ".js")
		if name == "fs_promises" {
			name = "fs/promises"
		} else if name == "string_decoder" {
			name = "string_decoder"
		}
		m[name] = f
	}
	return m
}

func mustListBuiltin() []string {
	ents, err := builtinFS.ReadDir("builtin")
	if err != nil {
		panic(err)
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".js") {
			out = append(out, e.Name())
		}
	}
	return out
}

func readBuiltin(name string) (string, error) {
	f, ok := builtinNames()[name]
	if !ok {
		return "", errUnknownBuiltin(name)
	}
	data, err := builtinFS.ReadFile("builtin/" + f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type unknownBuiltin string

func (u unknownBuiltin) Error() string { return "unknown builtin: " + string(u) }
func errUnknownBuiltin(n string) error { return unknownBuiltin(n) }
