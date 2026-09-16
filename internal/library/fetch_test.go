package library

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Until M9's cookie any local process can reach the daemon, so an
// unrestricted fetcher would be a request-forgery gadget: it would turn
// helmstudio into a proxy onto every loopback service and the whole LAN, with
// the daemon's own network position. Each case below is one way in.
func TestAnImportURLCannotReachWhereItShouldNot(t *testing.T) {
	f := NewFetcher()
	// A resolver that answers with whatever the host name says, so no DNS is
	// needed and the cases are exact.
	f.Resolve = func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "public.example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "sneaky.example.com":
			// One public answer and one loopback: every address has to be
			// checked, not just the first.
			return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}, nil
		case "metadata.example.com":
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		case "intranet.example.com":
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	for _, c := range []struct {
		name, url string
		refused   bool
		because   string
	}{
		{"plain http", "http://public.example.com/m.yaml", true, "https"},
		{"a file url", "file:///etc/passwd", true, "https"},
		{"credentials in the url", "https://user:pw@public.example.com/m.yaml", true, "credentials"},
		{"loopback by name", "https://localhost/m.yaml", true, ""},
		{"loopback by address", "https://127.0.0.1:9200/m.yaml", true, "this machine"},
		{"ipv6 loopback", "https://[::1]/m.yaml", true, "this machine"},
		{"the cloud metadata address", "https://metadata.example.com/latest/meta-data/", true, "link-local"},
		{"a private address", "https://intranet.example.com/m.yaml", true, "private"},
		{"a host with one loopback answer", "https://sneaky.example.com/m.yaml", true, "this machine"},
		{"an ordinary public url", "https://public.example.com/m.yaml", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.CheckURL(context.Background(), c.url)
			if c.refused && err == nil {
				t.Fatalf("%s should be refused", c.url)
			}
			if !c.refused && err != nil {
				t.Fatalf("%s should be allowed: %v", c.url, err)
			}
			if c.because != "" && !strings.Contains(err.Error(), c.because) {
				t.Errorf("the refusal should say why (%q): %v", c.because, err)
			}
		})
	}
}

// tlsFetcher serves over real TLS, so the https-only rule stays in force in
// these tests rather than being switched off for them. AllowPrivate is the one
// concession: httptest listens on 127.0.0.1, which the rules refuse by design,
// and nothing in the daemon sets it.
func tlsFetcher(srv *httptest.Server) *Fetcher {
	return &Fetcher{AllowPrivate: true, Transport: srv.Client().Transport}
}

// A public URL that redirects to somewhere private is how every check above
// gets bypassed, so a redirect elsewhere is refused rather than followed.
func TestARedirectElsewhereIsRefused(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("id: sneaky\n"))
	}))
	defer target.Close()

	var origin *httptest.Server
	origin = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/elsewhere":
			http.Redirect(w, r, target.URL+"/m.yaml", http.StatusFound)
		case "/here":
			http.Redirect(w, r, origin.URL+"/final", http.StatusFound)
		default:
			_, _ = w.Write([]byte("id: fine\n"))
		}
	}))
	defer origin.Close()

	// Both servers must be reachable, or this test passes for the wrong
	// reason: an untrusted certificate on the redirect target would fail the
	// fetch before the redirect rule ever ran, and removing that rule would
	// not be noticed. Verification is off here so that what is under test is
	// the rule and nothing else.
	f := &Fetcher{AllowPrivate: true, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	_, err := f.Fetch(context.Background(), origin.URL+"/elsewhere")
	if err == nil {
		t.Fatal("a redirect to another host should be refused")
	}
	if !strings.Contains(err.Error(), "somewhere else") {
		t.Errorf("the refusal should say why: %v", err)
	}

	// A redirect within the same host is ordinary, and is followed.
	got, err := f.Fetch(context.Background(), origin.URL+"/here")
	if err != nil {
		t.Fatalf("a same-host redirect is ordinary: %v", err)
	}
	if !strings.Contains(got, "id: fine") {
		t.Errorf("got %q", got)
	}
}

// A manifest is a page of YAML. The limit stops a URL being used to pull
// anything substantial through the daemon.
func TestOversizeAndBinaryAreRefusedWithoutEchoingThem(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			_, _ = w.Write([]byte{0x00, 0x01, 0x02, 'S', 'E', 'C', 'R', 'E', 'T'})
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", MaxManifestBytes+100)))
	}))
	defer srv.Close()
	f := tlsFetcher(srv)

	_, err := f.Fetch(context.Background(), srv.URL+"/big")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("an oversize document should be refused: %v", err)
	}

	_, err = f.Fetch(context.Background(), srv.URL+"/binary")
	if err == nil {
		t.Fatal("binary should be refused")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("the response was echoed back in the error, which is a read primitive with extra steps: %v", err)
	}
}

// The ordinary case still works.
func TestFetchingAnOrdinaryManifest(t *testing.T) {
	const body = "id: wan-studio\nname: wan studio\n"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := tlsFetcher(srv).Fetch(context.Background(), srv.URL+"/m.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Errorf("got %q", got)
	}
}
