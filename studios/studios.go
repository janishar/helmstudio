// Package studios carries the bundled registry into the binary
// (docs/design/01-prd.md R72, docs/decisions.md M7 defaults).
//
// The registry is four pointers at four repositories. Embedding them means a
// daemon started anywhere knows the same four studios a checkout does, and
// `-studios` stays as a development override for pointing at a directory of
// entries under test — the same shape `schema.Manifest` uses for the schema.
package studios

import (
	"embed"
	"io/fs"
)

// Bundled holds the registry entries, one per studio.
//
//go:embed *.yaml
var Bundled embed.FS

// Entries returns the bundled entry filenames, sorted, as fs.ReadDir sorts.
func Entries() ([]string, error) {
	des, err := fs.ReadDir(Bundled, ".")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(des))
	for _, d := range des {
		if !d.IsDir() {
			out = append(out, d.Name())
		}
	}
	return out, nil
}
