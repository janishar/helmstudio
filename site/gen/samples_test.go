package gen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every sample on every page is a file, and the gate runs it or the page says
// why not (docs/decisions.md M10 Q9).
//
// runs names the test that runs each sample. A sample a page marks not-run is
// absent; every other sample is here, and the tests below take their work from
// this table, so a sample added to it is run rather than listed.
var runs = map[string]string{
	"quickstart/02-environment.sh": "TestTheQuickstartRunsAsWritten",
	"quickstart/05-run.sh":         "TestTheQuickstartRunsAsWritten",
	"quickstart/05-output.txt":     "TestTheQuickstartRunsAsWritten",
	"quickstart/06-make.sh":        "TestTheQuickstartRunsAsWritten",
	"quickstart/07-items.sh":       "TestTheQuickstartRunsAsWritten",
	"quickstart/08-validate.sh":    "TestTheQuickstartRunsAsWritten",
	"hello-studio/helmstudio.yaml": "TestTheQuickstartRunsAsWritten",
	"hello-studio/studio.py":       "TestTheQuickstartRunsAsWritten",

	"record/record.py":     "TestThePythonSamplesRun",
	"sessions/sessions.py": "TestThePythonSamplesRun",
	"timeline/timeline.py": "TestThePythonSamplesRun",

	"theming/helmstudio.yaml":    "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/server.py":          "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/go/main.go":         "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/node/server.mjs":    "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/index.html":         "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/studio.css":         "TestTheThemingStudioServesWhatItsPageLinks",
	"theming/lint.sh":            "TestTheThemingStudioServesWhatItsPageLinks",
	"providers/embedded/main.go": "TestTheEmbeddedProviderSampleRecords",

	"wrap/helmstudio.yaml":        "TestTheManifestSamplesValidate",
	"groups/helmstudio.yaml":      "TestTheManifestSamplesValidate",
	"publishing/tern-studio.yaml": "TestTheManifestSamplesValidate",
	"wrap/validate.sh":            "TestTheManifestSamplesValidate",

	"install/which-helm.sh": "TestTheInstallSamplesRun",
}

// A missing interpreter or tool fails, unless skipping was asked for, as the
// conformance suite and the media tests do.
const (
	envAllowMissingClients = "HELM_ALLOW_MISSING_CLIENTS"
	envAllowMissingFFmpeg  = "HELM_ALLOW_MISSING_FFMPEG"
)

func need(t *testing.T, tool, allow string) string {
	t.Helper()
	p, err := exec.LookPath(tool)
	if err == nil {
		return p
	}
	if os.Getenv(allow) == "" {
		t.Fatalf("%s is not installed, so the samples that need it cannot run; install it, or set %s=1 to skip on purpose", tool, allow)
	}
	t.Skipf("%s is not installed; skipped because %s is set", tool, allow)
	return ""
}

