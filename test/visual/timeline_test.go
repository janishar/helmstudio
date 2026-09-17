package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/chrome"
	"github.com/janishar/helmstudio/internal/timeline"
)

// What helm-timeline does, read from the page (04 §5; M8 Q7–Q13).
//
// The goldens hold what the editor looks like. These hold what it sends and
// what it does with the answer: that a write is the shape the daemon expects,
// that a refusal is shown in the daemon's own words rather than papered over,
// that undo walks revisions, that a drag lands where the daemon will put it,
// and that the preview plays each clip from where the sequence says.
//
// The sequence behind the page is laid out by internal/timeline itself (see
// timeline_fixture_test.go), so every assertion about a document is an
// assertion about what the daemon's rules produce.

func timelinePage(t *testing.T, ctx context.Context, base, query string) *chrome.Page {
	t.Helper()
	return open(t, ctx, base+"/fixtures/timeline.html?theme=dark&"+query, 1280, 1100)
}

// eval runs an expression and decodes its value, failing the test on error.
func eval(t *testing.T, ctx context.Context, p *chrome.Page, expr string, v any) {
	t.Helper()
	if err := p.Eval(ctx, expr, v); err != nil {
		t.Fatalf("evaluating: %v\n%s", err, expr)
	}
}

// waitJS is a small async helper every expression below can use.
const waitJS = `const wait = async (ok) => { for (let i = 0; i < 200; i++) { if (ok()) return true; await new Promise(r => setTimeout(r, 25)); } return false; };
const tl = document.getElementById("tl");
const key = (el, k, extra) => el.dispatchEvent(new KeyboardEvent("keydown", Object.assign({ key: k, bubbles: true, composed: true }, extra || {})));
const clip = (k) => tl.shadowRoot.querySelector('[data-key="' + k + '"]');`

// The editor snaps a drag before the daemon answers, so its arithmetic has to
// be the daemon's: the exact ratios for NTSC rates, and Go's rounding, which
// takes a negative half away from zero where JavaScript's Math.round does not.
func TestTheEditorsArithmeticIsTheDaemons(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	type probe struct {
		FPS     float64 `json:"fps"`
		Seconds float64 `json:"seconds"`
	}
	var cases []probe
	for _, fps := range []float64{23.976, 24, 25, 29.97, 30, 48, 50, 59.94, 60} {
		for _, s := range []float64{0, 0.02, 5.04, 1001.0 / 24000 * 7.5, 14.16, 120.96 / 24, -2.5 / 24, -0.5 / fps, 3599.999} {
			cases = append(cases, probe{fps, s})
		}
	}
	in, _ := json.Marshal(cases)
	var got []struct {
		Frames int64   `json:"frames"`
		Even   int64   `json:"even"`
		Snap   float64 `json:"snap"`
	}
	eval(t, ctx, p, `(async () => {
		const seq = await import("/sdk/v1/ui/sequence.js");
		return `+string(in)+`.map(c => ({ frames: seq.frames(c.seconds, c.fps), even: seq.evenFrames(c.seconds, c.fps), snap: seq.snap(c.seconds, c.fps) }));
	})()`, &got)
	for i, c := range cases {
		r, ok := timeline.RateOf(c.FPS)
		if !ok {
			t.Fatalf("%v is not a rate", c.FPS)
		}
		if want := r.Frames(c.Seconds); got[i].Frames != want {
			t.Errorf("frames(%v s at %v fps) = %d, daemon says %d", c.Seconds, c.FPS, got[i].Frames, want)
		}
		if want := r.EvenFrames(c.Seconds); got[i].Even != want {
			t.Errorf("evenFrames(%v s at %v fps) = %d, daemon says %d", c.Seconds, c.FPS, got[i].Even, want)
		}
		if want := r.Seconds(r.Frames(c.Seconds)); math.Abs(got[i].Snap-want) > 1e-12 {
			t.Errorf("snap(%v s at %v fps) = %v, daemon says %v", c.Seconds, c.FPS, got[i].Snap, want)
		}
	}

	// And laying V1 out puts every clip where Normalize did.
	fixture := newSequenceFixture(t)
	doc := fixture.current()
	docJSON, _ := json.Marshal(doc)
	var laid []float64
	eval(t, ctx, p, `(async () => {
		const seq = await import("/sdk/v1/ui/sequence.js");
		const doc = `+string(docJSON)+`;
		const scrambled = doc.tracks.map(t => ({ ...t, clips: t.clips.map(c => ({ ...c, at: 999 })) }));
		return seq.lay(scrambled, doc.target.fps)[0].clips.map(c => c.at);
	})()`, &laid)
	for i, c := range doc.Tracks[0].Clips {
		if math.Abs(laid[i]-*c.At) > 1e-12 {
			t.Errorf("V1 clip %d laid at %v, Normalize put it at %v", i+1, laid[i], *c.At)
		}
	}
	var duration float64
	eval(t, ctx, p, `(async () => (await import("/sdk/v1/ui/sequence.js")).duration(`+string(docJSON)+`))()`, &duration)
	if want := timeline.Duration(doc.Target, doc.Tracks); math.Abs(duration-want) > 1e-12 {
		t.Errorf("duration = %v, daemon says %v", duration, want)
	}
}

