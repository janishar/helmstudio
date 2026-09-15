package helm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorKindsFollowTheStatus(t *testing.T) {
	for status, kind := range map[int]Kind{
		400: KindInvalid, 413: KindInvalid, 416: KindInvalid, 422: KindInvalid,
		401: KindUnauthenticated, 403: KindForbidden, 421: KindForbidden,
		404: KindNotFound, 410: KindNotFound, 409: KindConflict, 429: KindQuotaExceeded, 507: KindQuotaExceeded,
		501: KindUnsupported, 503: KindUnavailable,
		500: KindInternal, 418: KindInternal,
	} {
		if got := KindForStatus(status); got != kind {
			t.Errorf("status %d: %s; want %s", status, got, kind)
		}
	}
	if KindOf(errors.New("dial tcp: connection refused")) != KindUnavailable {
		t.Error("a transport failure is not Unavailable")
	}
}

func TestErrorBodiesAndTheTokenGoOverTheWire(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"etag_mismatch","message":"changed","details":{"x":1}}`)
	}))
	defer srv.Close()
	c := NewRemote(srv.URL+"/api/v1", "hs_live_abc")
	_, err := c.KV.Get(context.Background(), "ui", "k")
	var e *Error
	if !errors.As(err, &e) || e.Status != 409 || e.Code != "etag_mismatch" || e.Details["x"] != float64(1) || e.Kind() != KindConflict {
		t.Fatalf("error = %#v", err)
	}
	if auth != "Bearer hs_live_abc" {
		t.Fatalf("Authorization = %q", auth)
	}
}

func TestEventStreamParsesEventsAndSkipsComments(t *testing.T) {
	body := ": ping\n\nid: 7\nevent: item\ndata: {\"a\":\ndata: 1}\n\nevent: end\ndata: {}\n\n"
	s := newEventStream(&http.Response{Body: io.NopCloser(strings.NewReader(body))})
	ev, err := s.Next()
	if err != nil || ev.ID != "7" || ev.Name != "item" || string(ev.Data) != "{\"a\":\n1}" {
		t.Fatalf("first = %+v, %v", ev, err)
	}
	var v map[string]any
	if err := ev.Decode(&v); err != nil || v["a"] != float64(1) {
		t.Fatalf("decode = %v, %v", v, err)
	}
	if ev, err = s.Next(); err != nil || ev.Name != "end" {
		t.Fatalf("second = %+v, %v", ev, err)
	}
	if _, err = s.Next(); err != io.EOF {
		t.Fatalf("after the end: %v; want io.EOF", err)
	}
}

func TestFromEnvSelectsTheProvider(t *testing.T) {
	t.Setenv(EnvAPI, "")
	RegisterEmbedded(nil)
	if _, err := FromEnv(); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no HELM_API, nothing registered: %v", err)
	}
	opened := false
	RegisterEmbedded(func() (*Client, error) { opened = true; return &Client{}, nil })
	defer RegisterEmbedded(nil)
	if _, err := FromEnv(); err != nil || !opened {
		t.Fatalf("no HELM_API: embedded opened %v, %v", opened, err)
	}
	opened = false
	t.Setenv(EnvAPI, "http://127.0.0.1:8700/api/v1")
	t.Setenv(EnvToken, "")
	if _, err := FromEnv(); err == nil || opened {
		t.Fatalf("HELM_API without a token: %v (embedded opened %v)", err, opened)
	}
	t.Setenv(EnvToken, "hs_live_x")
	if c, err := FromEnv(); err != nil || c == nil || opened {
		t.Fatalf("HELM_API and a token: %v (embedded opened %v)", err, opened)
	}
}
