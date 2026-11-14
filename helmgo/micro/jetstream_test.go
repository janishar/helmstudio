package micro

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
)

// runServer runs an in-process NATS server with JetStream on port (-1 picks one) with its own empty store.
func runServer(t *testing.T, port int) *server.Server {
	t.Helper()
	natsServer, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go natsServer.Start()
	if !natsServer.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(natsServer.Shutdown)
	return natsServer
}

// startServer runs an in-process NATS server with JetStream and returns a client connected to it.
func startServer(t *testing.T) NatsClient {
	t.Helper()
	natsServer := runServer(t, -1)
	client, err := NewNatsClient(Config{URL: natsServer.ClientURL(), Name: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Conn().Close)
	return client
}

// addStream creates a stream as .tools/nats-setup does (2 minute duplicate window).
func addStream(t *testing.T, client NatsClient, name, subjects string) {
	t.Helper()
	_, err := client.jetStream().CreateStream(context.Background(), jetstream.StreamConfig{
		Name: name, Subjects: []string{subjects}, Duplicates: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// eventually fails the test unless condition turns true within the time given.
func eventually(t *testing.T, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logBuffer collects the log's output for a test; Write and String are safe across goroutines.
type logBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// captureLog sends the log to a buffer until the test ends, so a test can wait for a line such as "(retrying)".
func captureLog(t *testing.T) *logBuffer {
	t.Helper()
	buffer := &logBuffer{}
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buffer
}

// settled reports whether the consumer has nothing left to deliver and nothing awaiting an ack.
func settled(client NatsClient, stream, durable string) bool {
	backlog, err := NatsJetStreamBacklog(context.Background(), client, NatsJetStreamConsumer{Stream: stream, Durable: durable})
	return err == nil && backlog == 0
}

// projector is today's api-projector consumer, the one the tests consume with.
var projector = NatsJetStreamConsumer{Stream: "EVT", Durable: "api-projector", Subject: "evt.job.>",
	AckWait: 30 * time.Second, MaxAckPending: 1, NakDelay: 100 * time.Millisecond}

// event is a test message body. Fail makes the handler fail its first try.
type event struct {
	ID   string `json:"id"`
	Fail bool   `json:"fail"`
}

// recorder collects the ids a handler saw, and closes done once it has seen want of them.
type recorder struct {
	mu   sync.Mutex
	seen []string
	done chan struct{}
	want int
}

func newRecorder(want int) *recorder { return &recorder{done: make(chan struct{}), want: want} }

func (r *recorder) handle(_ context.Context, data *event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, data.ID)
	if len(r.seen) == r.want {
		close(r.done)
	}
	if data.Fail && len(r.seen) == 1 {
		return errors.New("first try fails")
	}
	return nil
}

func (r *recorder) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

// everything routes every subject to one handler in the helper-call shape: decode, run, then ack, nak or drop.
func everything(handler func(context.Context, *event) error) *NatsJetStreamGroup {
	group := &NatsJetStreamGroup{}
	group.AddHandler(">", func(ctx context.Context, msg *NatsJetStreamMessage) {
		body, err := JsonToMsg[event](msg.Data())
		if err != nil {
			TermNatsJetStreamMessage(msg, err)
			return
		}
		if err := handler(ctx, body); err != nil {
			NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		AckNatsJetStreamMessage(msg)
	})
	return group
}

func wait(t *testing.T, done <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatal("timed out")
	}
}

func TestNatsJetStreamPublishStoresAMessageIDOnce(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "CMD", "cmd.>")
	ctx := context.Background()
	for _, msgID := range []string{"job1:1", "job1:1", "job1:2"} {
		if err := NatsJetStreamPublish(ctx, client, "cmd.job.generate", &event{ID: "a"}, msgID); err != nil {
			t.Fatal(err)
		}
	}
	stream, _ := client.jetStream().Stream(ctx, "CMD")
	info, _ := stream.Info(ctx)
	if info.State.Msgs != 2 {
		t.Errorf("messages = %d, want 2", info.State.Msgs)
	}
}

func TestNatsJetStreamPublishFailsWithoutAStream(t *testing.T) {
	client := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := NatsJetStreamPublish(ctx, client, "cmd.job.generate", &event{}, ""); err == nil {
		t.Error("no error")
	}
}

// One at a time in stream order: a failing message and its retry come before the next one; invalid JSON is dropped.
func TestNatsJetStreamConsumeAcksNaksAndDrops(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "EVT", "evt.>")
	ctx, cancel := context.WithCancel(context.Background())
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a", "fail": true}`)) // nak'd, then redelivered
	_ = client.Conn().Publish("evt.job.1.status", []byte(`not json`))                  // dropped, never handled
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "b"}`))
	_ = client.Conn().Publish("evt.other", []byte(`{"id": "x"}`)) // outside the filter

	logged := captureLog(t)
	got := newRecorder(3)
	finished := make(chan struct{})
	go func() { NatsJetStreamConsume(ctx, client, projector, everything(got.handle)); close(finished) }()
	wait(t, got.done, 5*time.Second)
	eventually(t, 5*time.Second, func() bool { return settled(client, "EVT", "api-projector") })
	cancel()
	wait(t, finished, 5*time.Second)
	if !strings.Contains(logged.String(), "dropping evt.job.1.status: bad request: invalid JSON") {
		t.Errorf("not json was not dropped: %s", logged.String())
	}

	if want := []string{"a", "a", "b"}; !slices.Equal(got.ids(), want) {
		t.Errorf("seen = %v, want %v", got.ids(), want)
	}
}

func TestNatsJetStreamConsumeWaitsForTheStream(t *testing.T) {
	client := startServer(t)
	logged := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := newRecorder(1)
	go NatsJetStreamConsume(ctx, client, projector, everything(got.handle))
	// The first try finds no stream and says it will retry; only then does the stream appear.
	eventually(t, 5*time.Second, func() bool { return strings.Contains(logged.String(), "(retrying)") })
	addStream(t, client, "EVT", "evt.>")
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a"}`))
	wait(t, got.done, 2*retryDelay+2*time.Second)
}

