// Command gen generates the runtime SDK clients and the studio-api router from
// api/openapi.yaml (docs/design/04-packages.md §4, docs/decisions.md M4 Q3):
//
//   - Go types and client:   packages/helm-runtime-sdk/go/zz_types.go, zz_client.go
//   - Go router:             internal/api/studioapi/zz_server.go
//   - Python client:         packages/helm-runtime-sdk/python/helm_runtime_sdk/_generated.py
//   - Node client:           packages/helm-runtime-sdk/node/src/generated.js
//
// Only operations tagged studio-api are generated. Every output is stdlib-only.
// Run from the repository root: go run ./api/gen. The gate runs it and fails on
// a diff, so generated files are never edited by hand.
package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
)

func main() {
	spec := flag.String("spec", "api/openapi.yaml", "the contract")
	root := flag.String("root", ".", "repository root to write into")
	flag.Parse()
	if err := run(*spec, *root); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(spec, root string) error {
	d, err := load(spec)
	if err != nil {
		return err
	}
	outputs := []struct {
		path  string
		src   string
		gofmt bool
	}{
		{"packages/helm-runtime-sdk/go/zz_types.go", genGoTypes(d), true},
		{"packages/helm-runtime-sdk/go/zz_client.go", genGoClient(d), true},
		{"internal/api/studioapi/zz_server.go", genGoServer(d), true},
		{"packages/helm-runtime-sdk/python/helm_runtime_sdk/_generated.py", genPython(d), false},
		{"packages/helm-runtime-sdk/node/src/generated.js", genNode(d), false},
	}
	for _, o := range outputs {
		src := []byte(o.src)
		if o.gofmt {
			formatted, err := format.Source(src)
			if err != nil {
				return fmt.Errorf("%s does not parse as Go (%v); the generator is wrong", o.path, err)
			}
			src = formatted
		}
		path := filepath.Join(root, filepath.FromSlash(o.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return err
		}
	}
	return nil
}
