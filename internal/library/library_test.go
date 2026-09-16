package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const validManifest = `id: %s
name: %s
kinds: [video]
repo: https://github.com/someone/%s
ref: v1.0.0
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [metal]
processes:
  - name: studio
    role: main
    cmd: "./dist/%s --port {port}"
    port: { prefer: 8790 }
    health: { tcp: true, timeout_s: 60 }
`

func manifestFor(id, name string) string {
	return fmt.Sprintf(validManifest, id, name, id, id)
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The claim the whole precedence design rests on: when a higher source has an
// id, the lower ones are never read. A resolver that read each source in turn
// until one parsed would pass every other test in this file and would silently
// run the registry's version of a studio the user had deliberately overridden.
func TestAHigherSourceShortCircuitsTheOnesBelowIt(t *testing.T) {
	local := t.TempDir()
	write(t, local, "wan-studio.yaml", manifestFor("wan-studio", "wan studio, local"))

	registry := fstest.MapFS{
		"wan-studio.yaml": &fstest.MapFile{Data: []byte(manifestFor("wan-studio", "wan studio, registry"))},
	}

	var registryReads int
	r := New(
		Dir(SourceLocal, local),
		Counting(FS(SourceRegistry, registry, "registry"), &registryReads),
	)

	entries, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Source != SourceLocal {
		t.Errorf("source is %q, want local", e.Source)
	}
	if e.Overrides != SourceRegistry {
		t.Errorf("overrides is %q, want registry — the card has to be able to say what it shadows", e.Overrides)
	}
	if e.Manifest == nil || e.Manifest.Name != "wan studio, local" {
		t.Errorf("resolved the wrong manifest: %+v", e.Manifest)
	}
	if registryReads != 0 {
		t.Errorf("the registry was read %d times; a shadowed source must never be read", registryReads)
	}
}

// An invalid winner is listed invalid — it does not fall through. Falling
// through is how a typo in an override silently runs someone else's build
// commands instead of the user's.
func TestAnInvalidWinnerDoesNotFallThrough(t *testing.T) {
	local := t.TempDir()
	write(t, local, "wan-studio.yaml", "id: wan-studio\nname: [this is not a name]\n")

	registry := fstest.MapFS{
		"wan-studio.yaml": &fstest.MapFile{Data: []byte(manifestFor("wan-studio", "wan studio, registry"))},
	}

	var registryReads int
	r := New(
		Dir(SourceLocal, local),
		Counting(FS(SourceRegistry, registry, "registry"), &registryReads),
	)
	entries, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	switch {
	case e.State != StateInvalid:
		t.Errorf("state is %q, want invalid", e.State)
	case e.Valid:
		t.Error("an unparseable manifest is not valid")
	case len(e.Errors) == 0:
		t.Error("an invalid entry must carry its errors, or there is nothing to fix")
	case e.Source != SourceLocal:
		t.Errorf("source is %q; the invalid file is still the one that won", e.Source)
	case e.Manifest != nil:
		t.Error("an invalid entry has no manifest")
	}
	if registryReads != 0 {
		t.Errorf("the registry was read %d times; an invalid winner must not fall through to it", registryReads)
	}
}

// Entries from different sources coexist: R2's "one bad manifest never blocks
// the others" holds across ids, which is a different claim from what happens
// within one id.
func TestSourcesCoexistAcrossIds(t *testing.T) {
	local := t.TempDir()
	write(t, local, "mine.yaml", manifestFor("mine", "mine"))
	write(t, local, "broken.yaml", "nonsense: [")

	registry := fstest.MapFS{
		"theirs.yaml": &fstest.MapFile{Data: []byte(manifestFor("theirs", "theirs"))},
	}

	entries, err := New(Dir(SourceLocal, local), FS(SourceRegistry, registry, "registry")).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (mine, broken, theirs)", len(entries))
	}
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	if byID["mine"].State != StateResolved || byID["theirs"].State != StateResolved {
		t.Error("a broken entry blocked the others")
	}
	if byID["broken"].State != StateInvalid {
		t.Error("the broken entry should be listed invalid, not hidden")
	}
	// Ordered by id, so the list does not reshuffle between reads.
	if entries[0].ID != "broken" || entries[1].ID != "mine" || entries[2].ID != "theirs" {
		t.Errorf("entries are not in id order: %s %s %s", entries[0].ID, entries[1].ID, entries[2].ID)
	}
}

