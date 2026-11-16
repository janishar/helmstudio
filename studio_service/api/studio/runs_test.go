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

// The projector reads the publishing host and the session from the subject evt.run.<host>.<session_id>.<kind>.
func TestRouteOf(t *testing.T) {
	if host, session := routeOf("evt.run.gcp.0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10.status"); host != "gcp" || session != "0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10" {
		t.Fatal(host, session)
	}
	for _, subject := range []string{"evt.run.status", "evt.run.0b5c1f8e.status", "evt.run.a.b.c.d"} {
		if host, session := routeOf(subject); host != "" || session != "" {
			t.Fatal("malformed subjects name no host or session:", subject)
		}
	}
}
