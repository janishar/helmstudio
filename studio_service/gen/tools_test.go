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