// A pointer with no inline manifest and nothing cached is a studio whose
// manifest has not been read, not a broken one. Fetching is a user action.
func TestAPointerWithNothingCachedIsNotFetchedRatherThanBroken(t *testing.T) {
	registry := fstest.MapFS{
		"wan-studio.yaml": &fstest.MapFile{Data: []byte(
			"id: wan-studio\nrepo: https://github.com/someone/wan-studio\nref: v1.0.0\n")},
	}
	entries, err := New(FS(SourceRegistry, registry, "registry")).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]
	if e.State != StateNotFetched {
		t.Errorf("state is %q, want not_fetched", e.State)
	}
	if !e.Valid {
		t.Error("the pointer itself is perfectly valid; it just has nothing to show yet")
	}
	if e.Repo != "https://github.com/someone/wan-studio" || e.Ref != "v1.0.0" {
		t.Errorf("a not-fetched entry still knows where to fetch from: %+v", e)
	}
}

// A pointer carrying an inline manifest resolves to it, and says the source is
// the registry — which is true, and is what the card shows.
func TestAPointerWithAnInlineManifestResolves(t *testing.T) {
	inline := "id: wan-studio\nrepo: https://github.com/someone/wan-studio\nref: v1.0.0\nmanifest:\n"
	for _, line := range splitLines(manifestFor("wan-studio", "wan studio")) {
		if line == "" {
			inline += "\n"
			continue
		}
		inline += "  " + line + "\n"
	}
	registry := fstest.MapFS{"wan-studio.yaml": &fstest.MapFile{Data: []byte(inline)}}

	entries, err := New(FS(SourceRegistry, registry, "registry")).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]
	if e.State != StateResolved {
		t.Fatalf("state is %q with errors %v", e.State, e.Errors)
	}
	if e.Manifest == nil || e.Manifest.Name != "wan studio" {
		t.Errorf("did not resolve the inline manifest: %+v", e.Manifest)
	}
	if e.Source != SourceRegistry {
		t.Errorf("source is %q, want registry", e.Source)
	}
}

// local_path means the build happens in a directory already on this machine,
// so there is nothing anyone could have reviewed: Draft (Q15).
func TestLocalPathIsDraft(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "mine.yaml", `id: mine
name: mine
kinds: [video]
local_path: /Users/someone/code/mine
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [metal]
processes:
  - name: studio
    role: main
    cmd: "./mine --port {port}"
    port: { prefer: 8790 }
    health: { tcp: true, timeout_s: 60 }
`)
	entries, err := New(Dir(SourceLocal, dir)).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Level != LevelDraft {
		t.Errorf("level is %q, want draft", entries[0].Level)
	}
}

// `_reverted/` sits in the same directory as local manifests and must never be
// mistaken for a studio. An id cannot begin with an underscore, which is why
// that name was chosen (Q9).
func TestRevertedFilesAreNotStudios(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "mine.yaml", manifestFor("mine", "mine"))
	write(t, filepath.Join(dir, "_reverted"), "mine-20260916.yaml", manifestFor("mine", "an old copy"))

	entries, err := New(Dir(SourceLocal, dir)).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "mine" {
		t.Fatalf("reverted files leaked into the library: %+v", entries)
	}
	if entries[0].Manifest.Name != "mine" {
		t.Errorf("resolved the reverted copy: %q", entries[0].Manifest.Name)
	}
}
