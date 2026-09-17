package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/janishar/helmstudio/internal/platform"
)

// fakeHelm stands in for a released helm: it names its version, and prints
// helm's usage. Without a version it is a release from before --version.
func fakeHelm(version string) string {
	script := "#!/bin/sh\n"
	if version != "" {
		script += "if [ \"$1\" = --version ]; then echo 'helm " + version + " (0123456789ab)'; exit 0; fi\n"
	}
	return script + "echo 'usage: helm <command> [arguments]'\necho '  validate <manifest.yaml>...   validate one or more studio manifests'\n"
}

// releases serves GitHub's API for the repository's releases, and the
// releases' files, as the test lays them out.
type releases struct {
	srv      *httptest.Server
	requests atomic.Int64
	latest   string   // the tag /releases/latest names; "" answers 404, as GitHub does with only pre-releases
	list     []string // the tags /releases lists, newest first
	files    map[string][]byte
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{files: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		switch req.URL.Path {
		case "/releases/latest":
			if r.latest == "" {
				http.NotFound(w, req)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": r.latest})
		case "/releases":
			var list []map[string]any
			for _, tag := range r.list {
				list = append(list, map[string]any{"tag_name": tag, "draft": false, "prerelease": true})
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			body, ok := r.files[req.URL.Path]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// thisPlatform is this machine as helm's release archives name it.
func thisPlatform(t *testing.T) string {
	t.Helper()
	// The operating system comes from internal/platform, the one place that may decide it.
	plat := platform.Name + "_" + runtime.GOARCH
	if !slices.Contains(releasedPlatforms, plat) {
		t.Skipf("helm is not released for %s", plat)
	}
	return plat
}

// publish lays out a release of helm for this machine, with a checksum for
// other bytes when tampered is set.
func (r *releases) publish(t *testing.T, version, helm string, tampered bool) {
	t.Helper()
	dir := fmt.Sprintf("helm_%s_%s", version, thisPlatform(t))
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{dir + "/LICENSE", "MIT\n", 0o644}, {dir + "/helm", helm, 0o755}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	summed := archive
	if tampered {
		summed = append(slices.Clone(archive), 0)
	}
	r.files["/v"+version+"/"+dir+".tar.gz"] = archive
	r.files["/v"+version+"/SHA256SUMS"] = []byte(fmt.Sprintf("%x  %s.tar.gz\n", sha256.Sum256(summed), dir))
}

// installedHelm is a helm at version in a directory of its own, as the installer leaves it.
func installedHelm(t *testing.T, version string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bin", "helm")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte(fakeHelm(version)), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// options upgrades exe, at current, from r, which stands in for GitHub's own
// releases unless mirror is set.
func (r *releases) options(exe, current, want string, out *bytes.Buffer) upgradeOptions {
	return upgradeOptions{
		current: current, exe: exe, want: want,
		releasesURL: r.srv.URL, apiURL: r.srv.URL, mirror: true,
		client: upgradeClient(true), stdout: out,
		provenance: func(string) (bool, error) {
			return false, errors.New("a mirror's archive is never checked for provenance")
		},
	}
}

func assertHelm(t *testing.T, exe, want string) {
	t.Helper()
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s is %q, want %q", exe, got, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range entries {
		if e.Name() != "helm" {
			t.Errorf("helm upgrade left %s beside helm", e.Name())
		}
	}
}

func TestUpgradeReplacesHelmWithTheNewestRelease(t *testing.T) {
	r := newReleases(t)
	r.latest = "v1.1.0"
	r.publish(t, "1.1.0", fakeHelm("1.1.0"), false)
	exe := installedHelm(t, "1.0.0")
	var out bytes.Buffer

	if err := upgrade(context.Background(), r.options(exe, "1.0.0", "", &out)); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}
	assertHelm(t, exe, fakeHelm("1.1.0"))
	for _, want := range []string{"the archive matches SHA256SUMS", "provenance not checked: the archive came from HELM_RELEASES_URL", "replaced helm 1.0.0 with helm 1.1.0 at " + exe} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("helm upgrade does not say %q:\n%s", want, out.String())
		}
	}
}

// While there are only pre-releases, GitHub has no latest release, and the
// newest pre-release is the newest release.
func TestUpgradeFindsTheNewestPreReleaseWhileThereIsNoRelease(t *testing.T) {
	r := newReleases(t)
	r.list = []string{"v1.1.0-rc.2", "v1.1.0-rc.1"}
	r.publish(t, "1.1.0-rc.2", fakeHelm("1.1.0-rc.2"), false)
	exe := installedHelm(t, "1.1.0-rc.1")
	var out bytes.Buffer

	if err := upgrade(context.Background(), r.options(exe, "1.1.0-rc.1", "", &out)); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}
	assertHelm(t, exe, fakeHelm("1.1.0-rc.2"))
}

func TestUpgradeChangesNothingWhenNoReleaseIsNewer(t *testing.T) {
	for _, c := range []struct{ current, latest, says string }{
		{"1.1.0", "v1.1.0", "helm 1.1.0 is already installed"},
		{"1.2.0-rc.1", "v1.1.0", "helm 1.2.0-rc.1 is newer than the newest release, 1.1.0; nothing to do"},
	} {
		r := newReleases(t)
		r.latest = c.latest
		exe := installedHelm(t, c.current)
		var out bytes.Buffer
		if err := upgrade(context.Background(), r.options(exe, c.current, "", &out)); err != nil {
			t.Fatalf("helm %s: upgrade: %v", c.current, err)
		}
		if !strings.Contains(out.String(), c.says) {
			t.Errorf("helm %s: helm upgrade said %q, want %q", c.current, out.String(), c.says)
		}
		assertHelm(t, exe, fakeHelm(c.current))
		if n := r.requests.Load(); n != 1 {
			t.Errorf("helm %s: helm upgrade made %d requests, want only the one that found the newest release", c.current, n)
		}
	}
}

// --version installs that version, an older one included, without asking
// GitHub for the newest; 1.0.0-rc.1 was released before helm had --version.
func TestUpgradeInstallsTheVersionItIsAskedFor(t *testing.T) {
	r := newReleases(t)
	r.publish(t, "1.0.0-rc.1", fakeHelm(""), false)
	exe := installedHelm(t, "1.1.0")
	var out bytes.Buffer

	if err := upgrade(context.Background(), r.options(exe, "1.1.0", "v1.0.0-rc.1", &out)); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}
	assertHelm(t, exe, fakeHelm(""))
	if n := r.requests.Load(); n != 2 {
		t.Errorf("helm upgrade made %d requests, want the archive and SHA256SUMS only", n)
	}
}

func TestUpgradeRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	r := newReleases(t)
	r.latest = "v1.1.0"
	r.publish(t, "1.1.0", fakeHelm("1.1.0"), true)
	exe := installedHelm(t, "1.0.0")

	err := upgrade(context.Background(), r.options(exe, "1.0.0", "", &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "does not match its checksum in SHA256SUMS; helm was not changed") {
		t.Errorf("err = %v, want a checksum refusal", err)
	}
	assertHelm(t, exe, fakeHelm("1.0.0"))
}

func TestUpgradeRefusesAHelmThatNamesAnotherVersion(t *testing.T) {
	r := newReleases(t)
	r.latest = "v1.1.0"
	r.publish(t, "1.1.0", fakeHelm("1.2.3"), false)
	exe := installedHelm(t, "1.0.0")

	err := upgrade(context.Background(), r.options(exe, "1.0.0", "", &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "says it is helm 1.2.3 (0123456789ab), not helm 1.1.0; helm was not changed") {
		t.Errorf("err = %v, want a refusal naming both versions", err)
	}
	assertHelm(t, exe, fakeHelm("1.0.0"))
}

// From GitHub itself, an archive without this repository's provenance is
// refused when the GitHub CLI could check it.
func TestUpgradeChecksProvenanceOutsideAMirror(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		r := newReleases(t)
		r.latest = "v1.1.0"
		r.publish(t, "1.1.0", fakeHelm("1.1.0"), false)
		exe := installedHelm(t, "1.0.0")
		o := r.options(exe, "1.0.0", "", &bytes.Buffer{})
		o.mirror = false
		o.provenance = func(string) (bool, error) { return false, errors.New("helm_1.1.0.tar.gz has no attestation") }
		if err := upgrade(context.Background(), o); err == nil || !strings.Contains(err.Error(), "no attestation") {
			t.Errorf("err = %v, want the provenance refusal", err)
		}
		assertHelm(t, exe, fakeHelm("1.0.0"))
	})
	t.Run("checked", func(t *testing.T) {
		r := newReleases(t)
		r.latest = "v1.1.0"
		r.publish(t, "1.1.0", fakeHelm("1.1.0"), false)
		exe := installedHelm(t, "1.0.0")
		var out bytes.Buffer
		o := r.options(exe, "1.0.0", "", &out)
		o.mirror = false
		o.provenance = func(string) (bool, error) { return true, nil }
		if err := upgrade(context.Background(), o); err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		if !strings.Contains(out.String(), "the archive was built by janishar/helmstudio's release workflow") {
			t.Errorf("helm upgrade does not say it checked provenance:\n%s", out.String())
		}
	})
}

