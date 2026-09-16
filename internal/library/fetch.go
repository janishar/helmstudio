package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Fetching a manifest from a URL (docs/decisions.md M7 Q14).
//
// This is the one place the daemon fetches an address a request chose, and it
// is deliberately the most restricted thing in the codebase.
//
// The problem is that until M9's cookie, any local process can reach the
// daemon — including another account on this Mac (the M2 review recorded it).
// An unrestricted fetcher would be a request-forgery gadget: "import this
// manifest from http://127.0.0.1:9200/_cluster/settings" turns helmstudio into
// a proxy onto every loopback service and the whole LAN, with the daemon's own
// network position.
//
// So: https only, no redirect to another scheme or host, refused when the host
// resolves to a loopback, link-local, private or otherwise non-public address,
// at most 1 MiB, ten seconds, no cookies and no credentials. And the bytes are
// never echoed back as text when they are not a manifest, because an error
// message that quotes the response is a read primitive with extra steps.

const (
	// MaxManifestBytes is generous for a document that is a page of YAML, and
	// small enough that a URL cannot be used to pull anything substantial.
	MaxManifestBytes = 1 << 20
	// FetchTimeout is short: a manifest is one small file from a host that is
	// either there or is not.
	FetchTimeout = 10 * time.Second
)

// ErrRefusedAddress means the URL pointed somewhere the daemon will not go.
var ErrRefusedAddress = errors.New("refused address")

// Fetcher reads manifests from URLs under the rules above.
type Fetcher struct {
	// Resolve looks a host up. A seam, so tests can present an address without
	// needing DNS that answers.
	Resolve func(ctx context.Context, host string) ([]net.IP, error)
	// Transport is the HTTP transport, for tests that serve over a local
	// listener. Production leaves it nil.
	Transport http.RoundTripper
	// AllowPrivate is set only by tests, which necessarily serve from
	// 127.0.0.1. Nothing in the daemon sets it.
	AllowPrivate bool
}

// NewFetcher returns the fetcher the daemon uses.
func NewFetcher() *Fetcher { return &Fetcher{} }

// CheckURL applies every rule that can be applied before connecting.
func (f *Fetcher) CheckURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("that is not a URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: only https:// URLs can be imported, and this is %q. An http:// URL could be rewritten in transit by anything between here and there, and a manifest is a list of commands to run", ErrRefusedAddress, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: the URL carries credentials, which helmstudio will not send", ErrRefusedAddress)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: the URL names no host", ErrRefusedAddress)
	}
	if err := f.checkHost(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// checkHost refuses a host that resolves anywhere the daemon should not reach.
func (f *Fetcher) checkHost(ctx context.Context, host string) error {
	if f.AllowPrivate {
		return nil
	}
	resolve := f.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	// A literal address needs no lookup, and must be checked the same way.
	if ip := net.ParseIP(host); ip != nil {
		return refuseIP(ip)
	}
	ips, err := resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("%s could not be looked up: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s resolves to nothing", ErrRefusedAddress, host)
	}
	// Every address, not just the first: a host that answers with one public
	// and one loopback address must not be usable to reach the loopback one.
	for _, ip := range ips {
		if err := refuseIP(ip); err != nil {
			return err
		}
	}
	return nil
}

func refuseIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%w: %s is this machine. helmstudio will not fetch from its own loopback, because that would make it a way to reach every local service with the daemon's own access", ErrRefusedAddress, ip)
	case ip.IsPrivate():
		return fmt.Errorf("%w: %s is a private address on this network. helmstudio will not fetch from the local network", ErrRefusedAddress, ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("%w: %s is link-local, which on a cloud host is where the instance metadata service lives", ErrRefusedAddress, ip)
	case ip.IsUnspecified(), ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: %s is not an address a manifest can be fetched from", ErrRefusedAddress, ip)
	}
	return nil
}

// Fetch reads a manifest from a URL.
//
// A redirect to a different scheme or host is refused rather than followed: a
// public URL that redirects to 127.0.0.1 is precisely the bypass every one of
// the checks above exists to stop, and following it would make them decorative.
func (f *Fetcher) Fetch(ctx context.Context, raw string) (string, error) {
	u, err := f.CheckURL(ctx, raw)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	client := &http.Client{
		Transport: f.Transport,
		// Jar is deliberately nil: no cookies are sent, and none are kept.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			first := via[0].URL
			if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
				return fmt.Errorf("%w: %s redirects to %s, which is somewhere else. A URL that redirects elsewhere is how every check above gets bypassed", ErrRefusedAddress, first.Host, req.URL.Host)
			}
			return f.checkHost(cctx, req.URL.Hostname())
		},
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/yaml, application/yaml, text/plain")
	req.Header.Set("User-Agent", "helmstudio")

	res, err := client.Do(req)
	if err != nil {
		// url.Error wraps the CheckRedirect refusal; unwrap so the caller sees
		// the sentence rather than "Get ...: refused address: ...".
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, ErrRefusedAddress) {
			return "", ue.Err
		}
		return "", fmt.Errorf("%s could not be reached: %w", u.Host, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", u, res.Status)
	}

	// One byte over the limit is enough to know it is over.
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxManifestBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", u, err)
	}
	if len(body) > MaxManifestBytes {
		return "", fmt.Errorf("%s is larger than %d bytes; a manifest is a page of YAML", u, MaxManifestBytes)
	}
	if !isProbablyText(body) {
		// Never echoed back. An error that quotes the response body is a read
		// primitive with extra steps.
		return "", fmt.Errorf("%s did not return text, so it is not a manifest", u)
	}
	return string(body), nil
}

// isProbablyText rejects binary before it is treated as a document.
func isProbablyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b[:min(len(b), 512)] {
		if c == 0 {
			return false
		}
	}
	return !strings.HasPrefix(string(b), "\x1f\x8b") // gzip
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
