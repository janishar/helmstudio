package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/janishar/helmstudio/internal/manifest"
)

// runValidate implements `helm validate [--json] <manifest.yaml>...`.
// It returns the process exit code directly rather than an error, so a
// manifest that fails validation — the expected, common case — exits 1
// without helm's own "helm: <err>" prefix wrapping a result already printed
// in full. stdout/stderr are parameters so tests can capture output without
// touching the real os.Stdout/os.Stderr.
func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: helm validate [--json] <manifest.yaml>...")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fs.Usage()
		return 2
	}

	// One Result per input file, always — a file that couldn't even be read
	// still gets an entry (with a "read" error) rather than silently
	// shrinking the array below len(files), which is indistinguishable from
	// a caller having passed fewer files.
	results := make([]manifest.Result, 0, len(files))
	anyInvalid := false
	for _, f := range files {
		res, err := manifest.Validate(f)
		if err != nil {
			res = manifest.Result{File: f, Errors: []manifest.Error{{
				File: f, Pointer: "/", Rule: "read", Message: err.Error(),
			}}}
		}
		results = append(results, res)
		if !res.OK() {
			anyInvalid = true
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintln(stderr, "helm:", err)
			return 1
		}
	} else {
		printText(stdout, results)
	}

	if anyInvalid {
		return 1
	}
	return 0
}

func printText(w io.Writer, results []manifest.Result) {
	for _, res := range results {
		if res.OK() {
			fmt.Fprintf(w, "%s: ok\n", res.File)
			continue
		}
		fmt.Fprintf(w, "%s: invalid\n", res.File)
		for _, e := range res.Errors {
			fmt.Fprintf(w, "  %s\n", e.String())
		}
	}
}
