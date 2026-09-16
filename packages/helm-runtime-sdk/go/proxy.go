package helm

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Environment variables the daemon injects at spawn for a studio's page
// (docs/design/07-platform-services.md §3, docs/decisions.md M6 Q7, Q8, Q14).
const (
	EnvTheme       = "HELM_THEME"
	EnvAccentDark  = "HELM_ACCENT_DARK"
	EnvAccentLight = "HELM_ACCENT_LIGHT"
	EnvSDKBase     = "HELM_SDK_BASE"
)

// ProxyPrefix is where a studio mounts the proxy on its own server.
const ProxyPrefix = "/helm/"

// ProxyConfig is what the proxy forwards to. Empty API or SDKBase means that
// half answers 404, as it does standalone; an empty Token forwards without
// one (a studio with no capabilities has none).
type ProxyConfig struct {
	API         string // HELM_API, e.g. http://127.0.0.1:8700/api/v1
	Token       string // HELM_TOKEN
	SDKBase     string // HELM_SDK_BASE, e.g. http://127.0.0.1:8700/sdk/v1
	AccentDark  string // HELM_ACCENT_DARK
	AccentLight string // HELM_ACCENT_LIGHT
	// Client sends the forwarded requests; nil is a client with no timeout,
	// since event streams stay open.
	Client *http.Client
}

// ProxyFromEnv reads ProxyConfig from the variables the daemon and helm dev
// inject.
func ProxyFromEnv() ProxyConfig {
	return ProxyConfig{API: os.Getenv(EnvAPI), Token: os.Getenv(EnvToken), SDKBase: os.Getenv(EnvSDKBase),
		AccentDark: os.Getenv(EnvAccentDark), AccentLight: os.Getenv(EnvAccentLight)}
}

// Proxy is the same-origin proxy a studio mounts at /helm/ on its own server,
// so its page can use the platform API without ever holding a token
// (docs/decisions.md M6 Q10):
//
//	mux.Handle(helm.ProxyPrefix, helm.Proxy(helm.ProxyFromEnv()))
//
// It answers exactly three things:
//   - /helm/api/v1/<studio-api path> — forwarded to HELM_API with the studio's
//     token added. Only the studio API and the theme stream: a launcher path
//     (install, launch, stop, reclaim) is 404 and never reaches the daemon.
//   - /helm/sdk/v1/<file> — GET and HEAD, forwarded to HELM_SDK_BASE without a
//     token.
//   - /helm/accent.css — the studio's hue as --helm-studio-accent and
//     --helm-on-studio-accent for each theme (03 §2c).
//
// The page's own Authorization, cookies, Origin and Referer are never
// forwarded, and the daemon's Set-Cookie is never returned. The Python and
// Node runtime SDKs ship the same proxy; test/conformance holds all three to
// the same behaviour.
func Proxy(cfg ProxyConfig) http.Handler {
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	return &proxy{cfg: cfg}
}

type proxy struct{ cfg ProxyConfig }

// ProxyStudioSegments are the first path segments under /api/v1/ the proxy
// forwards: every studio-api operation's, and the tokenless theme stream's.
var ProxyStudioSegments = map[string]bool{
	"me": true, "events": true, "kv": true, "sessions": true, "records": true,
	"assets": true, "assets:adopt": true, "gallery": true, "handoff": true,
	"inbox": true, "jobs": true, "theme": true,
	"timeline": true, "timeline:append": true,
}

// Request headers a page may send through; everything else is dropped.
var proxyRequestHeaders = []string{"Accept", "Content-Type", "Range", "If-Range", "If-Match", "If-None-Match", "Last-Event-ID"}

