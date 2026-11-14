package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/orago/micro"
	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/catalog/dto"
	"github.com/helmstudio/studio_service/common"
)

// controller serves /v1/catalog. shutdown closes when the service stops, so an open event stream doesn't hold up
// the server's shutdown.
type controller struct {
	micro.Controller
	service  *Service
	shutdown <-chan struct{}
}

// NewController returns the controller. It takes no providers: the module's prefix guard authenticates all of /v1/.
func NewController(service *Service, shutdown <-chan struct{}) micro.Controller {
	return &controller{Controller: micro.NewController("/v1/catalog", nil, nil), service: service, shutdown: shutdown}
}

// MountRoutes registers the routes of contracts/openapi.yaml's catalog tag.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.POST("/sessions", c.createHandler)
	group.GET("/sessions", c.listHandler)
	group.GET("/sessions/:id", c.getHandler)
	group.PATCH("/sessions/:id", c.patchHandler)
	group.POST("/sessions/:id/refresh", c.refreshHandler)
	group.POST("/sessions/:id/reset", c.resetHandler)
	group.GET("/memory", c.memoryHandler)
	group.GET("/stats", c.statsHandler)
	group.GET("/metrics", c.metricsHandler)
	group.GET("/jobs", c.jobsHandler)
	group.GET("/jobs/:id", c.jobHandler)
	group.GET("/library", c.libraryHandler)
	group.PUT("/favorites/:kind/:item_id", c.favoriteHandler(true))
	group.DELETE("/favorites/:kind/:item_id", c.favoriteHandler(false))
	group.GET("/estimate", c.estimateHandler)
	group.POST("/sessions/:id/raw", c.rawHandler)
	group.POST("/sessions/:id/guidelines", c.guidelineHandler)
	group.POST("/sessions/:id/uploads", c.uploadHandler)
	group.POST("/sessions/:id/poses/suggest", c.suggestPosesHandler)
	group.GET("/sessions/:id/events", c.eventsHandler)
	group.POST("/questions/:id/answer", c.answerHandler)
	group.POST("/sessions/:id/start", c.startHandler)
	group.GET("/sessions/:id/next-step", c.nextStepHandler)
	group.POST("/sessions/:id/runs", c.runHandler)
	group.POST("/sessions/:id/runs/:run_id/stop", c.stopRunHandler)
	group.POST("/sessions/:id/images/stop", c.stopImagesHandler)
	group.POST("/sessions/:id/regenerate", c.regenerateHandler)
	group.PATCH("/images/:id", c.reviewHandler)
	group.POST("/images/:id/pick", c.pickHandler)
	group.POST("/sessions/:id/approve", c.approveHandler)
	group.PATCH("/skus/:id", c.patchSkuHandler)
	group.POST("/sessions/:id/requests", c.typedRequestHandler)
	group.POST("/sessions/:id/download", c.downloadHandler)
}

// reply sends out with status, or the error.
func reply[T any](ctx *gin.Context, status int, out T, err error) {
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, status, out)
}

// withBody decodes and validates the JSON body, then answers fn's result with status.
func withBody[B, T any](ctx *gin.Context, status int, fn func(caller common.Caller, id string, body *B) (T, error)) {
	body, err := network.ReqBody[B](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := fn(common.MustCaller(ctx), ctx.Param("id"), body)
	reply(ctx, status, out, err)
}

// byID answers fn(caller, :id) with status (the routes without a body).
func byID[T any](ctx *gin.Context, status int, fn func(ctx *gin.Context, caller common.Caller, id string) (T, error)) {
	out, err := fn(ctx, common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, status, out, err)
}

// ------------------------------------------------------------------ sessions

// createHandler: POST /sessions — the body (project_name) is optional.
func (c *controller) createHandler(ctx *gin.Context) {
	body, err := network.ReqBodyOptional[dto.SessionCreate](ctx)
	if err == nil {
		if detail := body.Validate(); detail != "" {
			err = network.NewUnprocessableError(detail, nil)
		}
	}
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.Create(ctx.Request.Context(), common.MustCaller(ctx), body)
	reply(ctx, http.StatusCreated, out, err)
}

// listHandler: GET /sessions — newest first, each with its counts for the dashboard.
func (c *controller) listHandler(ctx *gin.Context) {
	out, err := c.service.List(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// getHandler: GET /sessions/{id}.
func (c *controller) getHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.SessionDetail, error) {
		return c.service.Get(ctx.Request.Context(), caller, id)
	})
}

