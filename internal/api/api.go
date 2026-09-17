// Package api is the daemon's HTTP surface: the plain shelf's studio, launch,
// stop, process and log operations from api/openapi.yaml, plus one live log
// stream that document does not have yet (docs/decisions.md, "M2
// supervision"), and, with WithInstall, install, jobs and the weights cache
// (docs/decisions.md, "M3 install and weights").
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/approval"
	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/library"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/theme"
	"github.com/janishar/helmstudio/internal/weights"
)

// Base is the API prefix (api/openapi.yaml servers.url).
const Base = "/api/v1"

// Server serves the API and the shelf.
type Server struct {
	sup   *supervisor.Supervisor
	hosts map[string]bool // acceptable Host header values
	mux   *http.ServeMux
	logf  func(string, ...any)

	installer *install.Installer
	weights   *weights.Service
	logsRoot  string

	studioAPI *studioapi.Handler
	service   *studioapi.Service
	theme     *theme.Settings

	secrets     platform.SecretStore
	secretStore *store.Store
	approvals   *store.Store
	library     *library.Resolver
	local       *library.Local
	repoReader  *library.Reader
	fetcher     *library.Fetcher
	about       *About
}

// New returns the handler for a daemon listening on listenAddr, which must be
// a loopback host:port. shelf holds the plain shelf's static files.
func New(sup *supervisor.Supervisor, shelf fs.FS, listenAddr string, logf func(string, ...any), opts ...Option) (*Server, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen address %q: %w", listenAddr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("listen address %q is not loopback; helmstudio only listens on this machine", listenAddr)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{sup: sup, mux: http.NewServeMux(), logf: logf, hosts: map[string]bool{
		net.JoinHostPort("127.0.0.1", port): true,
		net.JoinHostPort("localhost", port): true,
		net.JoinHostPort("::1", port):       true,
	}}
	for _, o := range opts {
		o(s)
	}
	s.mux.HandleFunc("GET "+Base+"/studios", s.listStudios)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}", s.getStudio)
	s.mux.HandleFunc("POST "+Base+"/studios/{action}", s.studioAction)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/approval", s.getApproval)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/processes", s.getProcesses)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/logs", s.getLogFiles)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/processes/{name}/logs", s.streamLogs)
	s.routeInstall()
	if s.service != nil {
		s.theme = s.service.Theme()
	}
	s.routeTheme()
	s.routeSecrets()
	s.routeAbout()
	s.routeManifests()
	if s.service != nil {
		s.mux.HandleFunc("GET "+Base+"/assets:reclaim", s.service.ServeReclaim)
		s.mux.HandleFunc("POST "+Base+"/assets:reclaim", s.service.ServeReclaim)
	}
	s.mux.Handle("GET /", http.FileServerFS(shelf))
	return s, nil
}

