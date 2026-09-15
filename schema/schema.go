// Package schema carries schema/manifest.json into every binary that validates
// a manifest, so validation never depends on where the source tree was when a
// binary was built (docs/decisions.md, M4 second review #1).
//
// manifest.json is the contract and is not edited here; this file only embeds it.
package schema

import _ "embed"

// Manifest is schema/manifest.json, byte for byte.
//
//go:embed manifest.json
var Manifest []byte
