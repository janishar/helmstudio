package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	helmcss "github.com/janishar/helmstudio/packages/helm-css"
	helmruntimenode "github.com/janishar/helmstudio/packages/helm-runtime-sdk/node"
)

// The theme and the SDK files (docs/decisions.md M6 Q8, Q9, Q14):
//
//	GET  /api/v1/launcher/settings/theme   launcher: {theme}
//	PUT  /api/v1/launcher/settings/theme   launcher: {theme} → {theme}
//	GET  /api/v1/theme                     tokenless: {theme}
//	GET  /api/v1/theme/events              tokenless: SSE, theme events
//	GET  /sdk/v1/<file>                    static helm-css and the browser runtime
//
// The last three are the only routes exempt from the Origin check: a studio's
// page on its own port reaches them, and they carry nothing but the theme and
// public files.

// SDKPrefix is where the helm packages are served.
const SDKPrefix = "/sdk/v1/"

func (s *Server) routeTheme() {
	s.mux.HandleFunc("GET "+SDKPrefix+"{file...}", s.serveSDK)
	if s.theme == nil {
		return
	}
	s.mux.HandleFunc("GET "+Base+"/launcher/settings/theme", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"theme": s.theme.Hub.Current()})
	})
	s.mux.HandleFunc("PUT "+Base+"/launcher/settings/theme", s.putTheme)
	s.mux.HandleFunc("GET "+Base+"/theme", s.theme.Hub.ServeCurrent)
	s.mux.HandleFunc("GET "+Base+"/theme/events", s.theme.Hub.ServeEvents)
}

func (s *Server) putTheme(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Theme string `json:"theme"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body must be {\"theme\": \"system\" | \"light\" | \"dark\"}")
		return
	}
	if err := s.theme.Set(r.Context(), body.Theme); err != nil {
		if body.Theme != "system" && body.Theme != "light" && body.Theme != "dark" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_theme", fmt.Sprintf("theme %q is not one of system, light, dark", body.Theme))
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"theme": body.Theme})
}

// originExempt reports whether r is one of the GETs a studio's page may make
// from its own origin.
func originExempt(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	return strings.HasPrefix(p, SDKPrefix) || p == Base+"/theme" || p == Base+"/theme/events"
}

// sdkFile finds a served file: helm-css at the root, the browser runtime
// under runtime/, and helm-runtime.js re-exporting it (04 §8).
func sdkFile(name string) (fs.FS, string, bool) {
	switch {
	case name == "helm-runtime.js":
		return nil, "", true
	case strings.HasPrefix(name, "runtime/"):
		src, ok := helmruntimenode.BrowserFiles[strings.TrimPrefix(name, "runtime/")]
		return helmruntimenode.Browser, src, ok
	case slicesContains(helmcss.Layers, name) || slicesContains(helmcss.Derived, name):
		return helmcss.Files, name, true
	case strings.HasPrefix(name, "fonts/") && path.Ext(name) == ".woff2", name == "fonts/LICENSE.txt":
		return helmcss.Files, name, true
	}
	return nil, "", false
}

const runtimeShim = "// helm-runtime.js: the browser build of @helmstudio/runtime (docs/design/04-packages.md §8).\nexport * from \"./runtime/browser.js\";\n"

var sdkTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
}

func (s *Server) serveSDK(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	fsys, src, ok := sdkFile(name)
	if !ok || name != path.Clean(name) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no SDK file %q is served", name))
		return
	}
	var body []byte
	if fsys == nil {
		body = []byte(runtimeShim)
	} else {
		b, err := fs.ReadFile(fsys, src)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no SDK file %q is served", name))
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		body = b
	}
	w.Header().Set("Content-Type", sdkTypes[path.Ext(name)])
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
