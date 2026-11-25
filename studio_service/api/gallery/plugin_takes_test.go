package gallery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/common"
)

// Plugins: takes (API 1.70): a plugin caller reads only its owner's takes and makes a new one from them (source tool,
// its parents, its tool), checked as an upload; another user's take is 404 every way, and a user's own token (no
// Plugin) never reaches these.
func TestPluginTakes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	src := f.record(0, "a fox")
	image := encoded(t, "png")
	_ = f.store.Put(ctx, "u/"+userA+"/runs/"+f.runID+"/0.png", bytes.NewReader(image), int64(len(image)), "image/png")
	plugA := common.Caller{ID: userA, Plugin: "media-tools"}
	plugB := common.Caller{ID: userB, Plugin: "media-tools"}

	got, err := f.service.GetPluginTake(ctx, plugA, src)
	if err != nil || got.ID != src || got.Kind != "image" || !strings.Contains(got.URL, "/files/u/"+userA+"/") {
		t.Fatal(got, err)
	}
	for _, c := range []common.Caller{plugB, callerA} { // another owner's pass, a user token: 404
		if _, err := f.service.GetPluginTake(ctx, c, src); status(err) != 404 {
			t.Fatal(c, err)
		}
	}
	body := func(parents ...string) *dto.PluginTakeCreate {
		b := &dto.PluginTakeCreate{Parents: parents, Tool: "edit_image", Input: json.RawMessage(`{"take_id": "x", "rotate": 90}`),
			Title: "a fox · rotated 90°", Kind: "image", ContentType: "image/png", Bytes: int64(len(image)), Width: 3, Height: 2}
		if d := b.Validate(); d != "" {
			t.Fatal(d)
		}
		return b
	}
	if _, err := f.service.CreatePluginTake(ctx, plugB, body(src)); status(err) != 404 {
		t.Fatal("a new take from another user's take", err)
	}
	if _, err := f.service.CreatePluginTake(ctx, callerA, body(src)); status(err) != 404 {
		t.Fatal("a user token made a plugin take", err)
	}
	up, err := f.service.CreatePluginTake(ctx, plugA, body(src))
	if err != nil || up.PosterUploadURL != nil {
		t.Fatal(up, err)
	}
	parsed, _ := url.Parse(up.UploadURL)
	key := strings.TrimPrefix(parsed.Path, "/files/")
	if key != "u/"+userA+"/uploads/"+up.TakeID+".png" {
		t.Fatal(key)
	}
	if _, err := f.service.CompletePluginTake(ctx, plugA, up.TakeID); status(err) != 409 {
		t.Fatal("complete before the PUT", err)
	}
	_ = f.store.Put(ctx, key, bytes.NewReader(image), int64(len(image)), "image/png")
	if _, err := f.service.CompletePluginTake(ctx, plugB, up.TakeID); status(err) != 404 {
		t.Fatal("another user's complete", err)
	}
	take, err := f.service.CompletePluginTake(ctx, plugA, up.TakeID)
	var tool struct {
		Plugin, Tool string
		Input        map[string]any
	}
	_ = json.Unmarshal(take.Tool, &tool)
	if err != nil || take.Source != "tool" || take.MadeWith != nil || len(take.Parents) != 1 || take.Parents[0] != src ||
		*take.Width != 3 || tool.Plugin != "media-tools" || tool.Tool != "edit_image" || tool.Input["rotate"] != 90.0 {
		t.Fatal(take, string(take.Tool), err)
	}
	if again, err := f.service.CompletePluginTake(ctx, plugA, up.TakeID); err != nil || again.ID != take.ID {
		t.Fatal("a repeated complete", err)
	}
	if shown, err := f.service.Get(ctx, callerA, take.ID); err != nil || string(shown.Tool) == "" {
		t.Fatal("the gallery shows the new take with its tool", err)
	}

	// a file that is not what it says is discarded
	bad, _ := f.service.CreatePluginTake(ctx, plugA, body(src))
	_ = f.store.Put(ctx, "u/"+userA+"/uploads/"+bad.TakeID+".png", strings.NewReader(strings.Repeat("x", len(image))), int64(len(image)), "image/png")
	if _, err := f.service.CompletePluginTake(ctx, plugA, bad.TakeID); status(err) != 422 {
		t.Fatal(err)
	}
}

func TestPluginTakeBodies(t *testing.T) {
	ok := func() dto.PluginTakeCreate {
		return dto.PluginTakeCreate{Parents: []string{userA}, Tool: "edit_audio", Input: json.RawMessage(`{}`), Title: "t",
			Kind: "audio", ContentType: "audio/mp4", Bytes: 10, DurationS: 2}
	}
	if b := ok(); b.Validate() != "" {
		t.Fatal(b.Validate())
	}
	for want, edit := range map[string]func(*dto.PluginTakeCreate){
		"kind and content_type differ":    func(b *dto.PluginTakeCreate) { b.ContentType = "image/png" },
		"an audio take needs duration_s":  func(b *dto.PluginTakeCreate) { b.DurationS = 0 },
		"parents: 1 to 8 take ids":        func(b *dto.PluginTakeCreate) { b.Parents = nil },
		"parents: each a take id, once":   func(b *dto.PluginTakeCreate) { b.Parents = []string{userA, userA} },
		"input is larger than 4096 bytes": func(b *dto.PluginTakeCreate) { b.Input = json.RawMessage(`{"a":"` + strings.Repeat("x", 5000) + `"}`) },
		"a video needs video":             func(b *dto.PluginTakeCreate) { b.Kind, b.ContentType, b.Width, b.Height = "video", "video/mp4", 4, 4 },
		"file is larger than 50 MB":       func(b *dto.PluginTakeCreate) { b.Bytes = dto.MaxUploadBytes + 1 },
		"an image or a video needs width and height": func(b *dto.PluginTakeCreate) {
			b.Kind, b.ContentType, b.DurationS = "image", "image/png", 0
		},
	} {
		b := ok()
		edit(&b)
		if got := b.Validate(); got != want {
			t.Errorf("%s: %q", want, got)
		}
	}
}