func samplesRunBy(test string) []string {
	var out []string
	for path, by := range runs {
		if by == test {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func TestEverySampleIsOnAPageAndRunOrMarked(t *testing.T) {
	res, _ := buildSite(t, "/")
	marks := map[string]map[bool][]string{} // sample -> marked not-run? -> pages
	for _, p := range res.Pages {
		for _, s := range p.Samples {
			if marks[s.Path] == nil {
				marks[s.Path] = map[bool][]string{}
			}
			marks[s.Path][s.NotRun != ""] = append(marks[s.Path][s.NotRun != ""], p.URL)
		}
	}
	samples := filepath.Join(siteDir, "samples")
	err := filepath.WalkDir(samples, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(samples, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(filepath.Base(rel), ".") || strings.Contains(rel, "__pycache__") {
			return nil
		}
		if marks[rel] == nil {
			t.Errorf("samples/%s is on no page", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, m := range marks {
		_, ran := runs[path]
		switch {
		case len(m[true]) > 0 && len(m[false]) > 0:
			t.Errorf("%s is marked not-run on %v and not on %v", path, m[true], m[false])
		case len(m[true]) > 0 && ran:
			t.Errorf("%s is marked not-run on %v, and %s runs it; one of the two is wrong", path, m[true], runs[path])
		case len(m[true]) == 0 && !ran:
			t.Errorf("%s is on %v, no test runs it, and no page says so", path, m[false])
		}
	}
	for path := range runs {
		if marks[path] == nil {
			t.Errorf("runs names %s, which no page includes", path)
		}
	}
}

// ---------------------------------------------------------------- helm dev

// devStudio is a `helm dev` the test started, and its output.
type devStudio struct {
	cmd      *exec.Cmd
	out      *syncBuffer
	done     chan error
	stopOnce sync.Once
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startDev(t *testing.T, cmd *exec.Cmd) *devStudio {
	t.Helper()
	d := &devStudio{cmd: cmd, out: &syncBuffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = d.out, d.out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { d.done <- cmd.Wait(); close(d.done) }()
	t.Cleanup(func() { d.stop(t) })
	return d
}

// stop interrupts helm dev as Ctrl-C would, and waits for it to stop the
// studio.
func (d *devStudio) stop(t *testing.T) {
	d.stopOnce.Do(func() {
		_ = d.cmd.Process.Signal(os.Interrupt)
		select {
		case <-d.done:
		case <-time.After(60 * time.Second):
			_ = d.cmd.Process.Kill()
			t.Errorf("helm dev did not stop within a minute of an interrupt:\n%s", d.out.String())
		}
	})
}

// waitForLine waits for a line of helm dev's output matching re, and returns
// its submatches.
func (d *devStudio) waitForLine(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	deadline := time.After(90 * time.Second)
	for {
		if m := re.FindStringSubmatch(d.out.String()); m != nil {
			return m
		}
		select {
		case err := <-d.done:
			t.Fatalf("helm dev ended (%v) before printing %s:\n%s", err, re, d.out.String())
		case <-deadline:
			t.Fatalf("helm dev never printed %s:\n%s", re, d.out.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitHealthy(t *testing.T, d *devStudio, address string) {
	t.Helper()
	deadline := time.After(90 * time.Second)
	for {
		if res, err := http.Get(address); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case err := <-d.done:
			t.Fatalf("helm dev ended (%v) before %s answered:\n%s", err, address, d.out.String())
		case <-deadline:
			t.Fatalf("%s never answered:\n%s", address, d.out.String())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// pythonEnvironment makes an environment with the standard library's venv,
// and puts the runtime SDK on its path, as installing it would without the
// network.
func pythonEnvironment(t *testing.T, python, dir string) string {
	t.Helper()
	venv := filepath.Join(dir, ".venv")
	if out, err := exec.Command(python, "-m", "venv", venv).CombinedOutput(); err != nil {
		t.Fatalf("python3 -m venv: %v\n%s", err, out)
	}
	addSDK(t, filepath.Join(venv, "bin", "python"))
	return venv
}

func addSDK(t *testing.T, python string) {
	t.Helper()
	sdk := filepath.Join(repoRoot, "packages", "helm-runtime-sdk", "python")
	script := `import site, sys; open(site.getsitepackages()[0] + "/helm-runtime-sdk.pth", "w").write(sys.argv[1] + "\n")`
	if out, err := exec.Command(python, "-c", script, sdk).CombinedOutput(); err != nil {
		t.Fatalf("putting the runtime SDK on the environment's path: %v\n%s", err, out)
	}
	// A .pth file is read when the interpreter starts, so a second one checks it.
	if out, err := exec.Command(python, "-c", "import helm_runtime_sdk").CombinedOutput(); err != nil {
		t.Fatalf("the runtime SDK is not importable from the environment: %v\n%s", err, out)
	}
}

// copyDir copies a sample's directory, so helm dev's .helm lands in a
// temporary directory rather than in the repository.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == ".helm" || d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func envWith(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		// Nothing the test's own shell had active leaks into a sample's.
		if strings.HasPrefix(kv, "VIRTUAL_ENV=") || strings.HasPrefix(kv, "HELM_") && !strings.HasPrefix(kv, "HELM_FFMPEG=") && !strings.HasPrefix(kv, "HELM_FFPROBE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// ---------------------------------------------------------------- the quickstart

// The quickstart, followed as written in a workspace of its own. Steps 1, 3
// and 4 need the network, and the page says so: the test checks that each
// names what this checkout has, then stands in for it from the checkout —
// helm built from it on the path, the runtime SDK's directory on the
// environment's path, and the example's two files copied.
func TestTheQuickstartRunsAsWritten(t *testing.T) {
	need(t, "python3", envAllowMissingClients)
	need(t, "curl", envAllowMissingClients)
	if ln, err := net.Listen("tcp", "127.0.0.1:8765"); err != nil {
		t.Fatalf("port 8765 is taken, and the quickstart as written asks for it: %v", err)
	} else {
		ln.Close()
	}
	q := filepath.Join(siteDir, "samples", "quickstart")
	for _, step := range samplesRunBy("TestTheQuickstartRunsAsWritten") {
		if _, err := os.Stat(filepath.Join(siteDir, "samples", step)); err != nil {
			t.Fatal(err)
		}
	}

	// What the steps that need the network would fetch is in this checkout.
	const raw = "https://raw.githubusercontent.com/janishar/helmstudio/main/"
	if got, want := onlyCommand(t, filepath.Join(q, "01-install-helm.sh")), `/bin/bash -c "$(curl -fsSL `+raw+`installer/install.sh)"`; got != want {
		t.Errorf("step 1 runs %q; want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "installer", "install.sh")); err != nil {
		t.Errorf("step 1 runs installer/install.sh: %v", err)
	}
	if got := onlyCommand(t, filepath.Join(q, "03-install-sdk.sh")); got != "pip install helm-runtime-sdk" {
		t.Errorf("step 3 runs %q; want pip install helm-runtime-sdk", got)
	}
	sdk := filepath.Join(repoRoot, "packages", "helm-runtime-sdk", "python")
	if b, err := os.ReadFile(filepath.Join(sdk, "pyproject.toml")); err != nil || !strings.Contains(string(b), "\nname = \"helm-runtime-sdk\"\n") {
		t.Errorf("step 3 installs helm-runtime-sdk, and the runtime SDK's pyproject.toml names no such package (%v)", err)
	}
	example := filepath.Join(siteDir, "samples", "hello-studio")
	b, err := os.ReadFile(filepath.Join(q, "04-get-example.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var fetched []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		name, ok := strings.CutPrefix(l, "curl -fsSLO "+raw+"site/samples/hello-studio/")
		if !ok {
			t.Errorf("step 4 runs %q; want a download of one of the example's files from main", l)
			continue
		}
		fetched = append(fetched, name)
	}
	if got := strings.Join(fetched, " "); got != "helmstudio.yaml studio.py" {
		t.Errorf("step 4 downloads %q; want the example's helmstudio.yaml and studio.py", got)
	}

	ws := t.TempDir()
	bin := filepath.Join(ws, "bin")
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "helm"), "./cmd/helm")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building helm, in place of step 1: %v\n%s", err, out)
	}
	path := "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")

	shell := func(dir, script string) string {
		t.Helper()
		cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+script)
		cmd.Dir = dir
		cmd.Env = envWith(path, "Q="+q, "SDK="+sdk, "EXAMPLE="+example)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s\n--- the script:\n%s", err, out, script)
		}
		return string(out)
	}
	// Step 2 as someone types it, then steps 3 and 4 from the checkout, in the
	// directory and the environment step 2 left.
	shell(ws, `source "$Q/02-environment.sh"
python -c 'import site, sys; open(site.getsitepackages()[0] + "/helm-runtime-sdk.pth", "w").write(sys.argv[1] + "\n")' "$SDK"
cp "$EXAMPLE/helmstudio.yaml" "$EXAMPLE/studio.py" .
test "$(pwd -P)" = "$(cd "`+ws+`/hello-studio" && pwd -P)"`)
	hello := filepath.Join(ws, "hello-studio")

	// Step 5, in the environment step 2 activated, with helm on the path as
	// step 1 leaves it. Its one line is exec'd, so the process the test
	// interrupts is helm dev itself.
	line := onlyCommand(t, filepath.Join(q, "05-run.sh"))
	cmd := exec.Command("bash", "-c", "source .venv/bin/activate && exec "+line)
	cmd.Dir = hello
	cmd.Env = envWith(path)
	dev := startDev(t, cmd)
	// A reader goes on when the studio says where it is. helm dev tails each
	// process's log on a timer, so the line comes a moment after the studio
	// prints it.
	dev.waitForLine(t, regexp.MustCompile(`(?m)^studio \| hello studio: http://127\.0\.0\.1:8765$`))
	waitHealthy(t, dev, "http://127.0.0.1:8765/healthz")

	// Steps 6, 7 and 8, in a second terminal.
	var made6 map[string]string
	if err := json.Unmarshal([]byte(shell(hello, `source "$Q/06-make.sh"`)), &made6); err != nil || made6["item_id"] == "" || made6["asset_id"] == "" {
		t.Fatalf("step 6 answered %v (%v); want an item and its asset", made6, err)
	}
	var items []struct {
		ID     string         `json:"id"`
		Title  string         `json:"title"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(shell(hello, `source "$Q/07-items.sh"`)), &items); err != nil {
		t.Fatalf("step 7: %v", err)
	}
	if len(items) != 1 || items[0].ID != made6["item_id"] || items[0].Params["prompt"] != "a lighthouse at dusk" || items[0].Params["seed"] != float64(7) {
		t.Errorf("step 7 listed %+v; want the item step 6 made, with its prompt and seed", items)
	}
	criteria := shell(hello, `source "$Q/08-validate.sh"`)
	for _, want := range []string{"helmstudio.yaml: ok", "5 of 7 checkable pass", "FAIL 13", "FAIL 15"} {
		if !strings.Contains(criteria, want) {
			t.Errorf("step 8 printed no %q; the page says the example fails 13 and 15:\n%s", want, criteria)
		}
	}

	dev.stop(t)
	matchOutput(t, filepath.Join(q, "05-output.txt"), dev.out.String())
	if _, err := os.Stat(filepath.Join(hello, ".helm")); err != nil {
		t.Errorf("the studio's data is not beside its manifest, as the page says: %v", err)
	}
}

// onlyCommand is a script's one line that is not a comment.
func onlyCommand(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("%s has %d commands; the test runs it as one", file, len(lines))
	}
	return lines[0]
}

// matchOutput checks that every line of a sample output appears, in order,
// in what was printed; "…" in the sample matches anything.
func matchOutput(t *testing.T, sample, printed string) {
	t.Helper()
	b, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(printed, "\n")
	at := 0
	for _, want := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		parts := strings.Split(want, "…")
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		re := regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
		found := false
		for ; at < len(lines); at++ {
			if re.MatchString(lines[at]) {
				found, at = true, at+1
				break
			}
		}
		if !found {
			t.Errorf("the page shows %q, and helm dev printed no such line after the ones before it:\n%s", want, printed)
			return
		}
	}
}

// ---------------------------------------------------------------- Python samples

// Each Python sample runs inside a studio under helm dev — with the token,
// the stage directory and the capabilities a studio has — through a runner
// studio that runs a file and reports its exit and output.
func TestThePythonSamplesRun(t *testing.T) {
	python := need(t, "python3", envAllowMissingClients)
	dir := t.TempDir()
	venv := pythonEnvironment(t, python, dir)
	runner := filepath.Join(dir, "runner")
	copyDir(t, filepath.Join(siteDir, "testdata", "runner"), runner)

	cmd := exec.Command(helmBin, "dev", "-f", "helmstudio.yaml")
	cmd.Dir = runner
	cmd.Env = envWith("PATH=" + filepath.Join(venv, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"))
	dev := startDev(t, cmd)
	port := dev.waitForLine(t, regexp.MustCompile(`runner \| runner: http://127\.0\.0\.1:(\d+)`))[1]
	base := "http://127.0.0.1:" + port
	waitHealthy(t, dev, base+"/healthz")

	for _, sample := range samplesRunBy("TestThePythonSamplesRun") {
		t.Run(sample, func(t *testing.T) {
			if sample == "timeline/timeline.py" {
				need(t, "ffmpeg", envAllowMissingFFmpeg)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			file := filepath.Join(siteDir, "samples", filepath.FromSlash(sample))
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/run?file="+url.QueryEscape(file), nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var run struct {
				Code           int
				Stdout, Stderr string
			}
			if err := json.NewDecoder(res.Body).Decode(&run); err != nil {
				t.Fatal(err)
			}
			if run.Code != 0 || strings.TrimSpace(run.Stdout) != "ok" {
				t.Errorf("%s exited %d\nstdout:\n%s\nstderr:\n%s", sample, run.Code, run.Stdout, run.Stderr)
			}
		})
	}
}

// ---------------------------------------------------------------- theming

// The theming guide's studio runs under helm dev with each of its three
// servers, and everything its page links is served through the proxy:
// helm-css, the browser runtime, the hue its manifest declares and the theme
// stream. Its stylesheet passes the lint as the page runs it.
func TestTheThemingStudioServesWhatItsPageLinks(t *testing.T) {
	samples := filepath.Join(siteDir, "samples", "theming")
	manifest, err := os.ReadFile(filepath.Join(samples, "helmstudio.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const pythonCmd = `cmd: "python3 server.py --port {port}"`
	if !bytes.Contains(manifest, []byte(pythonCmd)) {
		t.Fatalf("the theming manifest no longer runs %s, which this test swaps for the Go and JavaScript servers", pythonCmd)
	}

	servers := []struct {
		name    string
		cmd     string
		prepare func(t *testing.T, studio string) []string // the environment helm dev runs with
	}{
		{"python", pythonCmd, func(t *testing.T, studio string) []string {
			venv := pythonEnvironment(t, need(t, "python3", envAllowMissingClients), t.TempDir())
			return envWith("PATH=" + filepath.Join(venv, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"))
		}},
		{"go", `cmd: "./lantern --port {port}"`, func(t *testing.T, studio string) []string {
			build := exec.Command("go", "build", "-o", filepath.Join(studio, "lantern"), "./samples/theming/go")
			build.Dir = siteDir
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("building the Go server: %v\n%s", err, out)
			}
			return envWith()
		}},
		{"javascript", `cmd: "node node/server.mjs --port {port}"`, func(t *testing.T, studio string) []string {
			need(t, "node", envAllowMissingClients)
			// What npm install leaves for a package installed from a directory.
			modules := filepath.Join(studio, "node_modules", "@helmstudio")
			if err := os.MkdirAll(modules, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(repoRoot, "packages", "helm-runtime-sdk", "node"), filepath.Join(modules, "runtime")); err != nil {
				t.Fatal(err)
			}
			return envWith()
		}},
	}
	for _, server := range servers {
		t.Run(server.name, func(t *testing.T) {
			studio := filepath.Join(t.TempDir(), "theming")
			copyDir(t, samples, studio)
			env := server.prepare(t, studio)
			if err := os.WriteFile(filepath.Join(studio, "helmstudio.yaml"), bytes.Replace(manifest, []byte(pythonCmd), []byte(server.cmd), 1), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(helmBin, "dev", "-f", "helmstudio.yaml")
			cmd.Dir = studio
			cmd.Env = env
			dev := startDev(t, cmd)
			port := dev.waitForLine(t, regexp.MustCompile(`studio \| lantern studio: http://127\.0\.0\.1:(\d+)`))[1]
			base := "http://127.0.0.1:" + port
			waitHealthy(t, dev, base+"/healthz")
			checkThemedPage(t, base)
		})
	}

	lint := exec.Command("bash", filepath.Join(samples, "lint.sh"))
	lint.Dir = samples
	lint.Env = envWith("PATH=" + filepath.Dir(helmBin) + string(os.PathListSeparator) + os.Getenv("PATH"))
	if out, err := lint.CombinedOutput(); err != nil || !strings.Contains(string(out), "theme ok") {
		t.Errorf("lint.sh: %v\n%s", err, out)
	}
}

func checkThemedPage(t *testing.T, base string) {
	t.Helper()
	page := get(t, base+"/", "text/html")
	var linked []string
	for _, m := range regexp.MustCompile(`(?:href="|from ")(/[^"]*)"`).FindAllStringSubmatch(page, -1) {
		linked = append(linked, m[1])
	}
	sort.Strings(linked)
	want := []string{"/helm/accent.css", "/helm/sdk/v1/helm-runtime.js", "/helm/sdk/v1/helm.css", "/studio.css"}
	if strings.Join(linked, " ") != strings.Join(want, " ") {
		t.Fatalf("the page links %v; the test checks %v", linked, want)
	}
	get(t, base+"/studio.css", "text/css")
	if css := get(t, base+"/helm/sdk/v1/helm.css", "text/css"); !strings.Contains(css, "--helm-studio-accent") {
		t.Error("/helm/sdk/v1/helm.css is not helm-css")
	}
	get(t, base+"/helm/sdk/v1/helm-runtime.js", "javascript")
	accent := get(t, base+"/helm/accent.css", "text/css")
	for _, hue := range []string{"#e8745a", "#b0442b"} {
		if !strings.Contains(accent, hue) {
			t.Errorf("accent.css does not carry the manifest's hue %s:\n%s", hue, accent)
		}
	}
	if events := get(t, base+"/helm/api/v1/theme/events", "text/event-stream"); !strings.Contains(events, "event: theme") {
		t.Errorf("the theme stream did not reach the page through the proxy:\n%s", events)
	}
}

// ---------------------------------------------------------------- the embedded provider

// The providers page's Go studio records an output with no daemon and no
// helm dev, in a directory holding a studio's manifest, as the page says.
func TestTheEmbeddedProviderSampleRecords(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "embedded")
	build := exec.Command("go", "build", "-o", bin, "./samples/providers/embedded")
	build.Dir = siteDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the sample: %v\n%s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(siteDir, "samples", "hello-studio", "helmstudio.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helmstudio.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = envWith()
	out, err := cmd.CombinedOutput()
	if err != nil || !regexp.MustCompile(`^recorded \S+ through the embedded provider\n$`).Match(out) {
		t.Fatalf("the sample: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".helm", "helm.db")); err != nil {
		t.Errorf("the embedded provider kept nothing in ./.helm: %v", err)
	}
}

// get fetches a URL and checks its status and type. An event stream is read
// for its first event only.
func get(t *testing.T, address, contentType string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), contentType) {
		t.Fatalf("GET %s: %d %s; want 200 %s", address, res.StatusCode, res.Header.Get("Content-Type"), contentType)
	}
	if contentType == "text/event-stream" {
		var b strings.Builder
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if sc.Text() == "" && b.Len() > 0 {
				break
			}
			b.WriteString(sc.Text() + "\n")
		}
		return b.String()
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// ---------------------------------------------------------------- manifests

// A manifest or registry entry on a page validates, and a script that
// validates one prints what the page says it prints.
// Installing needs the network and the install page says so beside every
// sample that does. This one does not: `helm --version` is how a reader tells
// which helm they have, so the page's claim about it is run rather than
// asserted.
func TestTheInstallSamplesRun(t *testing.T) {
	for _, sample := range samplesRunBy("TestTheInstallSamplesRun") {
		file := filepath.Join(siteDir, "samples", filepath.FromSlash(sample))
		t.Run(sample, func(t *testing.T) {
			cmd := exec.Command("bash", file)
			cmd.Dir = filepath.Dir(file)
			cmd.Env = envWith("PATH=" + filepath.Dir(helmBin) + string(os.PathListSeparator) + os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", sample, err, out)
			}
			if !strings.HasPrefix(string(out), "helm ") {
				t.Errorf("%s printed %q; want it to name the helm it ran", sample, out)
			}
		})
	}
}

func TestTheManifestSamplesValidate(t *testing.T) {
	for _, sample := range samplesRunBy("TestTheManifestSamplesValidate") {
		file := filepath.Join(siteDir, "samples", filepath.FromSlash(sample))
		t.Run(sample, func(t *testing.T) {
			switch filepath.Ext(sample) {
			case ".yaml":
				if out, err := exec.Command(helmBin, "validate", file).CombinedOutput(); err != nil {
					t.Errorf("helm validate %s: %v\n%s", sample, err, out)
				}
			case ".sh":
				cmd := exec.Command("bash", file)
				cmd.Dir = filepath.Dir(file)
				cmd.Env = envWith("PATH=" + filepath.Dir(helmBin) + string(os.PathListSeparator) + os.Getenv("PATH"))
				out, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(out), "helmstudio.yaml: ok") || !strings.Contains(string(out), "checkable pass") {
					t.Errorf("%s: %v\n%s", sample, err, out)
				}
			default:
				t.Fatalf("no way to run %s", sample)
			}
		})
	}
}
