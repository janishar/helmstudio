package gen

import _ "embed"

// ToolCatalog is contracts/tools/catalog.json (API 1.44): the assistant's tools, copied by `make gen`.
//
//go:embed tools/catalog.json
var ToolCatalog []byte

// ProviderPresets is contracts/providers/presets.json (API 1.51): the model provider presets, copied by `make gen`.
//
//go:embed providers/presets.json
var ProviderPresets []byte