// A write is the whole of tracks, as a merge patch, with the ETag it read.
// V1's clips go without positions — the daemon lays a contiguous track, and a
// position sent with a trim would be refused as a gap — and nothing the daemon
// sets is sent back to it.
func TestTheEditorWritesWhatTheDaemonExpects(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Revision int64    `json:"revision"`
		IfMatch  string   `json:"ifMatch"`
		V1At     []any    `json:"v1At"`
		A1At     []any    `json:"a1At"`
		Keys     []string `json:"keys"`
		Out      float64  `json:"out"`
		Notice   string   `json:"notice"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		const etag = tl.doc.etag;
		clip("0:0").focus();
		key(clip("0:0"), "}", { shiftKey: true });
		await wait(() => tl.doc.revision === 2);
		const u = window.calls.find(c => c[0] === "update");
		const keys = new Set();
		for (const tr of u[2].tracks) { Object.keys(tr).forEach(k => keys.add("track." + k)); for (const c of tr.clips) Object.keys(c).forEach(k => keys.add("clip." + k)); }
		return { revision: tl.doc.revision, ifMatch: u[3] === etag ? "matched" : u[3],
			v1At: u[2].tracks[0].clips.map(c => c.at ?? null), a1At: u[2].tracks[1].clips.map(c => c.at ?? null),
			keys: [...keys].sort(), out: tl.doc.tracks[0].clips[0].out, notice: tl.notice.textContent };
	})()`, &got)

	if got.Revision != 2 {
		t.Fatalf("the write did not land: revision %d, notice %q", got.Revision, got.Notice)
	}
	if got.IfMatch != "matched" {
		t.Errorf("If-Match was %q, not the ETag the editor read", got.IfMatch)
	}
	for i, at := range got.V1At {
		if at != nil {
			t.Errorf("V1 clip %d was sent with at=%v; the video track is laid by the daemon", i+1, at)
		}
	}
	if len(got.A1At) != 1 || got.A1At[0] != float64(1) {
		t.Errorf("A1's clip was sent with at=%v; a sound clip keeps its position", got.A1At)
	}
	for _, k := range got.Keys {
		if k == "clip.studio_id" || k == "track.name" {
			t.Errorf("the write sent %s, which the daemon sets", k)
		}
	}
	// One frame longer, snapped as the daemon snaps: 121 frames becomes 122.
	r, _ := timeline.RateOf(24)
	if want := r.Seconds(122); math.Abs(got.Out-want) > 1e-12 {
		t.Errorf("out is %v after lengthening by a frame; want %v", got.Out, want)
	}
}

