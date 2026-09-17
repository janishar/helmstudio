package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

// installer/install.sh is the one command a studio author runs to install helm
// (docs/releasing.md, "Installing helm"). These tests run it against a release
// served from the test, as HELM_RELEASES_URL lets a mirror be, so nothing
// reaches GitHub.

// helmstudioHelm stands in for helm: what install.sh runs to check a binary is
// helmstudio's, and runs on this machine.
const helmstudioHelm = "#!/bin/sh\necho 'usage: helm <command> [arguments]'\necho '  validate <manifest.yaml>...   validate one or more studio manifests'\n"

// The platforms install.sh accepts are the platforms release-helm.yml releases.
func TestTheInstallerKnowsEveryPlatformHelmIsReleasedFor(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(root(t), "installer", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^PLATFORMS="([a-z0-9_ ]+)"$`).FindSubmatch(script)
	if m == nil {
		t.Fatal(`install.sh names no PLATFORMS="..."`)
	}
	targets, _ := releasedPlatforms(t)
	var released []string
	for _, target := range targets {
		released = append(released, strings.Replace(target, "/", "_", 1))
	}
	accepted := strings.Fields(string(m[1]))
	slices.Sort(released)
	slices.Sort(accepted)
	if !slices.Equal(accepted, released) {
		t.Errorf("install.sh accepts %v, and release-helm.yml releases %v", accepted, released)
	}
}

func TestTheInstallerInstallsAReleaseThatMatchesItsChecksum(t *testing.T) {
	plat := installerPlatform(t)
	rel := serveRelease(t, "9.8.7-rc.1", plat, helmstudioHelm, false)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	// An older helmstudio helm is replaced.
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(helmstudioHelm+"echo old\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runInstaller(t, home, "HELM_VERSION=9.8.7-rc.1", "HELM_RELEASES_URL="+rel.url, "HELM_INSTALL_DIR="+bin)
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(bin, "helm"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != helmstudioHelm {
		t.Errorf("installed helm is %q, want the release's", got)
	}
	if fi, _ := os.Stat(filepath.Join(bin, "helm")); fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed helm is not executable: %v", fi.Mode())
	}
	if !strings.Contains(out, "installed helm 9.8.7-rc.1 at "+bin+"/helm") {
		t.Errorf("install.sh does not say what it installed:\n%s", out)
	}
	assertNothingLeftBehind(t, bin)
}

func TestTheInstallerRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	plat := installerPlatform(t)
	rel := serveRelease(t, "9.8.7", plat, helmstudioHelm, true)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")

	out, err := runInstaller(t, home, "HELM_VERSION=9.8.7", "HELM_RELEASES_URL="+rel.url, "HELM_INSTALL_DIR="+bin)
	if err == nil {
		t.Fatalf("install.sh installed an archive that does not match SHA256SUMS:\n%s", out)
	}
	if !strings.Contains(out, "does not match its checksum in SHA256SUMS; nothing was installed") {
		t.Errorf("install.sh does not say why it refused:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(bin, "helm")); err == nil {
		t.Error("install.sh refused the archive, and left a helm behind")
	}
	assertNothingLeftBehind(t, bin)
}

// A version becomes part of a URL and a path, so anything else is refused
// before a request is made.
func TestTheInstallerRefusesSomethingThatIsNotAVersion(t *testing.T) {
	plat := installerPlatform(t)
	rel := serveRelease(t, "9.8.7", plat, helmstudioHelm, false)
	for _, v := range []string{"9.8.7/../../../x", "9.8.7;touch x", "latest", "9.8"} {
		home := t.TempDir()
		out, err := runInstaller(t, home, "HELM_VERSION="+v, "HELM_RELEASES_URL="+rel.url, "HELM_INSTALL_DIR="+filepath.Join(home, "bin"))
		if err == nil || !strings.Contains(out, "is not a version") {
			t.Errorf("HELM_VERSION=%q: err = %v, want it refused as not a version:\n%s", v, err, out)
		}
	}
	if n := rel.requests.Load(); n != 0 {
		t.Errorf("install.sh made %d requests for versions it refused", n)
	}
}

// Kubernetes' CLI is also called helm; install.sh never overwrites it.
func TestTheInstallerLeavesAnotherHelmAlone(t *testing.T) {
	plat := installerPlatform(t)
	rel := serveRelease(t, "9.8.7", plat, helmstudioHelm, false)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	other := "#!/bin/sh\necho 'The Kubernetes package manager'\n"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(other), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runInstaller(t, home, "HELM_VERSION=9.8.7", "HELM_RELEASES_URL="+rel.url, "HELM_INSTALL_DIR="+bin)
	if err == nil || !strings.Contains(out, "is another program called helm") {
		t.Fatalf("err = %v, want install.sh to refuse to replace another helm:\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(bin, "helm")); string(got) != other {
		t.Errorf("install.sh changed the other helm: %q", got)
	}
	if n := rel.requests.Load(); n != 0 {
		t.Errorf("install.sh downloaded %d files it would not install", n)
	}
}

// uninstaller.sh removes the helm install.sh installed, and what an interrupted
// install left beside it.
func TestTheUninstallerRemovesHelmstudiosHelm(t *testing.T) {
	needBash(t)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	leftover := filepath.Join(bin, ".helm-install.AbC123")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "helm_9.8.7_darwin_arm64.tar.gz"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(helmstudioHelm), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runUninstaller(t, home, "HELM_INSTALL_DIR="+bin)
	if err != nil {
		t.Fatalf("uninstaller.sh: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(bin, "helm")); err == nil {
		t.Errorf("uninstaller.sh left helm in %s:\n%s", bin, out)
	}
	if !strings.Contains(out, "removed "+bin+"/helm") {
		t.Errorf("uninstaller.sh does not say what it removed:\n%s", out)
	}
	assertNothingLeftBehind(t, bin)
}

// Kubernetes' CLI is also called helm, and install.sh never makes a link:
// uninstaller.sh removes neither.
func TestTheUninstallerLeavesAnotherHelmAlone(t *testing.T) {
	needBash(t)
	t.Run("another program", func(t *testing.T) {
		home := t.TempDir()
		bin := filepath.Join(home, "bin")
		other := "#!/bin/sh\necho 'The Kubernetes package manager'\n"
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(other), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := runUninstaller(t, home, "HELM_INSTALL_DIR="+bin)
		if err == nil || !strings.Contains(out, "is another program called helm") {
			t.Errorf("err = %v, want uninstaller.sh to refuse another helm:\n%s", err, out)
		}
		if got, _ := os.ReadFile(filepath.Join(bin, "helm")); string(got) != other {
			t.Errorf("uninstaller.sh changed the other helm: %q", got)
		}
	})
	t.Run("a link", func(t *testing.T) {
		home := t.TempDir()
		bin := filepath.Join(home, "bin")
		clone := filepath.Join(home, "helmstudio", "bin", "helm")
		for _, dir := range []string{bin, filepath.Dir(clone)} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(clone, []byte(helmstudioHelm), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(clone, filepath.Join(bin, "helm")); err != nil {
			t.Fatal(err)
		}
		out, err := runUninstaller(t, home, "HELM_INSTALL_DIR="+bin)
		if err == nil || !strings.Contains(out, "is a link") {
			t.Errorf("err = %v, want uninstaller.sh to leave a link alone:\n%s", err, out)
		}
		if _, err := os.Lstat(filepath.Join(bin, "helm")); err != nil {
			t.Errorf("uninstaller.sh removed the link: %v", err)
		}
		if _, err := os.Stat(clone); err != nil {
			t.Errorf("uninstaller.sh removed what the link points at: %v", err)
		}
	})
}

func TestTheUninstallerHasNothingToRemove(t *testing.T) {
	needBash(t)
	home := t.TempDir()
	out, err := runUninstaller(t, home, "HELM_INSTALL_DIR="+filepath.Join(home, "bin"))
	if err != nil || !strings.Contains(out, "nothing to remove") {
		t.Errorf("err = %v, want uninstaller.sh to succeed and say there is nothing to remove:\n%s", err, out)
	}
}

// What install.sh puts in a directory, uninstaller.sh takes out again.
func TestInstallingThenUninstallingLeavesTheDirectoryAsItWas(t *testing.T) {
	plat := installerPlatform(t)
	rel := serveRelease(t, "9.8.7", plat, helmstudioHelm, false)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "other-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if out, err := runInstaller(t, home, "HELM_VERSION=9.8.7", "HELM_RELEASES_URL="+rel.url, "HELM_INSTALL_DIR="+bin); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if out, err := runUninstaller(t, home, "HELM_INSTALL_DIR="+bin); err != nil {
		t.Fatalf("uninstaller.sh: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"other-tool"}) {
		t.Errorf("%s holds %v after installing and uninstalling, want only what was there before", bin, names)
	}
}

// installerPlatform is this machine as install.sh names it, if helm is released for it.
func installerPlatform(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"bash", "curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("HELM_ALLOW_MISSING_CLIENTS") == "" {
				t.Fatalf("%s is not installed, so install.sh cannot be run; install it, or set HELM_ALLOW_MISSING_CLIENTS=1 to skip on purpose", tool)
			}
			t.Skipf("%s is not installed; skipped because HELM_ALLOW_MISSING_CLIENTS is set", tool)
		}
	}
	// The operating system comes from internal/platform, the one place that may decide it.
	plat := platform.Name + "_" + runtime.GOARCH
	targets, _ := releasedPlatforms(t)
	if !slices.Contains(targets, platform.Name+"/"+runtime.GOARCH) {
		t.Skipf("helm is not released for %s, so install.sh refuses this machine", plat)
	}
	return plat
}

type release struct {
	url      string
	requests atomic.Int64
}

// serveRelease serves helm_<version>_<plat>.tar.gz and SHA256SUMS as a GitHub
// Release lays them out, with a checksum for other bytes when tampered is set.
func serveRelease(t *testing.T, version, plat, helm string, tampered bool) *release {
	t.Helper()
	dir := fmt.Sprintf("helm_%s_%s", version, plat)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{dir + "/helm", helm, 0o755}, {dir + "/LICENSE", "MIT\n", 0o644}} {
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
	sums := fmt.Sprintf("%x  %s.tar.gz\n", sha256.Sum256(summed), dir)

	rel := &release{}
	files := map[string][]byte{
		"/v" + version + "/" + dir + ".tar.gz": archive,
		"/v" + version + "/SHA256SUMS":         []byte(sums),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel.requests.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	rel.url = srv.URL
	return rel
}

// runInstaller runs install.sh with only PATH and HOME from this process, so
// no HELM_* variable of the person running the tests reaches it.
func runInstaller(t *testing.T, home string, env ...string) (string, error) {
	t.Helper()
	return runScript(t, "install.sh", os.Getenv("PATH"), home, env...)
}

// runUninstaller runs uninstaller.sh with a PATH of the system's directories
// only, so no helm the person running the tests installed is found on it.
func runUninstaller(t *testing.T, home string, env ...string) (string, error) {
	t.Helper()
	return runScript(t, "uninstaller.sh", "/usr/bin:/bin", home, env...)
}

func runScript(t *testing.T, script, path, home string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root(t), "installer", script))
	cmd.Dir = home
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + home}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func needBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		if os.Getenv("HELM_ALLOW_MISSING_CLIENTS") == "" {
			t.Fatal("bash is not installed, so the installer's scripts cannot be run; install it, or set HELM_ALLOW_MISSING_CLIENTS=1 to skip on purpose")
		}
		t.Skip("bash is not installed; skipped because HELM_ALLOW_MISSING_CLIENTS is set")
	}
}

func assertNothingLeftBehind(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".helm-install.") {
			t.Errorf("%s was left behind in %s", e.Name(), dir)
		}
	}
}
