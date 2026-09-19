package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

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

// helm dev runs a studio from its own manifest with no daemon: the process is
// spawned by the supervisor with the platform environment, {data} is
// ./.helm/data, and the SDK the studio would use reaches a working studio API
// backed by ./.helm/helm.db (docs/design/05 §5a, Q26).
func TestHelmDevRunsAStudioAgainstItsOwnHelmDirectory(t *testing.T) {
	studio := t.TempDir()
	envFile := filepath.Join(studio, "env.txt")
	os.WriteFile(filepath.Join(studio, "helmstudio.yaml"), []byte(`id: dev-studio
name: dev studio
kinds: [video]
repo: https://example.com/dev-studio.git
license: MIT
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
capabilities: [kv, assets, gallery]
processes:
  - name: web
    role: main
    cmd: "echo data={data}; env | grep ^HELM_ > `+envFile+`.tmp && mv `+envFile+`.tmp `+envFile+`; sleep 60"
`), 0o644)

	stop := make(chan struct{})
	ready := make(chan string, 1)
	var stdout, stderr syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- runDev(devOptions{manifest: filepath.Join(studio, "helmstudio.yaml"), addr: "127.0.0.1:0", stdout: &stdout, stderr: &stderr, stop: stop, ready: ready})
	}()
	var base string
	select {
	case base = <-ready:
	case err := <-done:
		t.Fatalf("helm dev ended before it was ready: %v\n%s", err, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatal("helm dev never became ready")
	}

	deadline := time.Now().Add(10 * time.Second)
	var env string
	for {
		if b, err := os.ReadFile(envFile); err == nil {
			env = string(b)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the studio never wrote its environment\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(env), "\n") {
		k, v, _ := strings.Cut(line, "=")
		vars[k] = v
	}
	if vars["HELM_API"] != base || strings.Contains(base, ":8700") || vars["HELM_TOKEN"] == "" {
		t.Fatalf("environment = %v; want HELM_API %s and a token", vars, base)
	}

	// What the studio's own SDK would do.
	c := helm.NewRemote(vars["HELM_API"], vars["HELM_TOKEN"])
	ctx := context.Background()
	me, err := c.Me.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	helmDir := filepath.Join(studio, ".helm")
	if me.StudioID != "dev-studio" || me.Paths.Data != filepath.Join(helmDir, "data") || !strings.HasPrefix(me.Paths.Stage, filepath.Join(helmDir, "stage")) {
		t.Fatalf("me = %+v", me)
	}
	if _, err := c.KV.Put(ctx, "ui", "last", map[string]any{"session": "example"}, nil); err != nil {
		t.Fatal(err)
	}
	take := filepath.Join(vars["HELM_STAGE_DIR"], "take.mp4")
	os.WriteFile(take, []byte("a take"), 0o644)
	asset, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: take, Kind: helm.AssetKindVideo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: asset.ID, Params: map[string]any{"seed": 42}}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Records.Insert(ctx, "takes", map[string]any{})
	if helm.CodeOf(err) != "capability_required" {
		t.Fatalf("records without the capability: %v", err)
	}
	// helm dev echoes the studio's output as it tails the log.
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(stdout.String(), "web | data="+filepath.Join(helmDir, "data")); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Errorf("{data} was not ./.helm/data, or the output was not echoed: %s", stdout.String())
			break
		}
	}
	for _, p := range []string{"helm.db", "assets", "data"} {
		if _, err := os.Stat(filepath.Join(helmDir, p)); err != nil {
			t.Errorf("./.helm/%s: %v", p, err)
		}
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helm dev: %v\n%s", err, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("helm dev did not stop")
	}
	if _, err := c.Me.Get(ctx); err == nil {
		t.Fatal("the studio API still answers after helm dev stopped")
	}
}

// A studio with selectable weights needs a choice before it can launch
// ({models.selected}, M7 Q21). An install makes it on the approval screen;
// `helm dev` has no approval screen, so -select is where a checkout says it —
// and with nothing said, the launch is refused with the choices named rather
// than run against an arbitrary checkpoint.
func TestHelmDevSelectsTheCheckpointToLaunchWith(t *testing.T) {
	studio := t.TempDir()
	os.WriteFile(filepath.Join(studio, "helmstudio.yaml"), []byte(`id: pick-studio
name: pick studio
kinds: [image]
repo: https://example.com/pick-studio.git
license: MIT
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
capabilities: []
weights:
  - { name: small, repo: example/small, dest: small, selectable: true }
  - { name: large, repo: example/large, dest: large, selectable: true }
processes:
  - name: web
    role: main
    cmd: "echo model={models.selected}; sleep 60"
`), 0o644)
	large := t.TempDir()
	os.WriteFile(filepath.Join(large, "model.safetensors"), []byte("weights"), 0o644)

	// No choice: refused, and the refusal says what may be chosen.
	var quiet syncBuffer
	err := runDev(devOptions{manifest: filepath.Join(studio, "helmstudio.yaml"), addr: "127.0.0.1:0",
		links: []string{"large=" + large}, stdout: &quiet, stderr: &quiet, stop: make(chan struct{})})
	if err == nil || !strings.Contains(err.Error(), "no checkpoint is chosen") || !strings.Contains(err.Error(), "large") {
		t.Fatalf("helm dev with no -select = %v; want a refusal naming the choices", err)
	}

	stop := make(chan struct{})
	ready := make(chan string, 1)
	var stdout, stderr syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- runDev(devOptions{manifest: filepath.Join(studio, "helmstudio.yaml"), addr: "127.0.0.1:0",
			links: []string{"large=" + large}, selected: "large",
			stdout: &stdout, stderr: &stderr, stop: stop, ready: ready})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("helm dev ended before it was ready: %v\n%s", err, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatal("helm dev never became ready")
	}
	// {models.selected} is the chosen weight's own path: the directory -link
	// pointed at, which is what that weight resolves to.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stdout.String(), "web | model="); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the studio never reported its model: %s", stdout.String())
		}
	}
	want, err := filepath.EvalSymlinks(large) // the link is to the real path
	if err != nil {
		t.Fatal(err)
	}
	if line := stdout.String(); !strings.Contains(line, "model="+want) {
		t.Errorf("{models.selected} was not the chosen checkpoint %s: %s", want, line)
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helm dev: %v\n%s", err, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("helm dev did not stop")
	}
}
