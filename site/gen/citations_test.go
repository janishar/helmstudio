package gen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every shape of citation the contract actually writes. A design reference
// survives, a decision-log reference does not, and prose that was nothing but
// a citation becomes nothing.
func TestDecisionRefsGoAndDesignRefsStay(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"bare Q", "Ids are bare ULIDs (Q29).", "Ids are bare ULIDs."},
		{"milestone Q", "Needs `approval` on the same terms as `:install` (M7 Q10).", "Needs `approval` on the same terms as `:install`."},
		{"design ref kept", "The studio's own directory (06 §6).", "The studio's own directory (06 §6)."},
		{"requirement kept", "Every studio endpoint refuses this (R34).", "Every studio endpoint refuses this (R34)."},
		{"mixed, semicolon", "A sequence (R44–R49, 05 §6, §7; docs/decisions.md M8 Q7–Q19).", "A sequence (R44–R49, 05 §6, §7)."},
		{"mixed, hyphens", "The shape (R3a-R3d, 05 §5a; docs/decisions.md M7 Q17-Q19).", "The shape (R3a-R3d, 05 §5a)."},
		{"amended clause", "The pointer shape (R2, amended M7 Q6).", "The pointer shape (R2)."},
		{"bare run after a ref", "R67, docs/decisions.md M3 Q9 and Q11.", "R67."},
		{"citation only", "docs/decisions.md M3 Q6.", ""},
		{"leading clause", "R34, Q14. The body is the raw bytes.", "R34. The body is the raw bytes."},
		{"leading clause, all decision", "M8 Q9, Q11. The studio that created it.", "The studio that created it."},
		{"leading design ref alone", "06 §6. The studio's own directory.", "06 §6. The studio's own directory."},
		{"a first sentence of prose is left alone", "Q is not a citation here. It stands for a question.", "Q is not a citation here. It stands for a question."},
		{"prose that merely starts with a ref", "05 §9, scored only where a manifest alone answers (Q16).", "05 §9, scored only where a manifest alone answers."},
		{"nothing to do", "The bytes, once, named by sha256.", "The bytes, once, named by sha256."},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := withoutDecisionRefs(c.in); got != c.want {
				t.Errorf("withoutDecisionRefs(%q)\n = %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// The citations that cannot be lifted out mechanically, because they are woven
// into a sentence's grammar rather than sitting beside it as apparatus.
// Removing one leaves broken prose, and api/openapi.yaml is the contract: this
// repository does not edit the contract to make a page read better. Each is
// listed here so that a NEW one fails the gate rather than joining them
// quietly. Raised with the human, 2026-09-18.
var wovenCitations = []string{
	// api/openapi.yaml, GET /assets/{id}/blob.
	"Readable by the rules in Q9",
}

// The whole point, checked against the real contract rather than examples: no
// rendered reference page names a decision-log answer, and the design
// references are still there.
func TestTheRenderedReferenceCitesNoDecisionLog(t *testing.T) {
	_, out := buildSite(t, "/")
	qRef := regexp.MustCompile(`\bQ\d+\b|docs/decisions\.md`)
	designRef := regexp.MustCompile(`\b0\d §`)
	designs := 0
	err := filepath.WalkDir(filepath.Join(out, "docs", "reference"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".html" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		body := string(b)
		for _, woven := range wovenCitations {
			body = strings.ReplaceAll(body, woven, "")
		}
		if m := qRef.FindAllString(body, -1); len(m) > 0 {
			t.Errorf("%s still cites the decision log: %v", rel, m[:min(len(m), 6)])
		}
		designs += len(designRef.FindAllString(string(b), -1))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if designs == 0 {
		t.Error("no design reference survived anywhere in the reference; the strip is too wide")
	}
	t.Logf("%d design references kept", designs)
}

// The contract itself is untouched: the citations live there.
func TestTheContractKeepsItsCitations(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "docs/decisions.md"); n == 0 {
		t.Error("api/openapi.yaml no longer names the decision log; the contract was edited to make the page pass")
	}
}
