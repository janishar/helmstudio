package file

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
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

// MaxUpload is the largest image or audio upload a link may carry (50 MB); MaxLoraUpload the largest LoRA (2 GiB). studio_service
// signs nothing larger.
const (
	MaxUpload     = 52428800
	MaxLoraUpload = 2147483648
	// MaxImportVideo caps an imported video (contracts/README.md, Import: mp4 at most 2 GB)
	MaxImportVideo = 2147483648
)

// uploadTypes are the content types an image upload may have.
var uploadTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// loraKey is the only key shape a LoRA upload may have: u/<owner>/loras/<lora_id>.safetensors.
var loraKey = regexp.MustCompile(`^u/[0-9a-f-]{36}/loras/[0-9a-f-]{36}\.safetensors$`)

// importVideoKey is the only key shape a video upload may have: an imported take, u/<owner>/imports/<take_id>.mp4.
var importVideoKey = regexp.MustCompile(`^u/[0-9a-f-]{36}/imports/[0-9a-f-]{36}\.mp4$`)

// audioUploadKey is an audio upload's key (API 1.19; m4a from 1.38): u/<owner>/uploads/<take_id>.wav|.mp3|.m4a, its
// type by its extension.
var audioUploadKey = regexp.MustCompile(`^u/[^/]+/uploads/[^/]+\.(wav|mp3|m4a)$`)

// audioTypes maps an audio upload key's extension to its one content type.
var audioTypes = map[string]string{"wav": "audio/wav", "mp3": "audio/mpeg", "m4a": "audio/mp4"}

// uploadLimit is the cap and the allowed content types of a key: a LoRA key takes application/octet-stream up to
// 2 GiB, an import's video key video/mp4 up to 2 GiB, an audio upload's key its wav, mp3 or m4a type up to 50 MB, any
// other key an image up to 50 MB.
func uploadLimit(key string) (int64, func(string) bool) {
	if m := audioUploadKey.FindStringSubmatch(key); m != nil {
		return MaxUpload, func(ct string) bool { return ct == audioTypes[m[1]] }
	}
	if loraKey.MatchString(key) {
		return MaxLoraUpload, func(ct string) bool { return ct == "application/octet-stream" }
	}
	if importVideoKey.MatchString(key) {
		return MaxImportVideo, func(ct string) bool { return ct == "video/mp4" }
	}
	return MaxUpload, func(ct string) bool { return uploadTypes[ct] }
}

