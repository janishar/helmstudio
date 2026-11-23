package gen

import (
	"bytes"
	"os"
	"testing"
)

func TestToolCatalogCopyIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../../contracts/tools/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ToolCatalog, want) {
		t.Fatal("make gen: gen/tools/catalog.json differs from contracts/tools/catalog.json")
	}
}

func TestProviderPresetsCopyIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../../contracts/providers/presets.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ProviderPresets, want) {
		t.Fatal("make gen: gen/providers/presets.json differs from contracts/providers/presets.json")
	}
}
