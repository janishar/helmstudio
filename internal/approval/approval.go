// Package approval builds what a studio would run, and the digest that
// authorises it (docs/design/01-prd.md R62, R63; docs/decisions.md M7 Q10-Q13).
//
// Installing a studio is running someone else's code on your machine with your
// permissions. The obligation is not to pretend otherwise but to make the
// decision visible, and this package is where "visible" is decided.
//
// Two things it gets right that the previous design did not:
//
// **Every command, not every build command.** R62 said "every build command
// verbatim". A manifest also runs `processes[].cmd` and `health.exec` at every
// launch, and `import.run` once — so a harmless `build[]` with a hostile `cmd`
// would have passed a screen that showed build steps only. Commands are
// grouped by when they run, because "this runs every time you launch it" is a
// different question from "this runs once, now".
//
// **The digest covers what executes, and nothing else.** Sizes, free disk and
// the checkpoint selection are shown and not covered: none of them changes
// what runs, and a digest over them would ask for approval again every time a
// download finished. What it does cover is every command with its cwd, shell
// and env, the capabilities, the network hosts, the submodules, the resolved
// commit and where the manifest came from.
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/janishar/helmstudio/internal/manifest"
)

// When a command runs. The grouping is the point: a person reading this screen
// is deciding about two different risks at once.
type When string

const (
	// AtInstall is a build step: it runs once, now, if they say yes.
	AtInstall When = "install"
	// AtLaunch is a process command or a health probe: it runs every time.
	AtLaunch When = "launch"
	// AtFirstLaunch is import.run, which runs once when the studio first starts.
	AtFirstLaunch When = "first_launch"
)

// Flag marks something in a string a reader would not otherwise see.
type Flag string

const (
	FlagControl       Flag = "control_characters"
	FlagNewline       Flag = "newline"
	FlagZeroWidth     Flag = "zero_width"
	FlagBidirectional Flag = "bidirectional"
)

// Command is one thing that will run, as the manifest wrote it.
type Command struct {
	When    When   `json:"when"`
	Label   string `json:"label"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	// Shell is which shell runs the command, not whether one does: the schema
	// defaults it to sh, and `sh -c` versus `bash -c` changes what a string
	// means.
	Shell string            `json:"shell,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
	Flags []Flag            `json:"flags,omitempty"`
}

// Submodule is one entry from .gitmodules at the resolved commit. A submodule
// is code that will be cloned and may be built, from a URL the top-level
// repository chose, so it belongs on this screen as much as `repo` does.
type Submodule struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}

// Weight is a model this studio would download or link.
type Weight struct {
	Name       string `json:"name"`
	Repo       string `json:"hf_repo,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	State      string `json:"state,omitempty"`
	Selectable bool   `json:"selectable"`
	Optional   bool   `json:"optional,omitempty"`
}

// CheckState is how a check came out. NotRun is a first-class outcome here.
type CheckState string

const (
	CheckPass   CheckState = "pass"
	CheckWarn   CheckState = "warn"
	CheckFail   CheckState = "fail"
	CheckNotRun CheckState = "not_run"
)

// Check is one row of the screen's checklist.
type Check struct {
	Name     string     `json:"name"`
	State    CheckState `json:"state"`
	Required bool       `json:"required"`
	Detail   string     `json:"detail,omitempty"`
}

// Preview is the whole screen.
type Preview struct {
	StudioID string `json:"studio_id"`
	Name     string `json:"name,omitempty"`
	Source   string `json:"source"`
	Level    string `json:"level"`
	Digest   string `json:"digest"`

	Repo      string `json:"repo,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Transport string `json:"transport,omitempty"`
	LocalPath string `json:"local_path,omitempty"`

	Submodules   []Submodule             `json:"submodules,omitempty"`
	Commands     []Command               `json:"commands"`
	Capabilities []manifest.Capability   `json:"capabilities"`
	NetworkHosts []string                `json:"network_hosts,omitempty"`
	Weights      []Weight                `json:"weights"`
	Selection    string                  `json:"selection,omitempty"`
	Checks       []Check                 `json:"checks"`
	Criteria     manifest.CriteriaResult `json:"criteria"`
}

