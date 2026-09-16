package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/janishar/helmstudio/schema"
)

// The editor's form is generated from the schema, so the schema has to reach
// the page — and it has to be the same bytes the daemon validates against.
func TestTheManifestSchemaIsServedToThePage(t *testing.T) {
	srv, _ := libraryServer(t)

	rec := do(t, srv, "GET", "/schema/manifest.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /schema/manifest.json: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q", got)
	}
	if rec.Body.String() != string(schema.Manifest) {
		t.Error("the schema served is not the one this binary validates against")
	}
	// Byte-identical is the point: a form drawn from a copy would offer fields
	// the daemon rejects the day the two diverge.
	onDisk, err := os.ReadFile(filepath.Join("..", "..", "schema", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != string(onDisk) {
		t.Error("the schema served is not schema/manifest.json")
	}

	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag: the editor would refetch the contract on every redraw")
	}
	again := do(t, srv, "GET", "/schema/manifest.json", map[string]string{"If-None-Match": etag})
	if again.Code != http.StatusNotModified {
		t.Errorf("If-None-Match returned %d, want 304", again.Code)
	}
}

// `helm dev` serves one studio from a path on the command line. It has no
// library to write to, so it has no editor, so it serves no schema.
func TestADevDaemonServesNoSchema(t *testing.T) {
	srv, _ := newServer(t)
	if rec := do(t, srv, "GET", "/schema/manifest.json", nil); rec.Code == http.StatusOK {
		t.Error("a daemon with no manifest operations served the editor's schema")
	}
}
