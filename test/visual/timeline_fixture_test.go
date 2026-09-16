package visual

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/timeline"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// The sequence helm-timeline's fixture edits (03 §11's café sequence).
//
// The document is not written by hand. It is laid out and snapped by
// internal/timeline.Normalize, every edit the page sends goes through the same
// function, and the plan chip is internal/timeline.Plan over probes. The
// component is therefore only ever drawn against documents the daemon's own
// rules produce — a hand-written fixture would be a second copy of those rules,
// and the first thing to go wrong would be a golden of an editor that draws a
// document the daemon would never send.
//
// What this is not is the API: ownership, ETags on the wire and revisions in
// the store are M8a's, tested by its conformance suite. This serves only the
// document's arithmetic, over plain JSON.

// The fixture's assets. Crockford base32, so each is shaped like a real id.
const (
	assetTake1 = "01JBTM0000000000000000TK1A"
	assetLTX   = "01JBTM0000000000000000XD4B"
	assetKlein = "01JBTM0000000000000000KN1C"
	assetTake2 = "01JBTM0000000000000000TK2D"
	assetVoice = "01JBTM0000000000000000VC0E"
	assetRoom  = "01JBTM0000000000000000RM0F"
)

// fixtureStudio is the studio the page belongs to: a studio holding timeline
// and gallery.read_all, as M8 Q20 says a cross-studio sequence is edited in.
const fixtureStudio = "sequencer-studio"

// sources are what the asset store would have recorded. The second take's
// studio is empty: the caller cannot learn it, so its clip is "another studio".
var sources = map[string]timeline.Source{
	assetTake1: {Kind: timeline.KindVideo, Duration: 5.54, StudioID: "h3-studio"},
	assetLTX:   {Kind: timeline.KindVideo, Duration: 4.56, StudioID: fixtureStudio},
	assetKlein: {Kind: timeline.KindImage, StudioID: fixtureStudio},
	assetTake2: {Kind: timeline.KindVideo, Duration: 2.86},
	assetVoice: {Kind: timeline.KindAudio, Duration: 6.5, StudioID: fixtureStudio},
	assetRoom:  {Kind: timeline.KindAudio, Duration: 20},
}

// probes are what ffprobe would have found, for the plan.
var probes = timeline.Probes{
	assetTake1: h264(1920, 1080),
	assetLTX:   h264(704, 448),
	assetKlein: {Container: "png_pipe", Codec: "png", Width: 1920, Height: 1080},
	assetTake2: h264(800, 448),
}

func h264(w, h int64) timeline.Stream {
	return timeline.Stream{
		Container: "mov,mp4,m4a,3gp,3g2,mj2", Codec: "h264", Profile: "High", Level: 40,
		Width: w, Height: h, PixelFormat: "yuv420p", FieldOrder: "progressive",
		TimeBase: "1/12288", FrameRate: timeline.Rate{Num: 24, Den: 1}, ParameterSets: fmt.Sprintf("sps-%dx%d", w, h),
		HasAudio: true, AudioCodec: "aac", SampleRate: 48000, Channels: 2,
	}
}

func f64(v float64) *float64 { return &v }

// initialTracks is the café sequence as a studio would send it: V1 laid end to
// end, a dissolve into the ltx clip, a still, and two sound tracks.
func initialTracks() []helm.TimelineTrack {
	return []helm.TimelineTrack{
		{Kind: "video", Clips: []helm.TimelineClip{
			{AssetID: assetTake1, Out: f64(5.04)},
			{AssetID: assetLTX, In: f64(0.5), Out: f64(4.56), TransitionIn: &helm.TimelineTransition{Type: "dissolve", Duration: 0.5}},
			{AssetID: assetKlein, Hold: f64(2.2)},
			{AssetID: assetTake2},
		}},
		{Kind: "audio", Clips: []helm.TimelineClip{
			{AssetID: assetVoice, At: f64(1), GainDb: f64(-3)},
		}},
		{Kind: "audio", GainDb: f64(-12), Clips: []helm.TimelineClip{
			{AssetID: assetRoom, Out: f64(14.16)},
		}},
	}
}

// sequenceFixture holds one page's sequence and every revision of it.
type sequenceFixture struct {
	mu      sync.Mutex
	target  helm.TimelineTarget
	history []helm.Timeline // index 0 is revision 1
}

