package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// Recheck checks a request's bearer token again: nil while it is still valid (not expired, signed out or disabled).
type Recheck func(ctx context.Context, token string) error

// controller serves /v1/studio. shutdown closes when the service stops, so an open event stream doesn't hold up the
// server's shutdown; recheck ends an open event stream once its token is no longer valid.
type controller struct {
	micro.Controller
	service  *Service
	recheck  Recheck
	shutdown <-chan struct{}
}

// NewController returns the controller. It takes no providers: the module's prefix guard authenticates all of /v1/.
func NewController(service *Service, recheck Recheck, shutdown <-chan struct{}) micro.Controller {
	return &controller{Controller: micro.NewController("/v1/studio", nil, nil), service: service, recheck: recheck,
		shutdown: shutdown}
}

// MountRoutes registers the routes of contracts/openapi.yaml's studio tag.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/sessions", c.listSessionsHandler)
	group.POST("/sessions", c.createSessionHandler)
	group.GET("/sessions/:id", c.getSessionHandler)
	group.PATCH("/sessions/:id", c.renameSessionHandler)
	group.DELETE("/sessions/:id", c.deleteSessionHandler)
	group.GET("/sessions/:id/runs", c.listRunsHandler)
	group.POST("/sessions/:id/runs", c.createRunHandler)
	group.GET("/sessions/:id/events", c.eventsHandler)
	group.GET("/workflows", c.listWorkflowsHandler)
	group.GET("/workflows/:id", c.getWorkflowHandler)
	group.GET("/runs/:id", c.getRunHandler)
	group.POST("/runs/:id/cancel", c.cancelRunHandler)
	group.GET("/hosts", c.listHostsHandler)
	group.GET("/queue", c.queueHandler)
	group.GET("/batches", c.listBatchesHandler)
	group.POST("/batches", c.createBatchHandler)
	group.GET("/batches/:id", c.getBatchHandler)
	group.GET("/batches/:id/rows", c.listBatchRowsHandler)
	group.POST("/batches/:id/pause", c.controlBatchHandler(c.service.PauseBatch))
	group.POST("/batches/:id/resume", c.controlBatchHandler(c.service.ResumeBatch))
	group.POST("/batches/:id/stop", c.controlBatchHandler(c.service.StopBatch))
	group.POST("/batches/:id/rows/:row/retry", c.retryBatchRowHandler)
	group.GET("/batches/:id/events", c.batchEventsHandler)
}

// MountNats registers studio.runs.create (the service's group is "studio"), pipelines_service's request for a batch
// run.
func (c *controller) MountNats(group micro.NatsGroup) error {
	return group.AddGroup("runs").AddEndpoint("create", micro.NatsHandlerFunc(c.runsCreateHandler))
}

