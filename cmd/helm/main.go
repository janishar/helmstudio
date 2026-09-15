// Command helm is the helmstudio CLI: validate, dev, test, doctor, adopt.
// validate and dev exist; the rest land in later milestones.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "validate":
		os.Exit(runValidate(os.Args[2:], os.Stdout, os.Stderr))
	case "dev":
		os.Exit(runDevCommand(os.Args[2:], os.Stdout, os.Stderr))
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "helm: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: helm <command> [arguments]

commands:
  validate <manifest.yaml>...   validate one or more studio manifests
  dev [-f helmstudio.yaml]      run a studio against the embedded provider, no daemon`)
}