// patchHandler: PATCH /sessions/{id}.
func (c *controller) patchHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.SessionPatch) (dto.Session, error) {
		return c.service.Patch(ctx.Request.Context(), caller, id, body)
	})
}

// refreshHandler: POST /sessions/{id}/refresh.
func (c *controller) refreshHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.SessionDetail, error) {
		return c.service.Refresh(ctx.Request.Context(), caller, id)
	})
}

// resetHandler: POST /sessions/{id}/reset.
func (c *controller) resetHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.SessionDetail, error) {
		return c.service.Reset(ctx.Request.Context(), caller, id)
	})
}

// ------------------------------------------------------------------ memory, stats, library, favorites, estimate

// memoryHandler: GET /memory.
func (c *controller) memoryHandler(ctx *gin.Context) {
	out, err := c.service.Memory(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// statsHandler: GET /stats — the account page's totals.
func (c *controller) statsHandler(ctx *gin.Context) {
	out, err := c.service.Stats(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// metricsHandler: GET /metrics?session_id= — the account page's quality by product.
func (c *controller) metricsHandler(ctx *gin.Context) {
	var sessionID *string
	if id := ctx.Query("session_id"); id != "" {
		if !utility.IsUUID(id) {
			network.SendError(ctx, http.StatusUnprocessableEntity, "session_id must be a uuid")
			return
		}
		sessionID = &id
	}
	out, err := c.service.Metrics(ctx.Request.Context(), common.MustCaller(ctx), sessionID)
	reply(ctx, http.StatusOK, out, err)
}

// jobsHandler: GET /jobs — the dev Jobs view's list (the caller's own jobs).
func (c *controller) jobsHandler(ctx *gin.Context) {
	out, err := c.service.Jobs(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// jobHandler: GET /jobs/{id} — one job and its events.
func (c *controller) jobHandler(ctx *gin.Context) {
	out, err := c.service.Job(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// libraryHandler: GET /library.
func (c *controller) libraryHandler(ctx *gin.Context) {
	out, err := c.service.Library(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// favoriteHandler: PUT (on) or DELETE (off) /favorites/{kind}/{item_id}.
func (c *controller) favoriteHandler(on bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		out, err := c.service.SetFavorite(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("kind"), ctx.Param("item_id"), on)
		reply(ctx, http.StatusOK, out, err)
	}
}

// estimateHandler: GET /estimate?images=&resolution=.
func (c *controller) estimateHandler(ctx *gin.Context) {
	images, err := strconv.Atoi(ctx.Query("images"))
	if err != nil || images < 0 || images > 10000 {
		network.SendError(ctx, http.StatusUnprocessableEntity, "images must be 0 to 10000")
		return
	}
	resolution := ctx.Query("resolution")
	if resolution == "" {
		resolution = "2K"
	}
	out, err := c.service.Estimate(images, resolution)
	reply(ctx, http.StatusOK, out, err)
}

// ------------------------------------------------------------------ inputs

// multipartForm parses a multipart body (files over 32 MiB spill to temp files) or answers why it can't: nil, false
// when the body isn't multipart.
func multipartForm(ctx *gin.Context) (*multipart.Form, bool, error) {
	if !strings.HasPrefix(ctx.ContentType(), "multipart/form-data") {
		return nil, false, nil
	}
	if err := ctx.Request.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, true, network.NewRequestTooLargeError("the upload is too large", nil)
		}
		return nil, true, network.NewBadRequestError("unparseable multipart body", nil)
	}
	return ctx.Request.MultipartForm, true, nil
}

// field is the first value of a form field, or "".
func field(form *multipart.Form, name string) string {
	if values := form.Value[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// oneFile is the single file of the form field name, or the 422.
func oneFile(form *multipart.Form, name string) (*multipart.FileHeader, error) {
	if files := form.File[name]; len(files) == 1 {
		return files[0], nil
	}
	return nil, network.NewUnprocessableError("send exactly one file in the multipart field `"+name+"`", nil)
}

// rawHandler: POST /sessions/{id}/raw — multipart files (or one CSV), or JSON {kind, url | csv_text}.
func (c *controller) rawHandler(ctx *gin.Context) {
	form, isMultipart, err := multipartForm(ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if !isMultipart {
		withBody(ctx, http.StatusAccepted, func(caller common.Caller, id string, body *dto.RawLink) (dto.RawAccepted, error) {
			return c.service.AddRawLink(ctx.Request.Context(), caller, id, body)
		})
		return
	}
	out, err := c.service.AddRawFiles(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"),
		RawForm{Kind: field(form, "kind"), Label: field(form, "label"), Files: form.File["files"],
			Families: form.Value["families"], Paths: form.Value["paths"]})
	reply(ctx, http.StatusAccepted, out, err)
}

// guidelineHandler: POST /sessions/{id}/guidelines — multipart file (+ target), or JSON {url, target}.
func (c *controller) guidelineHandler(ctx *gin.Context) {
	form, isMultipart, err := multipartForm(ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	if !isMultipart {
		withBody(ctx, http.StatusCreated, func(caller common.Caller, id string, body *dto.GuidelineLink) (dto.InputAdded, error) {
			return c.service.AddGuidelineLink(ctx.Request.Context(), caller, id, body)
		})
		return
	}
	header, err := oneFile(form, "file")
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.AddGuidelineFile(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), field(form, "target"), header)
	reply(ctx, http.StatusCreated, out, err)
}

// uploadHandler: POST /sessions/{id}/uploads — one model, background or pose reference photo.
func (c *controller) uploadHandler(ctx *gin.Context) {
	form, isMultipart, err := multipartForm(ctx)
	if err == nil && !isMultipart {
		err = network.NewUnprocessableError("send multipart/form-data with `file` and `kind`", nil)
	}
	var header *multipart.FileHeader
	if err == nil {
		header, err = oneFile(form, "file")
	}
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.Upload(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), field(form, "kind"),
		field(form, "name"), field(form, "category"), header)
	reply(ctx, http.StatusCreated, out, err)
}

// suggestPosesHandler: POST /sessions/{id}/poses/suggest.
func (c *controller) suggestPosesHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.PoseSuggestRequest) (dto.PoseSuggestion, error) {
		return c.service.SuggestPoses(ctx.Request.Context(), caller, id, body)
	})
}

// ------------------------------------------------------------------ processing

// eventsHandler: GET /sessions/{id}/events — JSON page after ?since, or SSE with Accept: text/event-stream (replay
// after since / Last-Event-ID, then new events as the projector stores them).
func (c *controller) eventsHandler(ctx *gin.Context) {
	cursor := ctx.Query("since")
	if last := ctx.GetHeader("Last-Event-ID"); last != "" {
		cursor = last
	}
	since := int64(0)
	if cursor != "" {
		var err error
		if since, err = strconv.ParseInt(cursor, 10, 64); err != nil || since < 0 {
			network.SendError(ctx, http.StatusUnprocessableEntity, "since must be a whole number >= 0")
			return
		}
	}
	requestCtx, caller, id := ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")
	page, err := c.service.Events(requestCtx, caller, id, since) // 404 before any stream starts
	if err != nil || !strings.Contains(ctx.GetHeader("Accept"), "text/event-stream") {
		reply(ctx, http.StatusOK, page, err)
		return
	}
	writer := ctx.Writer
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher := http.NewResponseController(writer)
	send := func(page dto.EventPage) bool {
		for _, event := range page.Events {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data); err != nil {
				return false
			}
		}
		return flusher.Flush() == nil
	}
	if !send(page) {
		return
	}
	since = page.LastSeq
	poll, ping := time.NewTicker(PollEvery), time.NewTicker(KeepAlive)
	defer poll.Stop()
	defer ping.Stop()
	for {
		select {
		case <-requestCtx.Done():
			return
		case <-c.shutdown:
			return
		case <-ping.C:
			if _, err := fmt.Fprint(writer, ": keep-alive\n\n"); err != nil || flusher.Flush() != nil {
				return
			}
		case <-poll.C:
			page, err := c.service.Events(requestCtx, caller, id, since)
			if err != nil { // the session is gone or the database is down: the client reconnects with Last-Event-ID
				return
			}
			if len(page.Events) > 0 && !send(page) {
				return
			}
			since = page.LastSeq
		}
	}
}

// answerHandler: POST /questions/{id}/answer.
func (c *controller) answerHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.AnswerRequest) (dto.Answered, error) {
		return c.service.Answer(ctx.Request.Context(), caller, id, *body.Option)
	})
}

// startHandler: POST /sessions/{id}/start.
func (c *controller) startHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.Started, error) {
		return c.service.Start(ctx.Request.Context(), caller, id)
	})
}

