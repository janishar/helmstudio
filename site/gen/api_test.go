package gen

import (
	"strings"
	"testing"
)

// A client call the reference names has to exist in that client. The Go
// generator wraps long doc comments, and the wrap can split the marker at its
// one space, so the matcher has to read a comment as a whole.
func TestCallsForFindsAWrappedMarker(t *testing.T) {
	c := &clientFiles{
		goSrc: strings.Join([]string{
			"type GalleryAPI interface {",
			"	// Update: Star, tag or rename one of the caller's own items. (PATCH",
			"	// /gallery/items/{id})",
			"	Update(ctx context.Context, id string, body map[string]any) (*Item, error)",
			"	// Delete: Soft-delete one of the caller's own items. (DELETE /gallery/items/{id})",
			"	Delete(ctx context.Context, id string) error",
			"}",
		}, "\n"),
		pySrc: strings.Join([]string{
			"    def update(self, id: str, body: Dict[str, Any]) -> Any:",
			`        """Star, tag or rename one of the caller's own items. (PATCH /gallery/items/{id})"""`,
		}, "\n"),
		jsSrc: strings.Join([]string{
			"  /** Star, tag or rename one of the caller's own items. (PATCH /gallery/items/{id}) */",
			"  update(id, body) {",
		}, "\n"),
	}
	calls, err := c.callsFor("PATCH", "/gallery/items/{id}")
	if err != nil {
		t.Fatalf("callsFor: %v", err)
	}
	want := []apiCall{
		{Language: "Go", Signature: "Update(ctx context.Context, id string, body map[string]any) (*Item, error)"},
		{Language: "Python", Signature: "def update(self, id: str, body: Dict[str, Any]) -> Any"},
		{Language: "JavaScript", Signature: "update(id, body)"},
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}

	// The same marker must not match the method after it.
	if calls, err := c.callsFor("DELETE", "/gallery/items/{id}"); err == nil {
		t.Errorf("DELETE matched %+v, though only the Go client has it", calls)
	} else if !strings.Contains(err.Error(), "Python client has no method") {
		t.Errorf("DELETE: %v, want the Python client named", err)
	}
}

func TestCallsForRefusesAnOperationAClientLacks(t *testing.T) {
	c := &clientFiles{
		goSrc: "\t// Get: One item. (GET /gallery/items/{id})\n\tGet(ctx context.Context, id string) (*Item, error)",
		pySrc: "    def get(self, id: str) -> Any:\n        \"\"\"One item. (GET /gallery/items/{id})\"\"\"",
		jsSrc: "  /** One item. */\n  get(id) {",
	}
	_, err := c.callsFor("GET", "/gallery/items/{id}")
	if err == nil || !strings.Contains(err.Error(), "JavaScript client has no method") {
		t.Fatalf("err = %v, want the JavaScript client named", err)
	}
}
