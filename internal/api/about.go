package api

import (
	"net/http"
	"runtime/debug"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/theme"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// About is what a daemon knows about itself for Settings' This Mac and About
// sections (03 §12a): its directories, and the version a release stamped.
type About struct {
	Dirs    *platform.Dirs
	Version string
}

// WithAbout serves GET /launcher/settings/about.
func WithAbout(a About) Option {
	return func(s *Server) { s.about = &a }
}

// aboutDoc is api/openapi.yaml's LauncherAbout.
type aboutDoc struct {
	DaemonVersion string    `json:"daemon_version"`
	Commit        string    `json:"commit,omitempty"`
	Modified      bool      `json:"modified,omitempty"`
	APIVersion    string    `json:"api_version"`
	SDKMajors     sdkMajors `json:"sdk_majors"`
	Paths         aboutDirs `json:"paths"`
}

type sdkMajors struct {
	CSS     int `json:"css"`
	Runtime int `json:"runtime"`
	UI      int `json:"ui"`
}

type aboutDirs struct {
	Data    string `json:"data"`
	Cache   string `json:"cache"`
	Logs    string `json:"logs"`
	Models  string `json:"models"`
	Library string `json:"library"`
}

func (s *Server) routeAbout() {
	s.mux.HandleFunc("GET "+Base+"/launcher/settings/about", s.getAbout)
}

func (s *Server) getAbout(w http.ResponseWriter, r *http.Request) {
	if s.about == nil || s.about.Dirs == nil {
		writeError(w, http.StatusNotImplemented, "unsupported", "this daemon was started without its directories")
		return
	}
	d := s.about.Dirs
	doc := aboutDoc{
		DaemonVersion: s.about.Version,
		APIVersion:    helm.APIVersion,
		// One major serves all three packages (04 §9), and it is the one a
		// manifest's sdk pins are checked against at launch.
		SDKMajors: sdkMajors{CSS: theme.ServedMajor, Runtime: theme.ServedMajor, UI: theme.ServedMajor},
		Paths:     aboutDirs{Data: d.Data(), Cache: d.Cache(), Logs: d.Logs(), Models: d.Models(), Library: d.Library()},
	}
	if doc.DaemonVersion == "" {
		doc.DaemonVersion = "dev"
	}
	doc.Commit, doc.Modified = buildCommit()
	writeJSON(w, http.StatusOK, doc)
}

// buildCommit is the commit the Go toolchain recorded when this binary was
// built from a checkout, and whether the tree had changes it does not hold.
func buildCommit() (commit string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, kv := range info.Settings {
		switch kv.Key {
		case "vcs.revision":
			commit = kv.Value
		case "vcs.modified":
			modified = kv.Value == "true"
		}
	}
	return commit, modified
}