// Today's projector stops for good when its consumer is deleted; NatsJetStreamConsume recreates it. The new consumer
// starts from the stream's first message, so "a" comes again before "b".
func TestNatsJetStreamConsumeRecreatesADeletedConsumer(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "EVT", "evt.>")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := newRecorder(1)
	all := newRecorder(3)
	go NatsJetStreamConsume(ctx, client, projector, everything(func(ctx context.Context, data *event) error {
		_ = first.handle(ctx, data)
		return all.handle(ctx, data)
	}))
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a"}`))
	wait(t, first.done, 5*time.Second)
	if err := client.jetStream().DeleteConsumer(context.Background(), "EVT", "api-projector"); err != nil {
		t.Fatal(err)
	}
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "b"}`))
	wait(t, all.done, 2*retryDelay+2*time.Second)
	if want := []string{"a", "a", "b"}; !slices.Equal(all.ids(), want) {
		t.Errorf("seen = %v, want %v", all.ids(), want)
	}
}

// NATS restarts with an empty store while the client is away, and the stream is set up again, as nats-setup does: the
// consumer is gone, but no "consumer deleted" status reaches the client. NatsJetStreamConsume must still notice and
// recreate it, or projection stops for good.
func TestNatsJetStreamConsumeRecreatesAConsumerLostWhileDisconnected(t *testing.T) {
	first := runServer(t, -1)
	port := first.Addr().(*net.TCPAddr).Port
	client, err := NewNatsClient(Config{URL: first.ClientURL(), Name: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Conn().Close)
	addStream(t, client, "EVT", "evt.>")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before, all := newRecorder(1), newRecorder(2)
	go NatsJetStreamConsume(ctx, client, projector, everything(func(ctx context.Context, data *event) error {
		_ = before.handle(ctx, data)
		return all.handle(ctx, data)
	}))
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a"}`))
	wait(t, before.done, 5*time.Second)

	first.Shutdown()
	first.WaitForShutdown()
	runServer(t, port)
	eventually(t, 10*time.Second, client.Conn().IsConnected)
	addStream(t, client, "EVT", "evt.>")
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "b"}`))
	wait(t, all.done, 30*time.Second)
	if want := []string{"a", "b"}; !slices.Equal(all.ids(), want) {
		t.Errorf("seen = %v, want %v", all.ids(), want)
	}
}

func TestNatsJetStreamConsumeReturnsAfterTheHandlerFinishes(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "EVT", "evt.>")
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	var handlerDone bool
	handler := func(context.Context, *event) error {
		close(started)
		<-release
		handlerDone = true
		return nil
	}
	finished := make(chan struct{})
	go func() { NatsJetStreamConsume(ctx, client, projector, everything(handler)); close(finished) }()
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a"}`))
	wait(t, started, 5*time.Second)
	cancel()
	select {
	case <-finished:
		t.Fatal("returned while the handler ran")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	wait(t, finished, 5*time.Second)
	if !handlerDone {
		t.Error("handler not done")
	}
}

