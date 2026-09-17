package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
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

// A studio in the registry, with one build step to recognise it by.
const registryStudio = `id: reg-studio
name: reg studio
kinds: [image]
repo: https://github.com/someone/reg
ref: v1.0.0
requires: { os: [darwin], arch: [arm64] }
runtime: { framework: other, backends: [cpu] }
build:
  - { name: Build, run: "make registry" }
processes:
  - name: studio
    role: main
    cmd: "./reg --port {port}"
    port: { prefer: 8770 }
    health: { tcp: true, timeout_s: 30 }
`

// overrideServer is a daemon with a registry beneath a local directory and the
// approval gate in front of install, which is the arrangement an Override is
// made in.
func overrideServer(t *testing.T) *Server {
	t.Helper()
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st, Grace: time.Second, PortMin: 44000, PortMax: 44999})

	local := library.NewLocal(d.Data())
	res := library.New(
		library.Dir(library.SourceLocal, local.Dir),
		library.FS(library.SourceRegistry, fstest.MapFS{"reg-studio.yaml": {Data: []byte(registryStudio)}}, "registry"),
	)
	// As the daemon starts: the supervisor is given what the library resolves.
	ok, _, err := res.Studios()
	if err != nil {
		t.Fatal(err)
	}
	var studios []supervisor.Studio
	for _, e := range ok {
		studios = append(studios, supervisor.Studio{Manifest: e.Manifest, File: e.File})
	}
	sup.SetStudios(studios)

	srv, err := New(sup, fstest.MapFS{}, addr, t.Logf,
		WithLibrary(res),
		WithApproval(st),
		WithManifests(local, library.NewReader(filepath.Join(d.Cache(), "manifests")), library.NewFetcher()))
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// commandsFor is every command the approval screen would show for id.
func commandsFor(t *testing.T, srv *Server, id string) (int, string) {
	t.Helper()
	rec := do(t, srv, "GET", "/api/v1/studios/"+id+"/approval", nil)
	if rec.Code != http.StatusOK {
		return rec.Code, rec.Body.String()
	}
	var p struct {
		Commands []struct {
			Command string `json:"command"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, c := range p.Commands {
		all = append(all, c.Command)
	}
	return rec.Code, strings.Join(all, "\n")
}

// Q10: after an Override, "the user's next install or launch shows the changed
// commands instead of running them". The preview is built from what the
// supervisor was given, and the supervisor was given the library once, at
// startup — so a saved Override showed, approved and ran the registry's
// commands until the daemon restarted.
func TestAnOverrideIsWhatTheNextInstallShows(t *testing.T) {
	srv := overrideServer(t)

	if _, cmds := commandsFor(t, srv, "reg-studio"); !strings.Contains(cmds, "make registry") {
		t.Fatalf("before the override the screen shows:\n%s", cmds)
	}

	mine := strings.Replace(registryStudio, "make registry", "make mine", 1)
	body, _ := json.Marshal(map[string]string{"text": mine})
	if rec := doJSON(t, srv, "PUT", "/api/v1/launcher/manifests/reg-studio", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	_, cmds := commandsFor(t, srv, "reg-studio")
	if !strings.Contains(cmds, "make mine") || strings.Contains(cmds, "make registry") {
		t.Errorf("after saving an override the approval screen shows:\n%s\nwant the override's command, and not the registry's", cmds)
	}

	// And Revert puts the registry's back.
	if rec := do(t, srv, "DELETE", "/api/v1/launcher/manifests/reg-studio", nil); rec.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body)
	}
	if _, cmds := commandsFor(t, srv, "reg-studio"); !strings.Contains(cmds, "make registry") {
		t.Errorf("after reverting the approval screen shows:\n%s", cmds)
	}
}

// A studio that arrives by import or Duplicate is a studio someone means to
// install. The supervisor did not know it existed, so installing it answered
// "no studio" until the daemon restarted.
func TestANewEntryCanBeInstalledWithoutARestart(t *testing.T) {
	srv := overrideServer(t)

	body, _ := json.Marshal(map[string]string{"new_id": "reg-copy"})
	if rec := doJSON(t, srv, "POST", "/api/v1/launcher/manifests/reg-studio:duplicate", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	if code, out := commandsFor(t, srv, "reg-copy"); code != http.StatusOK {
		t.Errorf("a duplicate's approval screen answered %d: %s", code, out)
	}

	imported := strings.NewReplacer("reg-studio", "imp-studio", "reg studio", "imp studio").Replace(registryStudio)
	items, _ := json.Marshal(map[string]any{"items": []map[string]string{{"text": imported}}})
	rec := doJSON(t, srv, "POST", "/api/v1/launcher/manifests:import", string(items))
	var report struct {
		Confirm string `json:"confirm"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil || report.Confirm == "" {
		t.Fatalf("import report: %d %s", rec.Code, rec.Body)
	}
	confirm, _ := json.Marshal(map[string]any{"items": []map[string]string{{"text": imported}}, "confirm": report.Confirm})
	if rec := doJSON(t, srv, "POST", "/api/v1/launcher/manifests:import", string(confirm)); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if code, out := commandsFor(t, srv, "imp-studio"); code != http.StatusOK {
		t.Errorf("an imported studio's approval screen answered %d: %s", code, out)
	}
}

// A registry entry's fields live under /manifest, so its criteria point there
// too — the same place its errors point, and the place the form edits.
func TestAnEntrysCriteriaPointIntoItsInlineManifest(t *testing.T) {
	srv, _ := libraryServer(t)
	inline := "  " + strings.ReplaceAll(strings.TrimSuffix(localManifest, "\n"), "\n", "\n  ") + "\n"
	entry := "id: wan-studio\nrepo: https://github.com/someone/wan\nref: v0.3.1\nmanifest:\n" + inline
	body, err := json.Marshal(map[string]string{"text": entry})
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, "POST", "/api/v1/launcher/manifests:validate", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Valid    bool   `json:"valid"`
		Kind     string `json:"kind"`
		Criteria struct {
			Items []struct {
				Number  int    `json:"number"`
				Pointer string `json:"pointer"`
			} `json:"items"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.Kind != "pointer" {
		t.Fatalf("the entry is %q and valid=%v: %s", got.Kind, got.Valid, rec.Body)
	}
	pointers := map[int]string{}
	for _, c := range got.Criteria.Items {
		pointers[c.Number] = c.Pointer
	}
	if pointers[8] != "/manifest/license" || pointers[1] != "/manifest/id" {
		t.Errorf("criterion 8 points at %q and 1 at %q; want them under /manifest", pointers[8], pointers[1])
	}
}
