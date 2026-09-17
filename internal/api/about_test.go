package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/theme"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Settings' This Mac and About (03 §12a) are served, not written into the
// page: the directories are the ones this daemon was started with, and the
// SDK majors are the ones a manifest's sdk pins are checked against.
func TestAboutIsWhatThisDaemonWasStartedWith(t *testing.T) {
	d := platformtest.Dirs(t)
	sup := supervisor.New(supervisor.Config{Dirs: d})
	srv, err := New(sup, fstest.MapFS{}, addr, t.Logf, WithAbout(About{Dirs: d, Version: "1.2.0"}))
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, "GET", Base+"/launcher/settings/about", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET about: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		DaemonVersion string         `json:"daemon_version"`
		APIVersion    string         `json:"api_version"`
		SDKMajors     map[string]int `json:"sdk_majors"`
		Paths         map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DaemonVersion != "1.2.0" || got.APIVersion != helm.APIVersion {
		t.Errorf("version %q, api %q; want 1.2.0 and %s", got.DaemonVersion, got.APIVersion, helm.APIVersion)
	}
	for _, pkg := range []string{"css", "runtime", "ui"} {
		if got.SDKMajors[pkg] != theme.ServedMajor {
			t.Errorf("sdk_majors[%s] = %d, want the served major %d", pkg, got.SDKMajors[pkg], theme.ServedMajor)
		}
	}
	for key, want := range map[string]string{"data": d.Data(), "cache": d.Cache(), "logs": d.Logs(), "models": d.Models(), "library": d.Library()} {
		if got.Paths[key] != want {
			t.Errorf("paths.%s = %q, want %q", key, got.Paths[key], want)
		}
	}
	// And nothing about a token or a secret: this is read by a page.
	if body := rec.Body.String(); strings.Contains(strings.ToLower(body), "token") {
		t.Errorf("about mentions a token: %s", body)
	}
}

// A daemon started without its directories says it cannot answer, rather
// than answering with empty paths a person would copy.
func TestAboutWithoutDirectoriesIsUnsupported(t *testing.T) {
	sup := supervisor.New(supervisor.Config{Dirs: platformtest.Dirs(t)})
	srv, err := New(sup, fstest.MapFS{}, addr, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, "GET", Base+"/launcher/settings/about", nil)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), `"unsupported"`) {
		t.Fatalf("GET about with no directories: %d %s", rec.Code, rec.Body)
	}
}
