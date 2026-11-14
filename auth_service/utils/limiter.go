package utils

import (
	"sync"
	"time"
)

// Limiter counts failures per key in a fixed window.
// ponytail: in-process, per replica; move to a shared store (NATS KV / Postgres) when auth runs replicated.
type Limiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	m      map[string]*window
}

// window is one key's failure count and when it started.
type window struct {
	n     int
	start time.Time
}

// NewLimiter returns a Limiter that blocks a key after max failures within w.
func NewLimiter(max int, w time.Duration) *Limiter {
	return &Limiter{max: max, window: w, m: map[string]*window{}}
}

// Blocked reports whether key has used up its failures in the current window.
func (l *Limiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.m[key]
	return w != nil && time.Since(w.start) < l.window && w.n >= l.max
}

// Fail records a failure for key, starting a new window when the last one has ended.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.m[key]
	if w == nil || now.Sub(w.start) >= l.window {
		if len(l.m) > 100_000 { // drop expired windows to shrink the map
			for k, v := range l.m {
				if now.Sub(v.start) >= l.window {
					delete(l.m, k)
				}
			}
		}
		w = &window{start: now}
		l.m[key] = w
	}
	w.n++
}

// Reset clears key's failures, for after a success.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}
