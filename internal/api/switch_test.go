package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Q13 over HTTP: a stale or made-up preempt stops nothing and answers 409
// preview_changed with the arithmetic, the busy state and a fresh digest; the
// old boolean form is no confirmation at all.
func TestAStalePreemptIsPreviewChanged(t *testing.T) {
	srv, _ := newServer(t)
	if rec := do(t, srv, "POST", Base+"/studios/first-heavy:launch", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("launch: %d %s", rec.Code, rec.Body)
	}
	first := waitRunning(t, srv, "first-heavy")

	type details struct {
		Error   string `json:"error"`
		Details struct {
			Heavy struct {
				Busy struct {
					State   string `json:"state"`
					Message string `json:"message"`
				} `json:"busy"`
				Confirm string `json:"confirm"`
			} `json:"heavy"`
		} `json:"details"`
	}
	var conflict details
	rec := do(t, srv, "POST", Base+"/studios/second-heavy:launch", nil)
	json.Unmarshal(rec.Body.Bytes(), &conflict)
	if rec.Code != http.StatusConflict || conflict.Error != "heavy_conflict" || conflict.Details.Heavy.Confirm == "" || conflict.Details.Heavy.Busy.State != "unknown" {
		t.Fatalf("conflict: %d %s; want heavy_conflict with a confirm digest and busy unknown (no probe declared)", rec.Code, rec.Body)
	}
	for _, preempt := range []string{"true", "0123456789abcdef"} {
		rec := do(t, srv, "POST", Base+"/studios/second-heavy:launch?preempt="+preempt, nil)
		var got details
		json.Unmarshal(rec.Body.Bytes(), &got)
		if rec.Code != http.StatusConflict || got.Error != "preview_changed" || got.Details.Heavy.Confirm != conflict.Details.Heavy.Confirm {
			t.Fatalf("preempt=%s: %d %s; want 409 preview_changed with the current digest", preempt, rec.Code, rec.Body)
		}
	}
	if now := waitRunning(t, srv, "first-heavy"); now.GroupRunID != first.GroupRunID {
		t.Fatalf("a stale preempt replaced the running group: %s → %s", first.GroupRunID, now.GroupRunID)
	}
}
