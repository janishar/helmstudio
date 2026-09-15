// Package hubtest is a fake Hugging Face Hub for tests: the revision and tree
// APIs, the resolve endpoint (a 302 to a signed CDN URL for LFS files, the
// bytes otherwise) and a CDN that honours Range, with switches to drop a
// connection, stall a download, expire signed URLs or demand a token.
package hubtest

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Hub is enough of the Hugging Face Hub for the downloader: the revision
// and tree APIs, the resolve endpoint (a 302 to a signed "CDN" URL for LFS
// files, the bytes directly otherwise) and a CDN that honours Range.
type Hub struct {
	t      *testing.T
	Server *httptest.Server
	Commit string

	// Mu guards the switches below while a download runs.
	Mu    sync.Mutex
	repos map[string]map[string][]byte // repo → path → content
	lfs   map[string]bool              // path → served through the CDN
	Log   []Request
	sig   int

	// NeedToken makes huggingface.co answer 401 without a bearer token.
	NeedToken bool
	// CutAfter makes the next CDN response for a path stop after this many
	// bytes and drop the connection.
	CutAfter map[string]int64
	// StallAt makes the next CDN response for a path send this many bytes,
	// then hold the connection open until the client goes away.
	StallAt map[string]int64
	// ExpireNext makes the next n CDN requests answer 403, as an expired
	// signed URL does.
	ExpireNext int
	// IgnoreRange makes the next ranged CDN request for a path answer 206
	// with the whole file from byte 0, whatever range was asked for: a broken server
	// the downloader must not append from.
	IgnoreRange map[string]bool
	// WrongETag makes resolve report a different etag for a path.
	WrongETag map[string]bool
	// Stalled is closed once a stalled response has sent its bytes.
	Stalled chan struct{}
}

// Request is one request the Hub answered.
type Request struct {
	Method, Host, Path, Range string
	Status                    int
}

// New starts a Hub for the test.
func New(t *testing.T) *Hub {
	h := &Hub{t: t, Commit: "c0ffee" + strings.Repeat("0", 34), repos: map[string]map[string][]byte{}, lfs: map[string]bool{},
		CutAfter: map[string]int64{}, StallAt: map[string]int64{}, WrongETag: map[string]bool{}, IgnoreRange: map[string]bool{}, Stalled: make(chan struct{})}
	h.Server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.Server.Close)
	return h
}

// Add serves content as path in repo; an LFS file is served through the CDN.
func (h *Hub) Add(repo, path string, content []byte, lfs bool) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.repos[repo] == nil {
		h.repos[repo] = map[string][]byte{}
	}
	h.repos[repo][path] = content
	h.lfs[repo+"/"+path] = lfs
}

// Content is n deterministic bytes.
func Content(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31) ^ seed
	}
	return b
}

func sha256hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func sha1hex(b []byte) string   { s := sha1.Sum(b); return hex.EncodeToString(s[:]) }

func (h *Hub) etag(repo, path string) string {
	c := h.repos[repo][path]
	if h.lfs[repo+"/"+path] {
		return sha256hex(c)
	}
	return sha1hex(c)
}

func (h *Hub) record(r *http.Request, status int) {
	h.Log = append(h.Log, Request{Method: r.Method, Host: r.Host, Path: r.URL.Path, Range: r.Header.Get("Range"), Status: status})
}

// Requests returns the logged requests whose path contains substr.
func (h *Hub) Requests(substr string) []Request {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	var out []Request
	for _, r := range h.Log {
		if strings.Contains(r.Path, substr) {
			out = append(out, r)
		}
	}
	return out
}

func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	h.Mu.Lock()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/models/"):
		h.serveAPI(w, r, strings.TrimPrefix(p, "/api/models/"))
	case strings.HasPrefix(p, "/cdn/"):
		h.serveCDN(w, r, strings.TrimPrefix(p, "/cdn/"))
	case strings.Contains(p, "/resolve/"):
		h.serveResolve(w, r)
	default:
		h.record(r, 404)
		h.Mu.Unlock()
		http.NotFound(w, r)
	}
}