// The live api-projector consumer was made by today's projector code; NatsJetStreamConsume must leave it as it is.
func TestNatsJetStreamConsumeKeepsTodaysConsumer(t *testing.T) {
	client := startServer(t)
	addStream(t, client, "EVT", "evt.>")
	ctx := context.Background()
	before, err := client.jetStream().CreateOrUpdateConsumer(ctx, "EVT", jetstream.ConsumerConfig{
		Durable: "api-projector", FilterSubject: "evt.job.>", AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: 30 * time.Second, MaxAckPending: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, _ := before.Info(ctx)
	// A message handled proves NatsJetStreamConsume could create-or-update the consumer, not just retry around a
	// rejected update while the old consumer sits there unchanged.
	logged := captureLog(t)
	_ = client.Conn().Publish("evt.job.1.status", []byte(`{"id": "a"}`))
	got := newRecorder(1)
	runCtx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() { NatsJetStreamConsume(runCtx, client, projector, everything(got.handle)); close(finished) }()
	wait(t, got.done, 5*time.Second)
	cancel()
	wait(t, finished, 5*time.Second)
	if strings.Contains(logged.String(), "(retrying)") {
		t.Errorf("consumer retried: %s", logged.String())
	}
	after, _ := client.jetStream().Consumer(ctx, "EVT", "api-projector")
	afterInfo, _ := after.Info(ctx)
	if beforeInfo.Created != afterInfo.Created || afterInfo.Config.FilterSubject != "evt.job.>" ||
		afterInfo.Config.MaxAckPending != 1 || afterInfo.Config.AckWait != 30*time.Second {
		t.Errorf("before %+v\nafter %+v", beforeInfo.Config, afterInfo.Config)
	}
}

// The watch starts before the bucket exists, picks it up on a retry, and sees a value put then and one put later.
func TestNatsJetStreamWatchKVSeesCurrentAndLaterValues(t *testing.T) {
	client := startServer(t)
	logged := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	seen := map[string]string{}
	saw := func(key string) func() bool {
		return func() bool { mu.Lock(); defer mu.Unlock(); _, ok := seen[key]; return ok }
	}
	go NatsJetStreamWatchKV(ctx, client, "registry", func(key string, value []byte) error {
		mu.Lock()
		defer mu.Unlock()
		seen[key] = string(value)
		if value == nil {
			seen[key] = "<nil>"
		}
		return nil
	}, func() { mu.Lock(); defer mu.Unlock(); seen["<loaded>"] = "" })
	// The first try finds no bucket and says it will retry; only then does the bucket appear.
	eventually(t, 5*time.Second, func() bool { return strings.Contains(logged.String(), "watch registry: ") })
	bucket, err := client.jetStream().CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "registry", History: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bucket.Put(ctx, "workflows", []byte("w"))
	eventually(t, 2*retryDelay+2*time.Second, saw("workflows"))
	eventually(t, 5*time.Second, saw("<loaded>")) // the end of the current values was signalled
	_, _ = bucket.Put(ctx, "prices", []byte("p"))
	eventually(t, 5*time.Second, saw("prices"))
	_ = bucket.Delete(ctx, "workflows") // a delete comes as a nil value
	eventually(t, 5*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return seen["workflows"] == "<nil>" })
	mu.Lock()
	defer mu.Unlock()
	if seen["prices"] != "p" {
		t.Errorf("seen = %v", seen)
	}
}

// A put needs the bucket; once it exists, the value is stored under the key.
func TestNatsJetStreamPutKV(t *testing.T) {
	client := startServer(t)
	ctx := context.Background()
	if err := NatsJetStreamPutKV(ctx, client, "cancels", "r1", []byte("now")); err == nil {
		t.Fatal("put without a bucket succeeded")
	}
	bucket, err := client.jetStream().CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "cancels", History: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := NatsJetStreamPutKV(ctx, client, "cancels", "r1", []byte("now")); err != nil {
		t.Fatal(err)
	}
	if entry, err := bucket.Get(ctx, "r1"); err != nil || string(entry.Value()) != "now" {
		t.Fatalf("get: %v %v", entry, err)
	}
}

func TestNewNatsClientMakesAJetStreamContextWhileNatsIsDown(t *testing.T) {
	if client := newDeadClient(t, time.Second); client.jetStream() == nil {
		t.Error("no JetStream context")
	}
}
