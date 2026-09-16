package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/weights"
)

// Option configures a Server.
type Option func(*Server)

// WithStudioAPI serves every studio-api operation through h, and the
// launcher's asset reclaim through svc (docs/decisions.md M4).
func WithStudioAPI(h *studioapi.Handler, svc *studioapi.Service) Option {
	return func(s *Server) {
		s.studioAPI, s.service = h, svc
	}
}

// WithInstall serves the install, job and weight operations
// (api/openapi.yaml "Registry, install, weights" and "Jobs"), plus the
// additions recorded in docs/decisions.md ("M3 install and weights").
func WithInstall(in *install.Installer, w *weights.Service, logsRoot string) Option {
	return func(s *Server) {
		s.installer, s.weights, s.logsRoot = in, w, logsRoot
	}
}

func (s *Server) routeInstall() {
	if s.installer == nil {
		return
	}
	// The launcher's job queue (first review #2): never task jobs, which are
	// a studio's own and reach only its token at /jobs.
	s.mux.HandleFunc("PUT "+Base+"/studios/{id}/selection", s.putSelection)
	s.mux.HandleFunc("GET "+Base+"/launcher/jobs", s.listJobs)
	s.mux.HandleFunc("GET "+Base+"/launcher/jobs/{id}", s.getJob)
	s.mux.HandleFunc("POST "+Base+"/launcher/jobs/{action}", s.jobAction)
	s.mux.HandleFunc("GET "+Base+"/launcher/jobs/{id}/logs", s.streamJobLogs)
	s.mux.HandleFunc("GET "+Base+"/models", s.listModels)
	s.mux.HandleFunc("GET "+Base+"/models/{id}", s.getModel)
	s.mux.HandleFunc("DELETE "+Base+"/models/{id}", s.deleteModel)
	s.mux.HandleFunc("GET "+Base+"/models:reclaim", s.previewReclaim)
	s.mux.HandleFunc("POST "+Base+"/models:reclaim", s.reclaim)
	s.mux.HandleFunc("POST "+Base+"/studios/{id}/weights/{action}", s.weightAction)
}

var installStatus = map[install.ErrorKind]int{
	install.KindNotFound:     http.StatusNotFound,
	install.KindNotInstalled: http.StatusConflict,
	install.KindBlocked:      http.StatusUnprocessableEntity,
	install.KindConflict:     http.StatusConflict,
}

// failInstall maps install and weights errors; anything else goes to fail.
func (s *Server) failInstall(w http.ResponseWriter, err error) {
	var ie *install.Error
	switch {
	case errors.As(err, &ie):
		writeJSON(w, installStatus[ie.Kind], errorBody{Error: string(ie.Kind), Message: ie.Message, Details: ie.Details})
	case errors.Is(err, errTaskJob):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, install.ErrNoJob), errors.Is(err, weights.ErrNoArtifact):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, install.ErrNotRunning):
		writeError(w, http.StatusConflict, "not_running", err.Error())
	case errors.Is(err, weights.ErrInUse):
		writeError(w, http.StatusConflict, "in_use", err.Error())
	case errors.Is(err, weights.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, weights.ErrMissing):
		writeError(w, http.StatusConflict, "missing", err.Error())
	default:
		s.fail(w, err)
	}
}

