package micro

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubjectMatches(t *testing.T) {
	for _, test := range []struct {
		pattern, subject string
		want             bool
	}{
		{"evt.job.*.status", "evt.job.j1.status", true},
		{"evt.job.*.status", "evt.job.j1.result", false},
		{"evt.job.*.status", "evt.job.status", false},
		{"evt.job.*.status", "evt.job.j1.status.x", false},
		{"evt.job.>", "evt.job.j1.status", true},
		{"evt.job.>", "evt.job", false},
		{"cmd.job.cancel", "cmd.job.cancel", true},
		{">", "anything.at.all", true},
	} {
		if got := subjectMatches(test.pattern, test.subject); got != test.want {
			t.Errorf("%s ~ %s = %v", test.pattern, test.subject, got)
		}
	}
}

// Two message types with their own payloads, as a service's message package defines them.
type statusMessage struct {
	Data struct {
		Status string `json:"status"`
	} `json:"data"`
}

type resultMessage struct {
	Data struct {
		Kind string `json:"kind"`
	} `json:"data"`
}

// testProjector holds a consumer's handlers written as a service writes them: decode with JsonToMsg, do the work,
// finish the message with a helper.
type testProjector struct {
	mu   sync.Mutex
	seen []string
	fail error // returned by the result handler's work
	done chan struct{}
	want int
}

func (p *testProjector) record(entry string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, entry)
	if len(p.seen) == p.want && p.done != nil {
		close(p.done)
	}
}

func (p *testProjector) MountNatsJetStream(group *NatsJetStreamGroup) {
	group.AddHandler("evt.job.*.status", p.statusHandler)
	group.AddHandler("evt.job.*.result", p.resultHandler)
	group.AddHandler("evt.job.*.forgotten", func(context.Context, *NatsJetStreamMessage) {}) // finishes nothing
}

func (p *testProjector) statusHandler(ctx context.Context, msg *NatsJetStreamMessage) {
	status, err := JsonToMsg[statusMessage](msg.Data())
	if err != nil {
		TermNatsJetStreamMessage(msg, err)
		return
	}
	p.record("status " + status.Data.Status)
	AckNatsJetStreamMessage(msg)
	AckNatsJetStreamMessage(msg) // a second finish sends nothing
}

func (p *testProjector) resultHandler(ctx context.Context, msg *NatsJetStreamMessage) {
	result, err := JsonToMsg[resultMessage](msg.Data())
	if err != nil {
		TermNatsJetStreamMessage(msg, err)
		return
	}
	p.record("result " + result.Data.Kind)
	if p.fail != nil {
		NakNatsJetStreamMessage(ctx, msg, p.fail)
		return
	}
	AckNatsJetStreamMessage(msg)
}

func TestHandleRunsTheMatchingHandler(t *testing.T) {
	p := &testProjector{fail: errors.New("store down")}
	group := &NatsJetStreamGroup{}
	p.MountNatsJetStream(group)
	handle := func(subject, body string) error { return group.Handle(context.Background(), subject, []byte(body)) }
	if err := handle("evt.job.j1.status", `{"data": {"status": "running"}}`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		subject, body string
		dropped       bool   // terminated, not nak'd
		text          string // in the error
	}{
		{"evt.job.j1.result", `{"data": {"kind": "brand"}}`, false, "store down"},
		{"evt.job.j1.weird", `{}`, true, "no handler"},
		{"evt.job.j1.status", `{"data": {"status": 7}}`, true, "bad request: invalid JSON"},
		{"evt.job.j1.forgotten", `{}`, false, "did not finish"},
	} {
		err := handle(test.subject, test.body)
		if err == nil || errors.Is(err, ErrNatsJetStreamMessageTerminated) != test.dropped ||
			!strings.Contains(err.Error(), test.text) {
			t.Errorf("%s %s: %v, want dropped %v with %q", test.subject, test.body, err, test.dropped, test.text)
		}
	}
	if want := []string{"status running", "result brand"}; !slices.Equal(p.seen, want) {
		t.Errorf("seen %v, want %v", p.seen, want)
	}
}

// One consumer with a handler per subject: messages still come one at a time in stream order, across types.
func TestNatsJetStreamConsumeRoutesInStreamOrder(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "EVT", "evt.>")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &testProjector{done: make(chan struct{}), want: 3}
	group := &NatsJetStreamGroup{}
	p.MountNatsJetStream(group)
	_ = client.Conn().Publish("evt.job.j1.status", []byte(`{"data": {"status": "running"}}`))
	_ = client.Conn().Publish("evt.job.j1.weird", []byte(`{}`)) // no handler: dropped
	_ = client.Conn().Publish("evt.job.j1.result", []byte(`{"data": {"kind": "brand"}}`))
	_ = client.Conn().Publish("evt.job.j1.status", []byte(`{"data": {"status": "succeeded"}}`))
	go NatsJetStreamConsume(ctx, client, projector, group)
	wait(t, p.done, 5*time.Second)
	eventually(t, 5*time.Second, func() bool { return settled(client, "EVT", "api-projector") })
	p.mu.Lock()
	defer p.mu.Unlock()
	if want := []string{"status running", "result brand", "status succeeded"}; !slices.Equal(p.seen, want) {
		t.Errorf("got %v, want %v", p.seen, want)
	}
}

// A nak or a drop always reports an error from Handle, even when the handler gave none, so it never reads as an ack.
func TestHandleReportsANakOrDropWithoutAReason(t *testing.T) {
	group := &NatsJetStreamGroup{}
	group.AddHandler("a", func(ctx context.Context, msg *NatsJetStreamMessage) {
		NakNatsJetStreamMessage(ctx, msg, nil)
	})
	group.AddHandler("b", func(_ context.Context, msg *NatsJetStreamMessage) {
		TermNatsJetStreamMessage(msg, nil)
	})
	for _, subject := range []string{"a", "b"} {
		if err := group.Handle(context.Background(), subject, nil); err == nil {
			t.Errorf("%s: nil error", subject)
		}
	}
}

// A drop's log line says which field broke, not just "bad request: invalid JSON".
func TestTermLogsTheDecodeCause(t *testing.T) {
	logged := captureLog(t)
	group := &NatsJetStreamGroup{}
	group.AddHandler(">", func(_ context.Context, msg *NatsJetStreamMessage) {
		if _, err := JsonToMsg[statusMessage](msg.Data()); err != nil {
			TermNatsJetStreamMessage(msg, err)
			return
		}
		AckNatsJetStreamMessage(msg)
	})
	_ = group.Handle(context.Background(), "evt.job.j1.status", []byte(`{"data": {"status": 7}}`))
	if line := logged.String(); !strings.Contains(line, "bad request: invalid JSON (json: cannot unmarshal number") {
		t.Errorf("log = %q", line)
	}
}
