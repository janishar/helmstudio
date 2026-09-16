package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/schema"
	"gopkg.in/yaml.v3"
)

// Writing manifests (docs/design/01-prd.md R3a-R3d; docs/decisions.md M7 Q17,
// Q18, Q19).
//
//	GET    /api/v1/studios/{id}/manifest        the resolved text and its digest
//	POST   /api/v1/launcher/manifests:validate  a verdict; writes nothing
//	POST   /api/v1/launcher/manifests:edit      one field; writes nothing
//	PUT    /api/v1/launcher/manifests/{id}      save a Local manifest
//	DELETE /api/v1/launcher/manifests/{id}      Revert — moves, never deletes
//	POST   /api/v1/launcher/manifests/{id}:duplicate
//	POST   /api/v1/launcher/manifests:import
//	POST   /api/v1/launcher/repositories:read
//
// The page never parses YAML. It sends text and gets back errors with lines
// and pointers, the criteria, and the document as JSON; a form edit goes as a
// JSON pointer and a value. A JavaScript YAML parser would be a second
// implementation, and its disagreements with this one about anchors, merge
// keys, `on` and duplicate keys would show one manifest while validating
// another.

// WithManifests serves the manifest operations. Without it they are not served
// at all, which is what `helm dev` wants: one studio, from a path on the
// command line, with no library to write to.
func WithManifests(local *library.Local, reader *library.Reader, fetcher *library.Fetcher) Option {
	return func(s *Server) {
		s.local, s.repoReader, s.fetcher = local, reader, fetcher
	}
}

func (s *Server) routeManifests() {
	if s.local == nil {
		return
	}
	s.mux.HandleFunc("GET /schema/manifest.json", s.manifestSchema)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/manifest", s.getManifest)
	s.mux.HandleFunc("POST "+Base+"/launcher/manifests:validate", s.validateManifest)
	s.mux.HandleFunc("POST "+Base+"/launcher/manifests:edit", s.editManifest)
	s.mux.HandleFunc("POST "+Base+"/launcher/manifests:import", s.importManifests)
	s.mux.HandleFunc("POST "+Base+"/launcher/repositories:read", s.readRepository)
	s.mux.HandleFunc("PUT "+Base+"/launcher/manifests/{id}", s.saveManifest)
	s.mux.HandleFunc("DELETE "+Base+"/launcher/manifests/{id}", s.revertManifest)
	// :duplicate is an action on a path that also has a bare id route, so it
	// is matched before the bare one (the M8a router finding).
	s.mux.HandleFunc("POST "+Base+"/launcher/manifests/{action}", s.manifestAction)
}

// manifestSchema serves schema/manifest.json, which the editor's form is
// generated from (03 §13a, M7 Q17).
//
// It is a static document at a path of its own rather than an API operation:
// it takes no arguments, returns the same bytes for everyone, and changes only
// when the contract does — which is what `/sdk/v1/helm.css` is too. Making it
// an operation would put a constant in the generated clients of three
// languages that have no use for it.
//
// The bytes are the same ones every validator in this repository embeds, so a
// form field that the daemon would reject cannot be drawn.
func (s *Server) manifestSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+library.Digest(schema.Manifest)+`"`)
	http.ServeContent(w, r, "manifest.json", time.Time{}, bytes.NewReader(schema.Manifest))
}

// check is api/openapi.yaml's ManifestCheck: a verdict whether or not it is
// valid, plus the document as JSON, which is what the form renders from.
type check struct {
	Valid    bool                     `json:"valid"`
	Kind     manifest.Kind            `json:"kind"`
	Errors   []manifest.Error         `json:"errors"`
	Document any                      `json:"document,omitempty"`
	Text     string                   `json:"text,omitempty"`
	Criteria *manifest.CriteriaResult `json:"criteria,omitempty"`
}

