// Command helm is the helmstudio CLI: validate, dev, test, doctor, adopt.
// validate and dev exist, with upgrade; the rest land in later milestones.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// version is the release this helm was built as. .github/workflows/release-helm.yml
// sets it with -ldflags "-X main.version=<version>"; every other build is dev.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "dev":
		return runDevCommand(args[1:], stdout, stderr)
	case "upgrade":
		return runUpgrade(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stderr)
		return 0
	case "--version", "-v", "version":
		info, _ := debug.ReadBuildInfo()
		fmt.Fprintln(stdout, versionLine(version, info))
		return 0
	default:
		fmt.Fprintf(stderr, "helm: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: helm <command> [arguments]

commands:
  validate <manifest.yaml>...   validate one or more studio manifests
  validate -theme <dir>         lint a studio's stylesheets against helm-css (-strict to fail)
  dev [-f helmstudio.yaml]      run a studio against the embedded provider, no daemon
  upgrade [--version <v>]       replace this helm with the newest release
  --version                     print which helm this is`)
}

// versionLine is what helm --version prints: the version, and the commit the
// build recorded, if it recorded one, marked when the tree had changes.
func versionLine(version string, info *debug.BuildInfo) string {
	line := "helm " + version
	if info == nil {
		return line
	}
	var commit string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	switch {
	case commit != "" && modified:
		line += " (" + commit + ", modified)"
	case commit != "":
		line += " (" + commit + ")"
	}
	return line
}
