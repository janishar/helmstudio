// Command helmstudio is the daemon: one local process that supervises studios
// and owns helm.db (docs/design/01-prd.md).
//
// It loads the studio manifests, records installs and downloads a previous
// daemon left mid-flight as interrupted, re-adopts whatever the last daemon
// left running, and serves the plain shelf, the supervision API and the
// install and weights API on loopback.
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
	"syscall"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
	"github.com/janishar/helmstudio/studios"
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

	// The studio API (docs/design/07-platform-services.md §3): tokens minted
	// per launch, a stage directory per launch, and the service both
	// providers serve. The conformance suite builds its daemon from the same
	// constructor.
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: dirs, API: "http://" + addr + studioapi.Base,
		Provider: "daemon", Version: version, Logf: log.Printf})

	w := weights.New(weights.Config{Store: st, Dirs: dirs, HF: weights.NewHF(huggingFaceToken), Logf: log.Printf})
	sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Weights: w, Logf: log.Printf, Platform: plat.Launches})
	sup.SetStudios(loadStudios(dirs, studiosDir))
	installer := install.New(install.Config{Store: st, Dirs: dirs, Supervisor: sup, Weights: w, Logf: log.Printf})
	if err := installer.Sweep(ctx); err != nil {
		return err
	}

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
	// A group that died with the last daemon never had its tokens revoked or
	// its stage cleared.
	revoked, cleared, err := plat.Readopted(ctx, sup)
	if err != nil {
		return err
	}
	if revoked > 0 || len(cleared) > 0 {
		log.Printf("revoked %d studio token(s) and cleared %d stage director(ies) left by groups that are no longer running", revoked, len(cleared))
	}
	svc, studioAPI := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
	// An export a killed helmstudio left rendering is stopped, and its partial
	// file removed, before anything is served (docs/decisions.md M8 Q16).
	svc.SweepExports(ctx)

	handler, err := api.New(sup, web.Shelf, addr, log.Printf, api.WithInstall(installer, w, dirs.Logs()), api.WithStudioAPI(studioAPI, svc), api.WithSecrets(platform.Secrets(), st), api.WithApproval(st))
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
	go svc.WatchJobs(baseCtx, 500*time.Millisecond)
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
	svc.Events().Shutdown("helmstudio is stopping")
	cancelRequests()
	_ = srv.Shutdown(shutdownCtx)
	// Install jobs stop first, recorded interrupted, so a build step is never
	// left running beside a studio being stopped.
	if err := installer.Shutdown(shutdownCtx); err != nil {
		log.Print(err)
	}
	return sup.Shutdown(shutdownCtx)
}

// version is reported by GET /me; release builds set it with -ldflags.
var version = "dev"

// hfTokenName is the Keychain account the Hugging Face token is stored under,
// in the helmstudio service (R43). Settings writes it through
// PUT /launcher/settings/huggingface-token (M6 Q21); it can still be set by
// hand with `security add-generic-password -s helmstudio -a huggingface-token -w`.
const hfTokenName = api.HFTokenName

// huggingFaceToken reads the token for each request, so one added while the
// daemon runs is used on the next retry. No token, or no secret store on this
// platform, means asking anonymously.
func huggingFaceToken(ctx context.Context) (string, error) {
	tok, err := platform.Secrets().Get(ctx, hfTokenName)
	if errors.Is(err, platform.ErrSecretNotFound) || errors.Is(err, platform.ErrNoSecretStore) {
		return "", nil
	}
	return tok, err
}

// loadStudios resolves the library: every studio this machine knows about,
// from the local directory, installed checkouts, the manifest cache and the
// bundled registry, first match winning by existence (internal/library).
//
// An entry that does not resolve is logged rather than dropped silently. R2 as
// amended by M7 Q6 says an invalid manifest is *listed* as invalid — the
// supervisor can only be given studios it could launch, so the library's own
// view of the problems reaches the API through GET /studios instead.
func loadStudios(dirs *platform.Dirs, registryDir string) []supervisor.Studio {
	ok, problems, err := library.Default(dirs.Data(), dirs.Cache(), studios.Bundled, registryDir).Studios()
	if err != nil {
		log.Printf("resolving the library: %v", err)
		return nil
	}
	for _, e := range problems {
		switch e.State {
		case library.StateNotFetched:
			log.Printf("%s: its manifest has not been fetched yet (%s)", e.ID, e.Repo)
		default:
			for _, err := range e.Errors {
				log.Printf("%s is listed invalid: %s", e.ID, err)
			}
		}
	}
	if len(ok) == 0 && len(problems) == 0 {
		log.Printf("no studios in the library")
	}
	out := make([]supervisor.Studio, 0, len(ok))
	for _, e := range ok {
		out = append(out, supervisor.Studio{Manifest: e.Manifest, File: e.File})
	}
	return out
}