// inspect validates text and describes it, whichever document it is.
func inspect(name string, data []byte) check {
	kind, res, err := manifest.ValidateAny(name, data)
	out := check{Kind: kind, Errors: res.Errors}
	if err != nil {
		out.Errors = []manifest.Error{{File: name, Pointer: "/", Rule: "read", Message: err.Error()}}
		return out
	}
	if out.Errors == nil {
		out.Errors = []manifest.Error{}
	}
	out.Valid = res.OK()

	// The document as JSON, for the form — whether or not it is valid, as
	// api/openapi.yaml's ManifestCheck says. An editor exists to fix invalid
	// documents, and a form that went blank the moment one was invalid would
	// leave the page guessing at what the text contains; a page that guesses
	// which parents exist replaces the ones it could not see. Decoding here
	// rather than on the page is the whole point: one parser, and the form
	// renders what the validator read.
	var doc any
	if err := yamlToAny(data, &doc); err == nil {
		if m, ok := doc.(map[string]any); ok {
			out.Document = m
		}
	}
	if !out.Valid {
		return out
	}

	var m *manifest.Manifest
	switch kind {
	case manifest.KindPointer:
		if e, _, err := manifest.LoadEntryBytes(name, data); err == nil && e != nil {
			m = e.Manifest
		}
	default:
		m, _, _ = manifest.LoadBytes(name, data)
	}
	if m != nil {
		c := manifest.Criteria(m)
		out.Criteria = &c
	}
	return out
}

func (s *Server) getManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.library == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "this daemon serves no library")
		return
	}
	entries, err := s.library.Resolve()
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": e.ID, "text": string(e.Text), "digest": library.Digest(e.Text),
			"source": e.Source, "kind": kindOf(e.Text), "file": e.File,
		})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q is known", id))
}

func kindOf(data []byte) manifest.Kind { return manifest.DetectKind(data) }

func (s *Server) validateManifest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	writeJSON(w, http.StatusOK, inspect("pasted text", []byte(body.Text)))
}

func (s *Server) editManifest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Pointer string `json:"pointer"`
		Value   any    `json:"value"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	out, err := manifest.Edit([]byte(body.Text), body.Pointer, body.Value)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_edit", err.Error())
		return
	}
	res := inspect("edited text", out)
	res.Text = string(out)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) saveManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	data := []byte(body.Text)

	// An invalid document is refused rather than saved broken. The library
	// would list it as invalid, which is right for a file someone edited
	// outside helmstudio — but writing one from inside it, on purpose, would
	// be helmstudio breaking the user's library for them.
	res := inspect(id, data)
	if !res.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, res)
		return
	}
	if declared := declaredID(data); declared != "" && declared != id {
		writeError(w, http.StatusUnprocessableEntity, "id_mismatch",
			fmt.Sprintf("this document declares id %q and would be saved as %q; a local file is named for the id it claims", declared, id))
		return
	}

	digest, err := s.local.Save(id, data, r.Header.Get("If-Match"))
	switch {
	case errors.Is(err, library.ErrIfMatchRequired):
		writeError(w, http.StatusPreconditionRequired, "if_match_required",
			"saving over an existing manifest needs the digest the editor read, so two tabs cannot overwrite each other")
		return
	case errors.Is(err, library.ErrDigestMismatch):
		writeError(w, http.StatusConflict, "etag_mismatch",
			"this manifest changed since it was opened. Read it again before saving.")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	if s.local.Provenance(id) == nil {
		_ = s.local.SetProvenance(id, library.Provenance{Kind: "written"})
	}
	s.reloadLibrary()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "text": body.Text, "digest": digest,
		"source": library.SourceLocal, "kind": kindOf(data), "file": s.local.Path(id),
	})
}

func (s *Server) revertManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	moved, err := s.local.Revert(id)
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("%s has no local manifest to revert", id))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.reloadLibrary()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "moved_to": moved})
}

// manifestAction serves :duplicate, whose path shares a segment with the bare
// id route.
func (s *Server) manifestAction(w http.ResponseWriter, r *http.Request) {
	id, action, ok := strings.Cut(r.PathValue("action"), ":")
	if !ok || action != "duplicate" {
		writeError(w, http.StatusNotFound, "not_found", "the only action here is :duplicate")
		return
	}
	var body struct {
		NewID string `json:"new_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !library.ValidID(body.NewID) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_id",
			fmt.Sprintf("%q cannot name a studio: use lowercase letters, digits and hyphens, starting with a letter", body.NewID))
		return
	}
	if s.local.Exists(body.NewID) {
		writeError(w, http.StatusConflict, "id_taken", fmt.Sprintf("a local manifest for %q already exists", body.NewID))
		return
	}

	entries, err := s.library.Resolve()
	if err != nil {
		s.fail(w, err)
		return
	}
	var source []byte
	for _, e := range entries {
		if e.ID == id {
			source = e.Text
		}
	}
	if source == nil {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q is known", id))
		return
	}
	renamed, err := library.Rename(source, body.NewID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_document", err.Error())
		return
	}
	digest, err := s.local.Save(body.NewID, renamed, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	_ = s.local.SetProvenance(body.NewID, library.Provenance{Kind: "duplicated", FromID: id})
	s.reloadLibrary()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": body.NewID, "text": string(renamed), "digest": digest,
		"source": library.SourceLocal, "kind": kindOf(renamed), "file": s.local.Path(body.NewID),
	})
}

