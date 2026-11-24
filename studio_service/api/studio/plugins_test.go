package studio

import (
	"context"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// API 1.61: the repo's and the test stack's manifests are read; a bad one, one naming another folder or one off the
// local host rule is skipped.
func TestReadManifests(t *testing.T) {
	s := &Service{}
	s.SetLookup(func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "evil.example.com" {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("172.30.98.9")}, nil
	})
	bad := t.TempDir()
	for name, body := range map[string]string{
		"wrong-name": `{"name":"other","title":"x","version":"0.1.0","kind":"assistant-tools","description":"x","author":"x","may_do":["x"],"url":"http://a:1","health":"/h","tools":[{"name":"t","description":"x","input":{"type":"object"},"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}}]}`,
		"public":     `{"name":"public","title":"x","version":"0.1.0","kind":"assistant-tools","description":"x","author":"x","may_do":["x"],"url":"http://evil.example.com","health":"/h","tools":[{"name":"t","description":"x","input":{"type":"object"},"risk":{"uses_gpu":false,"deletes":false,"sends_off_machine":false}}]}`,
		"unknown":    `{"name":"unknown","extra":1}`,
		"empty-risk": `{"name":"empty-risk","title":"x","version":"0.1.0","kind":"assistant-tools","description":"x","author":"x","may_do":["x"],"url":"http://a:1","health":"/h","tools":[{"name":"t","description":"x","input":{"type":"object"},"risk":{}}]}`,
		"no-tools":   `{"name":"no-tools","title":"x","version":"0.1.0","kind":"assistant-tools","description":"x","author":"x","may_do":["x"],"url":"http://a:1","health":"/h"}`,
		"good":       `{"name":"good","title":"x","version":"0.1.0","kind":"model-provider","description":"x","author":"x","may_do":["x"],"url":"http://a:1","health":"/h","provider":{"path":"/v1","models":["m"],"key_hint":"k"}}`,
	} {
		if err := os.MkdirAll(filepath.Join(bad, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bad, name, "plugin.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.SetPluginDirs([]string{"../../../plugins", "../../../contract_tests/fake_plugins", bad})
	got := s.readManifests(context.Background())
	names := slices.Sorted(maps.Keys(got))
	want := []string{"animator", "claude-code-bridge", "fake-provider", "fake-tools", "good", "web-search"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if r := got["web-search"].M.Tools[0].Risk; r == nil || r.SendsOffMachine == nil || !*r.SendsOffMachine {
		t.Fatal("web-search's search sends off the machine")
	}
}
