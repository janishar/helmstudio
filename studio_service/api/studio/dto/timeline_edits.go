package dto

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/timeline"
)

// Timeline edits by ops (contracts/openapi.yaml TimelineEdits, TimelineOp, TimelineEditResult, TimelineRestore,
// TimelineRevision*, TimelineAgentView, TimelineFrame; API 1.49).

// MaxOps is how many ops one /edits call may carry.
const MaxOps = 200

// opFields is each op's fields (TimelineOp): every one it takes, then the required ones (op aside).
var opFields = map[string][2][]string{
	"move":         {{"clip", "index", "at", "track"}, {"clip"}},
	"trim":         {{"clip", "edge", "delta"}, {"clip", "edge", "delta"}},
	"split":        {{"track", "at", "id", "ids"}, {"track", "at"}},
	"remove":       {{"clip", "ripple"}, {"clip"}},
	"lift":         {{"clip"}, {"clip"}},
	"replace":      {{"clip", "take_id"}, {"clip", "take_id"}},
	"set":          {{"clip", "gain_db", "mute", "hold", "transition_in", "link"}, {"clip"}},
	"add":          {{"take_ids", "track", "ids"}, {"take_ids"}},
	"insert":       {{"at", "hold", "id"}, {"at", "hold"}},
	"place":        {{"at", "hold", "id", "gap_id"}, {"at", "hold"}},
	"add_track":    {},
	"remove_track": {{"track"}, {"track"}},
	"track":        {{"track", "gain_db", "mute"}, {"track"}},
	// 1.50: the Timeline tools' ops and the markers'
	"roll":          {{"clip", "delta"}, {"clip", "delta"}},
	"slip":          {{"clip", "delta"}, {"clip", "delta"}},
	"slide":         {{"clip", "delta"}, {"clip", "delta"}},
	"overwrite":     {{"clip", "at", "track", "ids"}, {"clip", "at"}},
	"split_all":     {{"at", "ids"}, {"at"}},
	"remove_range":  {{"from", "to", "ids"}, {"from", "to"}},
	"lift_range":    {{"from", "to", "ids"}, {"from", "to"}},
	"marker":        {{"id", "at", "name", "color"}, {"at"}},
	"remove_marker": {{"id"}, {"id"}},
}

// IDsOps take ids for the clips they make in `ids` (not one per take, as add's).
var IDsOps = []string{"split", "overwrite", "split_all", "remove_range", "lift_range"}

// MarkerColors are a marker's colours (TimelineMarkerColor).
var MarkerColors = []string{"red", "orange", "yellow", "green", "blue", "purple"}

// TimelineEdits is POST /timelines/{id}/edits.
type TimelineEdits struct {
	BaseRevision int           `json:"base_revision"`
	Ops          []timeline.Op `json:"ops"`
	DryRun       bool          `json:"dry_run"`
}

// Validate checks the shapes of every op (TimelineOp); whether it applies is the rules'.
func (b *TimelineEdits) Validate() string {
	switch {
	case b.BaseRevision < 1:
		return "base_revision is required"
	case len(b.Ops) == 0:
		return "ops must have 1 to 200 items"
	case len(b.Ops) > MaxOps:
		return fmt.Sprintf("too_many_ops: at most %d ops per call", MaxOps)
	}
	for i, op := range b.Ops {
		if detail := checkOp(op); detail != "" {
			return fmt.Sprintf("ops[%d]: %s", i, detail)
		}
	}
	return ""
}

