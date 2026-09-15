package embedded

import (
	"io"
	"net/http"
	"sync"
)

// Transport serves requests by calling h in process: no socket, no port. The
// response body is a pipe, so a byte range streams and an event stream stays
// open for as long as the handler writes to it.
func Transport(h http.Handler) http.RoundTripper { return roundTripper{h: h} }

type roundTripper struct{ h http.Handler }

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	go func() {
		defer func() {
			w.WriteHeader(http.StatusOK)
			pw.Close()
		}()
		if req.Body == nil {
			req.Body = http.NoBody
		}
		rt.h.ServeHTTP(w, req)
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
	}
	return &http.Response{
		Status:        http.StatusText(w.status),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.sent,
		Body:          pr,
		ContentLength: -1,
		Request:       req,
	}, nil
}

type pipeWriter struct {
	header http.Header
	sent   http.Header
	status int
	pw     *io.PipeWriter
	once   sync.Once
	ready  chan struct{}
}

func (w *pipeWriter) Header() http.Header { return w.header }

func (w *pipeWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		w.sent = w.header.Clone()
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

// Flush satisfies http.Flusher; a pipe has nothing buffered.
func (w *pipeWriter) Flush() {}