// A refusal is shown in the daemon's words, and the document goes back to what
// the daemon last said. Shortening V1 by a frame leaves the ambience on A2
// running past the end, which the rules refuse (M8 Q8).
func TestARefusalIsShownInTheDaemonsWords(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Revision int64   `json:"revision"`
		Out      float64 `json:"out"`
		Drawn    string  `json:"drawn"`
		Notice   string  `json:"notice"`
		Tone     string  `json:"tone"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		clip("0:0").focus();
		key(clip("0:0"), "]");
		await wait(() => tl.notice.dataset.tone === "error");
		return { revision: tl.doc.revision, out: tl.doc.tracks[0].clips[0].out,
			drawn: clip("0:0").querySelector(".what").textContent, notice: tl.notice.textContent, tone: tl.notice.dataset.tone };
	})()`, &got)
	if got.Tone != "error" || !strings.Contains(got.Notice, "past the sequence's") {
		t.Fatalf("notice %q (%s); want the daemon's refusal", got.Notice, got.Tone)
	}
	if strings.Contains(got.Notice, "invalid_timeline") || strings.Contains(got.Notice, "422") {
		t.Errorf("the notice shows the wire: %q", got.Notice)
	}
	r, _ := timeline.RateOf(24)
	if got.Revision != 1 || math.Abs(got.Out-r.Seconds(121)) > 1e-12 || got.Drawn != "5.04s" {
		t.Errorf("after a refusal: revision %d, out %v, drawn %q; want the document the daemon last sent", got.Revision, got.Out, got.Drawn)
	}
}

// A conflict means the sequence changed somewhere else. The editor reads it
// again and says so; it does not replay the edit onto a document nobody here
// has seen.
func TestAConflictReadsTheSequenceAgain(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Notice  string `json:"notice"`
		Gets    int    `json:"gets"`
		Updates int    `json:"updates"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		const before = window.calls.filter(c => c[0] === "get").length;
		window.script.conflictOnce = true;
		clip("0:0").focus();
		key(clip("0:0"), "}", { shiftKey: true });
		await wait(() => tl.notice.dataset.tone === "error");
		await wait(() => window.calls.filter(c => c[0] === "get").length > before);
		return { notice: tl.notice.textContent, gets: window.calls.filter(c => c[0] === "get").length - before, updates: window.calls.filter(c => c[0] === "update").length };
	})()`, &got)
	if !strings.Contains(got.Notice, "changed somewhere else") {
		t.Errorf("notice %q", got.Notice)
	}
	if got.Gets != 1 {
		t.Errorf("the sequence was read %d times after the conflict; want once", got.Gets)
	}
	if got.Updates != 1 {
		t.Errorf("%d writes were sent; a conflicting edit is not replayed", got.Updates)
	}
}

// Undo writes the previous revision back, with the ETag; Redo walks forward
// through what was undone, and a new edit ends that (M8 Q10).
func TestUndoAndRedoWalkRevisions(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Reverts   [][]any `json:"reverts"`
		Outs      []float64
		UndoFirst bool `json:"undoFirst"`
		RedoAfter bool `json:"redoAfter"`
		RedoEnds  bool `json:"redoEnds"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		const undoFirst = tl.undoBtn.disabled;
		const outs = [];
		clip("0:0").focus();
		key(clip("0:0"), "}", { shiftKey: true });
		await wait(() => tl.doc.revision === 2);
		outs.push(tl.doc.tracks[0].clips[0].out);
		key(tl, "z", { metaKey: true });
		await wait(() => tl.doc.revision === 3);
		outs.push(tl.doc.tracks[0].clips[0].out);
		const redoAfter = !tl.redoBtn.disabled;
		key(tl, "z", { metaKey: true, shiftKey: true });
		await wait(() => tl.doc.revision === 4);
		outs.push(tl.doc.tracks[0].clips[0].out);
		key(tl, "z", { metaKey: true });
		await wait(() => tl.doc.revision === 5);
		clip("0:0").focus();
		key(clip("0:0"), "}", { shiftKey: true });
		await wait(() => tl.doc.revision === 6);
		return { reverts: window.calls.filter(c => c[0] === "revert").map(c => [c[2].revision, c[3]]),
			outs, undoFirst, redoAfter, redoEnds: tl.redoBtn.disabled };
	})()`, &got)
	if !got.UndoFirst {
		t.Error("Undo was offered on the first revision, which has nothing before it")
	}
	want := [][]any{{float64(1), `"2"`}, {float64(2), `"3"`}, {float64(1), `"4"`}}
	if fmt.Sprint(got.Reverts) != fmt.Sprint(want) {
		t.Errorf("reverts sent: %v; want %v (revision, If-Match)", got.Reverts, want)
	}
	r, _ := timeline.RateOf(24)
	if len(got.Outs) != 3 || got.Outs[0] != r.Seconds(122) || got.Outs[1] != r.Seconds(121) || got.Outs[2] != r.Seconds(122) {
		t.Errorf("out after edit, undo, redo: %v", got.Outs)
	}
	if !got.RedoAfter {
		t.Error("Redo was not offered after an undo")
	}
	if !got.RedoEnds {
		t.Error("Redo was still offered after a new edit")
	}
}

