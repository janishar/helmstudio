package api

import (
	"net/http"
	"strings"

	"github.com/janishar/helmstudio/internal/api/studioapi"
)

// The launcher's gallery (03 §10, docs/decisions.md 2026-09-19).
//
// The three operations under /launcher/ are the studio-api gallery and asset
// reads served to the daemon's own page. Nothing about them is implemented
// here: the request is re-pointed at the studio-api path it mirrors and
// answered by the same generated router over the same service, under a
// principal that is the launcher rather than a studio. So the parameter
// decoding, Q9's read rule, the filter digest, the paging and the error shapes
// are the ones a studio gets, and there is no second copy of them to drift.
//
// What keeps that from being "the studio API with no token" is the mux: only
// the three patterns below reach this handler, and a path it does not have is
// the launcher's 404 like any other.

// routeGallery mounts them. Without a service there is no store to read.
func (s *Server) routeGallery() {
	if s.service == nil {
		return
	}
	h := studioapi.NewHandler(s.service, studioapi.Fixed(studioapi.Launcher()), s.logf)
	for _, pattern := range []string{
		"GET " + Base + "/launcher/gallery/items",
		"GET " + Base + "/launcher/assets/{id}",
		"GET " + Base + "/launcher/assets/{id}/thumb",
	} {
		s.mux.Handle(pattern, asStudioAPI(h))
	}
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
