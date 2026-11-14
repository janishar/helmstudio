package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ora/orago/network"
)

// bodyLimit holds the per-path limit that NewBodyLimit configures.
type bodyLimit struct {
	limit func(path string) int64
}

// NewBodyLimit caps request bodies at limit(path) bytes before any handler reads them. A declared Content-Length is
// checked here. A chunked body can't be checked in advance, so reading past the limit fails with *http.MaxBytesError
// and the handler that reads it answers.
func NewBodyLimit(limit func(path string) int64) network.RootMiddleware {
	return &bodyLimit{limit: limit}
}

// Attach runs the check on every request.
func (m *bodyLimit) Attach(engine *gin.Engine) {
	engine.Use(m.handle)
}

// handle skips methods without a body. A POST with neither Content-Length nor chunked encoding is a 411, and a
// declared size that is too large or unparsable is a 413. Everything else gets a reader capped at the limit.
func (m *bodyLimit) handle(ctx *gin.Context) {
	request := ctx.Request
	if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
		return
	}

	limit := m.limit(request.URL.Path)
	contentLength := request.Header.Get("Content-Length")
	chunked := len(request.TransferEncoding) > 0
	if contentLength == "" && !chunked && request.Method == http.MethodPost {
		network.SendError(ctx, http.StatusLengthRequired, "Content-Length required")
		return
	}
	if contentLength != "" {
		size, err := strconv.ParseInt(contentLength, 10, 64)
		if err != nil || size > limit {
			network.SendError(ctx, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
	}
	request.Body = http.MaxBytesReader(ctx.Writer, request.Body, limit)
}
