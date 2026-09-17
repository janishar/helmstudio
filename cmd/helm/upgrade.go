package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/platform"
)

// helm upgrade replaces the running helm with a newer release, with the checks
// installer/install.sh makes on a first install (docs/releasing.md, "Installing
// helm"): HTTPS only, the release's SHA256SUMS, build provenance when the
// GitHub CLI is signed in, and a binary that names the version it was
// downloaded as. It reaches the network only because it was asked to.

const (
	releasesRepo       = "janishar/helmstudio"
	defaultReleasesURL = "https://github.com/" + releasesRepo + "/releases/download"
	defaultAPIURL      = "https://api.github.com/repos/" + releasesRepo
	// Far larger than a helm archive; a download past it is not one.
	maxDownloadBytes = 256 << 20
)

// releasedPlatforms are the platforms .github/workflows/release-helm.yml
// builds; a test holds them together.
var releasedPlatforms = []string{"darwin_arm64", "linux_amd64", "linux_arm64"}

// A version is only digits, letters, dots and hyphens, so it cannot change a URL or a path.
var semverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

type upgradeOptions struct {
	current     string // the version the running helm was built as
	exe         string // the running helm's path
	want        string // --version, or "" for the newest release
	releasesURL string // laid out as a GitHub Release
	mirror      bool   // releasesURL is not GitHub's: plain HTTP is allowed, and provenance is not checked
	apiURL      string // the repository in GitHub's API
	client      *http.Client
	provenance  func(archive string) (checked bool, err error)
	stdout      io.Writer
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	want := fs.String("version", "", "install `version`, such as 1.0.0, instead of the newest release")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: helm upgrade [--version <version>]

Replaces this helm with the newest release of helm, or with --version. The
archive is checked against the release's SHA256SUMS, and against its build
provenance when the GitHub CLI is signed in. HELM_RELEASES_URL downloads from a
mirror laid out as the release is, without the provenance check.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "helm upgrade: finding this helm: %v\n", err)
		return 1
	}
	o := upgradeOptions{
		current: version, exe: exe, want: *want,
		releasesURL: defaultReleasesURL, apiURL: defaultAPIURL,
		provenance: ghProvenance, stdout: stdout,
	}
	if mirror := os.Getenv("HELM_RELEASES_URL"); mirror != "" {
		o.releasesURL, o.mirror = mirror, true
	}
	o.client = upgradeClient(o.mirror)
	if err := upgrade(context.Background(), o); err != nil {
		fmt.Fprintf(stderr, "helm upgrade: %v\n", err)
		return 1
	}
	return 0
}

// upgradeClient follows GitHub's redirects to where a release's files are
// kept, and only over HTTPS unless the person named a mirror.
func upgradeClient(mirror bool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if !mirror && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing a redirect to %s, which is not HTTPS", req.URL.Redacted())
			}
			return nil
		},
	}
}

func upgrade(ctx context.Context, o upgradeOptions) error {
	say := func(format string, a ...any) { fmt.Fprintf(o.stdout, "helm upgrade: "+format+"\n", a...) }
	if o.current == "dev" || !semverPattern.MatchString(o.current) {
		return errors.New("this helm was built from a clone, not installed from a release: update the clone and build it again, or install a release with installer/install.sh")
	}
	plat := platform.Name + "_" + runtime.GOARCH
	if !slices.Contains(releasedPlatforms, plat) {
		return fmt.Errorf("helm is released for %s, and this machine is %s", strings.Join(releasedPlatforms, ", "), plat)
	}
	target, explicit := strings.TrimPrefix(o.want, "v"), o.want != ""
	if !explicit {
		newest, err := newestRelease(ctx, o)
		if err != nil {
			return err
		}
		target = newest
	}
	if !semverPattern.MatchString(target) {
		return fmt.Errorf("%q is not a version, such as 1.0.0", target)
	}
	switch c := compareVersions(target, o.current); {
	case c == 0:
		say("helm %s is already installed at %s", o.current, o.exe)
		return nil
	case c < 0 && !explicit:
		say("helm %s is newer than the newest release, %s; nothing to do", o.current, target)
		return nil
	}

	fi, err := os.Lstat(o.exe)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a link, which the installer never makes; upgrade helm with whatever made it", o.exe)
	}
	work, err := makeWorkDir(filepath.Dir(o.exe))
	if err != nil {
		return fmt.Errorf("cannot write beside %s, so helm cannot be replaced there: %w", o.exe, err)
	}
	defer os.RemoveAll(work)

	archive := fmt.Sprintf("helm_%s_%s.tar.gz", target, plat)
	base := strings.TrimSuffix(o.releasesURL, "/") + "/v" + target
	say("downloading helm %s for %s from %s", target, plat, base)
	archivePath := filepath.Join(work, archive)
	if err := download(ctx, o.client, base+"/"+archive, archivePath); err != nil {
		return err
	}
	sumsPath := filepath.Join(work, "SHA256SUMS")
	if err := download(ctx, o.client, base+"/SHA256SUMS", sumsPath); err != nil {
		return err
	}
	expected, err := checksumFor(sumsPath, archive)
	if err != nil {
		return err
	}
	actual, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if expected != actual {
		return fmt.Errorf("%s does not match its checksum in SHA256SUMS; helm was not changed", archive)
	}
	say("the archive matches SHA256SUMS")

	switch {
	case o.mirror:
		say("provenance not checked: the archive came from HELM_RELEASES_URL")
	default:
		checked, err := o.provenance(archivePath)
		if err != nil {
			return err
		}
		if checked {
			say("the archive was built by %s's release workflow", releasesRepo)
		} else {
			say("provenance not checked: with the GitHub CLI signed in, gh attestation verify checks it")
		}
	}

	bin, err := extractHelm(archivePath, fmt.Sprintf("helm_%s_%s/helm", target, plat), work)
	if err != nil {
		return err
	}
	if err := checkHelm(ctx, bin, archive, target); err != nil {
		return err
	}
	if err := os.Rename(bin, o.exe); err != nil {
		return fmt.Errorf("replacing %s: %w", o.exe, err)
	}
	say("replaced helm %s with helm %s at %s", o.current, target, o.exe)
	return nil
}

