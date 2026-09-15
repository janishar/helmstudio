// Package api is the daemon's HTTP surface. In M2 it carries only what the
// plain shelf needs: the launcher's studio, launch, stop, process and log
// operations from api/openapi.yaml, plus one live log stream that document
// does not have yet (docs/decisions.md, "M2 supervision").
package api

import (
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

	"github.com/janishar/helmstudio/internal/supervisor"
)

// Base is the API prefix (api/openapi.yaml servers.url).
const Base = "/api/v1"

// Server serves the API and the shelf.
type Server struct {
	sup   *supervisor.Supervisor
	hosts map[string]bool // acceptable Host header values
	mux   *http.ServeMux
	logf  func(string, ...any)
}

// New returns the handler for a daemon listening on listenAddr, which must be
// a loopback host:port. shelf holds the plain shelf's static files.
func New(sup *supervisor.Supervisor, shelf fs.FS, listenAddr string, logf func(string, ...any)) (*Server, error) {
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
	s.mux.HandleFunc("GET "+Base+"/studios", s.listStudios)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}", s.getStudio)
	s.mux.HandleFunc("POST "+Base+"/studios/{action}", s.studioAction)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/processes", s.getProcesses)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/logs", s.getLogFiles)
	s.mux.HandleFunc("GET "+Base+"/studios/{id}/processes/{name}/logs", s.streamLogs)
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
	if origin := r.Header.Get("Origin"); origin != "" {
		o, ok := strings.CutPrefix(origin, "http://")
		if !ok || !s.hosts[o] {
			writeError(w, http.StatusForbidden, "bad_origin", fmt.Sprintf("requests from %q are not accepted", origin))
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	s.mux.ServeHTTP(w, r)
}

// Studio is one library entry as the shelf shows it. install_state from the
// api/openapi.yaml outline is absent: installations arrive in M3.
// ManifestLoaded is false for a studio still running from the last daemon
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
}

func (s *Server) studio(r *http.Request, st supervisor.Studio) (Studio, error) {
	m := st.Manifest
	out := Studio{ManifestLoaded: true, ID: m.ID, Name: m.Name, Description: m.Description, Kinds: m.Kinds, PeakRAMGB: m.PeakRAMGB,
		Root: m.LocalPath}
	for _, p := range m.EffectiveProcesses() {
		out.Heavy = out.Heavy || p.Heavy
	}
	if fi, err := os.Stat(out.Root); err == nil && fi.IsDir() {
		out.RootPresent = true
	}
	gs, err := s.sup.Status(r.Context(), m.ID)
	out.Group = gs
	return out, err
}

func (s *Server) listStudios(w http.ResponseWriter, r *http.Request) {
	out := []Studio{}
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
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) unmanaged(r *http.Request, id string) (Studio, error) {
	gs, err := s.sup.Status(r.Context(), id)
	return Studio{ID: id, Name: id, Kinds: []string{}, Heavy: true, Group: gs}, err
}

func (s *Server) getStudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, ok := s.sup.Studio(id)
	var v Studio
	var err error
	switch {
	case ok:
		v, err = s.studio(r, st)
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
	case "launch":
		preempt, _ := strconv.ParseBool(r.URL.Query().Get("preempt"))
		gs, err := s.sup.Launch(r.Context(), id, supervisor.LaunchOptions{Preempt: preempt})
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
	writeJSON(w, http.StatusOK, files)
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
}

// errorBody is every error response: a stable kind for code, a message for
// a person, and the memory arithmetic when that is the reason.
type errorBody struct {
	Error   string                      `json:"error"`
	Message string                      `json:"message"`
	Heavy   *supervisor.HeavyArithmetic `json:"heavy,omitempty"`
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	var se *supervisor.Error
	if errors.As(err, &se) {
		writeJSON(w, statusFor[se.Kind], errorBody{Error: string(se.Kind), Message: se.Message, Heavy: se.Heavy})
		return
	}
	s.logf("api: %v", err)
	writeError(w, http.StatusInternalServerError, "internal", err.Error())
}

func writeError(w http.ResponseWriter, status int, kind, msg string) {
	writeJSON(w, status, errorBody{Error: kind, Message: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