// Input is what a preview is built from. Everything here is already resolved:
// this package reads no repository and touches no network.
type Input struct {
	Manifest   *manifest.Manifest
	Source     string
	Level      string
	Commit     string
	Submodules []Submodule
	// Weights carries what the daemon knows about each declared weight —
	// sizes and whether it is already here. None of it enters the digest.
	Weights   []Weight
	Selection string
	// HostChecks are the requirement rows install itself would apply, passed
	// in because what a host has is not something a manifest knows.
	HostChecks []Check
}

// Build assembles the preview and computes its digest.
func Build(in Input) (Preview, error) {
	m := in.Manifest
	if m == nil {
		return Preview{}, fmt.Errorf("no manifest to preview")
	}
	p := Preview{
		StudioID:     m.ID,
		Name:         m.Name,
		Source:       in.Source,
		Level:        in.Level,
		Repo:         m.Repo,
		Ref:          m.Ref,
		Commit:       in.Commit,
		LocalPath:    m.LocalPath,
		Submodules:   in.Submodules,
		Commands:     Commands(m),
		Capabilities: manifest.CapabilitySentences(m.Capabilities),
		NetworkHosts: m.Network,
		Weights:      in.Weights,
		Selection:    in.Selection,
		Criteria:     manifest.Criteria(m),
	}
	p.Transport = transport(m)
	p.Checks = checks(m, in.HostChecks)

	d, err := Digest(in)
	if err != nil {
		return Preview{}, err
	}
	p.Digest = d
	return p, nil
}

// transport says in words what the repo URL means, because "it will be cloned"
// is a different sentence for https, ssh and a directory on this Mac.
func transport(m *manifest.Manifest) string {
	switch {
	case m.LocalPath != "":
		return "Builds a directory already on this Mac, without cloning: " + m.LocalPath
	case strings.HasPrefix(m.Repo, "https://"):
		return "Clones over HTTPS: " + m.Repo
	case strings.HasPrefix(m.Repo, "http://"):
		return "Clones over plain HTTP, which is not encrypted: " + m.Repo
	case strings.HasPrefix(m.Repo, "file://"):
		return "Clones a directory on this Mac: " + strings.TrimPrefix(m.Repo, "file://")
	case strings.HasPrefix(m.Repo, "git@"), strings.HasPrefix(m.Repo, "ssh://"):
		return "Clones over SSH, using this Mac's keys: " + m.Repo
	case m.Repo == "":
		return ""
	default:
		return "Clones: " + m.Repo
	}
}

// Commands is every command the manifest would run, grouped by when.
//
// **This function is the security surface.** A command field the schema gains
// and this function does not read is a command that runs without ever being
// shown, so the test beside it walks schema/manifest.json and fails on any
// string property that is neither listed here nor classified as running
// nothing. A new command field cannot be missed.
func Commands(m *manifest.Manifest) []Command {
	var out []Command

	// At install: the build steps, in the order they run.
	for _, b := range m.Build {
		out = append(out, flagged(Command{
			When: AtInstall, Label: b.Name, Command: b.Run, Cwd: b.Cwd, Shell: b.EffectiveShell(),
		}))
	}
	if m.Python != nil {
		out = append(out, Command{
			When:  AtInstall,
			Label: "Make a Python environment",
			// Not a manifest string: helmstudio does this itself, and saying
			// so beats leaving a gap between the steps shown and what runs.
			Command: "helmstudio creates a uv environment for Python " + m.Python.Version,
		})
	}

	// At every launch: each process, and each health probe that executes.
	for _, pr := range m.EffectiveProcesses() {
		out = append(out, flagged(Command{
			When: AtLaunch, Label: pr.Name, Command: pr.Cmd, Cwd: pr.Cwd, Shell: pr.EffectiveShell(), Env: pr.Env,
		}))
		if pr.Health != nil && pr.Health.Exec != "" {
			out = append(out, flagged(Command{
				When: AtLaunch, Label: pr.Name + " · health probe", Command: pr.Health.Exec,
			}))
		}
	}

	// Once, on first launch.
	if m.Import != nil && m.Import.Run != "" {
		out = append(out, flagged(Command{
			When: AtFirstLaunch, Label: "Import", Command: m.Import.Run, Cwd: m.Import.Cwd,
		}))
	}
	if out == nil {
		out = []Command{}
	}
	return out
}

// flagged marks what is in a string that a reader would not see. Verbatim means
// byte for byte, so nothing is stripped — but a command carrying a
// right-to-left override renders as something other than what will execute,
// and that is precisely the trick this screen exists to defeat.
func flagged(c Command) Command {
	c.Flags = Flags(c.Command)
	return c
}

