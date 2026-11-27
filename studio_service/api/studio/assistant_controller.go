package studio

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
)

// mountAssistant registers the assistant's routes (API 1.44).
func (c *controller) mountAssistant(group *gin.RouterGroup) {
	group.GET("/assistant/threads", func(ctx *gin.Context) {
		if page, ok := pageOr422(ctx, MaxAssistantThreads); ok {
			out, err := c.service.ListAssistantThreads(ctx.Request.Context(), common.MustCaller(ctx), page)
			reply(ctx, http.StatusOK, out, err)
		}
	})
	group.POST("/assistant/first-launch", func(ctx *gin.Context) {
		noContent(ctx, c.service.MarkAssistantFirstLaunch(ctx.Request.Context(), common.MustCaller(ctx)))
	})
	group.POST("/assistant/threads", func(ctx *gin.Context) {
		body, err := strictBody[AssistantThreadCreate](ctx, false)
		var out AssistantThread
		if err == nil {
			out, err = c.service.CreateAssistantThread(ctx.Request.Context(), common.MustCaller(ctx), body)
		}
		reply(ctx, http.StatusCreated, out, err)
	})
	group.GET("/assistant/threads/:id", func(ctx *gin.Context) {
		out, err := c.service.GetAssistantThread(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.PATCH("/assistant/threads/:id", func(ctx *gin.Context) {
		if !utility.IsUUID(ctx.Param("id")) {
			network.SendMixedError(ctx, errNotFound)
			return
		}
		body, err := strictBody[AssistantThreadPatch](ctx, false)
		var out AssistantThread
		if err == nil {
			out, err = c.service.PatchAssistantThread(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	group.DELETE("/assistant/threads/:id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteAssistantThread(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")))
	})
	group.POST("/assistant/threads/:id/messages", func(ctx *gin.Context) {
		caller := common.MustCaller(ctx)
		if _, err := c.service.GetAssistantThread(ctx.Request.Context(), caller, ctx.Param("id")); err != nil {
			network.SendMixedError(ctx, err) // 404 before any 422
			return
		}
		body, err := strictBody[AssistantPost](ctx, false)
		var out AssistantAccepted
		if err == nil {
			out, err = c.service.PostAssistant(ctx.Request.Context(), caller, ctx.Param("id"), body)
		}
		reply(ctx, http.StatusAccepted, out, err)
	})
	group.GET("/assistant/threads/:id/events", func(ctx *gin.Context) {
		c.serveEvents(ctx, func(requestCtx context.Context, since int64) (any, []sseEvent, int64, error) {
			page, events, err := c.service.AssistantEvents(requestCtx, common.MustCaller(ctx), ctx.Param("id"), since)
			return page, events, page.LastSeq, err
		})
	})
	group.GET("/assistant/memory", func(ctx *gin.Context) {
		out, err := c.service.ListAssistantMemory(ctx.Request.Context(), common.MustCaller(ctx), ctx.Query("kind"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.DELETE("/assistant/memory", func(ctx *gin.Context) {
		noContent(ctx, c.service.ClearAssistantMemory(ctx.Request.Context(), common.MustCaller(ctx)))
	})
	group.PATCH("/assistant/memory/:id", func(ctx *gin.Context) {
		if !utility.IsUUID(ctx.Param("id")) {
			network.SendMixedError(ctx, errNotFound)
			return
		}
		body, err := strictBody[AssistantMemoryPatch](ctx, false)
		var out AssistantMemoryItem
		if err == nil {
			out, err = c.service.EditAssistantMemory(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	group.DELETE("/assistant/memory/:id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteAssistantMemory(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")))
	})
	group.GET("/admin/assistant", func(ctx *gin.Context) {
		out, err := c.service.GetAssistantSettings(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
	})
	group.GET("/admin/assistant/cache", func(ctx *gin.Context) {
		out, err := c.service.GetAssistantCache(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
	})
	group.PUT("/admin/assistant", func(ctx *gin.Context) {
		caller := common.MustCaller(ctx)
		if !caller.IsAdmin() {
			network.SendMixedError(ctx, errAdmin) // 403 before the body is read
			return
		}
		body, err := strictBody[AssistantSettingsPut](ctx, false)
		var out AssistantSettings
		if err == nil {
			out, err = c.service.PutAssistantSettings(ctx.Request.Context(), caller, body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
}

// noContent answers 204, or the error.
func noContent(ctx *gin.Context, err error) {
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// MountAssistant registers the handlers of pipelines' evt.assistant.<thread_id>.{step,steered} (durable studio-assistant).
func (p *Projector) MountAssistant(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.assistant.*.step", func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[message.AssistantStep]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		parts := strings.Split(msg.Subject(), ".")
		if event.JobID == nil || len(parts) != 4 || parts[2] != *event.JobID || event.Data.ThreadID != *event.JobID ||
			!utility.IsUUID(*event.JobID) || event.Data.Seq < 1 {
			micro.AckNatsJetStreamMessage(msg) // not a step of the thread its subject names: dropped
			return
		}
		if err := p.ApplyAssistantStep(ctx, event.OwnerID, event.Data); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}) // API 1.59: the steers a turn read, in order with its steps
	group.AddHandler("evt.assistant.*.steered", func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[message.AssistantSteered]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		parts := strings.Split(msg.Subject(), ".")
		if event.JobID == nil || len(parts) != 4 || parts[2] != *event.JobID || event.Data.ThreadID != *event.JobID {
			micro.AckNatsJetStreamMessage(msg)
			return
		}
		if err := p.ApplyAssistantSteered(ctx, event.OwnerID, event.Data); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	})
}
