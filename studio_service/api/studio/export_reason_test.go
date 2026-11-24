package studio

import (
	"testing"

	"github.com/helmstudio/studio_service/api/timeline"
)

// The export plan names a clip by its own shot number, as the lanes do, not by its position (QA T2).
func TestExportReasonUsesShotNumber(t *testing.T) {
	doc := timeline.Doc{Tracks: []timeline.Track{{Kind: "video", Clips: []timeline.Clip{
		{ID: "a", Number: 3}, {ID: "b", Number: 1}, {ID: "c", Number: 4},
	}}}}
	for code, want := range map[string]string{
		"transition": "Shot 4 dissolves in from the shot before",
		"profile":    "Shot 4 differs from Shot 3 in its profile",
	} {
		if got := exportReason(timeline.Reason{Code: code, Clip: "c"}, doc, nil).Message; got != want {
			t.Errorf("%s: %q, want %q", code, got, want)
		}
	}
	// a document stored before API 1.29 has no numbers: its position
	old := timeline.Doc{Tracks: []timeline.Track{{Kind: "video", Clips: []timeline.Clip{{ID: "a"}, {ID: "b"}}}}}
	if got := exportReason(timeline.Reason{Code: "trimmed", Clip: "b"}, old, nil).Message; got != "Shot 2 is trimmed" {
		t.Errorf("old doc: %q", got)
	}
}