// Dragging a clip along V1 reorders it, and the daemon lays the track again.
func TestDraggingReordersTheVideoTrack(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Sent     []string `json:"sent"`
		Laid     []string `json:"laid"`
		Selected int      `json:"selected"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		const still = clip("0:2");
		const r = still.getBoundingClientRect();
		const y = r.top + r.height / 2;
		const at = (x) => ({ bubbles: true, composed: true, clientX: x, clientY: y, pointerId: 3, button: 0, isPrimary: true });
		still.dispatchEvent(new PointerEvent("pointerdown", at(r.left + r.width / 2)));
		for (const dx of [-10, -200, -900]) tl.grid.dispatchEvent(new PointerEvent("pointermove", at(r.left + r.width / 2 + dx)));
		tl.grid.dispatchEvent(new PointerEvent("pointerup", at(r.left + r.width / 2 - 900)));
		await wait(() => tl.doc.revision === 2);
		const u = window.calls.find(c => c[0] === "update");
		return { sent: u[2].tracks[0].clips.map(c => c.asset_id), laid: tl.doc.tracks[0].clips.map(c => c.asset_id + "@" + c.at.toFixed(4)), selected: tl.selected.index };
	})()`, &got)
	wantSent := []string{assetKlein, assetTake1, assetLTX, assetTake2}
	if strings.Join(got.Sent, ",") != strings.Join(wantSent, ",") {
		t.Fatalf("sent order %v; want the still first", got.Sent)
	}
	r, _ := timeline.RateOf(24)
	wantLaid := []string{
		assetKlein + "@0.0000",
		assetTake1 + "@" + fmt.Sprintf("%.4f", r.Seconds(53)),
		assetLTX + "@" + fmt.Sprintf("%.4f", r.Seconds(53+121)),
		assetTake2 + "@" + fmt.Sprintf("%.4f", r.Seconds(53+121+97)),
	}
	if strings.Join(got.Laid, ",") != strings.Join(wantLaid, ",") {
		t.Errorf("laid %v; want %v", got.Laid, wantLaid)
	}
	if got.Selected != 0 {
		t.Errorf("the selection is on clip %d; it should follow the clip that moved", got.Selected+1)
	}
}

// Clips carry the identity hue of the studio that made them — here, only the
// page's own studio, because the studio API serves no other studio's hue. A
// clip whose studio the caller cannot learn is neutral and says so in words
// (03 §11, §17; M8 Q11).
func TestClipsSayWhoMadeThem(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got []struct {
		Who   string `json:"who"`
		Hue   string `json:"hue"`
		Label string `json:"label"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		return [...tl.shadowRoot.querySelectorAll(".clip")].map(c => ({ who: c.querySelector(".who").textContent, hue: c.style.getPropertyValue("--_hue").trim(), label: c.getAttribute("aria-label") }));
	})()`, &got)
	want := []struct{ who, hue string }{
		{"h3-studio", "var(--helm-status-idle)"},
		{"sequencer-studio", "var(--helm-studio-accent)"},
		{"sequencer-studio", "var(--helm-studio-accent)"},
		{"another studio", "var(--helm-status-idle)"},
		{"sequencer-studio", "var(--helm-studio-accent)"},
		{"another studio", "var(--helm-status-idle)"},
	}
	if len(got) != len(want) {
		t.Fatalf("drew %d clips, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Who != w.who || got[i].Hue != w.hue {
			t.Errorf("clip %d: %q in %q; want %q in %q", i+1, got[i].Who, got[i].Hue, w.who, w.hue)
		}
		if !strings.Contains(got[i].Label, "from "+w.who) {
			t.Errorf("clip %d's accessible name %q does not say who made it", i+1, got[i].Label)
		}
	}
}

