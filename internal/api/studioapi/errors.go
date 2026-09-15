package studioapi

import (
	"fmt"
	"net/http"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

// Every refusal is a *helm.Error: the same type the SDK returns, so the code a
// studio branches on is the code the daemon wrote (api/openapi.yaml,
// components.responses).

func apiError(status int, code, format string, args ...any) *helm.Error {
	return &helm.Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) *helm.Error {
	return apiError(http.StatusBadRequest, "bad_request", format, args...)
}

func badFilter(format string, args ...any) *helm.Error {
	return apiError(http.StatusBadRequest, "bad_filter", format, args...)
}

func badCursor() *helm.Error {
	return apiError(http.StatusBadRequest, "bad_cursor", "the cursor is not one this query issued; start again without it")
}

func unauthenticated(format string, args ...any) *helm.Error {
	return apiError(http.StatusUnauthorized, "unauthenticated", format, args...)
}

func capabilityRequired(capability string) *helm.Error {
	e := apiError(http.StatusForbidden, "capability_required", "this needs the %s capability, which the studio's manifest does not declare", capability)
	e.Details = map[string]any{"capability": capability}
	return e
}

func forbidden(code, format string, args ...any) *helm.Error {
	return apiError(http.StatusForbidden, code, format, args...)
}

func notFound(format string, args ...any) *helm.Error {
	return apiError(http.StatusNotFound, "not_found", format, args...)
}

func conflict(code, format string, args ...any) *helm.Error {
	return apiError(http.StatusConflict, code, format, args...)
}

func etagMismatch(what string) *helm.Error {
	return conflict("etag_mismatch", "%s changed since that etag was read; read it again and retry", what)
}

func unprocessable(code, format string, args ...any) *helm.Error {
	return apiError(http.StatusUnprocessableEntity, code, format, args...)
}

func quotaExceeded(quota string, limit, used int64) *helm.Error {
	e := apiError(http.StatusTooManyRequests, "quota_exceeded", "the studio's %s quota is full: %d of %d used", quota, used, limit)
	e.Details = map[string]any{"quota": quota, "limit": limit, "used": used}
	return e
}

func unsupported(format string, args ...any) *helm.Error {
	return apiError(http.StatusNotImplemented, "unsupported", format, args...)
}