// studioInstallAction serves :install, :retry and :uninstall. Retry is
// install: install resumes wherever the last attempt stopped.
func (s *Server) studioInstallAction(w http.ResponseWriter, r *http.Request, id, action string) {
	var j install.Job
	var err error
	switch action {
	case "install", "retry":
		j, err = s.installer.Install(r.Context(), id)
	case "uninstall":
		j, err = s.installer.Uninstall(r.Context(), id)
	}
	if err != nil {
		s.failInstall(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

// errTaskJob hides a studio's task job from the launcher's queue.
var errTaskJob = errors.New("no such job in the launcher's queue")

// launcherJobsKept bounds how far back the launcher's queue reads.
const launcherJobsKept = 1000

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.installer.Jobs(r.Context(), r.URL.Query().Get("studio"), launcherJobsKept)
	if err != nil {
		s.failInstall(w, err)
		return
	}
	jobs = slices.DeleteFunc(jobs, func(j install.Job) bool { return j.Kind == "task" })
	if page, ok := paginate(w, r, jobs, func(j install.Job) string { return j.ID }); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

// launcherJob reads a job the launcher may see: any but a task job.
func (s *Server) launcherJob(r *http.Request, id string) (install.Job, error) {
	j, err := s.installer.Job(r.Context(), id)
	if err == nil && j.Kind == "task" {
		return install.Job{}, errTaskJob
	}
	return j, err
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.launcherJob(r, r.PathValue("id"))
	if err != nil {
		s.failInstall(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request) {
	id, action, ok := strings.Cut(r.PathValue("action"), ":")
	if !ok || action != "cancel" {
		writeError(w, http.StatusNotFound, "not_found", "the only job action is :cancel")
		return
	}
	if _, err := s.launcherJob(r, id); err != nil {
		s.failInstall(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := s.installer.Cancel(ctx, id); err != nil {
		s.failInstall(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// streamJobLogs is a text/event-stream of an install job's build output
// (R9), step by step: "step" when a step's log begins, "line" per line, and
// "end" once the job has finished and every log has been sent.
func (s *Server) streamJobLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.launcherJob(r, id); err != nil {
		s.failInstall(w, err)
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	send := func(event string, v any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		b, _ := json.Marshal(v)
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		return err == nil
	}

	type cursor struct {
		offset  int64
		partial string
	}
	sent := map[string]*cursor{}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		j, err := s.installer.Job(r.Context(), id)
		if err != nil {
			return
		}
		finished := j.FinishedAt != nil || (j.State != install.JobRunning && j.State != install.JobQueued)
		for _, step := range j.Steps {
			if step.LogPath == "" || !filepath.IsLocal(step.LogPath) {
				continue
			}
			c := sent[step.LogPath]
			if c == nil {
				c = &cursor{}
				sent[step.LogPath] = c
				if !send("step", map[string]any{"step_index": step.Index, "step_name": step.Name, "command": step.Command}) {
					return
				}
			}
			f, err := os.Open(filepath.Join(s.logsRoot, step.LogPath))
			if err != nil {
				continue
			}
			f.Seek(c.offset, io.SeekStart)
			br := bufio.NewReaderSize(f, 64<<10)
			for {
				chunk, err := br.ReadString('\n')
				c.offset += int64(len(chunk))
				if strings.HasSuffix(chunk, "\n") {
					if !send("line", map[string]any{"step_index": step.Index, "text": c.partial + strings.TrimSuffix(chunk, "\n")}) {
						f.Close()
						return
					}
					c.partial = ""
				} else {
					c.partial += chunk
				}
				if err != nil {
					break
				}
			}
			f.Close()
			if finished && c.partial != "" {
				send("line", map[string]any{"step_index": step.Index, "text": c.partial})
				c.partial = ""
			}
		}
		if finished {
			send("end", map[string]any{"state": j.State, "last_error": j.LastError})
			rc.Flush()
			return
		}
		if rc.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	arts, err := s.weights.List(r.Context())
	if err != nil {
		s.failInstall(w, err)
		return
	}
	if arts == nil {
		arts = []weights.Artifact{}
	}
	// Newest first, as every collection is.
	slices.SortStableFunc(arts, func(a, b weights.Artifact) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	if page, ok := paginate(w, r, arts, func(a weights.Artifact) string { return a.ID }); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

// modelView is one artifact, with the one-item reclaim preview a DELETE of
// it must confirm when it is a download no studio uses.
type modelView struct {
	weights.Artifact
	Reclaim *weights.ReclaimPreview `json:"reclaim,omitempty"`
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	a, err := s.weights.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failInstall(w, err)
		return
	}
	v := modelView{Artifact: a}
	if a.Source == weights.SourceManaged && a.RefCount == 0 {
		p, err := s.weights.PreviewReclaimOne(r.Context(), a.ID)
		if err != nil {
			s.failInstall(w, err)
			return
		}
		if len(p.Items) > 0 {
			v.Reclaim = &p
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// deleteModel removes one artifact (api/openapi.yaml: "Reclaim a managed
// artifact with ref_count 0, or unlink a linked one"). A linked one is
// unlinked. A download must carry ?confirm= from GET /models/{id}'s reclaim
// preview, so what is deleted is exactly what was shown (decided in the M3
// review); without it, or when it no longer matches, nothing is deleted and
// 409 carries the current preview. One a studio uses is refused, naming it.
func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	a, err := s.weights.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failInstall(w, err)
		return
	}
	if a.Source == weights.SourceLinked {
		if err := s.weights.Unlink(r.Context(), a.ID); err != nil {
			s.failInstall(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.RefCount > 0 {
		writeError(w, http.StatusConflict, "in_use", fmt.Sprintf("%s at %s is used by %s; uninstall them first", a.HFRepo, a.Revision, strings.Join(a.Studios, ", ")))
		return
	}
	confirm := r.URL.Query().Get("confirm")
	if confirm == "" {
		p, err := s.weights.PreviewReclaimOne(r.Context(), a.ID)
		if err != nil {
			s.failInstall(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, errorBody{Error: "confirm_required",
			Message: fmt.Sprintf("deleting %s frees %d bytes; repeat with ?confirm=%s", a.Path, p.TotalBytes, p.Confirm), Details: map[string]any{"preview": p}})
		return
	}
	p, err := s.weights.ReclaimOne(r.Context(), a.ID, confirm)
	if errors.Is(err, weights.ErrPreviewChanged) {
		writeJSON(w, http.StatusConflict, errorBody{Error: "preview_changed", Message: err.Error(), Details: map[string]any{"preview": p}})
		return
	}
	if err != nil {
		s.failInstall(w, err)
		return
	}
	if len(p.Items) == 0 {
		writeError(w, http.StatusConflict, "not_reclaimable", fmt.Sprintf("%s is not reclaimable now (it may be downloading)", a.HFRepo))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) previewReclaim(w http.ResponseWriter, r *http.Request) {
	p, err := s.weights.PreviewReclaim(r.Context())
	if err != nil {
		s.failInstall(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// reclaim deletes exactly the previewed set: the body's confirm must match
// the current preview, or nothing is deleted and 409 carries the new one.
func (s *Server) reclaim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.Confirm == "" {
		writeError(w, http.StatusBadRequest, "bad_request", `send {"confirm": "<the confirm value from GET models:reclaim>"}`)
		return
	}
	p, err := s.weights.Reclaim(r.Context(), body.Confirm)
	if errors.Is(err, weights.ErrPreviewChanged) {
		writeJSON(w, http.StatusConflict, errorBody{Error: "preview_changed", Message: err.Error(), Details: map[string]any{"preview": p}})
		return
	}
	if err != nil {
		s.failInstall(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// weightAction serves POST /studios/{id}/weights/{name}:link with
// {"path": "/abs/dir"}, and :fetch.
func (s *Server) weightAction(w http.ResponseWriter, r *http.Request) {
	name, action, ok := strings.Cut(r.PathValue("action"), ":")
	id := r.PathValue("id")
	switch {
	case ok && action == "link":
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "bad_request", `send {"path": "/absolute/directory"}`)
			return
		}
		a, err := s.installer.LinkWeight(r.Context(), id, name, body.Path)
		if err != nil {
			var ie *install.Error
			if !errors.As(err, &ie) && !errors.Is(err, weights.ErrConflict) && !errors.Is(err, weights.ErrInUse) {
				// The directory check's refusals name what is missing.
				writeError(w, http.StatusUnprocessableEntity, "not_linkable", err.Error())
				return
			}
			s.failInstall(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	case ok && action == "fetch":
		j, err := s.installer.FetchWeight(r.Context(), id, name)
		if err != nil {
			s.failInstall(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, j)
	default:
		writeError(w, http.StatusNotFound, "not_found", "weight actions are :link and :fetch")
	}
}

// PUT /studios/{id}/selection — which checkpoint a studio launches with
// (docs/decisions.md M7 Q20, Q21).
//
// Changing it while the studio runs is refused: the running process was given
// a path at spawn, and changing the row underneath would make the database
// disagree with what is loaded in memory. Changing it while stopped needs no
// new approval — every selectable weight was approved with the manifest, and
// asking again at each switch would train someone to click through it.
func (s *Server) putSelection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.weights == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "this daemon does not serve weights")
		return
	}
	var body struct {
		Weight string `json:"weight"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Weight == "" {
		writeError(w, http.StatusBadRequest, "bad_request", `the body must be {"weight": "<name>"}`)
		return
	}
	if gs, err := s.sup.Status(r.Context(), id); err == nil {
		if gs.State == "starting" || gs.State == "running" {
			writeError(w, http.StatusConflict, "conflict",
				"stop the studio before changing its checkpoint: the running process was handed a path when it started, and changing it now would leave the record disagreeing with what is loaded")
			return
		}
	}
	if err := s.weights.Select(r.Context(), id, body.Weight); err != nil {
		writeError(w, http.StatusConflict, "not_fetched", err.Error()+". Use :fetch to download it, or :link to point at a copy you already have.")
		return
	}
	st, ok := s.sup.Studio(id)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "selection": body.Weight})
		return
	}
	v, err := s.studio(r, st)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
