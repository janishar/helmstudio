package auth

import (
	"context"
	"testing"
	"time"

	"github.com/helmstudio/orago/micro"
)

// fakeNats answers auth.validate.token without NATS: it embeds micro.NatsClient for what it doesn't use and overrides
// Request, counting the calls.
type fakeNats struct {
	micro.NatsClient
	t     *testing.T
	reply string
	calls int
}

// Request counts the call, checks it asks auth.validate.token and answers with the canned reply.
func (f *fakeNats) Request(_ context.Context, subject string, _ []byte) ([]byte, error) {
	f.calls++
	if subject != "auth.validate.token" {
		f.t.Fatal(subject)
	}
	return []byte(f.reply), nil
}

func TestServiceCachesAndRevokes(t *testing.T) {
	exp := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	nats := &fakeNats{t: t, reply: `{"user_id":"u1","email":"a@x","roles":["user"],"family_id":"f1","expires_at":"` + exp + `"}`}
	v := NewService(nats)
	for range 3 {
		c, err := v.Validate(context.Background(), "tok")
		if err != nil || c.ID != "u1" {
			t.Fatal(c, err)
		}
	}
	if nats.calls != 1 {
		t.Fatalf("cached: %d calls", nats.calls)
	}
	v.Revoked([]byte(`{"data":{"scope":"family","user_id":"u1","family_id":"other"}}`))
	_, _ = v.Validate(context.Background(), "tok")
	if nats.calls != 1 {
		t.Fatal("other family dropped the entry")
	}
	v.Revoked([]byte(`{"data":{"scope":"family","user_id":"u1","family_id":"f1"}}`))
	_, _ = v.Validate(context.Background(), "tok")
	v.Revoked([]byte(`{"data":{"scope":"user","user_id":"u1"}}`))
	_, _ = v.Validate(context.Background(), "tok")
	if nats.calls != 3 {
		t.Fatalf("revocations: %d calls", nats.calls)
	}
}
