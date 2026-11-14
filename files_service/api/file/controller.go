package file

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/storage"
)

// controller serves signed links. The signature is the auth: no API key, no bearer token.
type controller struct {
	network.Controller
	store  storage.Store
	signer *Signer
}

// NewController returns the controller of /files; signer checks the links it serves.
func NewController(store storage.Store, signer *Signer) network.Controller {
	return &controller{Controller: network.NewController("/files", nil, nil), store: store, signer: signer}
}

// MountRoutes registers GET and HEAD on /files/*path.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/*path", c.serveHandler)
	group.HEAD("/*path", c.serveHandler)
	group.PUT("/*path", c.uploadHandler)
}

// MaxUpload is the largest body an upload link may carry (50 MB); studio_service signs nothing larger.
const MaxUpload = 52428800

// uploadTypes are the content types an upload may have.
var uploadTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// uploadHandler stores the body of a signed upload link (PUT /files/<key>?exp=&bytes=&ct=&sig=) under its key, never
// over an existing object. The request must carry exactly the signed Content-Length (the server then never reads
// past it, so a larger body is never buffered) and Content-Type; the body streams to the object store.
func (c *controller) uploadHandler(ctx *gin.Context) {
	key := strings.TrimPrefix(ctx.Param("path"), "/")
	query := ctx.Request.URL.Query()
	size, err := strconv.ParseInt(query.Get("bytes"), 10, 64)
	if err != nil || size < 1 || size > MaxUpload || strings.Contains(key, "..") || !strings.HasPrefix(key, "u/") ||
		!c.signer.VerifyPut(key, query.Get("exp"), query.Get("bytes"), query.Get("ct"), query.Get("sig")) {
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	if ctx.Request.ContentLength != size {
		network.SendError(ctx, http.StatusLengthRequired, "Content-Length must be the signed bytes")
		return
	}
	contentType := query.Get("ct")
	if !uploadTypes[contentType] || ctx.GetHeader("Content-Type") != contentType {
		network.SendError(ctx, http.StatusUnsupportedMediaType, "Content-Type must be the signed type")
		return
	}
	// Create, not Put: of two PUTs of one link at most one stores, and an upload studio has checked is never replaced
	body := &counter{reader: http.MaxBytesReader(ctx.Writer, ctx.Request.Body, size)}
	err = c.store.Create(ctx.Request.Context(), key, body, size, contentType)
	switch {
	case errors.Is(err, storage.ErrExists):
		network.SendError(ctx, http.StatusConflict, "already uploaded")
		return
	case body.n != size:
		if err == nil {
			_ = c.store.Delete(context.WithoutCancel(ctx.Request.Context()), key) // never keep a partial object
		}
		network.SendError(ctx, http.StatusBadRequest, "the body ended before Content-Length bytes")
		return
	case err != nil:
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// counter counts the bytes read through it.
type counter struct {
	reader io.Reader
	n      int64
}

// Read reads and counts.
func (c *counter) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.n += int64(n)
	return n, err
}

// serveHandler streams one object. A bad link, a key outside u/ or a missing object is 403; a Stat that fails for any
// other reason is 500.
func (c *controller) serveHandler(ctx *gin.Context) {
	key := strings.TrimPrefix(ctx.Param("path"), "/") // Gin keeps the leading slash of a *path value
	if strings.Contains(key, "..") || !strings.HasPrefix(key, "u/") || !c.signer.Verify(key, ctx.Query("exp"), ctx.Query("sig")) {
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	info, err := c.store.Stat(ctx.Request.Context(), key)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			network.SendError(ctx, http.StatusInternalServerError, "internal error")
			return
		}
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	body, err := c.store.Get(ctx.Request.Context(), key)
	if err != nil {
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	defer body.Close()
	// uploaded documents bring arbitrary files (.html, .svg): never run them as active content on this origin
	ctx.Header("Content-Security-Policy", "sandbox")
	// a key never changes content (a new run or upload gets a new key) and studio's links stay the same for
	// up to a day: the browser keeps it as long as it likes (a year) without asking again; private, so no shared
	// cache holds it. The URL is the cache key, so a link re-signed the next day is a new entry.
	ctx.Header("Cache-Control", "private, max-age=31536000, immutable")
	ctx.Header("X-Content-Type-Options", "nosniff")
	contentType := info.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.Header("Content-Type", contentType)
	ctx.Header("Content-Length", strconv.FormatInt(info.Size, 10))
	_, _ = io.Copy(ctx.Writer, body)
}
