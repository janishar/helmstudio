package weights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the only host helmstudio asks for weights. File bytes
// come from wherever it redirects a resolve request to — a signed CDN URL on
// a Hugging Face host — and only over https.
const DefaultEndpoint = "https://huggingface.co"

var (
	// ErrAuthRequired means huggingface.co itself refused the request (401 or
	// 403): a gated or private repo, or a missing or wrong token. It is never
	// returned for a 403 from a signed download URL, which only means the URL
	// expired (docs/design/01-prd.md R15, R18).
	ErrAuthRequired = errors.New("Hugging Face needs a token for this repository")
	// ErrNotFound means the repo, revision or file does not exist.
	ErrNotFound = errors.New("not found on Hugging Face")
	// ErrVerify means a finished file did not match its listing.
	ErrVerify = errors.New("downloaded file does not match the repository listing")
)

// RemoteFile is one file of a repository listing at a pinned commit.
type RemoteFile struct {
	Path string
	Size int64
	// ETag identifies the content: the LFS sha256 for a large file, the git
	// blob id otherwise. It is what the resolve endpoint reports as
	// X-Linked-Etag or ETag for the same file.
	ETag string
}

// Observed is what the server reported about a file as it was downloaded.
type Observed struct {
	Size int64
	ETag string
}

// Verifier decides whether a finished download is the file the listing
// named. The default is size and etag (VerifySizeAndETag). Weight
// verification is an open decision — SHA256 behind an explicit Verify action
// is the other option (docs/decisions.md) — and this is the seam a hashing
// verifier plugs into, given the finished file's path.
type Verifier func(want RemoteFile, got Observed, file string) error

// VerifySizeAndETag compares the bytes on disk and the etag the server
// reported against the listing. It reads no file content.
func VerifySizeAndETag(want RemoteFile, got Observed, _ string) error {
	if got.Size != want.Size {
		return fmt.Errorf("%w: %s is %d bytes, the listing says %d", ErrVerify, want.Path, got.Size, want.Size)
	}
	if want.ETag != "" && normalizeETag(got.ETag) != normalizeETag(want.ETag) {
		return fmt.Errorf("%w: %s has etag %q, the listing says %q", ErrVerify, want.Path, got.ETag, want.ETag)
	}
	return nil
}

func normalizeETag(e string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(e), "W/"), `"`)
}

// HF talks to the Hugging Face Hub. The zero value is not usable; use NewHF.
type HF struct {
	// Endpoint is DefaultEndpoint unless a test points it elsewhere. An http
	// endpoint (a test server) also allows http redirects; an https one
	// never follows a redirect to plain http.
	Endpoint string
	// Token returns the Hugging Face token, or "" to ask anonymously.
	Token func(ctx context.Context) (string, error)
	// Client makes every request. Redirects are handled here, not by it, so
	// the token never reaches a CDN and a 403 can be told apart by host.
	Client *http.Client
	// Backoff is the wait before each retry of a network error or a 5xx,
	// reset whenever bytes arrive. After the last entry the download fails
	// and stays resumable.
	Backoff []time.Duration
	// MaxReresolves bounds consecutive 403s from signed URLs with no bytes
	// received in between, so a CDN that refuses every fresh URL is reported
	// rather than retried forever.
	MaxReresolves int
	// Verify checks each finished file. Default VerifySizeAndETag.
	Verify Verifier
	// Sleep waits d or until ctx ends. Tests shorten it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewHF returns a client for DefaultEndpoint with the documented defaults.
func NewHF(token func(context.Context) (string, error)) *HF {
	return &HF{Endpoint: DefaultEndpoint, Token: token}
}

func (c *HF) endpoint() string {
	if c.Endpoint == "" {
		return DefaultEndpoint
	}
	return strings.TrimSuffix(c.Endpoint, "/")
}

func (c *HF) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return defaultClient
}

// defaultClient has no overall timeout: a 20 GB file takes as long as it
// takes, and a stalled connection surfaces through the transport's limits.
var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ForceAttemptHTTP2:     true,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c *HF) backoff() []time.Duration {
	if len(c.Backoff) == 0 {
		return []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}
	}
	return c.Backoff
}

func (c *HF) maxReresolves() int {
	if c.MaxReresolves == 0 {
		return 8
	}
	return c.MaxReresolves
}

func (c *HF) verify() Verifier {
	if c.Verify == nil {
		return VerifySizeAndETag
	}
	return c.Verify
}

