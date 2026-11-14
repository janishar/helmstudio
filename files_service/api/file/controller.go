package file

import (
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
	// a key never changes content (a new revision, try or upload gets a new key) and catalog's links stay the same for
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