// A helm built from a clone, a helm reached through a link and a --version
// that is not a version are refused before anything is downloaded.
func TestUpgradeRefusesBeforeDownloading(t *testing.T) {
	r := newReleases(t)
	r.latest = "v1.1.0"
	r.publish(t, "1.1.0", fakeHelm("1.1.0"), false)

	dev := installedHelm(t, "")
	if err := upgrade(context.Background(), r.options(dev, "dev", "", &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "built from a clone") {
		t.Errorf("a dev build: err = %v, want it refused", err)
	}

	target := installedHelm(t, "1.0.0")
	link := filepath.Join(t.TempDir(), "helm")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(context.Background(), r.options(link, "1.0.0", "1.1.0", &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "is a link") {
		t.Errorf("a link: err = %v, want it refused", err)
	}
	assertHelm(t, target, fakeHelm("1.0.0"))

	for _, want := range []string{"1.1.0/../../x", "latest", "1.1"} {
		exe := installedHelm(t, "1.0.0")
		if err := upgrade(context.Background(), r.options(exe, "1.0.0", want, &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "is not a version") {
			t.Errorf("--version %s: err = %v, want it refused", want, err)
		}
	}
	if n := r.requests.Load(); n != 0 {
		t.Errorf("helm upgrade made %d requests for upgrades it refused", n)
	}
}

// A helm built as release-helm.yml builds one upgrades itself through the
// command a person runs: the binary replaced is the one running the upgrade.
func TestHelmUpgradeReplacesTheRunningBinary(t *testing.T) {
	thisPlatform(t)
	if testing.Short() {
		t.Skip("builds helm twice")
	}
	build := func(version string) []byte {
		out := filepath.Join(t.TempDir(), "helm")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -X main.version="+version, "-o", out, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building helm %s: %v\n%s", version, err, b)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	r := newReleases(t)
	r.publish(t, "9.9.1", string(build("9.9.1")), false)
	exe := filepath.Join(t.TempDir(), "bin", "helm")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, build("9.9.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe, "upgrade", "--version", "9.9.1")
	cmd.Env = append(os.Environ(), "HELM_RELEASES_URL="+r.srv.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helm upgrade: %v\n%s", err, out)
	}
	out, err := exec.Command(exe, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "helm 9.9.1") {
		t.Errorf("after the upgrade, helm --version = %q, %v; want helm 9.9.1", out, err)
	}
}

// The platforms helm upgrade accepts are the platforms release-helm.yml releases.
func TestUpgradeKnowsEveryPlatformHelmIsReleasedFor(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-helm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`for target in ([a-z0-9/ ]+); do`).FindSubmatch(b)
	if m == nil {
		t.Fatal("release-helm.yml names no platforms to build")
	}
	var built []string
	for _, target := range strings.Fields(string(m[1])) {
		built = append(built, strings.Replace(target, "/", "_", 1))
	}
	slices.Sort(built)
	if accepted := slices.Sorted(slices.Values(releasedPlatforms)); !slices.Equal(accepted, built) {
		t.Errorf("helm upgrade accepts %v, and release-helm.yml releases %v", accepted, built)
	}
}

func TestCompareVersions(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-rc.1", "1.0.0-rc.2", "1.0.0-rc.10", "1.0.0", "1.0.1", "1.10.0", "2.0.0",
	}
	for i, a := range ordered {
		for j, b := range ordered {
			want := compareInts(i, j)
			if got := compareVersions(a, b); got != want {
				t.Errorf("compareVersions(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
}
