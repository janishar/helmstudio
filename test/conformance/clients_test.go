package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Python and Node clients are generated from the same document as the Go
// client the suite runs through. Each gets a smoke test against the daemon
// (docs/decisions.md M4 Q25): a read and a write, an etag conflict, a merge
// patch, a filter, an adopt with a range read, a gallery query and a
// capability refusal, each checked in its own language. A missing
// interpreter fails the test unless HELM_ALLOW_MISSING_CLIENTS is set.
func TestPythonAndNodeClientsSmoke(t *testing.T) {
	e := openDaemon(t)
	me, err := e.C(S).Me.Get(ctx)
	noErr(t, err)
	for _, tc := range []struct{ name, tool, script string }{
		{"python", "python3", filepath.Join("..", "..", "packages", "helm-runtime-sdk", "python", "tests", "smoke.py")},
		{"node", "node", filepath.Join("..", "..", "packages", "helm-runtime-sdk", "node", "test", "smoke.mjs")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := exec.LookPath(tc.tool)
			if err != nil {
				// A skip is silent in go test's output and the gate would stay
				// green without ever running this client (second review #10).
				if os.Getenv("HELM_ALLOW_MISSING_CLIENTS") == "" {
					t.Fatalf("%s is not installed, so the %s client cannot be checked; install it, or set HELM_ALLOW_MISSING_CLIENTS=1 to skip on purpose", tc.tool, tc.name)
				}
				t.Skipf("%s is not installed; skipped because HELM_ALLOW_MISSING_CLIENTS is set", tc.tool)
			}
			cmd := exec.Command(path, tc.script)
			cmd.Env = append(cmd.Environ(), "HELM_API="+e.URL+"/api/v1", "HELM_TOKEN="+e.Tokens[S], "HELM_STAGE_DIR="+me.Paths.Stage, "SMOKE_STUDIO="+S)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
				t.Fatalf("%s smoke: %v\n%s", tc.name, err, out)
			}
		})
	}
}