// The chip is the daemon's answer: "video stream copy", or the first reason a
// copy cannot be taken; and without ffmpeg, export is unavailable rather than
// a button that fails (03 §11, M8 Q5, Q13).
func TestThePlanChipIsTheDaemonsAnswer(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	doc := newSequenceFixture(t).current()
	_, reasons := timeline.Plan(doc.Target, doc.Tracks, probes)
	if len(reasons) == 0 {
		t.Fatal("the fixture sequence would copy; it should conform")
	}

	read := `(async () => { ` + waitJS + ` await wait(() => !tl.chip.hidden); return { chip: tl.chip.textContent, mode: tl.chip.dataset.mode, exportDisabled: tl.exportBtn.disabled }; })()`
	for _, c := range []struct {
		query, chip, mode string
		disabled          bool
	}{
		{"pose=0&plan=real", "conform · " + reasons[0].Message, "conform", false},
		{"pose=0&plan=copy", "video stream copy", "copy", false},
		{"pose=0&plan=unsupported", "export unavailable", "unavailable", true},
	} {
		p := timelinePage(t, ctx, srv.URL, c.query)
		var got struct {
			Chip           string `json:"chip"`
			Mode           string `json:"mode"`
			ExportDisabled bool   `json:"exportDisabled"`
		}
		eval(t, ctx, p, read, &got)
		if got.Chip != c.chip || got.Mode != c.mode || got.ExportDisabled != c.disabled {
			t.Errorf("%s: chip %q (%s), export disabled %v; want %q (%s), %v", c.query, got.Chip, got.Mode, got.ExportDisabled, c.chip, c.mode, c.disabled)
		}
	}
}

// An export shows its progress in frames and can be cancelled, and a cancelled
// one says nothing was kept (M8 Q16, Q17).
func TestAnExportShowsProgressAndCancels(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0&export=running")

	var got struct {
		Progress string   `json:"progress"`
		Cancels  []string `json:"cancels"`
		Notice   string   `json:"notice"`
		Event    string   `json:"event"`
		Enabled  bool     `json:"enabled"`
		Live     string   `json:"live"`
		Kept     bool     `json:"kept"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		await wait(() => !tl.exportsRow.hidden);
		const progress = tl.exportsRow.textContent;
		// Someone reaching for Cancel keeps it: polls that change nothing leave
		// the button where it is, focused.
		const button = [...tl.exportsRow.querySelectorAll("button")].find(b => b.textContent === "Cancel");
		button.focus();
		await new Promise(r => setTimeout(r, 1600));
		const kept = tl.shadowRoot.activeElement === button && button.isConnected;
		let event = "";
		tl.addEventListener("export-stopped", (e) => { event = e.detail.job.state; });
		[...tl.exportsRow.querySelectorAll("button")].find(b => b.textContent === "Cancel").click();
		await wait(() => event !== "");
		return { progress, kept, cancels: window.calls.filter(c => c[0] === "cancelExport").map(c => c[2]), notice: tl.exportsRow.textContent, live: tl.exportsRow.getAttribute("aria-live"), event, enabled: !tl.exportBtn.disabled };
	})()`, &got)
	if !strings.Contains(got.Progress, "142 of 340 frames") {
		t.Errorf("progress row %q", got.Progress)
	}
	if !got.Kept {
		t.Error("polling an export that had not moved took keyboard focus off Cancel")
	}
	if len(got.Cancels) != 1 || got.Cancels[0] != "01JBTM0000000000000000JB1A" {
		t.Errorf("cancel calls %v", got.Cancels)
	}
	if got.Event != "cancelled" || !strings.Contains(got.Notice, "Nothing was kept") {
		t.Errorf("after cancelling: event %q, export row %q", got.Event, got.Notice)
	}
	if got.Live != "polite" {
		t.Errorf("the export row is not a live region (aria-live=%q), so how an export ended is never announced", got.Live)
	}
	if !got.Enabled {
		t.Error("Export was not offered again after the export ended")
	}
}

