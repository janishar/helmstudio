package visual

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	helmcss "github.com/janishar/helmstudio/packages/helm-css"
	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// TestH3Screens renders h3 studio's page, as its checkout has it, in both
// themes, for a person to look at before and after the token migration
// (docs/decisions.md M6 Q18). It needs h3's checkout, so it runs only when
// HELM_H3_DIR names one; HELM_H3_SHOTS is where the PNGs go. The page's own
// API calls fail without h3's server, so this shows its chrome, not its data.
// Not a golden: h3 lives in another repository.
func TestH3Screens(t *testing.T) {
	dir, out := os.Getenv("HELM_H3_DIR"), os.Getenv("HELM_H3_SHOTS")
	if dir == "" || out == "" {
		t.Skip("set HELM_H3_DIR to an h3 studio checkout and HELM_H3_SHOTS to an output directory")
	}
	b := openBrowser(t)
	accent, _ := helm.AccentCSS("#e0a33c", "#a06a10")
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(dir, "static", "index.html"))
	})
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(dir, "static")))))
	mux.Handle("/helm/sdk/v1/", http.StripPrefix("/helm/sdk/v1/", http.FileServerFS(helmcss.Files)))
	mux.HandleFunc("/helm/accent.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		io.WriteString(w, accent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, theme := range []string{"dark", "light"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		page, err := b.NewPage(ctx, 1440, 900, theme)
		if err != nil {
			t.Fatal(err)
		}
		if err := page.Navigate(ctx, srv.URL+"/"); err != nil {
			t.Fatal(err)
		}
		_ = page.Eval(ctx, `new Promise(r => setTimeout(() => r(true), 500))`, nil)
		if err := page.Resize(ctx, 1440, 900); err != nil {
			t.Fatal(err)
		}
		var shot []byte
		shot, err = page.Screenshot(ctx, 1440)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "h3-"+theme+".png"), shot, 0o644); err != nil {
			t.Fatal(err)
		}
		page.Close()
		cancel()
	}
}
