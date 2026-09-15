package supervisor

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
)

// Health states recorded on a process row. They are separate from process
// state, because healthy-then-unresponsive is not the same thing as exited.
const (
	healthUnknown = "unknown"
	healthPassing = "passing"
	healthFailing = "failing"
)

// probeTimeout bounds one probe attempt. A probe slower than its interval is
// a failed attempt, not a reason to stop probing.
func probeTimeout(h *manifest.Health) time.Duration {
	d := time.Duration(h.EffectiveIntervalS()) * time.Second
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

// probe runs one attempt of a process's health check: path (HTTP GET expects
// 200), tcp (a connect to the assigned port), or exec (a zero exit, run in
// its own process group so a hung probe is killed with its children).
func probe(ctx context.Context, h *manifest.Health, port int, execArgv []string, cwd string, env []string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout(h))
	defer cancel()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	switch {
	case h.Path != "":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+h.Path, nil)
		if err != nil {
			return false
		}
		resp, err := probeClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	case h.TCP:
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	case len(execArgv) > 0:
		cmd := exec.CommandContext(ctx, execArgv[0], execArgv[1:]...)
		cmd.Dir, cmd.Env = cwd, env
		return platform.RunInGroup(cmd) == nil
	}
	return false
}

// probeClient never follows a redirect and never reuses a connection, so a
// probe measures the process as it is now.
var probeClient = &http.Client{
	Transport:     &http.Transport{DisableKeepAlives: true, Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