// Without `editable`, nothing edits: no trim handles, keys do nothing, and the
// inspector's fields are read-only. Export is not an edit, and stays.
func TestAReadOnlyEditorChangesNothing(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0&editable=0")

	var got struct {
		Edges    int  `json:"edges"`
		Updates  int  `json:"updates"`
		Disabled bool `json:"disabled"`
		Export   bool `json:"export"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		// The sequence gains a revision elsewhere, so there is something an undo
		// could reach — on revision 1 there is nothing, and a read-only undo that
		// wrote would go unnoticed.
		const doc = await (await fetch("/timeline-fixture/doc")).json();
		await fetch("/timeline-fixture/update", { method: "POST", body: JSON.stringify({ tracks: doc.tracks, if_match: doc.etag }) });
		await tl.reload();
		await wait(() => tl.doc && tl.doc.revision === 2);
		clip("0:0").focus();
		key(clip("0:0"), "]");
		key(clip("0:0"), "Delete");
		// Every way a write could start: the undo keys, the page's own append,
		// and the editor's write path called directly. A read-only editor is
		// read-only because none of them writes, not because its buttons hide.
		key(tl, "z", { metaKey: true });
		key(tl, "z", { metaKey: true, shiftKey: true });
		await tl.append("`+assetVoice+`", "A1");
		await tl.edit((tracks) => tracks, "should not happen");
		tl.select(0, 1);
		await new Promise(r => setTimeout(r, 200));
		const inputs = [...tl.inspector.querySelectorAll("input")];
		return { edges: tl.shadowRoot.querySelectorAll(".edge").length, updates: window.calls.filter(c => ["update", "revert", "append"].includes(c[0])).length,
			disabled: inputs.length > 0 && inputs.every(i => i.disabled), export: !tl.exportBtn.hidden };
	})()`, &got)
	if got.Edges != 0 || got.Updates != 0 {
		t.Errorf("read-only: %d trim handles, %d writes", got.Edges, got.Updates)
	}
	if !got.Disabled {
		t.Error("the inspector's fields are editable in a read-only editor")
	}
	if !got.Export {
		t.Error("Export is hidden in a read-only editor")
	}
}

