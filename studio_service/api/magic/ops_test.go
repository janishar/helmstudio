package magic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type replayFixture struct {
	Documents map[string]json.RawMessage `json:"documents"`
	Cases     []struct {
		Description string          `json:"description"`
		Document    string          `json:"document"`
		Ops         []Op            `json:"ops"`
		Expect      json.RawMessage `json:"expect"`
	} `json:"cases"`
}

// replay is each case's ops applied in order, all or nothing: the document after the last, or {error, op_index}.
func replay(t *testing.T) (replayFixture, []any) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "magic", "edits.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx replayFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	var out []any
	for _, c := range fx.Cases {
		doc, err := Parse(fx.Documents[c.Document])
		if err != nil {
			t.Fatalf("%s: %v", c.Description, err)
		}
		var got any = nil
		for i, op := range c.Ops {
			r := Apply(doc, op)
			if r.Error != "" {
				got = map[string]any{"error": r.Error, "op_index": i}
				break
			}
			doc = r.Doc
		}
		if got == nil {
			if why := Check(doc); why != "" {
				t.Errorf("%s: the result breaks a rule: %s", c.Description, why)
			}
			got = doc
		}
		out = append(out, got)
	}
	return fx, out
}

// TestMagicReplay checks every edits.json case against its expect and, with MAGIC_REPLAY_OUT set, writes the results
// for contracts/check.py's replay.
func TestMagicReplay(t *testing.T) {
	fx, out := replay(t)
	for i, c := range fx.Cases {
		got, _ := json.Marshal(out[i])
		if len(c.Expect) > 0 && !sameJSON(got, c.Expect) {
			t.Errorf("%s:\n got %s\nwant %s", c.Description, got, c.Expect)
		}
	}
	if path := os.Getenv("MAGIC_REPLAY_OUT"); path != "" {
		data, _ := json.Marshal(out)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return string(p) == string(q)
}