// importItemReport is one row of the import report.
type importItemReport struct {
	Index        int              `json:"index"`
	Valid        bool             `json:"valid"`
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Kind         manifest.Kind    `json:"kind"`
	CollidesWith library.Source   `json:"collides_with,omitempty"`
	Errors       []manifest.Error `json:"errors"`
	Flags        []string         `json:"flags,omitempty"`
	Added        bool             `json:"added,omitempty"`
}

// importManifests reports on what was given, then adds it on confirmation.
//
// Two phases, because import is not install and neither is it a surprise: the
// first pass writes nothing and says what each item is, whether it collides,
// and anything worth reading before agreeing. The second adds exactly the
// reported set.
func (s *Server) importManifests(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"items"`
		Confirm     string `json:"confirm"`
		Resolutions []struct {
			Index  int    `json:"index"`
			Action string `json:"action"`
			NewID  string `json:"new_id"`
		} `json:"resolutions"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Items) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "there is nothing to import")
		return
	}

	entries, err := s.library.Resolve()
	if err != nil {
		s.fail(w, err)
		return
	}
	known := map[string]library.Source{}
	for _, e := range entries {
		known[e.ID] = e.Source
	}

	texts := make([]string, len(body.Items))
	reports := make([]importItemReport, len(body.Items))
	for i, item := range body.Items {
		rep := importItemReport{Index: i, Errors: []manifest.Error{}}
		text := item.Text
		switch {
		case item.URL != "" && item.Text != "":
			rep.Errors = append(rep.Errors, manifest.Error{Pointer: "/", Rule: "import",
				Message: "give either text or a url, not both"})
		case item.URL != "":
			fetched, err := s.fetcher.Fetch(r.Context(), item.URL)
			if err != nil {
				rep.Errors = append(rep.Errors, manifest.Error{Pointer: "/", Rule: "import", Message: err.Error()})
			}
			text = fetched
		}
		texts[i] = text

		if len(rep.Errors) == 0 {
			res := inspect(fmt.Sprintf("item %d", i+1), []byte(text))
			rep.Valid, rep.Kind, rep.Errors = res.Valid, res.Kind, res.Errors
			rep.ID = declaredID([]byte(text))
			rep.Name = declaredName([]byte(text))
			if src, ok := known[rep.ID]; ok && rep.ID != "" {
				rep.CollidesWith = src
			}
			// `local_path` means the build happens in a directory already on
			// this Mac, without cloning. Worth reading before agreeing.
			if p := declaredLocalPath([]byte(text)); p != "" {
				rep.Flags = append(rep.Flags, fmt.Sprintf("builds in %s on this Mac, without cloning", p))
			}
		}
		reports[i] = rep
	}

	digest := importDigest(reports)
	if body.Confirm == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": reports, "confirm": digest})
		return
	}
	if body.Confirm != digest {
		writeErrorDetails(w, http.StatusConflict, "preview_changed",
			"What would be imported changed since the report, so nothing was added. Read it again.",
			map[string]any{"items": reports, "confirm": digest})
		return
	}

	actions := map[int]struct {
		Action string
		NewID  string
	}{}
	for _, res := range body.Resolutions {
		actions[res.Index] = struct {
			Action string
			NewID  string
		}{res.Action, res.NewID}
	}

	for i := range reports {
		rep := &reports[i]
		if !rep.Valid || rep.ID == "" {
			continue
		}
		id, act := rep.ID, actions[i]
		switch {
		case act.Action == "skip":
			continue
		case act.Action == "rename" && act.NewID != "":
			id = act.NewID
		case rep.CollidesWith != "" && act.Action != "override":
			// A collision with no instruction is skipped rather than guessed
			// at: silently replacing an entry is the one thing R3a says not
			// to do.
			continue
		}
		text := texts[i]
		if id != rep.ID {
			renamed, err := library.Rename([]byte(text), id)
			if err != nil {
				continue
			}
			text = string(renamed)
		}
		ifMatch := ""
		if _, d, err := s.local.Read(id); err == nil {
			ifMatch = d
		}
		if _, err := s.local.Save(id, []byte(text), ifMatch); err != nil {
			continue
		}
		p := library.Provenance{Kind: "imported"}
		if body.Items[i].URL != "" {
			p.URL = body.Items[i].URL
		}
		_ = s.local.SetProvenance(id, p)
		rep.Added = true
		rep.ID = id
	}
	s.reloadLibrary()
	writeJSON(w, http.StatusOK, map[string]any{"items": reports})
}

