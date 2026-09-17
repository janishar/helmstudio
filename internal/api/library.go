package api

import (
	"net/http"

	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/supervisor"
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
	// Provenance is where a Local entry came from — imported, duplicated,
	// written here, or read from a folder. A note for the card and never a
	// trust signal: it says how a file arrived, not whether to believe it.
	Provenance *library.Provenance `json:"provenance,omitempty"`
	Repo       string              `json:"repo,omitempty"`
	Ref        string              `json:"ref,omitempty"`
	LocalPath  string              `json:"local_path,omitempty"`
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
		Hue:  hueOf(e.Manifest, e.ID),
		libraryFields: libraryFields{
			Source: e.Source, Overrides: e.Overrides, Level: e.Level,
			ManifestState: e.State, ManifestValid: e.Valid, Errors: e.Errors,
			ManifestFile: e.File, Repo: e.Repo, Ref: e.Ref,
			Provenance: s.provenance(e),
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
		out.Hue = hueOf(m, e.ID)
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

// provenance is the note on a Local entry: where the file came from.
//
// It is read here rather than carried on the entry because the resolver deals
// in documents and precedence, and knows nothing about how a file arrived. A
// note that belongs to one source has no business in the type every source
// shares.
func (s *Server) provenance(e library.Entry) *library.Provenance {
	if s.local == nil || e.Source != library.SourceLocal {
		return nil
	}
	return s.local.Provenance(e.ID)
}

// reloadLibrary gives the supervisor what the library resolves to now.
//
// Every write to the library calls it. The supervisor is what install, launch
// and the approval preview read a manifest from, and it was given the library
// once, at startup — so without this a saved Override was shown, approved and
// run as the registry's version until the daemon restarted, and a studio that
// arrived by import or Duplicate could not be installed at all. Q10 says the
// next install or launch after an Override shows the changed commands, which
// is only true if the thing building the preview has heard about it.
//
// Running groups are not affected: a process was handed its command when it
// started, and the next launch is the one that uses the new manifest.
func (s *Server) reloadLibrary() {
	if s.library == nil {
		return
	}
	ok, _, err := s.library.Studios()
	if err != nil {
		s.logf("re-resolving the library after a change: %v", err)
		return
	}
	studios := make([]supervisor.Studio, 0, len(ok))
	for _, e := range ok {
		studios = append(studios, supervisor.Studio{Manifest: e.Manifest, File: e.File})
	}
	s.sup.SetStudios(studios)
}
