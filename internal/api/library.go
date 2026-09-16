package api

import (
	"net/http"

	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/manifest"
)

// The library on GET /studios (docs/design/01-prd.md R3b; docs/decisions.md
// M7 Q6, Q18).
//
// The catalogue is a library, not an install list. Until this, the list came
// from the supervisor, which is only ever given studios that resolved to a
// valid manifest — so a studio whose override had a typo simply vanished, with
// nothing on screen to say where it went. R2 as amended says an invalid
// manifest is *listed* as invalid, and this is where that becomes true.
//
// So the library is the list, and the supervisor contributes group status for
// the entries it knows about.

// WithLibrary makes GET /studios the library. Without it the list is the
// supervisor's, which is what `helm dev` wants: one studio, given on the
// command line, with no registry behind it.
func WithLibrary(r *library.Resolver) Option {
	return func(s *Server) { s.library = r }
}

// libraryFields are what an entry carries beyond what a manifest says.
type libraryFields struct {
	Source        library.Source   `json:"source,omitempty"`
	Overrides     library.Source   `json:"overrides,omitempty"`
	Level         library.Level    `json:"level,omitempty"`
	ManifestState library.State    `json:"manifest_state,omitempty"`
	ManifestValid bool             `json:"manifest_valid"`
	Errors        []manifest.Error `json:"errors,omitempty"`
	ManifestFile  string           `json:"manifest_file,omitempty"`
	Repo          string           `json:"repo,omitempty"`
	Ref           string           `json:"ref,omitempty"`
	LocalPath     string           `json:"local_path,omitempty"`
	// ApprovalRequired is true when what would run now does not match what was
	// last approved, so the next install, retry or launch will ask (Q10).
	ApprovalRequired bool `json:"approval_required,omitempty"`
}

// fromLibrary lists the library, merging each entry with what the supervisor
// and the installer know about it.
func (s *Server) fromLibrary(r *http.Request) ([]Studio, error) {
	entries, err := s.library.Resolve()
	if err != nil {
		return nil, err
	}
	out := make([]Studio, 0, len(entries))
	for _, e := range entries {
		out = append(out, s.entry(r, e))
	}
	return out, nil
}

// entry turns one library entry into what the API serves.
//
// An entry whose manifest did not resolve still becomes a row: it carries its
// id, its source, why it is invalid and where the file is. That is the whole
// point — a studio that vanished is worse than a studio that says what is
// wrong with it, because only one of them can be fixed.
func (s *Server) entry(r *http.Request, e library.Entry) Studio {
	out := Studio{
		ID:   e.ID,
		Name: e.ID,
		libraryFields: libraryFields{
			Source: e.Source, Overrides: e.Overrides, Level: e.Level,
			ManifestState: e.State, ManifestValid: e.Valid, Errors: e.Errors,
			ManifestFile: e.File, Repo: e.Repo, Ref: e.Ref,
		},
	}
	if e.Manifest == nil {
		// Not fetched, or invalid. There is no manifest to read, and the
		// supervisor has never heard of it.
		out.Kinds = []string{}
		return out
	}

	// It resolved, so it is a studio the rest of the daemon knows.
	if st, ok := s.sup.Studio(e.ID); ok {
		if v, err := s.studio(r, st); err == nil {
			v.libraryFields = out.libraryFields
			out = v
		}
	} else {
		m := e.Manifest
		out.ManifestLoaded = true
		out.Name, out.Description, out.Kinds, out.PeakRAMGB = m.Name, m.Description, m.Kinds, m.PeakRAMGB
		for _, p := range m.EffectiveProcesses() {
			out.Heavy = out.Heavy || p.Heavy
		}
	}
	out.LocalPath = e.Manifest.LocalPath

	// Whether the next install or launch will ask. Computing it here means the
	// card can say so before anyone presses anything.
	if s.approvals != nil {
		if st, ok := s.findStudio(e.ID); ok {
			if p, err := s.preview(r.Context(), st); err == nil {
				if rec, err := s.recorded(r.Context(), e.ID); err == nil {
					out.ApprovalRequired = rec.Digest != p.Digest
				}
			}
		}
	}
	return out
}
