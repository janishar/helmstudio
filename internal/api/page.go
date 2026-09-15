package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// Page is every launcher collection's shape, the same as the studio API's
// (api/openapi.yaml conventions, docs/decisions.md M4 Q5).
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// paginate pages a list that is already in its order, using the key of the
// last item returned as the cursor. Launcher lists are small and read whole;
// the cursor keeps them consistent with every other collection.
func paginate[T any](w http.ResponseWriter, r *http.Request, all []T, key func(T) string) (Page[T], bool) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer from 1 to 200")
			return Page[T]{}, false
		}
		limit = n
	}
	start := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		found := false
		if err == nil {
			for i, it := range all {
				if key(it) == string(raw) {
					start, found = i+1, true
					break
				}
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "bad_cursor", "the cursor is not one this list issued, or its item is gone; start again without it")
			return Page[T]{}, false
		}
	}
	end := min(start+limit, len(all))
	p := Page[T]{Items: append([]T{}, all[start:end]...)}
	if end < len(all) {
		c := base64.RawURLEncoding.EncodeToString([]byte(key(all[end-1])))
		p.NextCursor = &c
	}
	return p, true
}