// ServeHTTP refuses any request whose Host is not this daemon's loopback
// address (DNS rebinding needs a name), and any browser request from another
// origin: launch and stop change what runs on the machine, and without this a
// page on any website could post to 127.0.0.1:8700. A request with no Origin
// header is not a cross-site browser request and is allowed, so curl works.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hosts[r.Host] {
		writeError(w, http.StatusMisdirectedRequest, "bad_host", fmt.Sprintf("Host %q is not this daemon's address", r.Host))
		return
	}
	if originExempt(r) {
		// The SDK files and the theme are public: a studio's page on its own
		// port fetches them in CORS mode (docs/decisions.md M6 Q9, Q14).
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if origin := r.Header.Get("Origin"); origin != "" {
		o, ok := strings.CutPrefix(origin, "http://")
		if !ok || !s.hosts[o] {
			writeError(w, http.StatusForbidden, "bad_origin", fmt.Sprintf("requests from %q are not accepted", origin))
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	// Studio-api routes first: every one requires a studio token (Q7). What
	// the studio router does not recognise is the launcher's.
	if s.studioAPI != nil && s.studioAPI.Serve(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Studio is one library entry as the shelf shows it. Root is the recorded
// checkout, or where install will put it; Install is present when the daemon
// serves install. ManifestLoaded is false for a studio still running from the last daemon
// whose manifest was not found or no longer validates: it can be stopped,
// not launched.
type Studio struct {
	ManifestLoaded bool                   `json:"manifest_loaded"`
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description,omitempty"`
	Kinds          []string               `json:"kinds"`
	Heavy          bool                   `json:"heavy"`
	PeakRAMGB      int                    `json:"peak_ram_gb,omitempty"`
	Root           string                 `json:"root"`
	RootPresent    bool                   `json:"root_present"`
	Group          supervisor.GroupStatus `json:"group"`
	// Selection is the checkpoint this studio launches with, and Selectable
	// the ones it could. They are here rather than in libraryFields because
	// they are facts about an installation, not about which source won.
	Selection  string            `json:"selection,omitempty"`
	Selectable []approval.Weight `json:"selectable,omitempty"`
	// RebuildNeededReason says why, when RebuildNeeded is set. An Override is
	// the usual cause, and a card that says "rebuild needed" without saying
	// what changed leaves someone guessing at their own edit.
	RebuildNeededReason string `json:"rebuild_needed_reason,omitempty"`
	// Hue is what the launcher draws the studio's stripe in, and it is always
	// there: a stripe that fell back to the accent is what 03 §2 forbids.
	Hue Hue `json:"hue"`
	*install.Info
	libraryFields
}

// Hue is a studio's identity hue per theme, "#rrggbb" (03 §2c).
type Hue struct {
	Dark  string `json:"dark"`
	Light string `json:"light"`
}

// hueOf is the manifest's hue, or the ramp entry for id when there is no
// manifest to declare one — an entry that did not load still has a stripe.
func hueOf(m *manifest.Manifest, id string) Hue {
	p := theme.RampFor(id)
	if m != nil {
		p = theme.Accent(m)
	}
	return Hue{Dark: p.Dark, Light: p.Light}
}

func (s *Server) studio(r *http.Request, st supervisor.Studio) (Studio, error) {
	m := st.Manifest
	out := Studio{ManifestLoaded: true, ID: m.ID, Name: m.Name, Description: m.Description, Kinds: m.Kinds, PeakRAMGB: m.PeakRAMGB,
		Root: m.LocalPath, Hue: hueOf(m, m.ID)}
	if s.installer != nil {
		info, err := s.installer.Info(r.Context(), m.ID, m)
		if err != nil {
			return out, err
		}
		out.Info = &info
		out.Root = s.installer.PlannedRoot(m)
		if info.Root != "" {
			out.Root = info.Root
		}
	}
	for _, p := range m.EffectiveProcesses() {
		out.Heavy = out.Heavy || p.Heavy
	}
	if fi, err := os.Stat(out.Root); err == nil && fi.IsDir() {
		out.RootPresent = true
	}
	out.Selection, out.Selectable = s.selection(r.Context(), m)
	if out.Info != nil && out.Info.RebuildNeeded {
		out.RebuildNeededReason = "The manifest changed since this checkout was built from it, so the build steps would run differently now."
	}
	gs, err := s.sup.Status(r.Context(), m.ID)
	out.Group = gs
	return out, err
}

// selection reads which checkpoint a studio launches with, and which it could.
//
// A manifest with no selectable weight has neither, and says so by carrying
// nothing: a studio with one checkpoint is not making a choice.
func (s *Server) selection(ctx context.Context, m *manifest.Manifest) (string, []approval.Weight) {
	var list []approval.Weight
	for _, w := range m.Weights {
		if !w.Selectable {
			continue
		}
		list = append(list, approval.Weight{
			Name: w.Name, Repo: w.Repo, Revision: w.Revision,
			Selectable: true, Optional: w.Optional, LocalPath: w.LocalPath,
		})
	}
	if len(list) == 0 || s.weights == nil {
		return "", list
	}
	name, err := s.weights.Selected(ctx, m.ID)
	if err != nil {
		return "", list
	}
	return name, list
}

func (s *Server) listStudios(w http.ResponseWriter, r *http.Request) {
	out := []Studio{}
	if s.library != nil {
		// The library is the list: every id this machine knows about, from
		// every source, including the ones whose manifest did not resolve.
		entries, err := s.fromLibrary(r)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = entries
		seen := map[string]bool{}
		for _, e := range out {
			seen[e.ID] = true
		}
		// A studio still running from a previous daemon is in the list even
		// when nothing in the library describes it any more: it can be
		// stopped, and something has to offer that.
		for _, id := range s.sup.Unmanaged() {
			if seen[id] {
				continue
			}
			v, err := s.unmanaged(r, id)
			if err != nil {
				s.fail(w, err)
				return
			}
			out = append(out, v)
		}
		slices.SortFunc(out, func(a, b Studio) int { return strings.Compare(a.ID, b.ID) })
		if page, ok := paginate(w, r, out, func(st Studio) string { return st.ID }); ok {
			writeJSON(w, http.StatusOK, page)
		}
		return
	}
	for _, st := range s.sup.Studios() {
		v, err := s.studio(r, st)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, v)
	}
	for _, id := range s.sup.Unmanaged() {
		v, err := s.unmanaged(r, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b Studio) int { return strings.Compare(a.ID, b.ID) })
	if page, ok := paginate(w, r, out, func(st Studio) string { return st.ID }); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

func (s *Server) unmanaged(r *http.Request, id string) (Studio, error) {
	gs, err := s.sup.Status(r.Context(), id)
	out := Studio{ID: id, Name: id, Kinds: []string{}, Heavy: true, Group: gs, Hue: hueOf(nil, id)}
	if err == nil && s.installer != nil {
		info, ierr := s.installer.Info(r.Context(), id, nil)
		out.Info, err = &info, ierr
	}
	return out, err
}

func (s *Server) getStudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, ok := s.sup.Studio(id)
	var v Studio
	var err error
	switch {
	case ok:
		v, err = s.studio(r, st)
	case s.library != nil:
		// It may be an entry whose manifest did not resolve, which the
		// supervisor has never heard of and which still has to be readable —
		// the editor opens from here.
		entries, lerr := s.library.Resolve()
		if lerr != nil {
			s.fail(w, lerr)
			return
		}
		for _, e := range entries {
			if e.ID == id {
				writeJSON(w, http.StatusOK, s.entry(r, e))
				return
			}
		}
		if slices.Contains(s.sup.Unmanaged(), id) {
			v, err = s.unmanaged(r, id)
			break
		}
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q is known", id))
		return
	case slices.Contains(s.sup.Unmanaged(), id):
		v, err = s.unmanaged(r, id)
	default:
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q is known", id))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// studioAction serves POST /studios/{id}:launch and /studios/{id}:stop. The
// ServeMux cannot match a wildcard and a literal inside one path segment, so
// the segment is split here.
func (s *Server) studioAction(w http.ResponseWriter, r *http.Request) {
	id, action, ok := strings.Cut(r.PathValue("action"), ":")
	if !ok {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST needs an action, such as :launch or :stop")
		return
	}
	switch action {
	case "install", "retry", "uninstall":
		if s.installer == nil {
			writeError(w, http.StatusNotImplemented, "not_implemented", "this daemon does not serve install")
			return
		}
		// Uninstall removes; it runs nothing of the studio's, so it needs no
		// approval. Install and retry run build steps.
		if action != "uninstall" && !s.requireApproval(w, r, id) {
			return
		}
		s.studioInstallAction(w, r, id, action)
	case "launch":
		// cmd and health.exec run here and never passed through install, so
		// launch is gated too — without this, an Override that edits a command
		// would simply run (M7 Q10).
		if !s.requireApproval(w, r, id) {
			return
		}
		// preempt carries the confirm digest of a heavy_conflict refusal
		// (docs/decisions.md M5 Q13); a stale one is preview_changed.
		gs, err := s.sup.Launch(r.Context(), id, supervisor.LaunchOptions{Confirm: r.URL.Query().Get("preempt")})
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, gs)
	case "stop":
		if err := s.sup.Stop(id); err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "stopping"})
	default:
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no action %q", action))
	}
}

func (s *Server) getProcesses(w http.ResponseWriter, r *http.Request) {
	gs, err := s.sup.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

func (s *Server) getLogFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sup.Studio(r.PathValue("id")); !ok && !slices.Contains(s.sup.Unmanaged(), r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no studio %q is known", r.PathValue("id")))
		return
	}
	files, err := s.sup.LogFiles(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if files == nil {
		files = []supervisor.LogFile{}
	}
	if page, ok := paginate(w, r, files, func(f supervisor.LogFile) string { return f.ID }); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

// streamLogs is a text/event-stream of one process's output. Events: "line"
// (data: a LogLine, id: its seq), "gap" (lines this viewer lost for being
// slow, or bytes the daemon skipped), and "end" when the process is not live
// and the file's tail has been sent. Reconnect with Last-Event-ID to resume.
//
// A viewer slower than the process loses lines and is told how many; it never
// slows the process or other viewers.
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	sub, err := s.sup.SubscribeLogs(r.Context(), r.PathValue("id"), r.PathValue("name"), after)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer sub.Close()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	send := func(event string, id uint64, v any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		b, _ := json.Marshal(v)
		if id > 0 {
			if _, err := fmt.Fprintf(w, "id: %d\n", id); err != nil {
				return false
			}
		}
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		return err == nil
	}
	line := func(l supervisor.LogLine) bool {
		if l.Gap > 0 {
			return send("gap", l.Seq, l)
		}
		return send("line", l.Seq, l)
	}
	for _, l := range sub.History {
		if !line(l) {
			return
		}
	}
	if sub.Lines == nil {
		send("end", 0, map[string]string{"reason": "the process is not running; this is the end of its last log"})
		rc.Flush()
		return
	}
	if rc.Flush() != nil {
		return
	}

	ping := time.NewTicker(time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case l := <-sub.Lines:
			if n := sub.Dropped(); n > 0 && !send("gap", 0, supervisor.LogLine{Gap: n, GapUnit: "lines"}) {
				return
			}
			if !line(l) {
				return
			}
			// Drain what is already waiting before flushing once.
			for more := true; more; {
				select {
				case l := <-sub.Lines:
					if !line(l) {
						return
					}
				default:
					more = false
				}
			}
			if rc.Flush() != nil {
				return
			}
		case <-ping.C:
			if n := sub.Dropped(); n > 0 && !send("gap", 0, supervisor.LogLine{Gap: n, GapUnit: "lines"}) {
				return
			}
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

var statusFor = map[supervisor.ErrorKind]int{
	supervisor.KindNotFound:       http.StatusNotFound,
	supervisor.KindAlreadyRunning: http.StatusConflict,
	supervisor.KindNotRunning:     http.StatusConflict,
	supervisor.KindNotLaunchable:  http.StatusUnprocessableEntity,
	supervisor.KindPortConflict:   http.StatusConflict,
	supervisor.KindHeavyConflict:  http.StatusConflict,
	supervisor.KindPreviewChanged: http.StatusConflict,
}

// errorBody is every error response (api/openapi.yaml components.schemas.Error):
// a stable code, a message for a person, and details such as the memory
// arithmetic when that is the reason (Q4).
type errorBody struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	var se *supervisor.Error
	if errors.As(err, &se) {
		body := errorBody{Error: string(se.Kind), Message: se.Message}
		if se.Heavy != nil {
			body.Details = map[string]any{"heavy": se.Heavy}
		}
		writeJSON(w, statusFor[se.Kind], body)
		return
	}
	s.logf("api: %v", err)
	writeError(w, http.StatusInternalServerError, "internal", err.Error())
}

func writeError(w http.ResponseWriter, status int, kind, msg string) {
	writeJSON(w, status, errorBody{Error: kind, Message: msg})
}

// writeErrorDetails is writeError with the structured part a caller acts on —
// the heavy arithmetic of a switch conflict, the preview of a refused install.
func writeErrorDetails(w http.ResponseWriter, status int, kind, msg string, details map[string]any) {
	writeJSON(w, status, errorBody{Error: kind, Message: msg, Details: details})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
