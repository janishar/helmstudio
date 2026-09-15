package studioapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Events (R41, 07 §3): one broker, a ring of the last events, and an audience
// test per event so a studio only ever receives what it may see.

type event struct {
	seq      uint64
	name     string
	data     []byte
	audience func(Principal) bool
}

// Broker fans events out to subscribers and keeps the last few for
// Last-Event-ID.
type Broker struct {
	mu sync.Mutex
	// boot distinguishes this broker's ids from those of a provider that ran
	// before it: sequence numbers restart at 1 with every daemon (second
	// review #2).
	boot   string
	seq    uint64
	ring   []event
	size   int
	subs   map[chan struct{}]struct{}
	closed bool
}

// NewBroker keeps the last size events.
func NewBroker(size int) *Broker {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return &Broker{boot: hex.EncodeToString(b[:]), size: size, subs: map[chan struct{}]struct{}{}}
}

// eventID is what a subscriber sees as id: and sends back as Last-Event-ID.
func (b *Broker) eventID(seq uint64) string { return b.boot + "-" + strconv.FormatUint(seq, 10) }

// resumeFrom reads a Last-Event-ID. It reports ok false for an id from another
// boot, one this broker never issued, or one that does not parse: the
// subscriber has lost events it cannot be told about individually.
func (b *Broker) resumeFrom(id string) (seq uint64, ok bool) {
	boot, n, found := strings.Cut(id, "-")
	if !found || boot != b.boot {
		return 0, false
	}
	seq, err := strconv.ParseUint(n, 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil || seq > b.seq {
		return 0, false
	}
	return seq, true
}

func onlyStudio(id string) func(Principal) bool {
	return func(p Principal) bool { return p.StudioID == id }
}

// Publish records an event for the principals audience accepts.
func (b *Broker) Publish(name string, data any, audience func(Principal) bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	b.mu.Lock()
	b.seq++
	b.ring = append(b.ring, event{seq: b.seq, name: name, data: raw, audience: audience})
	if len(b.ring) > b.size {
		b.ring = b.ring[len(b.ring)-b.size:]
	}
	for c := range b.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	b.mu.Unlock()
}

// Shutdown tells every subscriber the provider is going away.
func (b *Broker) Shutdown(reason string) {
	b.Publish("shutdown", helm.ShutdownEvent{Reason: reason}, func(Principal) bool { return true })
	b.mu.Lock()
	b.closed = true
	for c := range b.subs {
		close(c)
		delete(b.subs, c)
	}
	b.mu.Unlock()
}

// since returns events after seq, and whether some were already dropped.
func (b *Broker) since(seq uint64) ([]event, bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lost := len(b.ring) > 0 && seq > 0 && seq < b.ring[0].seq-1
	var out []event
	for _, e := range b.ring {
		if e.seq > seq {
			out = append(out, e)
		}
	}
	return out, lost, b.closed
}

func (s *Service) publishItem(change string, it helm.Item) {
	owner := it.StudioID
	s.events.Publish("item", helm.ItemEvent{Change: change, Item: it}, func(p Principal) bool {
		return p.StudioID == owner || p.has(string(helm.CapabilityGalleryReadAll))
	})
}

func (s *Service) EventsSubscribe(ctx context.Context, w http.ResponseWriter, r *http.Request, params helm.EventsSubscribeParams) error {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return err
	}
	b := s.events
	var last uint64
	resumeLost := false
	b.mu.Lock()
	current := b.seq
	b.mu.Unlock()
	if params.LastEventID != nil {
		seq, ok := b.resumeFrom(*params.LastEventID)
		if ok {
			last = seq
		} else {
			last, resumeLost = current, true
		}
	} else {
		last = current
	}
	wake := make(chan struct{}, 1)
	b.mu.Lock()
	if !b.closed {
		b.subs[wake] = struct{}{}
	}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if _, ok := b.subs[wake]; ok {
			delete(b.subs, wake)
		}
		b.mu.Unlock()
	}()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	first := params.LastEventID != nil
	for {
		evs, lost, closed := b.since(last)
		if first && (lost || resumeLost) {
			fmt.Fprintf(w, "event: gap\ndata: %s\n\n", mustJSON(helm.GapEvent{Reason: "events after that id were dropped; re-read state"}))
		}
		first = false
		for _, e := range evs {
			last = e.seq
			if e.audience != nil && !e.audience(p) {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", b.eventID(e.seq), e.name, e.data); err != nil {
				return nil
			}
		}
		if rc.Flush() != nil || closed {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-wake:
			if !ok {
				evs, _, _ := b.since(last)
				for _, e := range evs {
					if e.audience == nil || e.audience(p) {
						fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", b.eventID(e.seq), e.name, e.data)
					}
				}
				_ = rc.Flush()
				return nil
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// WatchJobs publishes a job event whenever a job's state or progress
// changes, for every kind of job, until ctx ends. Install and download jobs
// are written by the installer, which knows nothing of events; polling the
// table is how their progress reaches studios without coupling the two.
func (s *Service) WatchJobs(ctx context.Context, every time.Duration) {
	seen := map[string]string{}
	tick := time.NewTicker(every)
	defer tick.Stop()
	since := ms(s.now()) - every.Milliseconds()
	for {
		rows, err := s.st.Reader().QueryContext(ctx, `SELECT id, studio_id, state, COALESCE(progress_num, 0), COALESCE(progress_den, 0), COALESCE(cancel_requested_at, 0)
			FROM jobs WHERE state IN ('queued', 'running') OR finished_at >= ? OR created_at >= ?`, since, since)
		if err == nil {
			var changed []string
			current := map[string]string{}
			for rows.Next() {
				var id string
				var studio sql.NullString
				var state string
				var num, den, cancel int64
				if rows.Scan(&id, &studio, &state, &num, &den, &cancel) != nil {
					continue
				}
				sig := fmt.Sprintf("%s/%d/%d/%d", state, num, den, cancel)
				current[id] = sig
				if seen[id] != sig && studio.Valid {
					changed = append(changed, id)
				}
			}
			rows.Close()
			for _, id := range changed {
				if j, err := s.readJob(ctx, s.st.Reader(), id, true); err == nil && j.StudioID != nil {
					s.events.Publish("job", helm.JobEvent{Job: *j}, onlyStudio(*j.StudioID))
				}
			}
			seen = current
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			since = ms(s.now()) - 10*every.Milliseconds()
		}
	}
}
