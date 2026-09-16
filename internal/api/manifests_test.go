package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// The editor exists to fix invalid documents. `document` is "the parsed YAML
// as JSON … whether or not it is valid" (api/openapi.yaml, ManifestCheck), and
// the form renders from it — so a verdict that dropped it for an invalid
// manifest left the page to guess which parents existed, and a guessed parent
// is written as an empty map over the real one.
func TestAnInvalidManifestStillComesBackAsADocument(t *testing.T) {
	srv, _ := libraryServer(t)

	// Valid YAML, invalid manifest: `requires` is missing `arch`.
	body := `{"text": "id: half-done\nname: half done\nrequires:\n  os: [darwin]\n  ram_gb: 32\n"}`
	rec := doJSON(t, srv, "POST", "/api/v1/launcher/manifests:validate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Valid    bool           `json:"valid"`
		Document map[string]any `json:"document"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Valid {
		t.Fatal("a manifest with no processes validated")
	}
	req, ok := got.Document["requires"].(map[string]any)
	if !ok {
		t.Fatalf("no document for an invalid manifest: %s", rec.Body)
	}
	if req["ram_gb"] != float64(32) {
		t.Errorf("requires = %v", req)
	}

	// Text that is not YAML has no document, and says so by carrying none.
	rec = doJSON(t, srv, "POST", "/api/v1/launcher/manifests:validate", `{"text": "id: [unclosed\n"}`)
	var none struct {
		Document map[string]any `json:"document"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &none); err != nil {
		t.Fatal(err)
	}
	if none.Document != nil {
		t.Errorf("text that does not parse came back as a document: %v", none.Document)
	}
}

// doJSON sends a JSON body, as the launcher's client does.
func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
