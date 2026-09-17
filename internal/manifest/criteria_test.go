package manifest

import (
	"strings"
	"testing"
)

// A criterion points at the field it is about, so the editor can take someone
// from "Declares its licence: fail" to the licence field. A failing one points
// at the first thing missing, a passing one at where it is declared, and one
// no manifest can answer points nowhere rather than somewhere arbitrary.
func TestACriterionPointsAtTheFieldItIsAbout(t *testing.T) {
	load := func(t *testing.T, text string) *Manifest {
		t.Helper()
		m, res, err := LoadBytes("criteria.yaml", []byte(text))
		if err != nil || !res.OK() {
			t.Fatalf("loading: %v %v", err, res.Errors)
		}
		return m
	}
	pointers := func(r CriteriaResult) map[int]string {
		out := map[int]string{}
		for _, c := range r.Items {
			out[c.Number] = c.Pointer
		}
		return out
	}

	bare := pointers(Criteria(load(t, goodManifest)))
	for n, want := range map[int]string{
		1:  "/id",
		2:  "/requires/tools", // the first of the four it does not declare
		4:  "/processes/0/health",
		5:  "/processes",
		8:  "/license",
		13: "/test/profile",
		15: "/sdk",
	} {
		if bare[n] != want {
			t.Errorf("criterion %d points at %q, want %q", n, bare[n], want)
		}
	}
	for _, n := range []int{3, 6, 7, 9, 10, 11, 12, 14} {
		if bare[n] != "" {
			t.Errorf("criterion %d, which no manifest answers, points at %q", n, bare[n])
		}
	}

	// Declaring everything moves criterion 2 back to where it is declared, and
	// a fixed port sends criterion 5 to the field that fixes it.
	full := strings.Replace(goodManifest, "  arch: [arm64]\n", "  arch: [arm64]\n  tools: [git]\n  ram_gb: 32\n  disk_gb: 40\n", 1)
	full = strings.Replace(full, "port: { prefer: 8790 }", "port: { fixed: 8790 }", 1)
	full += "peak_ram_gb: 20\n"
	got := pointers(Criteria(load(t, full)))
	if got[2] != "/requires" {
		t.Errorf("a passing criterion 2 points at %q, want /requires", got[2])
	}
	if got[5] != "/processes/0/port/fixed" {
		t.Errorf("a fixed port points criterion 5 at %q, want /processes/0/port/fixed", got[5])
	}

	// In a registry entry the fields are under /manifest, as its errors are.
	under := pointers(Criteria(load(t, goodManifest)).UnderManifest())
	if under[8] != "/manifest/license" || under[3] != "" {
		t.Errorf("under an entry, criterion 8 points at %q and 3 at %q", under[8], under[3])
	}
}