func (c *HF) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// repoPath escapes "org/name" one segment at a time.
func repoPath(repo string) string {
	parts := strings.Split(repo, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func (c *HF) newRequest(ctx context.Context, method, rawURL string, auth bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "helmstudio")
	if auth && c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading the Hugging Face token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	return req, nil
}

// apiGet fetches a JSON document from the Hub API, retrying transient
// failures with the backoff.
func (c *HF) apiGet(ctx context.Context, rawURL string, what string, into any) (http.Header, error) {
	attempt := 0
	for {
		req, err := c.newRequest(ctx, http.MethodGet, rawURL, true)
		if err != nil {
			return nil, err
		}
		resp, err := c.client().Do(req)
		if err == nil {
			switch {
			case resp.StatusCode == http.StatusOK:
				defer resp.Body.Close()
				if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
					return nil, fmt.Errorf("%s: reading the response: %w", what, err)
				}
				return resp.Header, nil
			case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
				resp.Body.Close()
				return nil, fmt.Errorf("%s: %s answered %d: %w", what, c.endpoint(), resp.StatusCode, ErrAuthRequired)
			case resp.StatusCode == http.StatusNotFound:
				resp.Body.Close()
				return nil, fmt.Errorf("%s: %w", what, ErrNotFound)
			case resp.StatusCode == http.StatusTooManyRequests:
				wait := retryAfter(resp.Header)
				resp.Body.Close()
				if err := c.sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			case resp.StatusCode >= 500:
				err = fmt.Errorf("%s answered %d", c.endpoint(), resp.StatusCode)
				resp.Body.Close()
			default:
				resp.Body.Close()
				return nil, fmt.Errorf("%s: %s answered %d", what, c.endpoint(), resp.StatusCode)
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt >= len(c.backoff()) {
			return nil, fmt.Errorf("%s: giving up after %d attempts: %w", what, attempt+1, err)
		}
		if err := c.sleep(ctx, c.backoff()[attempt]); err != nil {
			return nil, err
		}
		attempt++
	}
}

// retryAfter reads a 429's Retry-After in seconds, defaulting to five and
// capping at five minutes.
func retryAfter(h http.Header) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, 5*time.Minute)
	}
	return 5 * time.Second
}

