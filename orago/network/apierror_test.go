package network

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestConstructorCodes(t *testing.T) {
	for _, c := range []struct {
		name        string
		constructor func(string, error) *APIError
		want        int
	}{
		{"BadRequest", NewBadRequestError, http.StatusBadRequest},
		{"Unauthorized", NewUnauthorizedError, http.StatusUnauthorized},
		{"Forbidden", NewForbiddenError, http.StatusForbidden},
		{"NotFound", NewNotFoundError, http.StatusNotFound},
		{"Conflict", NewConflictError, http.StatusConflict},
		{"LengthRequired", NewLengthRequiredError, http.StatusLengthRequired},
		{"RequestTooLarge", NewRequestTooLargeError, http.StatusRequestEntityTooLarge},
		{"Unprocessable", NewUnprocessableError, http.StatusUnprocessableEntity},
		{"TooManyRequests", NewTooManyRequestsError, http.StatusTooManyRequests},
		{"ServiceUnavailable", NewServiceUnavailableError, http.StatusServiceUnavailable},
		{"InternalServer", NewInternalServerError, http.StatusInternalServerError},
	} {
		cause := errors.New("cause")
		got := c.constructor("message", cause)
		if got.Code != c.want || got.Message != "message" || got.Err != cause {
			t.Errorf("%s: got %+v, want code %d", c.name, got, c.want)
		}
	}
}

func TestErrorIsMessageAlone(t *testing.T) {
	err := NewInternalServerError("public text", errors.New("private cause"))
	if got := err.Error(); got != "public text" {
		t.Errorf("Error() = %q", got)
	}
}

func TestSentinelWorksWithErrorsIs(t *testing.T) {
	sentinel := NewConflictError("email already registered", nil)
	other := NewConflictError("email already registered", nil)
	var err error = sentinel
	if !errors.Is(err, sentinel) {
		t.Error("the sentinel itself: errors.Is is false")
	}
	if !errors.Is(fmt.Errorf("create user: %w", err), sentinel) {
		t.Error("wrapped sentinel: errors.Is is false")
	}
	if errors.Is(err, other) {
		t.Error("two sentinels with the same text must stay distinct")
	}
}

func TestUnwrapReachesCause(t *testing.T) {
	cause := errors.New("cause")
	if !errors.Is(NewInternalServerError("message", cause), cause) {
		t.Error("errors.Is does not reach Err")
	}
	if NewNotFoundError("message", nil).Unwrap() != nil {
		t.Error("Unwrap with no cause must be nil")
	}
}
