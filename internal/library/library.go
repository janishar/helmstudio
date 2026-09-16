// Package library resolves the set of studios this machine knows about
// (docs/design/01-prd.md R3b, docs/decisions.md M7 Q5-Q8).
//
// The catalogue is a library, not an install list: installed studios are a
// subset of it. One entry per id, from three sources, highest first — a local
// manifest the user wrote, the manifest in the studio's own repository, and
// the inline manifest a registry pointer carries.
//
// **First match wins by existence, not by validity.** That distinction is the
// whole design. Falling through from a local manifest that fails to parse
// would silently run the registry's version of a studio the user had
// deliberately overridden, which is the worst possible outcome of a typo. So
// the highest source that *has a file* for an id wins, and if that file is
// broken the entry is listed broken, with its errors and a way to edit it. The
// sources below it are never read.
//
// **Nothing here touches the network.** Startup reads files: the bundled
// registry, the local directory, installed checkouts and the manifest cache. A
// pointer with nothing cached is listed as not fetched, with a Fetch action,
// rather than quietly reaching out on a laptop that just opened its lid.
package library

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/janishar/helmstudio/internal/manifest"
)

// Source is where an entry's manifest came from, highest precedence first.
type Source string

const (
	// SourceLocal is <data>/studios/<id>.yaml — what the user wrote or imported.
	SourceLocal Source = "local"
	// SourceRepo is helmstudio.yaml from the studio's own repository: its
	// installed checkout, or the cache of a fetched one.
	SourceRepo Source = "repo"
	// SourceRegistry is the inline manifest a bundled pointer carries.
	SourceRegistry Source = "registry"
)

// State is what is known about an entry's manifest.
type State string

const (
	// StateResolved means a manifest was read and validated.
	StateResolved State = "resolved"
	// StateNotFetched means the winning source is a pointer whose repository
	// has not been read, and which carries no inline manifest.
	StateNotFetched State = "not_fetched"
	// StateInvalid means the winning source has a file that does not validate.
	StateInvalid State = "invalid"
)

// Level is the certification level, which is derived and never declared
// (docs/decisions.md M7 Q15).
type Level string

const (
	// LevelDraft is a manifest with local_path: it builds a directory already
	// on this machine, so there is nothing anyone could have reviewed.
	LevelDraft Level = "draft"
	// LevelUnverified is everything else today. Verified and Registry need a
	// smoke harness and CI, and claiming either without one would be a lie.
	LevelUnverified Level = "unverified"
)

