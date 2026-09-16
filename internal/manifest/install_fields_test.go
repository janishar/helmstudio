package manifest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The fields install and weights run on decode, with the schema's defaults.
func TestLoadReturnsInstallAndWeightFields(t *testing.T) {
	// h3's registry entry carries its manifest inline until the repository
	// ships its own (M7 Q4); the manifest under test is that one.
	m, res, err := loadInlineManifest(t, "../../studios/h3-studio.yaml")
	if err != nil || !res.OK() {
		t.Fatalf("Load: %v %v", err, res.Errors)
	}
	if !m.Submodules || !slices.Contains(m.Requires.Tools, "make") || m.Requires.DiskGB != 200 || m.Requires.RAMGB != 64 {
		t.Fatalf("manifest decoded as submodules=%v requires=%+v", m.Submodules, m.Requires)
	}
	b := m.Build[0]
	if b.EffectiveShell() != "sh" || b.EffectiveTimeoutS() != 3600 || b.Optional || b.Cwd != "h3c" || b.EffectiveName() != "Build the Metal engine" {
		t.Fatalf("build step decoded as %+v", b)
	}
	if (BuildStep{Run: "make"}).EffectiveName() != "make" {
		t.Fatal("a build step's name should default to its command")
	}
	w := m.Weights[1]
	if w.Name != "ref2va" || !w.Optional || !slices.Equal(w.Files, []string{"Ref2VA/**"}) || w.EffectiveRevision() != "main" {
		t.Fatalf("weight decoded as %+v", w)
	}
}

// The digest follows values, not formatting: a comment or reordered keys keep
// it; any changed value moves it.
func TestDigestFollowsValuesNotFormatting(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) *Manifest {
		t.Helper()
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		m, res, err := Load(f)
		if err != nil || !res.OK() {
			t.Fatalf("%s: %v %v", name, err, res.Errors)
		}
		return m
	}
	base := `id: digest-probe
name: digest probe
kinds: [video]
local_path: /tmp/d
requires: { os: [darwin], arch: [arm64] }
runtime: { framework: other, backends: [cpu] }
build:
  - { run: make }
processes:
  - { name: studio, cmd: run }
`
	a := write("a.yaml", base)
	b := write("b.yaml", "# a comment\n"+`name: digest probe
id: digest-probe
kinds: [video]
local_path: /tmp/d
runtime: { backends: [cpu], framework: other }
requires: { arch: [arm64], os: [darwin] }
build:
  - run: make
processes:
  - { cmd: run, name: studio }
`)
	c := write("c.yaml", base[:len(base)-len("  - { name: studio, cmd: run }\n")]+"  - { name: studio, cmd: run2 }\n")
	if len(a.Digest) != 64 {
		t.Fatalf("digest %q is not a sha256", a.Digest)
	}
	if a.Digest != b.Digest {
		t.Errorf("formatting changed the digest: %s vs %s", a.Digest, b.Digest)
	}
	if a.Digest == c.Digest {
		t.Error("a changed command kept the digest")
	}
}
