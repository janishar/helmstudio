// Package theme is the launcher's theme and each studio's identity hue: the
// stored setting, the stream every running studio's page follows, the ramp a
// studio without a hue is given, and the SDK major a studio is served
// (docs/design/03-design-system.md §2c, §5; docs/decisions.md M6 Q7, Q8, Q9,
// Q14).
package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/store"
)

// The three values of the theme setting.
const (
	System = "system"
	Light  = "light"
	Dark   = "dark"
)

// Valid reports whether v is a theme value.
func Valid(v string) bool { return v == System || v == Light || v == Dark }

// settingKey is the settings row the theme is stored under (02 §5, schema v5).
const settingKey = "theme"

// Settings reads and writes the theme, and tells the hub when it changes.
type Settings struct {
	Store *store.Store
	Hub   *Hub
}

// Load returns the stored theme, System when none is stored.
func (s *Settings) Load(ctx context.Context) (string, error) {
	var v string
	err := s.Store.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return System, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the theme setting: %w", err)
	}
	if !Valid(v) {
		return System, nil
	}
	return v, nil
}

// Set stores the theme and publishes it.
func (s *Settings) Set(ctx context.Context, v string) error {
	if !Valid(v) {
		return fmt.Errorf("theme %q is not one of system, light, dark", v)
	}
	err := s.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, settingKey, v, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		return fmt.Errorf("storing the theme setting: %w", err)
	}
	s.Hub.Publish(v)
	return nil
}

// Hub holds the current theme and fans each change out to the theme stream's
// subscribers and to anyone registered with OnChange.
type Hub struct {
	mu        sync.Mutex
	current   string
	subs      map[chan string]struct{}
	listeners []func(string)
}

// NewHub starts with the stored theme.
func NewHub(current string) *Hub {
	if !Valid(current) {
		current = System
	}
	return &Hub{current: current, subs: map[chan string]struct{}{}}
}

// Current is the theme now.
func (h *Hub) Current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current
}

// OnChange registers f to be called with every published theme.
func (h *Hub) OnChange(f func(string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listeners = append(h.listeners, f)
}

// Publish makes v current and tells everyone, even when it did not change: a
// PUT of the same value is harmless and a page that missed a change catches
// up.
func (h *Hub) Publish(v string) {
	h.mu.Lock()
	h.current = v
	for c := range h.subs {
		select {
		case c <- v:
		default:
			// A subscriber that has not read the last value gets this one
			// next: drop the stale one first.
			select {
			case <-c:
			default:
			}
			select {
			case c <- v:
			default:
			}
		}
	}
	listeners := append([]func(string){}, h.listeners...)
	h.mu.Unlock()
	for _, f := range listeners {
		f(v)
	}
}

// Subscribers is how many theme streams are open.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *Hub) subscribe() (string, chan string, func()) {
	c := make(chan string, 1)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	cur := h.current
	h.mu.Unlock()
	return cur, c, func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.mu.Unlock()
	}
}

// Event is the only thing the tokenless theme endpoints carry
// (api/openapi.yaml ThemeEvent).
type Event struct {
	Theme string `json:"theme"`
}

// ServeCurrent answers GET /theme.
func (h *Hub) ServeCurrent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(Event{Theme: h.Current()})
}