// The preview parks each clip where the sequence says: one picture on a plain
// frame, two crossfading inside a dissolve with the incoming clip reading its
// handle before `in`, and a still for its hold with the next clip waiting on
// its first frame.
func TestThePreviewPlacesEachClipWhereTheSequenceSays(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0&preview=1")

	type layer struct {
		Tag     string  `json:"tag"`
		Asset   string  `json:"asset"`
		Hidden  bool    `json:"hidden"`
		Opacity float64 `json:"opacity"`
		Time    float64 `json:"time"`
	}
	var got map[string][]layer
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		const at = async (t) => {
			tl.seek(t);
			await new Promise(r => setTimeout(r, 80));
			return [...tl.stage.querySelectorAll("video, img")].map(m => ({ tag: m.tagName, asset: m.dataset.clip.split(":")[2], hidden: m.hidden,
				opacity: Number(m.style.opacity || 0), time: m.tagName === "IMG" ? 0 : m.currentTime }));
		};
		return { plain: await at(2), ahead: await at(4), dissolve: await at(5), still: await at(10) };
	})()`, &got)

	r, _ := timeline.RateOf(24)
	cut := r.Seconds(121)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

	visible := func(ls []layer) []layer {
		var out []layer
		for _, l := range ls {
			if !l.Hidden {
				out = append(out, l)
			}
		}
		return out
	}
	if v := visible(got["plain"]); len(v) != 1 || v[0].Asset != assetTake1 || !near(v[0].Time, 2) || v[0].Opacity != 1 {
		t.Errorf("at 2s: %+v; want the first take alone, at its own 2s", got["plain"])
	}
	// A second before the cut, the ltx clip waits on its own first frame —
	// its in point, 0.5s into its file, not the file's start.
	waiting := false
	for _, l := range got["ahead"] {
		if l.Asset == assetLTX && l.Hidden && near(l.Time, 0.5) {
			waiting = true
		}
	}
	if !waiting {
		t.Errorf("at 4s the ltx clip is not parked on its in point: %+v", got["ahead"])
	}
	v := visible(got["dissolve"])
	if len(v) != 2 {
		t.Fatalf("at 5s, inside the dissolve: %+v; want two pictures", got["dissolve"])
	}
	mix := (5 - (cut - 0.25)) / 0.5
	if v[0].Asset != assetTake1 || !near(v[0].Time, 5) || !near(v[0].Opacity, 1-mix) {
		t.Errorf("outgoing at 5s: %+v; want the take at 5s, opacity %.4f", v[0], 1-mix)
	}
	if v[1].Asset != assetLTX || !near(v[1].Time, 0.5+(5-cut)) || !near(v[1].Opacity, mix) {
		t.Errorf("incoming at 5s: %+v; want ltx at %.4f (its handle), opacity %.4f", v[1], 0.5+(5-cut), mix)
	}
	still := got["still"]
	if s := visible(still); len(s) != 1 || s[0].Tag != "IMG" || s[0].Asset != assetKlein {
		t.Errorf("at 10s: %+v; want the still alone", still)
	}
	parked := false
	for _, l := range still {
		if l.Asset == assetTake2 && l.Hidden && near(l.Time, 0) {
			parked = true
		}
	}
	if !parked {
		t.Errorf("at 10s the next take is not parked on its first frame: %+v", still)
	}
}

// frameAt is the whole of the preview's decision, so it is checked on every
// frame of the fixture sequence rather than at three.
func TestFrameAtOnEveryFrame(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	doc := newSequenceFixture(t).current()
	docJSON, _ := json.Marshal(doc)
	var faults []string
	eval(t, ctx, p, `(async () => {
		const seq = await import("/sdk/v1/ui/sequence.js");
		const doc = `+string(docJSON)+`;
		const out = [];
		const frames = seq.frames(seq.duration(doc), 24);
		for (let f = 0; f < frames; f++) {
			const t = seq.seconds(f, 24) + 1e-9;
			const x = seq.frameAt(doc, t);
			const sum = x.picture.reduce((n, p) => n + p.opacity, 0);
			if (x.picture.length < 1 || x.picture.length > 2) out.push("frame " + f + ": " + x.picture.length + " pictures");
			if (Math.abs(sum - 1) > 1e-9) out.push("frame " + f + ": opacities sum to " + sum);
			for (const s of x.sound) {
				if (s.gain < 0 || s.gain > 4) out.push("frame " + f + ": gain " + s.gain);
			}
			const voice = x.sound.find(s => s.clip.asset_id === "`+assetVoice+`");
			if (voice && Math.abs(voice.gain - seq.gain(-3)) > 1e-9) out.push("frame " + f + ": the voice's gain is " + voice.gain);
			const room = x.sound.find(s => s.clip.asset_id === "`+assetRoom+`");
			if (!room) out.push("frame " + f + ": the ambience is missing");
			else if (Math.abs(room.gain - seq.gain(-12)) > 1e-9) out.push("frame " + f + ": the ambience's gain is " + room.gain + ", not its track's");
			const still = x.sound.find(s => s.clip.asset_id === "`+assetKlein+`");
			if (still) out.push("frame " + f + ": a still has sound");
		}
		// A muted clip plays no sound of its own.
		const muted = structuredClone(doc);
		muted.tracks[0].clips[0].audio = false;
		if (seq.frameAt(muted, 1).sound.some(s => s.clip.asset_id === "`+assetTake1+`")) out.push("a muted clip still has sound");
		return out.slice(0, 20);
	})()`, &faults)
	for _, f := range faults {
		t.Error(f)
	}
}

// A studio's page decides how wide the editor is. The lanes are sized from the
// component's width, and in a grid parent an item is as wide as its content —
// so without containment each redraw widened the component, which widened the
// lanes, until a real studio page scrolled sideways for 52,000 pixels.
func TestTheEditorIsAsWideAsThePageSays(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, layout := range []string{"block", "grid"} {
		p := timelinePage(t, ctx, srv.URL, "pose=0&layout="+layout)
		var got struct {
			Page    float64 `json:"page"`
			Client  float64 `json:"client"`
			Host    float64 `json:"host"`
			Scrolls bool    `json:"scrolls"`
		}
		eval(t, ctx, p, `(async () => {
			`+waitJS+`
			tl.setZoom(3);
			for (let i = 0; i < 10; i++) { tl.drawTracks(); await new Promise(r => requestAnimationFrame(r)); }
			await new Promise(r => setTimeout(r, 300));
			return { page: document.documentElement.scrollWidth, client: document.documentElement.clientWidth, host: tl.getBoundingClientRect().width,
				scrolls: getComputedStyle(tl.scroller).overflowX === "auto" && tl.scroller.scrollWidth > tl.scroller.clientWidth };
		})()`, &got)
		// Zoomed in, the lanes are wider than the page and scroll inside it.
		if !got.Scrolls {
			t.Errorf("%s layout: zoomed in, the lanes do not scroll", layout)
		}
		if got.Page > got.Client+1 || got.Host > got.Client {
			t.Errorf("%s layout: the page is %v px wide in a %v px window, and the editor %v px", layout, got.Page, got.Client, got.Host)
		}
	}
}

// A page gives the editor a height of its own — a dialog, a pane — and the
// panel is a flex column, so the picture was the first thing squeezed out of
// it. A portrait sequence suffered first: 448×768 asks for 823px of stage at
// 480 wide, and what was left after the tracks was a few pixels of it, which
// read as "there is no video in the timeline".
func TestThePreviewKeepsItsRoomInAShortPage(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := timelinePage(t, ctx, srv.URL, "pose=0")

	var got struct {
		Stage  float64 `json:"stage"`
		Wrap   float64 `json:"wrap"`
		Tracks float64 `json:"tracks"`
		Shrink string  `json:"shrink"`
		Cap    string  `json:"cap"`
	}
	eval(t, ctx, p, `(async () => {
		`+waitJS+`
		await wait(() => tl.shadowRoot.querySelector(".stage"));
		// Once it has drawn its own document, so its own inline aspect ratio
		// is in place and this replaces it rather than racing it.
		await wait(() => tl.shadowRoot.querySelector(".facts").textContent.includes("fps"));
		// As a dialog does: a column with a height, the editor taking what is left.
		const box = document.createElement("div");
		box.style.cssText = "height: 560px; display: flex; flex-direction: column";
		tl.parentNode.insertBefore(box, tl);
		box.append(tl);
		tl.style.cssText = "flex: 1; min-height: 0";
		await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
		const stageEl = tl.shadowRoot.querySelector(".stage");
		const stage = stageEl.getBoundingClientRect();
		const wrap = tl.shadowRoot.querySelector(".stage-wrap");
		const body = tl.shadowRoot.querySelector(".body").getBoundingClientRect();
		// What holds for a sequence of any shape: the picture cannot be
		// squeezed out, and cannot take the page from the tracks either.
		return {
			stage: stage.height, wrap: wrap.getBoundingClientRect().height, tracks: body.height,
			shrink: getComputedStyle(wrap).flexShrink, cap: getComputedStyle(stageEl).maxHeight,
		};
	})()`, &got)

	if got.Stage < 120 {
		t.Errorf("the stage is %.0fpx tall in a 560px page; the picture has been squeezed out", got.Stage)
	}
	if got.Shrink != "0" {
		t.Errorf("the stage may shrink (flex-shrink: %s), so a page with a height of its own squeezes it away", got.Shrink)
	}
	if got.Cap != "320px" {
		t.Errorf("the stage has no cap (max-height: %s), so a tall sequence takes the page from the tracks", got.Cap)
	}
	if got.Tracks < 80 {
		t.Errorf("the tracks are %.0fpx tall; the picture has taken the page", got.Tracks)
	}
}
