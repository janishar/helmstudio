// Package export runs ffmpeg for the timeline: finding it, reading what a
// file really is, and running one render in its own process group so that a
// daemon killed mid-export leaves nothing behind (docs/decisions.md M8 Q5,
// Q16).
//
// What to render is decided in internal/timeline, which touches no file and
// runs no command. This package only carries it out.
package export

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/timeline"
)

// Where a build may be named, before M9 bundles one (M5 Q3's precedent).
const (
	EnvFFmpeg  = "HELM_FFMPEG"
	EnvFFprobe = "HELM_FFPROBE"
)

// MinMajor is the oldest ffmpeg this pipeline is written against. The filters
// it builds — xfade, apad with atrim, aresample's first_pts — and the options
// it passes have all been there since 6. The goldens are made with whatever
// major the gate records beside them.
const MinMajor = 6

// ErrMissing means no usable ffmpeg was found. The API answers 501
// `unsupported` with `details.tool`, as thumbnails already do for video, and
// creating or editing a sequence still works (M8 Q5).
var ErrMissing = errors.New("no usable ffmpeg")

// Tool is the ffmpeg and ffprobe this daemon found, and what they can do.
type Tool struct {
	FFmpeg  string
	FFprobe string
	Version string
	Major   int
	// Configuration is ffmpeg's own configure line, recorded so that a
	// question about an export's encoders has an answer.
	Configuration string

	once     sync.Once
	encoders map[string]bool
}

// Find locates ffmpeg and ffprobe: what the environment names, else the PATH.
// M9 bundles a pinned build and checks it first.
func Find() (*Tool, error) {
	ffmpeg, err := which(EnvFFmpeg, "ffmpeg")
	if err != nil {
		return nil, err
	}
	ffprobe, err := which(EnvFFprobe, "ffprobe")
	if err != nil {
		return nil, err
	}
	t := &Tool{FFmpeg: ffmpeg, FFprobe: ffprobe}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-version").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s did not answer -version: %v", ErrMissing, ffmpeg, err)
	}
	t.Version, t.Major, t.Configuration = parseVersion(string(out))
	if t.Major < MinMajor {
		return nil, fmt.Errorf("%w: %s is ffmpeg %s, and this needs %d or newer", ErrMissing, ffmpeg, t.Version, MinMajor)
	}
	for _, enc := range []string{timeline.VideoEncoder, timeline.AudioEncoder} {
		if !t.HasEncoder(enc) {
			return nil, fmt.Errorf("%w: %s has no %s encoder, which the h264 preset needs", ErrMissing, ffmpeg, enc)
		}
	}
	return t, nil
}

func which(env, name string) (string, error) {
	if v := os.Getenv(env); v != "" {
		if _, err := os.Stat(v); err != nil {
			return "", fmt.Errorf("%w: %s names %s, which is not there: %v", ErrMissing, env, v, err)
		}
		return v, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s is not on PATH; install it with `brew install ffmpeg`, or name one with %s", ErrMissing, name, env)
	}
	return path, nil
}

func parseVersion(out string) (version string, major int, configuration string) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "ffmpeg version "):
			version = strings.Fields(strings.TrimPrefix(line, "ffmpeg version "))[0]
		case strings.HasPrefix(line, "configuration:"):
			configuration = strings.TrimSpace(strings.TrimPrefix(line, "configuration:"))
		}
	}
	digits := version
	if i := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = digits[:i]
	}
	major, _ = strconv.Atoi(digits)
	return version, major, configuration
}

// HasEncoder reports whether the build can encode with name.
func (t *Tool) HasEncoder(name string) bool {
	t.once.Do(func() {
		t.encoders = map[string]bool{}
		out, err := exec.Command(t.FFmpeg, "-hide_banner", "-encoders").Output()
		if err != nil {
			return
		}
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 2 && strings.ContainsAny(fields[0], "VAS") && !strings.Contains(fields[0], "---") {
				t.encoders[fields[1]] = true
			}
		}
	})
	return t.encoders[name]
}

// CheckArgs refuses an argument list that names an encoder outside the
// licensing decision's allowlist, so the pipeline cannot quietly grow a
// dependency on a GPL build (M8 Q4).
func CheckArgs(args []string) error {
	for i, a := range args {
		if a != "-c:v" && a != "-c:a" && a != "-codec:v" && a != "-codec:a" && a != "-vcodec" && a != "-acodec" {
			continue
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s names no encoder", a)
		}
		if enc := args[i+1]; !timeline.AllowedEncoders[enc] {
			return fmt.Errorf("%s is not an encoder helmstudio may name: the licensing decision is an LGPL build with videotoolbox (docs/decisions.md M8 Q4)", enc)
		}
	}
	return nil
}

// context.Context is taken by every call that runs a tool, so a cancelled
// request stops the process rather than leaving it to finish.
var _ = context.Background

// ExitCodeOf reports a finished process's exit code, or -1.
func ExitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return platform.ExitCode(ee.ProcessState)
	}
	return -1
}