func (h *Hub) authorized(w http.ResponseWriter, r *http.Request) bool {
	if h.NeedToken && r.Header.Get("Authorization") != "Bearer good-token" {
		h.record(r, 401)
		h.Mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func (h *Hub) serveAPI(w http.ResponseWriter, r *http.Request, rest string) {
	if !h.authorized(w, r) {
		return
	}
	defer h.Mu.Unlock()
	parts := strings.Split(rest, "/")
	if len(parts) < 4 {
		h.record(r, 404)
		http.NotFound(w, r)
		return
	}
	repo := parts[0] + "/" + parts[1]
	files, ok := h.repos[repo]
	if !ok {
		h.record(r, 404)
		http.NotFound(w, r)
		return
	}
	h.record(r, 200)
	switch parts[2] {
	case "revision":
		json.NewEncoder(w).Encode(map[string]string{"sha": h.Commit})
	case "tree":
		var paths []string
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var out []map[string]any
		for _, p := range paths {
			e := map[string]any{"type": "file", "path": p, "size": len(files[p]), "oid": sha1hex(files[p])}
			if h.lfs[repo+"/"+p] {
				e["size"] = 134
				e["lfs"] = map[string]any{"oid": sha256hex(files[p]), "size": len(files[p])}
			}
			out = append(out, e)
		}
		out = append(out, map[string]any{"type": "directory", "path": "somedir"})
		json.NewEncoder(w).Encode(out)
	}
}

func (h *Hub) serveResolve(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}
	defer h.Mu.Unlock()
	// /<org>/<name>/resolve/<commit>/<path...>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 5)
	repo, path := parts[0]+"/"+parts[1], parts[4]
	c, ok := h.repos[repo][path]
	if !ok || parts[3] != h.Commit {
		h.record(r, 404)
		http.NotFound(w, r)
		return
	}
	etag := h.etag(repo, path)
	if h.WrongETag[path] {
		etag = strings.Repeat("f", len(etag))
	}
	if h.lfs[repo+"/"+path] {
		h.sig++
		w.Header().Set("X-Linked-Etag", `"`+etag+`"`)
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(c)))
		w.Header().Set("Location", fmt.Sprintf("%s/cdn/%s/%s?sig=%d", h.Server.URL, repo, path, h.sig))
		h.record(r, 302)
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	h.record(r, 200)
	http.ServeContent(w, r, path, time.Time{}, strings.NewReader(string(c)))
}

func (h *Hub) serveCDN(w http.ResponseWriter, r *http.Request, rest string) {
	if r.Header.Get("Authorization") != "" {
		h.t.Errorf("the token was sent to the CDN")
	}
	parts := strings.SplitN(rest, "/", 3)
	repo, path := parts[0]+"/"+parts[1], parts[2]
	c := h.repos[repo][path]
	if h.ExpireNext > 0 {
		h.ExpireNext--
		h.record(r, 403)
		h.Mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
		return
	}
	start := int64(0)
	if rg := r.Header.Get("Range"); rg != "" {
		fmt.Sscanf(rg, "bytes=%d-", &start)
	}
	cut, hasCut := h.CutAfter[path]
	delete(h.CutAfter, path)
	stall, hasStall := h.StallAt[path]
	delete(h.StallAt, path)
	status := http.StatusOK
	if start > 0 {
		status = http.StatusPartialContent
	}
	if h.IgnoreRange[path] && start > 0 {
		delete(h.IgnoreRange, path)
		start, status = 0, http.StatusPartialContent
	}
	h.record(r, status)
	h.Mu.Unlock()

	body := c[start:]
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if status == http.StatusPartialContent {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(c)-1, len(c)))
	}
	w.WriteHeader(status)
	switch {
	case hasCut:
		w.Write(body[:cut-start])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	case hasStall:
		w.Write(body[:stall-start])
		w.(http.Flusher).Flush()
		close(h.Stalled)
		<-r.Context().Done()
		panic(http.ErrAbortHandler)
	default:
		w.Write(body)
	}
}

// URL is the Hub's endpoint.
func (h *Hub) URL() string { return h.Server.URL }
