package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
)

// Busy states a switch dialog shows (docs/decisions.md M5 Q12). Unknown is
// never read as idle.
const (
	BusyBusy    = "busy"
	BusyIdle    = "idle"
	BusyUnknown = "unknown"
)

// busyTimeout bounds one busy probe (Q12).
const busyTimeout = 2 * time.Second

// BusyState is what a running studio said about its occupancy when asked.
type BusyState struct {
	State string `json:"state"`
	// Loaded is whether the studio holds a model, when it said.
	Loaded *bool `json:"loaded,omitempty"`
	// Message is the studio's own words when it answered; for unknown, why
	// helmstudio cannot tell.
	Message string `json:"message,omitempty"`
	// Progress is 0..1 through the work in flight, when the studio said.
	Progress *float64 `json:"progress,omitempty"`
}

// parseBusy reads the busy response contract (schema/manifest.json,
// $defs.process.busy): one JSON object and nothing after it, with the exact
// key "busy" holding a boolean, and "loaded", "message" and "progress" of
// their types when present. Keys are matched exactly — encoding/json's
// case-insensitive matching would read a Go studio's untagged {"Busy": false}
// as idle (M5 review #5). Unknown keys are ignored.
func parseBusy(body []byte) (BusyState, string) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return BusyState{}, "did not answer with a JSON object"
	}
	if _, err := dec.Token(); err != io.EOF {
		return BusyState{}, "answered a JSON object followed by more data"
	}
	var busy bool
	if raw, ok := fields["busy"]; !ok || json.Unmarshal(raw, &busy) != nil || string(bytes.TrimSpace(raw)) == "null" {
		return BusyState{}, "answered without a \"busy\" boolean"
	}
	st := BusyState{State: BusyIdle}
	if busy {
		st.State = BusyBusy
	}
	if raw, ok := fields["loaded"]; ok {
		var loaded bool
		if json.Unmarshal(raw, &loaded) != nil || string(bytes.TrimSpace(raw)) == "null" {
			return BusyState{}, "answered a \"loaded\" that is not a boolean"
		}
		st.Loaded = &loaded
	}
	if raw, ok := fields["message"]; ok {
		if json.Unmarshal(raw, &st.Message) != nil {
			return BusyState{}, "answered a \"message\" that is not a string"
		}
	}
	if raw, ok := fields["progress"]; ok {
		var p float64
		if json.Unmarshal(raw, &p) != nil || string(bytes.TrimSpace(raw)) == "null" {
			return BusyState{}, "answered a \"progress\" that is not a number"
		}
		if p < 0 || p > 1 {
			return BusyState{}, fmt.Sprintf("answered a progress of %v, outside 0..1", p)
		}
		st.Progress = &p
	}
	return st, ""
}

// probeBusy asks one process. Anything but a conforming answer within
// busyTimeout is unknown, with the reason.
func probeBusy(ctx context.Context, port int, path string) BusyState {
	unknown := func(format string, args ...any) BusyState {
		return BusyState{State: BusyUnknown, Message: fmt.Sprintf(format, args...)}
	}
	if port == 0 {
		return unknown("its busy probe needs a port, and the process declares none")
	}
	ctx, cancel := context.WithTimeout(ctx, busyTimeout)
	defer cancel()
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return unknown("%s is not a URL helmstudio can ask: %v", url, err)
	}
	resp, err := probeClient.Do(req)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return unknown("%s did not answer within %s", path, busyTimeout)
	case err != nil:
		return unknown("asking %s failed: %v", path, errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unknown("%s answered %d, not 200", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return unknown("reading %s's answer: %v", path, err)
	}
	st, why := parseBusy(body)
	if why != "" {
		return unknown("%s %s", path, why)
	}
	return st
}

// groupBusy asks every live member that declares a busy probe, in parallel.
// The group is busy if any member is, otherwise unknown if any member is
// unknown or none declares a probe, otherwise idle. A probe never changes a
// process's state or health.
func (s *Supervisor) groupBusy(ctx context.Context, g *group) BusyState {
	type ask struct {
		name, path string
		port       int
	}
	var asks []ask
	s.mu.Lock()
	for _, p := range g.procs {
		if !p.spawned || isClosed(p.exited) {
			continue
		}
		// A member re-adopted without a plan (its manifest no longer
		// resolves) is still asked, through the loaded manifest's process of
		// the same name and the port it holds (M5 review #6).
		var busy *manifest.Busy
		switch {
		case p.pl != nil:
			busy = p.pl.spec.Busy
		case g.studio.Manifest != nil:
			for _, sp := range g.studio.Manifest.EffectiveProcesses() {
				if sp.Name == p.name {
					busy = sp.Busy
				}
			}
		}
		if busy != nil {
			asks = append(asks, ask{name: p.name, path: busy.Path, port: p.port})
		}
	}
	s.mu.Unlock()
	if len(asks) == 0 {
		if g.studio.Manifest == nil {
			return BusyState{State: BusyUnknown, Message: fmt.Sprintf("%s's manifest is not loaded, so its busy probe is not known", g.displayName())}
		}
		return BusyState{State: BusyUnknown, Message: fmt.Sprintf("%s declares no busy probe", g.displayName())}
	}
	answers := make([]BusyState, len(asks))
	var wg sync.WaitGroup
	for i, a := range asks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = probeBusy(ctx, a.port, a.path)
		}()
	}
	wg.Wait()

	out := BusyState{State: BusyIdle}
	var unknown *BusyState
	for i, a := range answers {
		switch a.State {
		case BusyBusy:
			if out.State != BusyBusy {
				out.State, out.Message, out.Progress = BusyBusy, a.Message, a.Progress
			}
		case BusyUnknown:
			if unknown == nil {
				u := a
				u.Message = fmt.Sprintf("process %q: %s", asks[i].name, a.Message)
				unknown = &u
			}
		case BusyIdle:
			if out.Message == "" && out.State == BusyIdle {
				out.Message = a.Message
			}
		}
		if a.Loaded != nil {
			loaded := *a.Loaded || (out.Loaded != nil && *out.Loaded)
			out.Loaded = &loaded
		}
	}
	if out.State != BusyBusy && unknown != nil {
		unknown.Loaded = out.Loaded
		return *unknown
	}
	return out
}

func (g *group) displayName() string {
	if g.studio.Manifest != nil {
		return g.studio.Manifest.Name
	}
	return g.studioID
}

// confirmDigest names what a user saw when they confirmed stopping a running
// studio: which group, for which studio, and whether it was busy or holding a
// model. Progress and message are left out — they change during every
// render, and a confirmation should go stale only when the decision would.
func confirmDigest(a HeavyArithmetic, runID string) string {
	loaded := "?"
	if a.Busy.Loaded != nil {
		loaded = strconv.FormatBool(*a.Busy.Loaded)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", a.RunningStudio, runID, a.WantedStudio, a.Busy.State, loaded)))
	return hex.EncodeToString(sum[:12])
}
