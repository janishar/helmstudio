package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/themelint"
)

// runValidate implements `helm validate [--json] [-theme <dir> [-strict]] [<manifest.yaml>...]`.
// It returns the process exit code directly rather than an error, so a
// manifest that fails validation — the expected, common case — exits 1
// without helm's own "helm: <err>" prefix wrapping a result already printed
// in full. stdout/stderr are parameters so tests can capture output without
// touching the real os.Stdout/os.Stderr.
//
// -theme lints a studio's stylesheets against helm-css (docs/decisions.md M6
// Q17). Findings are advisory and exit 0; -strict makes them exit 1, which is
// what registry CI runs.
func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	themeDir := fs.String("theme", "", "lint the stylesheets under this directory for colour literals and non-Plex fonts")
	strict := fs.Bool("strict", false, "with -theme, exit 1 when the lint finds anything")
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: helm validate [--json] [-theme <dir> [-strict]] [<manifest.yaml>...]")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 && *themeDir == "" {
		fs.Usage()
		return 2
	}
	if *strict && *themeDir == "" {
		fmt.Fprintln(stderr, "helm validate: -strict applies to -theme")
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

	var theme *themelint.Report
	if *themeDir != "" {
		rep, err := themelint.Dir(*themeDir)
		if err != nil {
			fmt.Fprintf(stderr, "helm validate: -theme %s: %v\n", *themeDir, err)
			return 1
		}
		theme = &rep
		if *strict && len(rep.Findings) > 0 {
			anyInvalid = true
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		var v any = results
		if theme != nil {
			// The manifest-only shape stays a bare array; with -theme the two
			// reports sit side by side.
			v = struct {
				Manifests []manifest.Result `json:"manifests"`
				Theme     *themelint.Report `json:"theme"`
				Strict    bool              `json:"strict"`
			}{results, theme, *strict}
		}
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(stderr, "helm:", err)
			return 1
		}
	} else {
		printText(stdout, results)
		if theme != nil {
			printTheme(stdout, *themeDir, *theme, *strict)
		}
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

func printTheme(w io.Writer, dir string, rep themelint.Report, strict bool) {
	colours, fonts := 0, 0
	for _, f := range rep.Findings {
		if f.Kind == themelint.KindFont {
			fonts++
		} else {
			colours++
		}
	}
	switch {
	case len(rep.Findings) == 0:
		fmt.Fprintf(w, "%s: theme ok (%d stylesheet files, outside %s/)\n", dir, rep.Files, themelint.VendorDir)
	case strict:
		fmt.Fprintf(w, "%s: theme invalid: %d colour literals, %d font families in %d files\n", dir, colours, fonts, rep.Files)
	default:
		fmt.Fprintf(w, "%s: theme advisory: %d colour literals, %d font families in %d files (-strict to fail on them)\n", dir, colours, fonts, rep.Files)
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "  %s\n", f)
	}
	fmt.Fprintln(w, "  note: colours set from JavaScript are not checked")
}