func newSequenceFixture(t testing.TB) *sequenceFixture {
	target := helm.TimelineTarget{Width: 1920, Height: 1080, FPS: 24, SampleRate: 48000}
	f := &sequenceFixture{target: target}
	if _, err := f.write(initialTracks()); err != nil {
		t.Fatalf("the fixture sequence does not satisfy the daemon's rules: %v", err)
	}
	return f
}

func (f *sequenceFixture) current() helm.Timeline { return f.history[len(f.history)-1] }

// write normalizes tracks exactly as a PATCH would, and keeps the result.
func (f *sequenceFixture) write(tracks []helm.TimelineTrack) (helm.Timeline, error) {
	out, err := timeline.Normalize(f.target, tracks, sources)
	if err != nil {
		return helm.Timeline{}, err
	}
	revision := int64(len(f.history) + 1)
	owner := fixtureStudio
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC).Add(time.Duration(revision) * time.Minute)
	doc := helm.Timeline{
		ID: "01JBTM00000000000000SEQ01A", Name: "café sequence", Target: f.target, Tracks: out,
		Revision: revision, DurationS: timeline.Duration(f.target, out), ETag: strconv.Quote(strconv.FormatInt(revision, 10)),
		StudioID: &owner, CreatedAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC), UpdatedAt: at,
	}
	f.history = append(f.history, doc)
	return doc, nil
}

// mountSequenceFixture serves the fixture under /timeline-fixture/. A page asks
// for a fresh sequence with POST /timeline-fixture/reset, so one test's edits
// never reach another's page.
func mountSequenceFixture(t testing.TB, mux *http.ServeMux) {
	var mu sync.Mutex
	current := newSequenceFixture(t)
	get := func() *sequenceFixture {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	refuse := func(w http.ResponseWriter, status int, code, msg string) {
		reply(w, status, map[string]string{"error": code, "message": msg})
	}

	mux.HandleFunc("POST /timeline-fixture/reset", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		current = newSequenceFixture(t)
		mu.Unlock()
		reply(w, http.StatusOK, current.current())
	})
	mux.HandleFunc("GET /timeline-fixture/doc", func(w http.ResponseWriter, r *http.Request) {
		f := get()
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, http.StatusOK, f.current())
	})
	mux.HandleFunc("POST /timeline-fixture/update", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tracks  []helm.TimelineTrack `json:"tracks"`
			IfMatch string               `json:"if_match"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		f := get()
		f.mu.Lock()
		defer f.mu.Unlock()
		if body.IfMatch != f.current().ETag {
			refuse(w, http.StatusConflict, "etag_mismatch", "the sequence changed since it was read")
			return
		}
		doc, err := f.write(body.Tracks)
		var invalid *timeline.Invalid
		if errors.As(err, &invalid) {
			refuse(w, http.StatusUnprocessableEntity, "invalid_timeline", invalid.Error())
			return
		}
		if err != nil {
			refuse(w, http.StatusUnprocessableEntity, "invalid_timeline", err.Error())
			return
		}
		reply(w, http.StatusOK, doc)
	})
	mux.HandleFunc("POST /timeline-fixture/revert", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Revision int64  `json:"revision"`
			IfMatch  string `json:"if_match"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		f := get()
		f.mu.Lock()
		defer f.mu.Unlock()
		if body.IfMatch != f.current().ETag {
			refuse(w, http.StatusConflict, "etag_mismatch", "the sequence changed since it was read")
			return
		}
		if body.Revision < 1 || body.Revision > int64(len(f.history)) {
			refuse(w, http.StatusNotFound, "not_found", "no such revision")
			return
		}
		doc, err := f.write(f.history[body.Revision-1].Tracks)
		if err != nil {
			refuse(w, http.StatusUnprocessableEntity, "invalid_timeline", err.Error())
			return
		}
		reply(w, http.StatusOK, doc)
	})
	mux.HandleFunc("GET /timeline-fixture/plan", func(w http.ResponseWriter, r *http.Request) {
		f := get()
		f.mu.Lock()
		defer f.mu.Unlock()
		doc := f.current()
		mode, reasons := timeline.Plan(doc.Target, doc.Tracks, probes)
		if reasons == nil {
			reasons = []helm.ExportReason{}
		}
		reply(w, http.StatusOK, map[string]any{
			"mode": mode, "preset": "h264", "target": doc.Target, "duration_s": doc.DurationS,
			"frames": timeline.Frames(doc.Target, doc.Tracks), "reasons": reasons,
		})
	})
	// A media address that never answers, for preview tests: an element given
	// it stays unloaded, parked where it was told, and never errors.
	mux.HandleFunc("GET /timeline-fixture/media/{asset}", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
}
