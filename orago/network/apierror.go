package network

import "net/http"

// APIError is an error with the HTTP status and detail text a client should see. Err is the cause, for logs only.
type APIError struct {
	Code    int
	Message string
	Err     error
}

// Error returns Message alone, so callers that forward err.Error() (the NATS error field) never expose the cause.
func (e *APIError) Error() string { return e.Message }

// Unwrap returns the cause, for errors.Is and errors.As.
func (e *APIError) Unwrap() error { return e.Err }

// NewAPIError builds an APIError. The constructors below fix the status; err may be nil.
func NewAPIError(code int, message string, err error) *APIError {
	return &APIError{Code: code, Message: message, Err: err}
}

// NewBadRequestError returns a 400.
func NewBadRequestError(message string, err error) *APIError {
	return NewAPIError(http.StatusBadRequest, message, err)
}

// NewUnauthorizedError returns a 401.
func NewUnauthorizedError(message string, err error) *APIError {
	return NewAPIError(http.StatusUnauthorized, message, err)
}

// NewForbiddenError returns a 403.
func NewForbiddenError(message string, err error) *APIError {
	return NewAPIError(http.StatusForbidden, message, err)
}

// NewNotFoundError returns a 404.
func NewNotFoundError(message string, err error) *APIError {
	return NewAPIError(http.StatusNotFound, message, err)
}

// NewConflictError returns a 409.
func NewConflictError(message string, err error) *APIError {
	return NewAPIError(http.StatusConflict, message, err)
}

// NewLengthRequiredError returns a 411.
func NewLengthRequiredError(message string, err error) *APIError {
	return NewAPIError(http.StatusLengthRequired, message, err)
}

// NewRequestTooLargeError returns a 413.
func NewRequestTooLargeError(message string, err error) *APIError {
	return NewAPIError(http.StatusRequestEntityTooLarge, message, err)
}

// NewUnprocessableError returns a 422.
func NewUnprocessableError(message string, err error) *APIError {
	return NewAPIError(http.StatusUnprocessableEntity, message, err)
}

// NewTooManyRequestsError returns a 429.
func NewTooManyRequestsError(message string, err error) *APIError {
	return NewAPIError(http.StatusTooManyRequests, message, err)
}

// NewServiceUnavailableError returns a 503.
func NewServiceUnavailableError(message string, err error) *APIError {
	return NewAPIError(http.StatusServiceUnavailable, message, err)
}

// NewInternalServerError returns a 500.
func NewInternalServerError(message string, err error) *APIError {
	return NewAPIError(http.StatusInternalServerError, message, err)
}
