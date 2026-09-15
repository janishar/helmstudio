// Command helmstudio is the daemon: one local process that supervises studios
// and owns helm.db (docs/design/01-prd.md).
//
// In M2 it loads the studio manifests, re-adopts whatever the last daemon left
// running, and serves the plain shelf and the supervision API on loopback.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8700", "loopback address to serve the shelf and API on")
	studiosDir := flag.String("studios", "studios", "directory of studio manifests (*.yaml)")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("helmstudio: ")
	if err := run(*addr, *studiosDir); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(addr, studiosDir string) error {
	dirs, err := platform.Resolve(platform.Options{})
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dirs)
	if err != nil {
		return err
	}
	defer st.Close()

	sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Logf: log.Printf})
	sup.SetStudios(loadStudios(studiosDir))

	rep, err := sup.Readopt(ctx)
	if err != nil {
		return fmt.Errorf("re-adopting processes from the last run: %w", err)
	}
	for _, a := range rep.Adopted {
		log.Printf("re-adopted %s", a)
	}
	for _, g := range rep.Gone {
		log.Printf("gone since the last run, recorded as crashed and not signalled: %s", g)
	}
	for _, g := range rep.Survivors {
		log.Printf("warning: %s still has members although its recorded leader is gone; they were not signalled and may still hold memory", g)
	}
	for _, id := range rep.Unmanaged {
		log.Printf("warning: re-adopted %s, but its manifest is not loaded from %s; it counts as heavy and can only be stopped", id, studiosDir)
	}

	handler, err := api.New(sup, web.Shelf, addr, log.Printf)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s (is another helmstudio running?): %w", addr, err)
	}
	// Log streams never end on their own; cancelling their base context on
	// shutdown ends them, so an open browser tab cannot hold up stopping the
	// studios.
	baseCtx, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return baseCtx }}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Printf("serving http://%s — data %s, logs %s", addr, dirs.Data(), dirs.Logs())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("%v: stopping studios and shutting down", s)
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v; stopping studios", err)
		}
	}

	// A clean exit terminates children (R28); only kill -9 leaves them for
	// the next daemon to re-adopt.
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cancelRequests()
	_ = srv.Shutdown(shutdownCtx)
	return sup.Shutdown(shutdownCtx)
}

// loadStudios validates every manifest in dir. An invalid one is not listed,
// and its field and reason are logged; it never blocks the others (R2).
func loadStudios(dir string) []supervisor.Studio {
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	sort.Strings(files)
	if len(files) == 0 {
		log.Printf("no studio manifests in %s", dir)
	}
	var out []supervisor.Studio
	for _, f := range files {
		m, res, err := manifest.Load(f)
		switch {
		case err != nil:
			log.Printf("skipping %s: %v", f, err)
		case !res.OK():
			for _, e := range res.Errors {
				log.Printf("skipping invalid manifest: %s", e)
			}
		default:
			abs, _ := filepath.Abs(f)
			out = append(out, supervisor.Studio{Manifest: m, File: abs})
		}
	}
	return out
}
