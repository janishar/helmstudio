// Command site builds helmstudio's documentation and site into a directory of
// static files, ready for GitHub Pages under a custom domain.
//
//	go run ./cmd/site                          # into site/out, for helmstudio.in
//	go run ./cmd/site -base /helmstudio/ -cname ""   # for a project-pages address
//	go run ./cmd/site -serve 127.0.0.1:8760    # build, then serve it to look at
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/janishar/helmstudio/site/gen"
)

func main() {
	root := flag.String("root", "..", "the repository's root")
	siteDir := flag.String("site", ".", "the site's directory: content, samples and annotations")
	out := flag.String("out", "out", "where the site is written; emptied first")
	base := flag.String("base", "/", "the path the site is served under")
	cname := flag.String("cname", "helmstudio.in", "the custom domain GitHub Pages serves the site at, or empty for none")
	helm := flag.String("helm", "", "a built helm binary, whose usage the CLI reference quotes (built from -root when empty)")
	serve := flag.String("serve", "", "after building, serve the output at this loopback address")
	flag.Parse()

	res, err := gen.Build(gen.Options{Root: *root, Site: *siteDir, Out: *out, Base: *base, CNAME: *cname, Helm: *helm})
	if err != nil {
		fmt.Fprintf(os.Stderr, "site: %v\n", err)
		os.Exit(1)
	}
	if problems := gen.CheckLinks(*out, *base, res); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "site:", p)
		}
		os.Exit(1)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Printf("site: %d pages in %s\n", len(res.Pages), abs)
	if *serve != "" {
		log.Printf("site: serving %s at http://%s%s", abs, *serve, *base)
		handler := http.StripPrefix(trimSlash(*base), http.FileServer(http.Dir(*out)))
		log.Fatal(http.ListenAndServe(*serve, handler))
	}
}

func trimSlash(base string) string {
	if base == "/" {
		return ""
	}
	return base[:len(base)-1]
}