// Entry is one studio the machine knows about.
type Entry struct {
	ID    string `json:"id"`
	State State  `json:"manifest_state"`

	Source Source `json:"source"`
	// Overrides is the source this entry shadows, when it shadows one. It is
	// what the card means by "overrides the registry entry", and what Revert
	// would fall back to.
	Overrides Source `json:"overrides,omitempty"`
	Level     Level  `json:"level"`

	Manifest *manifest.Manifest `json:"-"`
	Valid    bool               `json:"manifest_valid"`
	Errors   []manifest.Error   `json:"errors,omitempty"`

	// File is where the winning document is, when it is a file on this
	// machine. Text and Digest are its bytes and their sha256.
	File   string `json:"manifest_file,omitempty"`
	Text   []byte `json:"-"`
	Digest string `json:"digest,omitempty"`

	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// Store is one source of manifests. List and Read are separate on purpose:
// resolution decides the winner from what each source *has*, and only then
// reads the winner — which is what makes "the lower sources are not read"
// a checkable claim rather than an intention.
type Store interface {
	Kind() Source
	// List returns the ids this store has something for. It must not read or
	// validate the documents themselves.
	List() ([]string, error)
	// Read returns the document for id, and a label for where it came from.
	Read(id string) (data []byte, file string, err error)
}

// Resolver holds the stores in precedence order.
type Resolver struct {
	Stores []Store
}

// New returns a resolver over stores, highest precedence first.
func New(stores ...Store) *Resolver { return &Resolver{Stores: stores} }

// Resolve derives the library: one entry per id, in id order.
//
// The two-phase shape — list everything, then read only winners — is what the
// milestone's review asks to see. A resolver that read each source in turn
// until one parsed would look almost identical and would quietly do the wrong
// thing on the day a user's override had a typo in it.
func (r *Resolver) Resolve() ([]Entry, error) {
	type has struct {
		store Store
		rank  int
	}
	// Phase one: who has what. No document is read here.
	holders := map[string][]has{}
	for rank, s := range r.Stores {
		ids, err := s.List()
		if err != nil {
			return nil, fmt.Errorf("listing %s manifests: %w", s.Kind(), err)
		}
		for _, id := range ids {
			holders[id] = append(holders[id], has{s, rank})
		}
	}

	ids := make([]string, 0, len(holders))
	for id := range holders {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		hs := holders[id]
		sort.Slice(hs, func(i, j int) bool { return hs[i].rank < hs[j].rank })
		winner := hs[0]

		e := Entry{ID: id, Source: winner.store.Kind(), Level: LevelUnverified}
		if len(hs) > 1 {
			e.Overrides = hs[1].store.Kind()
		}

		// Phase two: read exactly one document — the winner's.
		data, file, err := winner.store.Read(id)
		if err != nil {
			e.State, e.File = StateInvalid, file
			e.Errors = []manifest.Error{{
				File: file, Pointer: "/", Rule: "read", Message: err.Error(),
			}}
			out = append(out, e)
			continue
		}
		e.File = file
		fill(&e, file, data)
		out = append(out, e)
	}
	return out, nil
}

// fill validates data and records what it says. A pointer with no inline
// manifest is not an error: it is a studio whose manifest has not been read
// yet, and it says so rather than pretending to be broken.
func fill(e *Entry, file string, data []byte) {
	e.Text = data

	kind, res, err := manifest.ValidateAny(file, data)
	if err != nil {
		e.State, e.Valid = StateInvalid, false
		e.Errors = []manifest.Error{{File: file, Pointer: "/", Rule: "read", Message: err.Error()}}
		return
	}
	e.Errors = res.Errors
	if !res.OK() {
		e.State, e.Valid = StateInvalid, false
		return
	}
	e.Valid = true

	switch kind {
	case manifest.KindPointer:
		entry, _, err := manifest.LoadEntryBytes(file, data)
		if err != nil {
			e.State, e.Valid = StateInvalid, false
			e.Errors = []manifest.Error{{File: file, Pointer: "/", Rule: "read", Message: err.Error()}}
			return
		}
		e.Repo, e.Ref, e.Digest = entry.Repo, entry.Ref, entry.Digest
		if entry.Manifest == nil {
			// A pointer with nothing to read. Not fetched is a state, not a
			// failure: fetching is a user action (Q7).
			e.State = StateNotFetched
			return
		}
		e.Manifest, e.State = entry.Manifest, StateResolved
	default:
		m, _, err := manifest.LoadBytes(file, data)
		if err != nil {
			e.State, e.Valid = StateInvalid, false
			e.Errors = []manifest.Error{{File: file, Pointer: "/", Rule: "read", Message: err.Error()}}
			return
		}
		e.Manifest, e.State = m, StateResolved
		e.Repo, e.Ref, e.Digest = m.Repo, m.Ref, m.Digest
	}
	if e.Manifest != nil && e.Manifest.LocalPath != "" {
		e.Level = LevelDraft
	}
}

// Default returns the resolver a daemon uses: the three sources in precedence
// order, all of them files on this machine (docs/decisions.md M7 Q5, Q7).
//
// registryDir overrides the bundled registry, which is what `-studios` is for
// during development. Nothing here reaches the network, and a test asserts it:
// a pointer with nothing cached resolves to not_fetched rather than to a
// connection a user did not ask for.
func Default(dataRoot, cacheRoot string, bundled fs.FS, registryDir string) *Resolver {
	studios := filepath.Join(dataRoot, "studios")
	registry := FS(SourceRegistry, bundled, "registry")
	if registryDir != "" {
		registry = Dir(SourceRegistry, registryDir)
	}
	return New(
		// 1: what the user wrote or imported.
		Dir(SourceLocal, studios),
		// 2: the studio's own manifest — from its installed checkout first, so
		// an installed studio stays launchable when the cache has been purged
		// and the Mac is offline, then from the cache of a fetched one.
		Checkouts(studios),
		Cache(filepath.Join(cacheRoot, "manifests")),
		// 3: the pointer, with whatever inline manifest it carries.
		registry,
	)
}

// Studios returns the entries that resolved to a usable manifest, and the ones
// that did not. Both matter: the second list is what the library shows as
// invalid or not fetched, which R2 (amended by Q6) says must be visible rather
// than hidden.
func (r *Resolver) Studios() (ok []Entry, problems []Entry, err error) {
	entries, err := r.Resolve()
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.State == StateResolved && e.Manifest != nil {
			ok = append(ok, e)
		} else {
			problems = append(problems, e)
		}
	}
	return ok, problems, nil
}