// importDigest fingerprints a report, so the confirmed set is the reported one.
func importDigest(reports []importItemReport) string {
	parts := make([]string, 0, len(reports))
	for _, r := range reports {
		parts = append(parts, fmt.Sprintf("%d|%v|%s|%s|%v", r.Index, r.Valid, r.ID, r.Kind, r.CollidesWith))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func (s *Server) readRepository(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo string `json:"repo"`
		Ref  string `json:"ref"`
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var res library.Result
	var err error
	switch {
	case body.Path != "":
		res, err = s.repoReader.ReadFolder(body.Path)
	case body.Repo != "":
		res, err = s.repoReader.ReadRepo(r.Context(), body.Repo, body.Ref)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "give either a repo and ref, or a path")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	out := map[string]any{
		"found": res.Found, "commit": res.Commit, "repo": res.Repo, "ref": res.Ref,
		"submodules": res.Submodules,
	}
	if res.Found {
		out["text"] = res.Text
		out["check"] = inspect(body.Repo+body.Path, []byte(res.Text))
		// Caching here means the next startup resolves it without the network.
		if id := declaredID([]byte(res.Text)); id != "" && res.Commit != "" {
			if _, err := s.repoReader.Cache(id, res.Commit, res.Text); err != nil {
				s.logf("caching %s's manifest: %v", id, err)
			} else {
				// A pointer that was not fetched resolves now.
				s.reloadLibrary()
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Small readers over a document that may not be valid, for the report. Each
// answers "" rather than failing: an invalid document still has a report row.
func declaredID(data []byte) string        { return topString(data, "id") }
func declaredName(data []byte) string      { return topString(data, "name") }
func declaredLocalPath(data []byte) string { return topString(data, "local_path") }

func topString(data []byte, key string) string {
	var top map[string]any
	if err := yamlToAny(data, &top); err != nil || top == nil {
		return ""
	}
	if v, ok := top[key].(string); ok {
		return v
	}
	// A pointer's inline manifest carries these too.
	if inner, ok := top["manifest"].(map[string]any); ok {
		if v, ok := inner[key].(string); ok {
			return v
		}
	}
	return ""
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return false
	}
	return true
}

// yamlToAny decodes YAML into v. It is here rather than in a caller so that
// every one of them goes through the same decoder the validator uses.
func yamlToAny(data []byte, v any) error { return yaml.Unmarshal(data, v) }
