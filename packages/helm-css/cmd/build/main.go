// Command build writes helm-css's derived files — helm.css, helm.min.css and
// tokens.json — from its four layers. Run it through `make css` from the
// repository root.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	helmcss "github.com/janishar/helmstudio/packages/helm-css"
)

func main() {
	dir := filepath.Join("packages", "helm-css")
	out, err := helmcss.Build(os.DirFS(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helm-css:", err)
		os.Exit(1)
	}
	for _, name := range helmcss.Derived {
		if err := os.WriteFile(filepath.Join(dir, name), out[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "helm-css:", err)
			os.Exit(1)
		}
	}
}