// newestRelease is the newest release that is not a pre-release or, while
// there is none, the newest pre-release, as installer/install.sh picks it.
func newestRelease(ctx context.Context, o upgradeOptions) (string, error) {
	var latest struct {
		Tag string `json:"tag_name"`
	}
	found, err := getJSON(ctx, o.client, o.apiURL+"/releases/latest", &latest)
	if err != nil {
		return "", err
	}
	if found {
		return tagVersion(latest.Tag)
	}
	var list []struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
	}
	if _, err := getJSON(ctx, o.client, o.apiURL+"/releases?per_page=30", &list); err != nil {
		return "", err
	}
	for _, r := range list {
		if !r.Draft && strings.HasPrefix(r.Tag, "v") {
			return tagVersion(r.Tag)
		}
	}
	return "", errors.New("helmstudio has not released helm yet")
}

func tagVersion(tag string) (string, error) {
	v := strings.TrimPrefix(tag, "v")
	if !semverPattern.MatchString(v) {
		return "", fmt.Errorf("the newest release is tagged %q, which is not a version", tag)
	}
	return v, nil
}

// getJSON decodes url's JSON into out; found is false for a 404.
func getJSON(ctx context.Context, c *http.Client, url string, out any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return false, fmt.Errorf("listing helmstudio's releases: %w; set --version to install a version", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("listing helmstudio's releases: %s; set --version to install a version", resp.Status)
	}
	return true, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func download(ctx context.Context, c *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("downloading %s: larger than any helm release", url)
	}
	return nil
}

func checksumFor(sumsPath, archive string) (string, error) {
	b, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[1] == archive || fields[1] == "*"+archive) {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no checksum for %s; helm was not changed", archive)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractHelm unpacks only name, the binary, so nothing else in an archive
// lands anywhere.
func extractHelm(archivePath, name, dir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("unpacking %s: %w", filepath.Base(archivePath), err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%s holds no %s; helm was not changed", filepath.Base(archivePath), name)
		}
		if err != nil {
			return "", fmt.Errorf("unpacking %s: %w", filepath.Base(archivePath), err)
		}
		if h.Name != name || h.Typeflag != tar.TypeReg {
			continue
		}
		out := filepath.Join(dir, "helm")
		w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxDownloadBytes+1))
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", err
		}
		if n > maxDownloadBytes {
			return "", fmt.Errorf("%s in %s is larger than any helm", name, filepath.Base(archivePath))
		}
		return out, nil
	}
}

// checkHelm runs the new binary: it must name the version it was downloaded
// as, or, for a release from before helm had --version, print helm's usage.
func checkHelm(ctx context.Context, bin, archive, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "--version").Output()
	first, _, _ := strings.Cut(string(out), "\n")
	if strings.HasPrefix(first, "helm ") {
		if first == "helm "+target || strings.HasPrefix(first, "helm "+target+" ") {
			return nil
		}
		return fmt.Errorf("the helm in %s says it is %s, not helm %s; helm was not changed", archive, first, target)
	}
	usage, _ := exec.CommandContext(ctx, bin).CombinedOutput()
	if strings.Contains(string(usage), "studio manifests") {
		return nil
	}
	return fmt.Errorf("the helm in %s does not run on this machine; helm was not changed", archive)
}

// makeWorkDir makes a download directory beside helm, so replacing helm is a
// rename, named as installer/install.sh names its own, so
// installer/uninstaller.sh finds one an interrupted upgrade left.
func makeWorkDir(dir string) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for range 10 {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
		path := filepath.Join(dir, ".helm-install."+string(b))
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("could not name a download directory")
}

// ghProvenance checks an archive's build provenance with the GitHub CLI, when
// it is installed and signed in.
func ghProvenance(archive string) (bool, error) {
	gh, err := exec.LookPath("gh")
	if err != nil || exec.Command(gh, "auth", "status").Run() != nil {
		return false, nil
	}
	if err := exec.Command(gh, "attestation", "verify", archive, "--repo", releasesRepo).Run(); err != nil {
		return false, fmt.Errorf("%s has no attestation from %s's release workflow; helm was not changed", filepath.Base(archive), releasesRepo)
	}
	return true, nil
}

// compareVersions orders two semantic versions: a release after its
// pre-releases, and pre-release identifiers numerically where both are numbers.
func compareVersions(a, b string) int {
	ar, ap, _ := strings.Cut(a, "-")
	br, bp, _ := strings.Cut(b, "-")
	as, bs := strings.Split(ar, "."), strings.Split(br, ".")
	for i := 0; i < 3 && i < len(as) && i < len(bs); i++ {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return compareInts(x, y)
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	ai, bi := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		x, xerr := strconv.Atoi(ai[i])
		y, yerr := strconv.Atoi(bi[i])
		switch {
		case xerr == nil && yerr == nil:
			if x != y {
				return compareInts(x, y)
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(ai[i], bi[i]); c != 0 {
				return c
			}
		}
	}
	return compareInts(len(ai), len(bi))
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
