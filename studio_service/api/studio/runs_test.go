package studio

import "testing"

// A run goes queued → running → one terminal status (or queued straight to one); nothing follows a terminal status.
func TestCanMove(t *testing.T) {
	allowed := map[[2]string]bool{
		{"queued", "running"}: true, {"queued", "succeeded"}: true, {"queued", "failed"}: true, {"queued", "cancelled"}: true,
		{"running", "succeeded"}: true, {"running", "failed"}: true, {"running", "cancelled"}: true,
	}
	statuses := []string{"queued", "running", "succeeded", "failed", "cancelled", "bogus"}
	for _, from := range statuses {
		for _, to := range statuses {
			if got := canMove(from, to); got != allowed[[2]string{from, to}] {
				t.Errorf("canMove(%s, %s) = %v", from, to, got)
			}
		}
	}
}

// The projector reads the session from the subject evt.run.<session_id>.<kind>.
func TestSessionOf(t *testing.T) {
	if got := sessionOf("evt.run.0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10.status"); got != "0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10" {
		t.Fatal(got)
	}
	if sessionOf("evt.run.status") != "" || sessionOf("evt.run.a.b.c") != "" {
		t.Fatal("malformed subjects name no session")
	}
}
