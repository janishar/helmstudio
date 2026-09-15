package helm

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind is the one typed error shape shared by Go, Python and Node
// (docs/design/04-packages.md §4). Components branch on it, never on a status.
type Kind string

const (
	KindInvalid         Kind = "Invalid"
	KindUnauthenticated Kind = "Unauthenticated"
	KindForbidden       Kind = "Forbidden"
	KindNotFound        Kind = "NotFound"
	KindConflict        Kind = "Conflict"
	KindQuotaExceeded   Kind = "QuotaExceeded"
	KindUnsupported     Kind = "Unsupported"
	KindUnavailable     Kind = "Unavailable"
	KindInternal        Kind = "Internal"
)

// KindForStatus maps an HTTP status to its kind, as 04 §4 lists them.
func KindForStatus(status int) Kind {
	switch status {
	case 400, 413, 416, 422:
		return KindInvalid
	case 401:
		return KindUnauthenticated
	case 403, 421:
		return KindForbidden
	case 404, 410:
		return KindNotFound
	case 409:
		return KindConflict
	case 429, 507:
		return KindQuotaExceeded
	case 501:
		return KindUnsupported
	case 503:
		return KindUnavailable
	}
	return KindInternal
}

// Error is every error response (api/openapi.yaml components.schemas.Error),
// with the status it arrived with.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("helm: %s (%d %s): %s", e.Code, e.Status, http.StatusText(e.Status), e.Message)
}

// Kind is this error's kind.
func (e *Error) Kind() Kind {
	if e.Status == 0 {
		return KindUnavailable
	}
	return KindForStatus(e.Status)
}

// KindOf returns the kind of err: its Error's kind, Unavailable for a failure
// to reach the provider at all, and "" for nil.
func KindOf(err error) Kind {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind()
	}
	return KindUnavailable
}

// IsKind reports whether err is of kind k.
func IsKind(err error, k Kind) bool { return KindOf(err) == k }

// CodeOf returns the stable code of err, such as "etag_mismatch", or "".
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
