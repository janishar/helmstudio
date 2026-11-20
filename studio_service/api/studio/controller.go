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
	"unicode/utf8"

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
	group.POST("/runs/plan", c.planRunHandler)
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
	group.GET("/batches/:id/review", c.batchReviewHandler)
	group.POST("/batches/:id/rows/:row/pick", c.pickBatchRowHandler)
	group.GET("/batches/:id/rows/:row/execution", c.rowExecutionHandler)
	group.GET("/batches/:id/rows/:row/execution/:step/state", c.rowStepStateHandler)
	group.GET("/batches/:id/rows/:row/decisions", c.rowDecisionsHandler)
	group.POST("/batches/:id/again", c.againBatchHandler)
	group.GET("/takes/:id/decisions", c.takeDecisionsHandler)
	group.GET("/memory", c.listMemoryHandler)
	group.POST("/memory", c.createMemoryHandler)
	group.DELETE("/memory/:id", c.deleteMemoryHandler)
	group.GET("/pipelines", c.listPipelinesHandler)
	group.POST("/pipelines", c.createPipelineHandler)
	group.POST("/pipelines/validate", c.validatePipelineHandler)
	group.GET("/pipelines/:id", c.getPipelineHandler)
	group.PUT("/pipelines/:id", c.replacePipelineHandler)
	group.PATCH("/pipelines/:id", c.renamePipelineHandler)
	group.DELETE("/pipelines/:id", c.deletePipelineHandler)
	group.POST("/pipelines/:id/judge-tests", c.createJudgeTestHandler)
	group.GET("/judge-tests/:id", c.getJudgeTestHandler)
	group.POST("/takes/:id/pipeline", c.saveTakeAsPipelineHandler)
	group.GET("/batches/:id/chat", c.getChatHandler)
	group.POST("/batches/:id/chat", c.postChatHandler)
	group.GET("/model-sources", c.modelSourcesHandler)
	group.GET("/custom-nodes", c.customNodesHandler)
	group.GET("/admin/providers", c.listProvidersHandler)
	group.PUT("/admin/providers/:name", c.putProviderHandler)
	group.DELETE("/admin/providers/:name", c.deleteProviderHandler)
	group.POST("/admin/providers/:name/test", c.testProviderHandler)
	group.GET("/models", c.listModelsHandler)
	group.POST("/admin/models/:model/download", c.downloadModelHandler)
	group.DELETE("/admin/models/:model", c.deleteModelHandler)
	group.DELETE("/admin/loras/:lora_id", c.deleteSharedLoraHandler)
	group.GET("/admin/model-jobs", c.listModelJobsHandler)
	group.GET("/admin/model-jobs/:job_id", c.getModelJobHandler)
	group.POST("/admin/model-jobs/:job_id/cancel", c.cancelModelJobHandler)
	group.POST("/admin/hosts/:host/unload", c.adminUnloadHandler)
	group.PUT("/admin/hosts/:host/settings", c.putHostSettingsHandler)
	group.PUT("/admin/hosts/:host/pins", c.putHostPinsHandler)
	group.GET("/library", c.listLibraryHandler)
	group.POST("/library", c.createLibraryItemHandler)
	group.GET("/library/:id", c.getLibraryItemHandler)
	group.PUT("/library/:id", c.replaceLibraryItemHandler)
	group.DELETE("/library/:id", c.deleteLibraryItemHandler)
	group.GET("/library/:id/:look_id", c.starterLookHandler) // :id is the starter look's family
	group.PUT("/library/:id/:look_id", c.starterLookHandler)
	group.DELETE("/library/:id/:look_id", c.starterLookHandler)
	c.mountTimelines(group)
}

// MountNats registers studio.runs.create (the service's group is "studio"), pipelines_service's request for a batch
// run, and studio.tools (API 1.15), an agent's tool call that needs studio's data.
func (c *controller) MountNats(group micro.NatsGroup) error {
	if err := group.AddGroup("runs").AddEndpoint("create", micro.NatsHandlerFunc(c.runsCreateHandler)); err != nil {
		return err
	}
	return group.AddEndpoint("tools", micro.NatsHandlerFunc(c.toolsHandler))
}