// Response headers returned to the page.
var proxyResponseHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Cache-Control", "Content-Disposition", "Allow"}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), strings.TrimSuffix(ProxyPrefix, "/"))
	if !ok || !safePath(rest) {
		proxyNotFound(w, r)
		return
	}
	switch {
	case rest == "/accent.css":
		p.accent(w, r)
	case strings.HasPrefix(rest, "/api/v1/"):
		sub := strings.TrimPrefix(rest, "/api/v1/")
		first, _, _ := strings.Cut(sub, "/")
		if p.cfg.API == "" || !ProxyStudioSegments[first] {
			proxyNotFound(w, r)
			return
		}
		p.forward(w, r, strings.TrimRight(p.cfg.API, "/")+"/"+sub, p.cfg.Token)
	case strings.HasPrefix(rest, "/sdk/v1/"):
		if p.cfg.SDKBase == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			proxyNotFound(w, r)
			return
		}
		p.forward(w, r, strings.TrimRight(p.cfg.SDKBase, "/")+"/"+strings.TrimPrefix(rest, "/sdk/v1/"), "")
	default:
		proxyNotFound(w, r)
	}
}

// safePath refuses a path that could leave the prefix it was checked under
// once the upstream decodes it: dot segments, empty segments, and encoded
// slashes, backslashes or dots.
func safePath(p string) bool {
	lower := strings.ToLower(p)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%2e") || strings.Contains(p, "\\") {
		return false
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segs {
		if s == "." || s == ".." || (s == "" && i != len(segs)-1) {
			return false
		}
	}
	return true
}

func (p *proxy) forward(w http.ResponseWriter, r *http.Request, target, token string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		proxyError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" is not forwarded")
		return
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		body = r.Body
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		proxyError(w, http.StatusBadGateway, "proxy_error", "the request could not be forwarded")
		return
	}
	if body != nil {
		out.ContentLength = r.ContentLength
	}
	for _, h := range proxyRequestHeaders {
		if v := r.Header.Values(h); len(v) > 0 {
			out.Header[h] = append([]string(nil), v...)
		}
	}
	if token != "" {
		out.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := p.cfg.Client.Do(out)
	if err != nil {
		if r.Context().Err() == nil {
			// Never the error text: it can carry the upstream URL.
			proxyError(w, http.StatusServiceUnavailable, "unavailable", "helmstudio did not answer")
		}
		return
	}
	defer res.Body.Close()
	for _, h := range proxyResponseHeaders {
		if v := res.Header.Values(h); len(v) > 0 {
			w.Header()[h] = append([]string(nil), v...)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	rc := http.NewResponseController(w)
	buf := make([]byte, 32*1024)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush() // event streams arrive as they are written
		}
		if err != nil {
			return
		}
	}
}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (p *proxy) accent(w http.ResponseWriter, r *http.Request) {
	css, ok := AccentCSS(p.cfg.AccentDark, p.cfg.AccentLight)
	if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		proxyNotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, css)
	}
}

// AccentCSS is /helm/accent.css for a hue pair: --helm-studio-accent and
// --helm-on-studio-accent per theme, following data-theme the way
// helm-tokens.css does (03 §2c). ok is false unless both are "#rrggbb".
func AccentCSS(dark, light string) (string, bool) {
	if !hexColour.MatchString(dark) || !hexColour.MatchString(light) {
		return "", false
	}
	dark, light = strings.ToLower(dark), strings.ToLower(light)
	block := func(hex string) string {
		return "--helm-studio-accent: " + hex + "; --helm-on-studio-accent: " + onColour(hex) + ";"
	}
	return ":root { " + block(dark) + " }\n" +
		"@media (prefers-color-scheme: light) { :root:not([data-theme]) { " + block(light) + " } }\n" +
		":root[data-theme=\"light\"] { " + block(light) + " }\n", true
}

// onColour is #1a1400 or #ffffff, whichever contrasts more with hex.
func onColour(hex string) string {
	l := luminance(hex)
	dark := (l + 0.05) / (luminance("#1a1400") + 0.05)
	white := (1.05) / (l + 0.05)
	if dark >= white {
		return "#1a1400"
	}
	return "#ffffff"
}

func luminance(hex string) float64 {
	var rgb [3]float64
	for i := range rgb {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.04045 {
			rgb[i] = c / 12.92
		} else {
			rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}

func proxyNotFound(w http.ResponseWriter, r *http.Request) {
	proxyError(w, http.StatusNotFound, "not_found", "the helm proxy forwards only the studio API, the theme stream, the SDK files and accent.css")
}

func proxyError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