// ServeEvents answers GET /theme/events: the current theme at once, then
// every change, as `theme` events whose data is {"theme": …}.
func (h *Hub) ServeEvents(w http.ResponseWriter, r *http.Request) {
	cur, c, cancel := h.subscribe()
	defer cancel()
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	send := func(v string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		b, _ := json.Marshal(Event{Theme: v})
		if _, err := fmt.Fprintf(w, "event: theme\ndata: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(cur) {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case v := <-c:
			if !send(v) {
				return
			}
		case <-ping.C:
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// ---- identity hues (03 §2c) --------------------------------------------------

// Pair is a hue per theme, "#rrggbb".
type Pair struct {
	Dark  string
	Light string
}

// Ramp is 03 §2c's table: what a studio without a hue is given.
var Ramp = []Pair{
	{"#a1cb4d", "#5d7e1b"},
	{"#62cb4d", "#2b7e1b"},
	{"#4dcb77", "#1b7e3c"},
	{"#4dcbb6", "#1b7e6e"},
	{"#4dc1cb", "#1b767e"},
	{"#b64dcb", "#6e1b7e"},
}

// Accent is the manifest's hue, or the ramp entry its id hashes to.
func Accent(m *manifest.Manifest) Pair {
	if m.Hue != nil && m.Hue.Dark != "" && m.Hue.Light != "" {
		return Pair{Dark: strings.ToLower(m.Hue.Dark), Light: strings.ToLower(m.Hue.Light)}
	}
	return RampFor(m.ID)
}

// RampFor is the ramp entry FNV-1a of id selects.
func RampFor(id string) Pair {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return Ramp[h.Sum32()%uint32(len(Ramp))]
}

// On is the text colour for a fill of hex: #1a1400 or #ffffff, whichever
// contrasts more (03 §2c).
func On(hex string) string {
	dark, err1 := Contrast(hex, "#1a1400")
	light, err2 := Contrast(hex, "#ffffff")
	if err1 != nil || err2 != nil || dark >= light {
		return "#1a1400"
	}
	return "#ffffff"
}

// Contrast is WCAG 2's contrast ratio of two "#rrggbb" colours.
func Contrast(a, b string) (float64, error) {
	la, err := Luminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := Luminance(b)
	if err != nil {
		return 0, err
	}
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05), nil
}

// Luminance is WCAG 2's relative luminance of "#rrggbb".
func Luminance(hex string) (float64, error) {
	r, g, b, err := rgb(hex)
	if err != nil {
		return 0, err
	}
	lin := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b), nil
}

// Hue is the HSL hue angle of "#rrggbb", in degrees.
func Hue(hex string) (float64, error) {
	r, g, b, err := rgb(hex)
	if err != nil {
		return 0, err
	}
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if hi == lo {
		return 0, nil
	}
	d := hi - lo
	var h float64
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, nil
}

func rgb(hex string) (r, g, b float64, err error) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 0, 0, 0, fmt.Errorf("%q is not a #rrggbb colour", hex)
	}
	var ch [3]float64
	for i := range ch {
		v, perr := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("%q is not a #rrggbb colour", hex)
		}
		ch[i] = float64(v) / 255
	}
	return ch[0], ch[1], ch[2], nil
}

// ---- SDK majors (04 §9) ------------------------------------------------------

// ServedMajor is the one major of helm-css, helm-runtime-sdk and helm-ui-sdk
// this daemon serves under /sdk/v1/.
const ServedMajor = 1

// SDKMajor checks a manifest's sdk pins against what is served and returns
// the major to inject. A range whose major is not served refuses the launch.
func SDKMajor(m *manifest.Manifest) (int, error) {
	if m.SDK == nil {
		return ServedMajor, nil
	}
	for _, f := range []struct{ field, rng string }{{"runtime", m.SDK.Runtime}, {"ui", m.SDK.UI}, {"css", m.SDK.CSS}} {
		if f.rng == "" {
			continue
		}
		digits := strings.TrimLeft(f.rng, "^~")
		if i := strings.IndexByte(digits, '.'); i >= 0 {
			digits = digits[:i]
		}
		major, err := strconv.Atoi(digits)
		if err != nil {
			return 0, fmt.Errorf("sdk.%s %q is not a version range", f.field, f.rng)
		}
		if major != ServedMajor {
			return 0, fmt.Errorf("%s pins sdk.%s %q, and this helmstudio serves only major %d of the helm packages; update helmstudio, or pin %q", m.ID, f.field, f.rng, ServedMajor, "^"+strconv.Itoa(ServedMajor))
		}
	}
	return ServedMajor, nil
}