// ------------------------------------------------------------------ generation

// nextStepHandler: GET /sessions/{id}/next-step.
func (c *controller) nextStepHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.NextStep, error) {
		return c.service.GetNextStep(ctx.Request.Context(), caller, id)
	})
}

// runHandler: POST /sessions/{id}/runs.
func (c *controller) runHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusAccepted, func(caller common.Caller, id string, body *dto.RunRequest) (dto.RunAccepted, error) {
		return c.service.CreateRun(ctx.Request.Context(), caller, id, body)
	})
}

// stopRunHandler: POST /sessions/{id}/runs/{run_id}/stop.
func (c *controller) stopRunHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.RunStopped, error) {
		return c.service.StopRun(ctx.Request.Context(), caller, id, ctx.Param("run_id"))
	})
}

// stopImagesHandler: POST /sessions/{id}/images/stop.
func (c *controller) stopImagesHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.ImagesStop) (dto.ImagesStopped, error) {
		return c.service.StopImages(ctx.Request.Context(), caller, id, body)
	})
}

// regenerateHandler: POST /sessions/{id}/regenerate.
func (c *controller) regenerateHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusAccepted, func(caller common.Caller, id string, body *dto.RegenerateRequest) (dto.RunAccepted, error) {
		return c.service.Regenerate(ctx.Request.Context(), caller, id, body)
	})
}

