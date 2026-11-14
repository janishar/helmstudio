package studio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
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
	group.POST("/workflows", c.createWorkflowHandler)
	group.POST("/workflows/validate", c.validateWorkflowHandler)
	group.GET("/workflows/:id", c.getWorkflowHandler)
	group.PUT("/workflows/:id", c.replaceWorkflowHandler)
	group.PATCH("/workflows/:id", c.renameWorkflowHandler)
	group.DELETE("/workflows/:id", c.deleteWorkflowHandler)
	group.GET("/nodes", c.nodesHandler)
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

// listWorkflowsHandler: GET /workflows?source=.
func (c *controller) listWorkflowsHandler(ctx *gin.Context) {
	out, err := c.service.ListWorkflows(ctx.Request.Context(), common.MustCaller(ctx), ctx.Query("source"))
	reply(ctx, http.StatusOK, out, err)
}

// getWorkflowHandler: GET /workflows/{id}.
func (c *controller) getWorkflowHandler(ctx *gin.Context) {
	out, err := c.service.GetWorkflow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// createWorkflowHandler: POST /workflows — 201 with the workflow and its problems.
func (c *controller) createWorkflowHandler(ctx *gin.Context) {
	body, err := strictBody[dto.WorkflowCreate](ctx, false)
	var out dto.Workflow
	if err == nil {
		out, err = c.service.CreateWorkflow(ctx.Request.Context(), common.MustCaller(ctx), body)
	}
	replyWorkflow(ctx, http.StatusCreated, out, err)
}

// validateWorkflowHandler: POST /workflows/validate — other top-level keys are ignored (a WorkflowUpdate validates).
func (c *controller) validateWorkflowHandler(ctx *gin.Context) {
	body, err := strictBody[dto.WorkflowDoc](ctx, true)
	var out dto.WorkflowValidation
	if err == nil {
		out, err = c.service.ValidateWorkflow(*body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// replaceWorkflowHandler: PUT /workflows/{id}.
func (c *controller) replaceWorkflowHandler(ctx *gin.Context) {
	body, err := strictBody[dto.WorkflowUpdate](ctx, false)
	var out dto.Workflow
	if err == nil {
		out, err = c.service.ReplaceWorkflow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	replyWorkflow(ctx, http.StatusOK, out, err)
}

// renameWorkflowHandler: PATCH /workflows/{id}.
func (c *controller) renameWorkflowHandler(ctx *gin.Context) {
	body, err := strictBody[dto.WorkflowPatch](ctx, false)
	var out dto.Workflow
	if err == nil {
		out, err = c.service.RenameWorkflow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// deleteWorkflowHandler: DELETE /workflows/{id}.
func (c *controller) deleteWorkflowHandler(ctx *gin.Context) {
	if err := c.service.DeleteWorkflow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// nodesHandler: GET /nodes?env=.
func (c *controller) nodesHandler(ctx *gin.Context) {
	env := ctx.Query("env")
	if env != "" && !envPattern.MatchString(env) {
		network.SendError(ctx, http.StatusUnprocessableEntity, "env must be 1-32 lower-case letters and digits")
		return
	}
	out, err := c.service.Nodes(env)
	reply(ctx, http.StatusOK, out, err)
}

// envPattern is an env name.
var envPattern = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

// strictBody decodes a closed JSON body (an unknown field is 422; openTop ignores unknown top-level keys only) and
// validates it. The module's body limit caps it; a document is larger than helmgo's 64 KiB JSON cap allows.
func strictBody[B any](ctx *gin.Context, openTop bool) (*B, error) {
	invalid := func(err error) error { return network.NewUnprocessableError("invalid JSON body", err) }
	raw, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		return nil, invalid(err)
	}
	if openTop {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, invalid(err)
		}
		for key := range top {
			if !slices.Contains([]string{"title", "family", "env", "nodes", "studio"}, key) {
				delete(top, key)
			}
		}
		raw, _ = json.Marshal(top)
	}
	body := new(B)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		var detail string
		if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			detail = "unknown field " + strings.Trim(field, `"`)
		}
		return nil, network.NewUnprocessableError(cmp.Or(detail, "invalid JSON body"), err)
	}
	if validator, ok := any(body).(network.Validator); ok {
		if detail := validator.Validate(); detail != "" {
			return nil, network.NewUnprocessableError(detail, nil)
		}
	}
	return body, nil
}

// replyWorkflow answers like reply, with a WorkflowError's problems in its 422.
func replyWorkflow[T any](ctx *gin.Context, status int, out T, err error) {
	var problems *WorkflowError
	if errors.As(err, &problems) {
		network.SendJSON(ctx, http.StatusUnprocessableEntity, problems)
		return
	}
	reply(ctx, status, out, err)
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