// checkOp is one op's shape problem, or "".
func checkOp(op timeline.Op) string {
	fields, ok := opFields[op.Op]
	switch {
	case op.Op == "fill":
		return "fill is studio's"
	case !ok:
		return "unknown op " + fmt.Sprintf("%q", op.Op)
	}
	for key := range op.Set {
		if key != "op" && !slices.Contains(fields[0], key) {
			return fmt.Sprintf("%s does not take %s", op.Op, key)
		}
	}
	for _, key := range fields[1] {
		if raw, ok := op.Set[key]; !ok || string(raw) == "null" {
			return key + " is required"
		}
	}
	seconds := func(v *float64) bool { return v == nil || (*v >= 0 && *v <= 86400) }
	hold := func(v float64) bool { return v > 0 && v <= 3600 }
	id := func(key, v string) string {
		if _, given := op.Set[key]; given && !ClipID.MatchString(v) {
			return key + " must be 1-16 lowercase letters or digits"
		}
		return ""
	}
	switch {
	case !seconds(op.At) || !seconds(op.From) || !seconds(op.To):
		return "at, from and to must be seconds from 0 to 86400"
	case op.Index != nil && *op.Index < 0:
		return "index must be 0 or more"
	case math.Abs(op.Delta) > 86400 || math.IsNaN(op.Delta):
		return "delta must be seconds from -86400 to 86400"
	case op.Op == "trim" && op.Edge != "in" && op.Edge != "out":
		return "edge must be in or out"
	case op.Track != "" && !TrackName.MatchString(op.Track):
		return "track must be V1 or A2-A9"
	case slices.Contains(fields[1], "hold") && !hold(op.Hold):
		return "hold must be more than 0 and at most 3600"
	case op.TakeID != "" && !utility.IsUUID(op.TakeID):
		return "take " + op.TakeID + " not found"
	case len(op.TakeIDs) > 100 || (op.Op == "add" && len(op.TakeIDs) == 0):
		return "take_ids must have 1 to 100 items"
	case op.Op == "add" && len(op.IDs) > len(op.TakeIDs):
		return "ids: at most one per take"
	case len(op.IDs) > 32:
		return "ids: at most 32 items"
	case op.Name != nil && (*op.Name == "" || utf8.RuneCountInString(*op.Name) > 80):
		return "name must be 1-80 characters"
	case op.Color != nil && !slices.Contains(MarkerColors, *op.Color):
		return "color must be one of " + strings.Join(MarkerColors, ", ")
	}
	for _, key := range []string{"name", "color", "ripple"} { // a null is not a value here
		if raw, ok := op.Set[key]; ok && string(raw) == "null" {
			return key + " has the wrong type"
		}
	}
	for _, d := range []string{id("clip", op.Clip), id("id", op.ID), id("gap_id", op.GapID)} {
		if d != "" {
			return d
		}
	}
	for i, t := range op.TakeIDs {
		if !utility.IsUUID(t) {
			return fmt.Sprintf("take_ids[%d]: take %s not found", i, t)
		}
	}
	for _, v := range op.IDs {
		if !ClipID.MatchString(v) {
			return "ids must be 1-16 lowercase letters or digits"
		}
	}
	if op.Op == "set" || op.Op == "track" {
		var c timeline.Clip
		nullable := op.Op == "set"
		for key, raw := range op.Set {
			if key == "op" || key == "clip" || key == "track" || (nullable && string(raw) == "null") {
				continue
			}
			var err error
			switch key {
			case "gain_db":
				err = json.Unmarshal(raw, &c.GainDB)
			case "mute":
				err = json.Unmarshal(raw, &c.Mute)
			case "hold":
				err = json.Unmarshal(raw, &c.Hold)
			case "transition_in":
				err = json.Unmarshal(raw, &c.TransitionIn)
			case "link":
				if err = json.Unmarshal(raw, &c.Link); err == nil && !ClipID.MatchString(*c.Link) {
					return "link must be 1-16 lowercase letters or digits"
				}
			}
			if err != nil || string(raw) == "null" {
				return key + " has the wrong type"
			}
		}
		if c.Hold != nil && !hold(*c.Hold) {
			return "hold must be more than 0 and at most 3600"
		}
		if detail := checkClip(timeline.Clip{ID: "x", GainDB: c.GainDB, TransitionIn: c.TransitionIn}); detail != "" {
			return detail[1:]
		}
	}
	return ""
}

// TimelineRestore is POST /timelines/{id}/restore.
type TimelineRestore struct {
	Revision int `json:"revision"`
	To       int `json:"to"`
}

// Validate wants both revisions.
func (b *TimelineRestore) Validate() string {
	if b.Revision < 1 || b.To < 1 {
		return "revision and to are required"
	}
	return ""
}

// TimelineFrameRequest is POST /timelines/{id}/frame.
type TimelineFrameRequest struct {
	At *float64 `json:"at"`
}

// Validate wants sequence seconds.
func (b *TimelineFrameRequest) Validate() string {
	if b.At == nil || *b.At < 0 || *b.At > 86400 {
		return "at must be seconds from 0 to 86400"
	}
	return ""
}

// ------------------------------------------------------------------ answers

// TimelineEditResult is /edits' 200.
type TimelineEditResult struct {
	Revision int           `json:"revision"`
	DryRun   bool          `json:"dry_run"`
	Timeline Timeline      `json:"timeline"`
	Diff     timeline.Diff `json:"diff"`
	Warnings []string      `json:"warnings"`
}

// TimelineRevisionItem is one kept revision.
type TimelineRevisionItem struct {
	Revision   int       `json:"revision"`
	By         string    `json:"by"`
	Via        *string   `json:"via"`
	SavedAt    time.Time `json:"saved_at"`
	OpsSummary *string   `json:"ops_summary"`
	Clips      int       `json:"clips"`
	DurationS  float64   `json:"duration_s"`
}

// TimelineRevisionList is GET …/revisions.
type TimelineRevisionList struct {
	Revisions []TimelineRevisionItem `json:"revisions"`
}

// TimelineRevisionDoc is GET …/revisions/{n}.
type TimelineRevisionDoc struct {
	TimelineRevisionItem
	Document json.RawMessage `json:"document"`
}

// TimelineAgentClip is an agent view clip with its take's title.
type TimelineAgentClip struct {
	timeline.AgentClip
	TakeTitle *string `json:"take_title"`
}

// TimelineAgentView is GET /timelines/{id}?view=agent.
type TimelineAgentView struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Revision  int                    `json:"revision"`
	Target    timeline.Target        `json:"target"`
	DurationS float64                `json:"duration_s"`
	Frames    int                    `json:"frames"`
	Tracks    []timeline.AgentTrack  `json:"tracks"`
	Clips     []TimelineAgentClip    `json:"clips"`
	Links     map[string][]string    `json:"links"`   // 1.50
	Markers   []timeline.AgentMarker `json:"markers"` // 1.50
}

// TimelineFrame is POST /timelines/{id}/frame.
type TimelineFrame struct {
	timeline.FrameTime
	URL *string `json:"url"`
}