// reviewHandler: PATCH /images/{id}.
func (c *controller) reviewHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.ImageReview) (dto.Reviewed, error) {
		return c.service.ReviewImage(ctx.Request.Context(), caller, id, body.Status)
	})
}

// pickHandler: POST /images/{id}/pick.
func (c *controller) pickHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.ImagePick) (dto.Reviewed, error) {
		return c.service.PickTry(ctx.Request.Context(), caller, id, body.Attempt)
	})
}

// approveHandler: POST /sessions/{id}/approve.
func (c *controller) approveHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.ApproveRequest) (dto.Approved, error) {
		return c.service.Approve(ctx.Request.Context(), caller, id, body)
	})
}

// patchSkuHandler: PATCH /skus/{id}.
func (c *controller) patchSkuHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.SkuPatch) (dto.SkuList, error) {
		return c.service.PatchSku(ctx.Request.Context(), caller, id, body)
	})
}

// typedRequestHandler: POST /sessions/{id}/requests.
func (c *controller) typedRequestHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.TypedRequest) (dto.TypedResponse, error) {
		return c.service.TypedRequest(ctx.Request.Context(), caller, id, body)
	})
}

// downloadHandler: POST /sessions/{id}/download.
func (c *controller) downloadHandler(ctx *gin.Context) {
	byID(ctx, http.StatusOK, func(ctx *gin.Context, caller common.Caller, id string) (dto.Download, error) {
		return c.service.Download(ctx.Request.Context(), caller, id)
	})
}
