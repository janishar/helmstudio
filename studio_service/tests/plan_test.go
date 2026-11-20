// Integration tests of POST /v1/studio/runs/plan (API 1.25): the planned peak of a run before it is sent, and the
// Studio block's help and preset_order.
package tests

import (
	"fmt"
	"testing"
	"time"
)

// The ltx template at its defaults plans 45.87 GB on mac (ltx.models.valid's distilled int8 item); a larger canvas
// than planned_at has no plan; no live mlx host says why; a bad input is the run's 422; nothing is stored.
func TestPlanRun(t *testing.T) {
	e := setup(t)
	plan := func(inputs map[string]any, want int) map[string]any {
		return e.call(userA, "POST", "/v1/studio/runs/plan", map[string]any{"workflow_id": "ltx-text-to-video", "inputs": inputs}, want)
	}
	e.twoEnvs()
	if got := plan(nil, 200); got["host"] != "mac" || got["peak_gb"] != 45.87 || got["reason"] != nil {
		t.Fatalf("plan at the defaults: %v", got)
	}
	if got := plan(map[string]any{"canvas.width": 1280, "canvas.height": 704}, 200); got["host"] != "mac" || got["peak_gb"] != nil {
		t.Fatalf("a canvas past planned_at: %v", got)
	}
	if got := plan(map[string]any{"canvas.width": "wide"}, 422); got["detail"] == nil {
		t.Fatalf("a bad input: %v", got)
	}
	e.call(userA, "POST", "/v1/studio/runs/plan", map[string]any{}, 422) // workflow_id is required
	var runs int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs stored: %d %v", runs, err)
	}
	e.mac("mlx", nil, time.Now().Add(-2*time.Minute)) // stale: not live
	if got := plan(nil, 200); got["host"] != nil || got["peak_gb"] != nil || got["reason"] != "no live host for env mlx" {
		t.Fatalf("no live host: %v", got)
	}
}

// A section's help and the presets' order are kept as sent; an order that is not every preset once, or an empty
// help, is 422.
func TestStudioHelpAndPresetOrder(t *testing.T) {
	e := setup(t)
	doc := func(order []any, help string) map[string]any {
		d := echoDoc("Mine", 4)
		d["studio"] = map[string]any{"composer": []any{"prompt.text"},
			"inspector":    []any{map[string]any{"title": "Run", "items": []any{"echo.steps"}, "help": help}},
			"presets":      map[string]any{"Slow": map[string]any{"echo.steps": 20}, "Fast": map[string]any{"echo.steps": 2}},
			"preset_order": order}
		return d
	}
	mine := e.call(userA, "POST", "/v1/studio/workflows", doc([]any{"Slow", "Fast"}, "How long it runs."), 201)
	studio := mine["studio"].(map[string]any)
	if fmt.Sprint(studio["preset_order"]) != "[Slow Fast]" || studio["inspector"].([]any)[0].(map[string]any)["help"] != "How long it runs." {
		t.Fatal(studio)
	}
	got := e.call(userA, "GET", "/v1/studio/workflows/"+mine["id"].(string), nil, 200)["studio"].(map[string]any)
	if fmt.Sprint(got["preset_order"]) != "[Slow Fast]" {
		t.Fatal(got)
	}
	for _, order := range [][]any{{"Slow"}, {"Slow", "Slow"}, {"Slow", "Fast", "Draft"}} {
		if out := e.call(userA, "POST", "/v1/studio/workflows", doc(order, "x"), 422); out["detail"] != "studio.preset_order: every preset once" {
			t.Fatal(order, out)
		}
	}
	if out := e.call(userA, "POST", "/v1/studio/workflows", doc([]any{"Fast", "Slow"}, ""), 422); out["detail"] != "studio.inspector[0].help: 1-300 characters" {
		t.Fatal(out)
	}
}