// uploadHandler stores the body of a signed upload link (PUT /files/<key>?exp=&bytes=&ct=&sig=) under its key, never
// over an existing object. The request must carry exactly the signed Content-Length (the server then never reads
// past it, so a larger body is never buffered) and Content-Type; the body streams to the object store.
func (c *controller) uploadHandler(ctx *gin.Context) {
	key := strings.TrimPrefix(ctx.Param("path"), "/")
	query := ctx.Request.URL.Query()
	limit, allowed := uploadLimit(key)
	size, err := strconv.ParseInt(query.Get("bytes"), 10, 64)
	if err != nil || size < 1 || size > limit || strings.Contains(key, "..") || !strings.HasPrefix(key, "u/") ||
		!c.signer.VerifyPut(key, query.Get("exp"), query.Get("bytes"), query.Get("ct"), query.Get("sig")) {
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	if ctx.Request.ContentLength != size {
		network.SendError(ctx, http.StatusLengthRequired, "Content-Length must be the signed bytes")
		return
	}
	contentType := query.Get("ct")
	if !allowed(contentType) || ctx.GetHeader("Content-Type") != contentType {
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

// lookThumb is a starter look thumbnail's key (contracts/registry.schema.json, looks: thumb.key).
var lookThumb = regexp.MustCompile(`^packs/[a-z0-9][a-z0-9_-]{0,63}/looks/[a-z0-9][a-z0-9-]{0,63}-[0-9a-f]{16}\.(png|jpg|webp)$`)

// voicePreview is a starter voice's preview clip (contracts/registry.schema.json, voices: preview.key), served as the
// look thumbnails are (contracts/README.md, Voices).
var voicePreview = regexp.MustCompile(`^packs/[a-z0-9][a-z0-9_-]{0,63}/voices/[a-z0-9][a-z0-9-]{0,63}-[0-9a-f]{16}\.wav$`)

// serveHandler streams one object, or one byte range of it (Range: 206 with Content-Range; past the end: 416), so a
// <video> can seek. A bad link, a key outside u/, the starter look thumbnails (packs/<family>/looks/) and the starter
// voice previews (packs/<family>/voices/), or a missing
// object is 403; a Stat that fails for any other reason is 500. HEAD answers the headers only.
func (c *controller) serveHandler(ctx *gin.Context) {
	key := strings.TrimPrefix(ctx.Param("path"), "/") // Gin keeps the leading slash of a *path value
	// u/<owner>/… and the packs' starter look thumbnails and voice previews (packs/<family>/{looks,voices}/…, PUT by the
	// runners, never by a link)
	servable := strings.HasPrefix(key, "u/") || lookThumb.MatchString(key) || voicePreview.MatchString(key)
	if strings.Contains(key, "..") || !servable || !c.signer.Verify(key, ctx.Query("exp"), ctx.Query("sig")) {
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
	// uploaded documents bring arbitrary files (.html, .svg): never run them as active content on this origin
	ctx.Header("Content-Security-Policy", "sandbox")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Accept-Ranges", "bytes")
	first, last, ranged := byteRange(ctx.GetHeader("Range"), info.Size)
	if ranged && first > last {
		ctx.Header("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
		network.SendError(ctx, http.StatusRequestedRangeNotSatisfiable, "the range starts past the end of the file")
		return
	}
	// a key never changes content (a new run or upload gets a new key) and studio's links stay the same for
	// up to a day: the browser keeps it as long as it likes (a year) without asking again; private, so no shared
	// cache holds it. The URL is the cache key, so a link re-signed the next day is a new entry.
	ctx.Header("Cache-Control", "private, max-age=31536000, immutable")
	contentType := info.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.Header("Content-Type", contentType)
	if info.ETag != "" {
		ctx.Header("ETag", info.ETag)
	}
	ctx.Header("Content-Length", strconv.FormatInt(last-first+1, 10))
	status := http.StatusOK
	if ranged {
		status = http.StatusPartialContent
		ctx.Header("Content-Range", "bytes "+strconv.FormatInt(first, 10)+"-"+strconv.FormatInt(last, 10)+"/"+
			strconv.FormatInt(info.Size, 10))
	}
	if ctx.Request.Method == http.MethodHead {
		ctx.Status(status)
		return
	}
	body, err := c.open(ctx.Request.Context(), key, first, last-first+1)
	if err != nil {
		network.SendError(ctx, http.StatusForbidden, "invalid or expired link")
		return
	}
	defer body.Close()
	ctx.Status(status)
	_, _ = io.Copy(ctx.Writer, body)
}

// rangeReader is a store that reads part of an object itself (S3 GetObject with Range).
type rangeReader interface {
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
}

// open reads length bytes of the object from offset: the store's own range read (helmgo's S3 and Memory have one),
// else its stream with the bytes before offset skipped.
func (c *controller) open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if store, ok := c.store.(rangeReader); ok {
		return store.GetRange(ctx, key, offset, length)
	}
	body, err := c.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if _, err := io.CopyN(io.Discard, body, offset); err != nil {
		body.Close()
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(body, length), body}, nil
}

// byteRange reads a Range header (RFC 9110, one range of bytes) against an object of size bytes: the first and last
// byte to send and whether it is a range answer (206). No header, another unit, several ranges or a malformed one is
// the whole object (200). A range starting past the end (or a suffix of 0 bytes, or any range of an empty object)
// returns first > last: 416.
func byteRange(header string, size int64) (first, last int64, ranged bool) {
	spec, found := strings.CutPrefix(header, "bytes=")
	from, to, dash := strings.Cut(strings.TrimSpace(spec), "-")
	if !found || !dash || strings.Contains(spec, ",") {
		return 0, size - 1, false
	}
	a, errA := strconv.ParseInt(from, 10, 64)
	b, errB := strconv.ParseInt(to, 10, 64)
	switch {
	case from == "" && errB == nil && b >= 0: // the last b bytes
		if b == 0 || size == 0 {
			return 1, 0, true
		}
		return max(size-b, 0), size - 1, true
	case errA != nil || a < 0 || (to != "" && (errB != nil || b < a)):
		return 0, size - 1, false
	case a >= size:
		return 1, 0, true
	case to == "" || b >= size:
		return a, size - 1, true
	}
	return a, b, true
}