// toolsHandler answers studio.tools with {result, row?}, or {error, code}.
func (c *controller) toolsHandler(request micro.NatsRequest) {
	body := new(message.ToolsRequest)
	if err := json.Unmarshal(request.Data(), body); err != nil {
		micro.RespondNatsMessage(request, &message.ToolsReply{Error: "bad request: invalid JSON", Code: "invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := c.service.Tool(ctx, body)
	micro.RespondNatsMessage(request, &out)
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

// listWorkflowsHandler: GET /workflows?source=&media=.
func (c *controller) listWorkflowsHandler(ctx *gin.Context) {
	out, err := c.service.ListWorkflows(ctx.Request.Context(), common.MustCaller(ctx), ctx.Query("source"), ctx.Query("media"))
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
		out, err = c.service.ValidateWorkflow(ctx.Request.Context(), common.MustCaller(ctx), *body)
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
	if openTop {
		return strictBodyKeeping[B](ctx, "title", "family", "env", "nodes", "studio")
	}
	return strictBodyKeeping[B](ctx)
}

// strictBodyKeeping is strictBody; with keep given, top-level keys not in keep are ignored.
func strictBodyKeeping[B any](ctx *gin.Context, keep ...string) (*B, error) {
	openTop := len(keep) > 0
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
			if !slices.Contains(keep, key) {
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
	out, err := c.service.Hosts(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
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

// planRunHandler: POST /runs/plan — the run's planned peak, nothing stored (API 1.25).
func (c *controller) planRunHandler(ctx *gin.Context) {
	withBody(ctx, http.StatusOK, func(caller common.Caller, _ string, body *dto.RunRequest) (dto.RunPlan, error) {
		return c.service.PlanRun(ctx.Request.Context(), caller, body)
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

// retryBatchRowHandler: POST /batches/{id}/rows/{row}/retry — 202 with the row, queued again; the optional body
// {from_step} retries a pipeline row from a step.
func (c *controller) retryBatchRowHandler(ctx *gin.Context) {
	body, err := optionalBody[dto.BatchRowRetry](ctx)
	var out dto.BatchRow
	if err == nil {
		out, err = c.service.RetryBatchRow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"), body)
	}
	reply(ctx, http.StatusAccepted, out, err)
}

// optionalBody decodes a closed JSON body that may be absent (nil) and validates it.
func optionalBody[B any](ctx *gin.Context) (*B, error) {
	if ctx.Request.ContentLength == 0 {
		return nil, nil
	}
	return strictBody[B](ctx, false)
}

// batchReviewHandler: GET /batches/{id}/review.
func (c *controller) batchReviewHandler(ctx *gin.Context) {
	out, err := c.service.BatchReview(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// pickBatchRowHandler: POST /batches/{id}/rows/{row}/pick — 202 with the row, moving on.
func (c *controller) pickBatchRowHandler(ctx *gin.Context) {
	body, err := strictBody[dto.BatchPick](ctx, false)
	var out dto.BatchRow
	if err == nil {
		out, err = c.service.PickBatchRow(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"), body)
	}
	reply(ctx, http.StatusAccepted, out, err)
}

// rowExecutionHandler: GET /batches/{id}/rows/{row}/execution[?attempt=].
func (c *controller) rowExecutionHandler(ctx *gin.Context) {
	out, err := c.service.RowExecution(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"),
		ctx.Query("attempt"))
	reply(ctx, http.StatusOK, out, err)
}

// rowStepStateHandler: GET /batches/{id}/rows/{row}/execution/{step}/state[?attempt=].
func (c *controller) rowStepStateHandler(ctx *gin.Context) {
	out, err := c.service.RowStepState(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"),
		ctx.Param("step"), ctx.Query("attempt"))
	reply(ctx, http.StatusOK, out, err)
}

// rowDecisionsHandler: GET /batches/{id}/rows/{row}/decisions[?attempt=].
func (c *controller) rowDecisionsHandler(ctx *gin.Context) {
	out, err := c.service.RowDecisions(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("row"),
		ctx.Query("attempt"))
	reply(ctx, http.StatusOK, out, err)
}

// takeDecisionsHandler: GET /takes/{id}/decisions.
func (c *controller) takeDecisionsHandler(ctx *gin.Context) {
	out, err := c.service.TakeDecisions(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// againBatchHandler: POST /batches/{id}/again — 201 with the new batch.
func (c *controller) againBatchHandler(ctx *gin.Context) {
	body, err := strictBody[dto.BatchAgain](ctx, false)
	var out dto.Batch
	if err == nil {
		out, err = c.service.AgainBatch(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusCreated, out, err)
}

// listMemoryHandler: GET /memory?pipeline_id=&kind= — an unknown pipeline is a 404.
func (c *controller) listMemoryHandler(ctx *gin.Context) {
	out, err := c.service.ListMemory(ctx.Request.Context(), common.MustCaller(ctx), ctx.Query("pipeline_id"), ctx.Query("kind"))
	reply(ctx, http.StatusOK, out, err)
}

// createMemoryHandler: POST /memory — 201 with the note.
func (c *controller) createMemoryHandler(ctx *gin.Context) {
	body, err := strictBody[dto.MemoryCreate](ctx, false)
	var out dto.MemoryItem
	if err == nil {
		out, err = c.service.CreateMemoryNote(ctx.Request.Context(), common.MustCaller(ctx), body)
	}
	reply(ctx, http.StatusCreated, out, err)
}

// deleteMemoryHandler: DELETE /memory/{item_id} — 204.
func (c *controller) deleteMemoryHandler(ctx *gin.Context) {
	if err := c.service.DeleteMemoryItem(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// createJudgeTestHandler: POST /pipelines/{id}/judge-tests — 202 with the test (API 1.28).
func (c *controller) createJudgeTestHandler(ctx *gin.Context) {
	body, err := strictBody[dto.JudgeTestCreate](ctx, false)
	var out dto.JudgeTest
	if err == nil {
		out, err = c.service.CreateJudgeTest(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusAccepted, out, err)
}

// getJudgeTestHandler: GET /judge-tests/{id}.
func (c *controller) getJudgeTestHandler(ctx *gin.Context) {
	out, err := c.service.GetJudgeTest(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// ------------------------------------------------------------------ pipelines

// listPipelinesHandler: GET /pipelines.
func (c *controller) listPipelinesHandler(ctx *gin.Context) {
	out, err := c.service.ListPipelines(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// getPipelineHandler: GET /pipelines/{id}.
func (c *controller) getPipelineHandler(ctx *gin.Context) {
	out, err := c.service.GetPipeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// createPipelineHandler: POST /pipelines — 201 with the pipeline and its problems.
func (c *controller) createPipelineHandler(ctx *gin.Context) {
	body, err := strictBody[dto.PipelineCreate](ctx, false)
	var out dto.Pipeline
	if err == nil {
		out, err = c.service.CreatePipeline(ctx.Request.Context(), common.MustCaller(ctx), body)
	}
	replyWorkflow(ctx, http.StatusCreated, out, err)
}

// validatePipelineHandler: POST /pipelines/validate — other top-level keys are ignored.
func (c *controller) validatePipelineHandler(ctx *gin.Context) {
	body, err := strictBodyKeeping[dto.PipelineDoc](ctx, "title", "nodes", "edges", "remote")
	var out dto.PipelineValidation
	if err == nil {
		out, err = c.service.ValidatePipeline(ctx.Request.Context(), common.MustCaller(ctx), *body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// replacePipelineHandler: PUT /pipelines/{id}.
func (c *controller) replacePipelineHandler(ctx *gin.Context) {
	body, err := strictBody[dto.PipelineUpdate](ctx, false)
	var out dto.Pipeline
	if err == nil {
		out, err = c.service.ReplacePipeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	replyWorkflow(ctx, http.StatusOK, out, err)
}

// renamePipelineHandler: PATCH /pipelines/{id}.
func (c *controller) renamePipelineHandler(ctx *gin.Context) {
	body, err := strictBody[dto.PipelinePatch](ctx, false)
	var out dto.Pipeline
	if err == nil {
		out, err = c.service.RenamePipeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// deletePipelineHandler: DELETE /pipelines/{id}.
func (c *controller) deletePipelineHandler(ctx *gin.Context) {
	if err := c.service.DeletePipeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// saveTakeAsPipelineHandler: POST /takes/{id}/pipeline — 201 with the new pipeline; the body is optional.
func (c *controller) saveTakeAsPipelineHandler(ctx *gin.Context) {
	body, err := optionalBody[dto.TakePipeline](ctx)
	var out dto.Pipeline
	if err == nil {
		if body == nil {
			body = &dto.TakePipeline{}
		}
		out, err = c.service.SaveTakeAsPipeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	replyWorkflow(ctx, http.StatusCreated, out, err)
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

// ------------------------------------------------------------------ the library

// listLibraryHandler: GET /library?kind=&group=&q=&source=&family=&workflow=&limit=.
func (c *controller) listLibraryHandler(ctx *gin.Context) {
	query, detail := ParseLibraryQuery(ctx.Query("kind"), ctx.Query("group"), ctx.Query("q"), ctx.Query("limit"))
	query.Source, query.Family, query.Workflow = ctx.Query("source"), ctx.Query("family"), ctx.Query("workflow")
	switch {
	case detail != "":
	case query.Source != "" && query.Source != "mine" && query.Source != "starter":
		detail = "source must be mine or starter"
	case query.Family != "" && !familySlug.MatchString(query.Family):
		detail = "family must be a family id"
	case query.Workflow != "" && !familySlug.MatchString(query.Workflow):
		detail = "unknown workflow " + query.Workflow
	}
	if detail != "" {
		network.SendError(ctx, http.StatusUnprocessableEntity, detail)
		return
	}
	out, err := c.service.ListLibrary(ctx.Request.Context(), common.MustCaller(ctx), query)
	reply(ctx, http.StatusOK, out, err)
}

// familySlug is a family id (and the shape of a workflow id: a template slug or a UUID).
var familySlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// MaxLibraryList is the most items GET /library answers (own items and the starter looks).
const MaxLibraryList = 1000

// ParseLibraryQuery checks GET /library's query: q trimmed, a leading @ dropped, lower-cased; limit 1-1000 (1000).
func ParseLibraryQuery(kind, group, q, limit string) (LibraryQuery, string) {
	out := LibraryQuery{Kind: kind, Group: group, Q: strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "@")), Limit: MaxLibraryList}
	switch {
	case kind != "" && kind != "element" && kind != "look" && kind != "voice":
		return out, "kind must be element, look or voice"
	case group != "" && !slices.Contains(dto.AllGroups(), group):
		return out, "group must be one of " + strings.Join(dto.AllGroups(), ", ")
	case utf8.RuneCountInString(q) > 80:
		return out, "q must be at most 80 characters"
	}
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n < 1 || n > MaxLibraryList {
			return out, "limit must be a whole number from 1 to 1000"
		}
		out.Limit = n
	}
	return out, ""
}

// createLibraryItemHandler: POST /library — 201 with the item.
func (c *controller) createLibraryItemHandler(ctx *gin.Context) {
	body, err := strictBody[dto.LibraryItemCreate](ctx, false)
	var out dto.LibraryItem
	if err == nil {
		out, err = c.service.CreateLibraryItem(ctx.Request.Context(), common.MustCaller(ctx), body)
	}
	reply(ctx, http.StatusCreated, out, err)
}

// starterLookHandler: GET, PUT and DELETE /library/{family}/{look_id} — a starter look or voice (writes are 409).
func (c *controller) starterLookHandler(ctx *gin.Context) {
	id := ctx.Param("id") + "/" + ctx.Param("look_id")
	if !dto.LookID.MatchString(id) {
		network.SendMixedError(ctx, errNotFound)
		return
	}
	if ctx.Request.Method == http.MethodGet {
		out, err := c.service.GetStarterLook(ctx.Request.Context(), id)
		reply(ctx, http.StatusOK, out, err)
		return
	}
	network.SendMixedError(ctx, c.service.WriteStarterLook(ctx.Request.Context(), id))
}

// getLibraryItemHandler: GET /library/{id}.
func (c *controller) getLibraryItemHandler(ctx *gin.Context) {
	out, err := c.service.GetLibraryItem(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// replaceLibraryItemHandler: PUT /library/{id}.
func (c *controller) replaceLibraryItemHandler(ctx *gin.Context) {
	body, err := strictBody[dto.LibraryItemUpdate](ctx, false)
	var out dto.LibraryItem
	if err == nil {
		out, err = c.service.ReplaceLibraryItem(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// deleteLibraryItemHandler: DELETE /library/{id} — 204.
func (c *controller) deleteLibraryItemHandler(ctx *gin.Context) {
	if err := c.service.DeleteLibraryItem(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// ------------------------------------------------------------------ API 1.15: the director's chat, model sources

// getChatHandler: GET /batches/{id}/chat.
func (c *controller) getChatHandler(ctx *gin.Context) {
	out, err := c.service.Chat(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// postChatHandler: POST /batches/{id}/chat — 202 with the message, stored.
func (c *controller) postChatHandler(ctx *gin.Context) {
	body, err := strictBody[dto.ChatPost](ctx, false)
	var out dto.ChatMessage
	if err == nil {
		out, err = c.service.PostChat(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	}
	reply(ctx, http.StatusAccepted, out, err)
}

// modelSourcesHandler: GET /model-sources.
func (c *controller) modelSourcesHandler(ctx *gin.Context) {
	out, err := c.service.ModelSources(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// customNodesHandler: GET /custom-nodes.
func (c *controller) customNodesHandler(ctx *gin.Context) {
	out, err := c.service.CustomNodes()
	reply(ctx, http.StatusOK, out, err)
}

// listProvidersHandler: GET /admin/providers.
func (c *controller) listProvidersHandler(ctx *gin.Context) {
	out, err := c.service.ListProviders(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// putProviderHandler: PUT /admin/providers/{name} — 201 created, 200 replaced; 403 before the body is read.
func (c *controller) putProviderHandler(ctx *gin.Context) {
	caller := common.MustCaller(ctx)
	if !caller.IsAdmin() {
		network.SendMixedError(ctx, errAdmin)
		return
	}
	body, err := strictBody[dto.ProviderPut](ctx, false)
	var out dto.Provider
	created := false
	if err == nil {
		out, created, err = c.service.PutProvider(ctx.Request.Context(), caller, ctx.Param("name"), body)
	}
	reply(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], out, err)
}

// deleteProviderHandler: DELETE /admin/providers/{name} — 204.
func (c *controller) deleteProviderHandler(ctx *gin.Context) {
	if err := c.service.DeleteProvider(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("name")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// testProviderHandler: POST /admin/providers/{name}/test[?model=].
func (c *controller) testProviderHandler(ctx *gin.Context) {
	out, err := c.service.TestProvider(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("name"), ctx.Query("model"))
	reply(ctx, http.StatusOK, out, err)
}

// ------------------------------------------------------------------ models, model jobs, the hosts admin (API 1.16)

// adminBody is strictBody behind the admin check: 403 before the body is read.
func adminBody[B any](ctx *gin.Context) (common.Caller, *B, error) {
	caller := common.MustCaller(ctx)
	if !caller.IsAdmin() {
		return caller, nil, errAdmin
	}
	body, err := strictBody[B](ctx, false)
	return caller, body, err
}

// accepted answers 202 for a new job, 200 for the same unfinished one.
func accepted(created bool) int {
	if created {
		return http.StatusAccepted
	}
	return http.StatusOK
}

// listModelsHandler: GET /models.
func (c *controller) listModelsHandler(ctx *gin.Context) {
	out, err := c.service.ListModels(ctx.Request.Context(), common.MustCaller(ctx))
	reply(ctx, http.StatusOK, out, err)
}

// downloadModelHandler: POST /admin/models/{model}/download — 202 queued, 200 the same unfinished job.
func (c *controller) downloadModelHandler(ctx *gin.Context) {
	caller, body, err := adminBody[dto.ModelDownload](ctx)
	var out dto.ModelJob
	created := false
	if err == nil {
		out, created, err = c.service.DownloadModel(ctx.Request.Context(), caller, ctx.Param("model"), body)
	}
	reply(ctx, accepted(created), out, err)
}

// deleteModelHandler: DELETE /admin/models/{model}?host=&variant=.
func (c *controller) deleteModelHandler(ctx *gin.Context) {
	var variant *string
	if v, ok := ctx.GetQuery("variant"); ok {
		variant = &v
	}
	out, created, err := c.service.DeleteModel(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("model"), ctx.Query("host"), variant)
	reply(ctx, accepted(created), out, err)
}

// deleteSharedLoraHandler: DELETE /admin/loras/{lora_id}?host=.
func (c *controller) deleteSharedLoraHandler(ctx *gin.Context) {
	out, created, err := c.service.DeleteSharedLora(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("lora_id"), ctx.Query("host"))
	reply(ctx, accepted(created), out, err)
}

// listModelJobsHandler: GET /admin/model-jobs?host=&active=.
func (c *controller) listModelJobsHandler(ctx *gin.Context) {
	out, err := c.service.ListModelJobs(ctx.Request.Context(), common.MustCaller(ctx), ctx.Query("host"), ctx.Query("active"))
	reply(ctx, http.StatusOK, out, err)
}

// getModelJobHandler: GET /admin/model-jobs/{job_id}.
func (c *controller) getModelJobHandler(ctx *gin.Context) {
	out, err := c.service.GetModelJob(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("job_id"))
	reply(ctx, http.StatusOK, out, err)
}

// cancelModelJobHandler: POST /admin/model-jobs/{job_id}/cancel.
func (c *controller) cancelModelJobHandler(ctx *gin.Context) {
	out, err := c.service.CancelModelJob(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("job_id"))
	reply(ctx, http.StatusOK, out, err)
}

// adminUnloadHandler: POST /admin/hosts/{host}/unload — 202.
func (c *controller) adminUnloadHandler(ctx *gin.Context) {
	caller, body, err := adminBody[dto.HostUnload](ctx)
	var out dto.HostUnloadAccepted
	if err == nil {
		out, err = c.service.AdminUnload(ctx.Request.Context(), caller, ctx.Param("host"), body)
	}
	reply(ctx, http.StatusAccepted, out, err)
}

// putHostSettingsHandler: PUT /admin/hosts/{host}/settings.
func (c *controller) putHostSettingsHandler(ctx *gin.Context) {
	caller, body, err := adminBody[dto.HostSettingsPut](ctx)
	var out dto.HostSettings
	if err == nil {
		out, err = c.service.PutHostSettings(ctx.Request.Context(), caller, ctx.Param("host"), body)
	}
	reply(ctx, http.StatusOK, out, err)
}

// putHostPinsHandler: PUT /admin/hosts/{host}/pins.
func (c *controller) putHostPinsHandler(ctx *gin.Context) {
	caller, body, err := adminBody[dto.HostPins](ctx)
	var out dto.HostSettings
	if err == nil {
		out, err = c.service.PutHostPins(ctx.Request.Context(), caller, ctx.Param("host"), body)
	}
	reply(ctx, http.StatusOK, out, err)
}