// Flags reports what is hidden in s.
func Flags(s string) []Flag {
	var control, newline, zero, bidi bool
	for _, r := range s {
		switch {
		case r == '\n', r == '\r':
			newline = true
		case r == 0x200b, r == 0x200c, r == 0x200d, r == 0xfeff:
			zero = true
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f:
			bidi = true
		case r < 0x20 || r == 0x7f:
			control = true
		case unicode.Is(unicode.Cf, r):
			zero = true
		}
	}
	var out []Flag
	if control {
		out = append(out, FlagControl)
	}
	if newline {
		out = append(out, FlagNewline)
	}
	if zero {
		out = append(out, FlagZeroWidth)
	}
	if bidi {
		out = append(out, FlagBidirectional)
	}
	return out
}

// checks are the rows that run before install — and the two that do not.
func checks(m *manifest.Manifest, host []Check) []Check {
	out := []Check{{Name: "Manifest valid", State: CheckPass, Required: true,
		Detail: fmt.Sprintf("schema v1 · %d build steps · %d processes · %d weights",
			len(m.Build), len(m.EffectiveProcesses()), len(m.Weights))}}
	out = append(out, host...)

	if n := len(m.Build); n > 0 {
		out = append(out, Check{Name: "Will run these commands on your Mac", State: CheckWarn, Required: false,
			Detail: fmt.Sprintf("%d at install, and every process command at each launch", n)})
	}
	for _, c := range manifest.CapabilitySentences(m.Capabilities) {
		if c.Warning {
			out = append(out, Check{Name: c.Sentence, State: CheckWarn, Required: false,
				Detail: "capability: " + c.Name})
		}
	}
	// The two that are not run, with the reason. Never as passes: a smoke test
	// builds and runs the studio, which is what this screen asks permission
	// for, and theme conformance needs files nobody has cloned yet.
	out = append(out,
		Check{Name: "Theme conformance", State: CheckNotRun, Required: false,
			Detail: "Not run before install: it needs the studio's stylesheets, which are not cloned yet."},
		Check{Name: "Smoke test", State: CheckNotRun, Required: true,
			Detail: "Not run before install: it builds and runs the studio, which is what this screen is asking permission for."},
	)
	return out
}

// digestInput is exactly what the digest covers, in a fixed shape so that two
// runs over the same facts produce the same bytes.
type digestInput struct {
	StudioID     string      `json:"studio_id"`
	Source       string      `json:"source"`
	Commit       string      `json:"commit"`
	Repo         string      `json:"repo"`
	LocalPath    string      `json:"local_path"`
	Submodules   []Submodule `json:"submodules"`
	Commands     []Command   `json:"commands"`
	Capabilities []string    `json:"capabilities"`
	Network      []string    `json:"network"`
	Python       string      `json:"python"`
	Weights      []string    `json:"weights"`
}

// Digest is the fingerprint of everything that executes and where it comes
// from. What is deliberately absent is as important as what is present: sizes,
// free disk and the selection are shown on the screen and not covered here,
// because none of them changes what runs and a digest over them would ask for
// approval again every time a download finished.
func Digest(in Input) (string, error) {
	m := in.Manifest
	if m == nil {
		return "", fmt.Errorf("no manifest to fingerprint")
	}
	caps := append([]string(nil), m.Capabilities...)
	sort.Strings(caps)
	network := append([]string(nil), m.Network...)
	sort.Strings(network)

	// The weights by identity, not by size or state: which model is fetched is
	// part of what the user agreed to, how many bytes it is today is not.
	weights := make([]string, 0, len(m.Weights))
	for _, w := range m.Weights {
		weights = append(weights, strings.Join([]string{w.Name, w.Repo, w.Revision, w.Dest}, "\x00"))
	}
	sort.Strings(weights)

	python := ""
	if m.Python != nil {
		python = m.Python.Version
	}

	subs := append([]Submodule(nil), in.Submodules...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Path < subs[j].Path })

	b, err := json.Marshal(digestInput{
		StudioID: m.ID, Source: in.Source, Commit: in.Commit, Repo: m.Repo, LocalPath: m.LocalPath,
		Submodules: subs, Commands: Commands(m), Capabilities: caps, Network: network,
		Python: python, Weights: weights,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