// runsCreateHandler answers studio.runs.create with the run, or {error, code}.
func (c *controller) runsCreateHandler(request micro.NatsRequest) {
	body := new(message.RunsCreate)
	if err := json.Unmarshal(request.Data(), body); err != nil {
		micro.RespondNatsMessage(request, &message.RunsCreateError{Error: "bad request: invalid JSON", Code: "invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, refused := c.service.CreateBatchRun(ctx, body)
	if refused != nil {
		micro.RespondNatsMessage(request, refused)
		return
	}
	micro.RespondNatsMessage(request, &out)
}

// reply sends out with status, or the error.
func reply[T any](ctx *gin.Context, status int, out T, err error) {
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, status, out)
}

// withBody decodes and validates the JSON body, then answers fn(caller, :id, body) with status.
func withBody[B, T any](ctx *gin.Context, status int, fn func(caller common.Caller, id string, body *B) (T, error)) {
	body, err := network.ReqBody[B](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := fn(common.MustCaller(ctx), ctx.Param("id"), body)
	reply(ctx, status, out, err)
}

// ------------------------------------------------------------------ sessions

// listSessionsHandler: GET /sessions.
func (c *controller) listSessionsHandler(ctx *gin.Context) {
	out, err := c.service.ListSessions(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// createSessionHandler: POST /sessions — the body is optional.
func (c *controller) createSessionHandler(ctx *gin.Context) {
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
	out, err := c.service.CreateSession(ctx.Request.Context(), common.MustCaller(ctx), body)
	reply(ctx, http.StatusCreated, out, err)
}

// getSessionHandler: GET /sessions/{id}.
func (c *controller) getSessionHandler(ctx *gin.Context) {
	out, err := c.service.GetSession(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// renameSessionHandler: PATCH /sessions/{id}.
func (c *controller) renameSessionHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, id string, body *dto.SessionPatch) (dto.Session, error) {
		return c.service.RenameSession(ctx.Request.Context(), caller, id, body)
	})
}

// deleteSessionHandler: DELETE /sessions/{id}.
func (c *controller) deleteSessionHandler(ctx *gin.Context) {
	if err := c.service.DeleteSession(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// ------------------------------------------------------------------ workflows and hosts

// listWorkflowsHandler: GET /workflows.
func (c *controller) listWorkflowsHandler(ctx *gin.Context) {
	network.SendJSON(ctx, http.StatusOK, c.service.Workflows())
}

// getWorkflowHandler: GET /workflows/{id}.
func (c *controller) getWorkflowHandler(ctx *gin.Context) {
	out, err := c.service.Workflow(ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// queueHandler: GET /queue — the caller's open runs and, per host, a count of others' queued runs.
func (c *controller) queueHandler(ctx *gin.Context) {
	out, err := c.service.Queue(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// listHostsHandler: GET /hosts.
func (c *controller) listHostsHandler(ctx *gin.Context) {
	network.SendJSON(ctx, http.StatusOK, c.service.Hosts())
}

// ------------------------------------------------------------------ runs

// listRunsHandler: GET /sessions/{id}/runs.
func (c *controller) listRunsHandler(ctx *gin.Context) {
	out, err := c.service.ListRuns(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// createRunHandler: POST /sessions/{id}/runs — 202 with the run, queued or already failed.
func (c *controller) createRunHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusAccepted, func(caller common.Caller, id string, body *dto.RunRequest) (dto.Run, error) {
		return c.service.CreateRun(ctx.Request.Context(), caller, id, body)
	})
}

// getRunHandler: GET /runs/{id}.
func (c *controller) getRunHandler(ctx *gin.Context) {
	out, err := c.service.GetRun(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// cancelRunHandler: POST /runs/{id}/cancel — 202 with the run as it is now.
func (c *controller) cancelRunHandler(ctx *gin.Context) {
	out, err := c.service.CancelRun(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusAccepted, out, err)
}

// ------------------------------------------------------------------ events

// eventsHandler: GET /sessions/{id}/events — JSON page after ?since, or SSE with Accept: text/event-stream (replay
// after since / Last-Event-ID, then new events as they are stored).
func (c *controller) eventsHandler(ctx *gin.Context) {
	c.serveEvents(ctx, func(requestCtx context.Context, since int64) (any, []sseEvent, int64, error) {
		page, err := c.service.Events(requestCtx, common.MustCaller(ctx), ctx.Param("id"), since)
		events := make([]sseEvent, len(page.Events))
		for i, event := range page.Events {
			events[i] = sseEvent{event.Seq, event.Type, event}
		}
		return page, events, page.LastSeq, err
	})
}

// sseEvent is one event as a stream sends it: id, event and data (the whole event as JSON).
type sseEvent struct {
	Seq  int64
	Type string
	Data any
}

// serveEvents answers an event log: the JSON page after ?since, or SSE with Accept: text/event-stream (replay after
// since / Last-Event-ID, then new events as they are stored). The stream checks its token again before every poll and
// ends once it is expired or revoked; the client reconnects with a fresh token and Last-Event-ID. read returns the
// page, its events and the next cursor.
func (c *controller) serveEvents(ctx *gin.Context, read func(ctx context.Context, since int64) (any, []sseEvent, int64, error)) {
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
	requestCtx, token := ctx.Request.Context(), network.BearerToken(ctx)
	page, events, last, err := read(requestCtx, since) // 404 before any stream starts
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
	send := func(events []sseEvent) bool {
		for _, event := range events {
			data, _ := json.Marshal(event.Data)
			if _, err := fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data); err != nil {
				return false
			}
		}
		return flusher.Flush() == nil
	}
	if !send(events) {
		return
	}
	since = last
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
			if c.recheck(requestCtx, token) != nil {
				return
			}
			_, events, last, err := read(requestCtx, since)
			if err != nil { // gone, or the database is down: the client reconnects with Last-Event-ID
				return
			}
			if len(events) > 0 && !send(events) {
				return
			}
			since = last
		}
	}
}

// ------------------------------------------------------------------ batches

// listBatchesHandler: GET /batches.
func (c *controller) listBatchesHandler(ctx *gin.Context) {
	page, detail := ParsePage(ctx.Query("cursor"), ctx.Query("limit"))
	if detail != "" {
		network.SendError(ctx, http.StatusUnprocessableEntity, detail)
		return
	}
	out, err := c.service.ListBatches(ctx.Request.Context(), common.MustCaller(ctx), page)
	reply(ctx, http.StatusOK, out, err)
}

// createBatchHandler: POST /batches — 202 with the batch, queued.
func (c *controller) createBatchHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusAccepted, func(caller common.Caller, _ string, body *dto.BatchCreate) (dto.Batch, error) {
		return c.service.CreateBatch(ctx.Request.Context(), caller, body)
	})
}

// getBatchHandler: GET /batches/{id}.
func (c *controller) getBatchHandler(ctx *gin.Context) {
	out, err := c.service.GetBatch(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// listBatchRowsHandler: GET /batches/{id}/rows — 404 before any 422.
func (c *controller) listBatchRowsHandler(ctx *gin.Context) {
	page, detail := ParsePage(ctx.Query("cursor"), ctx.Query("limit"))
	caller := common.MustCaller(ctx)
	if detail != "" {
		if _, err := c.service.GetBatch(ctx.Request.Context(), caller, ctx.Param("id")); err != nil {
			network.SendMixedError(ctx, err)
			return
		}
		network.SendError(ctx, http.StatusUnprocessableEntity, detail)
		return
	}
	out, err := c.service.ListBatchRows(ctx.Request.Context(), caller, ctx.Param("id"), page)
	reply(ctx, http.StatusOK, out, err)
}

// controlBatchHandler: POST /batches/{id}/pause, /resume, /stop — 202 with the batch as it is now.
func (c *controller) controlBatchHandler(fn func(context.Context, common.Caller, string) (dto.Batch, error)) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		out, err := fn(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusAccepted, out, err)
	}
}

// retryBatchRowHandler: POST /batches/{id}/rows/{row}/retry — 202 with the row, queued again.
func (c *controller) retryBatchRowHandler(ctx *gin.Context) {
	out, err := c.service.RetryBatchRow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"))
	reply(ctx, http.StatusAccepted, out, err)
}

// batchEventsHandler: GET /batches/{id}/events — like the session events, per-batch seq.
func (c *controller) batchEventsHandler(ctx *gin.Context) {
	c.serveEvents(ctx, func(requestCtx context.Context, since int64) (any, []sseEvent, int64, error) {
		page, err := c.service.BatchEvents(requestCtx, common.MustCaller(ctx), ctx.Param("id"), since)
		events := make([]sseEvent, len(page.Events))
		for i, event := range page.Events {
			events[i] = sseEvent{event.Seq, event.Type, event}
		}
		return page, events, page.LastSeq, err
	})
}