// Resolve returns the commit a revision points at now. Downloads pin to it,
// so a branch that moves mid-download never mixes two versions of a file.
func (c *HF) Resolve(ctx context.Context, repo, revision string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	u := fmt.Sprintf("%s/api/models/%s/revision/%s", c.endpoint(), repoPath(repo), url.PathEscape(revision))
	if _, err := c.apiGet(ctx, u, fmt.Sprintf("resolving %s at %s", repo, revision), &out); err != nil {
		return "", err
	}
	if out.SHA == "" {
		return "", fmt.Errorf("resolving %s at %s: the response names no commit", repo, revision)
	}
	return out.SHA, nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// List returns every file of a repository at a commit.
func (c *HF) List(ctx context.Context, repo, commit string) ([]RemoteFile, error) {
	type entry struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		OID  string `json:"oid"`
		LFS  *struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"lfs"`
	}
	next := fmt.Sprintf("%s/api/models/%s/tree/%s?recursive=true", c.endpoint(), repoPath(repo), url.PathEscape(commit))
	var files []RemoteFile
	for next != "" {
		var page []entry
		h, err := c.apiGet(ctx, next, fmt.Sprintf("listing %s at %s", repo, commit), &page)
		if err != nil {
			return nil, err
		}
		for _, e := range page {
			if e.Type != "file" {
				continue
			}
			if !safeRelPath(e.Path) {
				return nil, fmt.Errorf("listing %s at %s: refusing file path %q, which is not inside the repository", repo, commit, e.Path)
			}
			f := RemoteFile{Path: e.Path, Size: e.Size, ETag: e.OID}
			if e.LFS != nil {
				f.Size, f.ETag = e.LFS.Size, e.LFS.OID
			}
			files = append(files, f)
		}
		next = ""
		if m := linkNext.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
			if !strings.HasPrefix(next, c.endpoint()+"/") {
				return nil, fmt.Errorf("listing %s: the next page points at %q, not %s", repo, next, c.endpoint())
			}
		}
	}
	return files, nil
}

// safeRelPath accepts a slash-separated relative path with no empty, "." or
// ".." segment.
func safeRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// partFile is where Download writes. The caller owns where it lives (always
// inside the models root, through os.Root) and what happens once it is
// complete.
type partFile interface {
	// Size is the current length of the partial file, 0 when absent.
	Size() (int64, error)
	// Open opens the partial file for appending, truncating it first when
	// truncate is set.
	Open(truncate bool) (io.WriteCloser, error)
	// Path is the partial file's path, for a Verifier that reads it.
	Path() string
}

// Download fetches f into part, resuming from part's current length. It
// returns once part holds the whole file and it has been verified; it does
// not rename it into place.
//
// Every attempt resolves the file afresh at huggingface.co, which redirects
// large files to a signed CDN URL. A 403 from that URL means it expired and
// is answered by resolving again and continuing from the same offset — never
// by starting over. A 401 or 403 from huggingface.co itself is
// ErrAuthRequired. Bytes already on disk are appended to only when the server
// confirms, through Content-Range, that it is sending exactly the next byte
// of the file of the listed size; the URL is pinned to a commit, so the
// content at that offset cannot have changed.
func (c *HF) Download(ctx context.Context, repo, commit string, f RemoteFile, part partFile, onBytes func(int64)) error {
	resolveURL := fmt.Sprintf("%s/%s/resolve/%s/%s", c.endpoint(), repoPath(repo), url.PathEscape(commit), escapePath(f.Path))
	what := fmt.Sprintf("%s: %s", repo, f.Path)
	transient, reresolves, verifyFailures := 0, 0, 0
	var etag string
	// matchETag checks the etag the Hub reports for the file against the
	// listing before any byte is written. The URL is pinned to a commit, so
	// the content cannot differ from the listing: a mismatch means the etag
	// the Hub reports is not the one the listing carries, and downloading
	// would only fail the same way at the end. Nothing is written or
	// truncated.
	matchETag := func(got string) error {
		if f.ETag == "" || normalizeETag(got) == normalizeETag(f.ETag) {
			return nil
		}
		return fmt.Errorf("%w: %s: the Hub reports etag %q and the listing says %q; nothing was downloaded, and the partial file, if any, is untouched", ErrVerify, what, got, f.ETag)
	}

	retry := func(err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if transient >= len(c.backoff()) {
			return fmt.Errorf("%s: giving up after %d attempts; the partial file is kept and Retry resumes it: %w", what, transient+1, err)
		}
		d := c.backoff()[transient]
		transient++
		return c.sleep(ctx, d)
	}

	for {
		off, err := part.Size()
		if err != nil {
			return err
		}
		if off > f.Size {
			// Longer than the file can be: nothing in it can be trusted.
			w, err := part.Open(true)
			if err != nil {
				return err
			}
			w.Close()
			off = 0
		}

		if off == f.Size {
			// Complete on disk. Ask only for the etag, without a body.
			if etag == "" {
				if etag, err = c.headETag(ctx, resolveURL, what); err != nil {
					if errors.Is(err, ErrAuthRequired) || errors.Is(err, ErrNotFound) || ctx.Err() != nil {
						return err
					}
					if rerr := retry(err); rerr != nil {
						return rerr
					}
					continue
				}
			}
			if err := matchETag(etag); err != nil {
				return err
			}
			verr := c.verify()(f, Observed{Size: off, ETag: etag}, part.Path())
			if verr == nil {
				return nil
			}
			if verifyFailures++; verifyFailures > 1 {
				return verr
			}
			// Fetch this one file again, once.
			w, err := part.Open(true)
			if err != nil {
				return err
			}
			w.Close()
			etag = ""
			continue
		}

		req, err := c.newRequest(ctx, http.MethodGet, resolveURL, true)
		if err != nil {
			return err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		resp, err := c.client().Do(req)
		if err != nil {
			if rerr := retry(err); rerr != nil {
				return rerr
			}
			continue
		}

		var body *http.Response
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			resp.Body.Close()
			return fmt.Errorf("%s: %s answered %d: %w", what, c.endpoint(), resp.StatusCode, ErrAuthRequired)
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return fmt.Errorf("%s: %w", what, ErrNotFound)
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(resp.Header)
			resp.Body.Close()
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		case resp.StatusCode >= 500:
			resp.Body.Close()
			if rerr := retry(fmt.Errorf("%s answered %d", c.endpoint(), resp.StatusCode)); rerr != nil {
				return rerr
			}
			continue
		case isRedirect(resp.StatusCode):
			etag = headerETag(resp.Header)
			loc, lerr := resp.Location()
			resp.Body.Close()
			if err := matchETag(etag); err != nil {
				return err
			}
			if lerr != nil {
				return fmt.Errorf("%s: the redirect has no usable location: %w", what, lerr)
			}
			if loc.Scheme != "https" && !(loc.Scheme == "http" && strings.HasPrefix(c.endpoint(), "http://")) {
				return fmt.Errorf("%s: refusing to download over %s from %s", what, loc.Scheme, loc.Host)
			}
			creq, err := c.newRequest(ctx, http.MethodGet, loc.String(), false)
			if err != nil {
				return err
			}
			creq.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
			cresp, err := c.client().Do(creq)
			if err != nil {
				if rerr := retry(err); rerr != nil {
					return rerr
				}
				continue
			}
			switch {
			case cresp.StatusCode == http.StatusForbidden || cresp.StatusCode == http.StatusUnauthorized:
				// An expired signed URL, not corruption and not auth.
				cresp.Body.Close()
				if reresolves++; reresolves > c.maxReresolves() {
					return fmt.Errorf("%s: %s refused %d fresh download URLs in a row without sending a byte; the partial file is kept and Retry resumes it", what, loc.Host, reresolves-1)
				}
				continue
			case cresp.StatusCode >= 500 || cresp.StatusCode == http.StatusTooManyRequests:
				cresp.Body.Close()
				if rerr := retry(fmt.Errorf("%s answered %d", loc.Host, cresp.StatusCode)); rerr != nil {
					return rerr
				}
				continue
			case cresp.StatusCode != http.StatusOK && cresp.StatusCode != http.StatusPartialContent:
				cresp.Body.Close()
				return fmt.Errorf("%s: %s answered %d", what, loc.Host, cresp.StatusCode)
			}
			body = cresp
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent:
			etag = headerETag(resp.Header)
			if err := matchETag(etag); err != nil {
				resp.Body.Close()
				return err
			}
			body = resp
		default:
			resp.Body.Close()
			return fmt.Errorf("%s: %s answered %d", what, c.endpoint(), resp.StatusCode)
		}

		truncate := false
		if body.StatusCode == http.StatusOK {
			// The server ignored the range and is sending the whole file.
			truncate = off > 0
			off = 0
		} else if err := checkContentRange(body.Header.Get("Content-Range"), off, f.Size); err != nil {
			body.Body.Close()
			return fmt.Errorf("%s: not appending to the partial file: %w", what, err)
		}
		w, err := part.Open(truncate)
		if err != nil {
			body.Body.Close()
			return err
		}
		n, copyErr := io.Copy(writeTagger{w}, &countingReader{r: io.LimitReader(body.Body, f.Size-off), on: onBytes})
		body.Body.Close()
		closeErr := w.Close()
		if closeErr != nil {
			return fmt.Errorf("%s: writing the partial file: %w", what, closeErr)
		}
		if n > 0 {
			transient, reresolves = 0, 0
		}
		if copyErr == nil && n == 0 {
			copyErr = errors.New("the server sent no bytes")
		}
		if copyErr != nil {
			if isWriteError(copyErr) {
				return fmt.Errorf("%s: writing the partial file: %w", what, copyErr)
			}
			if rerr := retry(copyErr); rerr != nil {
				return rerr
			}
			continue
		}
		// Loop: the size check at the top verifies a complete file or
		// resumes a short one.
	}
}

func (c *HF) headETag(ctx context.Context, resolveURL, what string) (string, error) {
	req, err := c.newRequest(ctx, http.MethodHead, resolveURL, true)
	if err != nil {
		return "", err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("%s: %s answered %d: %w", what, c.endpoint(), resp.StatusCode, ErrAuthRequired)
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("%s: %w", what, ErrNotFound)
	case resp.StatusCode == http.StatusOK || isRedirect(resp.StatusCode):
		return headerETag(resp.Header), nil
	}
	return "", fmt.Errorf("%s: %s answered %d", what, c.endpoint(), resp.StatusCode)
}

func isRedirect(code int) bool {
	return code == http.StatusFound || code == http.StatusMovedPermanently || code == http.StatusTemporaryRedirect || code == http.StatusPermanentRedirect || code == http.StatusSeeOther
}

// headerETag prefers X-Linked-Etag, which the Hub sets on a redirect to the
// LFS object and which carries its sha256.
func headerETag(h http.Header) string {
	if e := h.Get("X-Linked-Etag"); e != "" {
		return e
	}
	return h.Get("ETag")
}

func checkContentRange(v string, off, size int64) error {
	var start, end int64
	var total string
	if _, err := fmt.Sscanf(v, "bytes %d-%d/%s", &start, &end, &total); err != nil {
		return fmt.Errorf("unreadable Content-Range %q", v)
	}
	if start != off {
		return fmt.Errorf("asked for bytes from %d, the server sent from %d", off, start)
	}
	if total != "*" && total != strconv.FormatInt(size, 10) {
		return fmt.Errorf("the server's file is %s bytes, the listing says %d", total, size)
	}
	return nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// writeTagger marks errors from the destination, so a full disk is reported
// rather than retried as if the network had dropped.
type writeTagger struct{ w io.Writer }

func (t writeTagger) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil {
		err = writeError{err}
	}
	return n, err
}

type writeError struct{ err error }

func (e writeError) Error() string { return e.err.Error() }
func (e writeError) Unwrap() error { return e.err }

func isWriteError(err error) bool {
	var we writeError
	return errors.As(err, &we)
}

// countingReader reports bytes as they arrive.
type countingReader struct {
	r  io.Reader
	on func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.on != nil {
		c.on(int64(n))
	}
	return n, err
}
