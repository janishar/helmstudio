// Package schema carries the two documents that describe what a studio and a
// registry entry may say into every binary that validates one, so validation
// never depends on where the source tree was when a binary was built
// (docs/decisions.md, M4 second review #1).
//
// Both are contracts and are not edited here; this file only embeds them.
package schema

import _ "embed"

// Manifest is schema/manifest.json, byte for byte.
//
//go:embed manifest.json
var Manifest []byte

// RegistryEntry is schema/registry-entry.json, byte for byte: the pointer shape
// a studios/*.yaml file has, with an optional inline manifest for a repository
// that ships none (docs/decisions.md, M7 Q4).
//
//go:embed registry-entry.json
var RegistryEntry []byte
