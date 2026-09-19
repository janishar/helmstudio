package api

import (
	"net/http"
	"strings"

	"github.com/janishar/helmstudio/internal/api/studioapi"
)

// The studio-api operations the launcher mirrors: the gallery across every
// studio (03 §10) and the sequences in it (03 §11), both amended and recorded
// on 2026-09-19.
//
// Nothing about them is implemented here. The request is re-pointed at the
// studio-api path it mirrors and answered by the same generated router over
// the same service, under a principal that is the launcher rather than a
// studio. So the parameter decoding, Q9's read rule, the filter digest, the
// paging, `If-Match` and the error shapes are the ones a studio gets, and
// there is no second copy of any of them to drift.
//
// What keeps that from being "the studio API with no token" is this list: a
// path not on it is the launcher's 404 like any other, and 07 §3's "every
// studio endpoint refuses a request with no token" is untouched.
var mirrored = []string{
	"GET " + Base + "/launcher/gallery/items",
	"GET " + Base + "/launcher/assets/{id}",
	"GET " + Base + "/launcher/assets/{id}/thumb",
	"GET " + Base + "/launcher/timeline",
	"POST " + Base + "/launcher/timeline",
	"POST " + Base + "/launcher/timeline:append",
	"GET " + Base + "/launcher/timeline/{id}",
	"PATCH " + Base + "/launcher/timeline/{id}",
}

// A segment carrying an action is one segment to the mux, so POST
// /launcher/timeline/{id} would forward every action the studio API has on a
// sequence. These are the ones the launcher may take: not `:export`, whose
// owner is undecided (docs/decisions.md 2026-09-16, M8 Q14), not its `:plan`,
// and not `:open`, which is a studio's own window.
var timelineActions = []string{":revert"}

// routeMirrored mounts them. Without a service there is no store to read.
func (s *Server) routeMirrored() {
	if s.service == nil {
		return
	}
	h := studioapi.NewHandler(s.service, studioapi.Fixed(studioapi.Launcher()), s.logf)
	for _, pattern := range mirrored {
		s.mux.Handle(pattern, asStudioAPI(h))
	}
	s.mux.Handle("POST "+Base+"/launcher/timeline/{id}", onlyActions(asStudioAPI(h), timelineActions))
}

// onlyActions passes a path whose last segment ends in one of these actions
// and answers 404 for the rest, which is what the launcher says about a path
// it does not have.
func onlyActions(next http.Handler, actions []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		for _, a := range actions {
			if strings.HasSuffix(last, a) && len(last) > len(a) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeError(w, http.StatusNotFound, "not_found", "no operation at "+r.URL.Path)
	})
}

// asStudioAPI drops the /launcher segment and asks the studio-api router for
// the operation underneath. The gallery query is forced to every studio: the
// launcher has no items of its own, so `scope=self` would answer an empty page
// rather than the screen anybody asked for, and the contract gives it no scope
// to send.
func asStudioAPI(h *studioapi.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := *r.URL
		u.Path = strings.Replace(u.Path, Base+"/launcher/", Base+"/", 1)
		if u.RawPath != "" {
			u.RawPath = strings.Replace(u.RawPath, Base+"/launcher/", Base+"/", 1)
		}
		if strings.HasSuffix(u.Path, "/gallery/items") {
			q := u.Query()
			q.Set("scope", "all")
			u.RawQuery = q.Encode()
		}
		r2 := r.Clone(r.Context())
		r2.URL = &u
		h.ServeHTTP(w, r2)
	})
}
