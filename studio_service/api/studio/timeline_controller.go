package studio

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// mountTimelines registers the sequence, export, media-job and frame routes (API 1.17).
func (c *controller) mountTimelines(group *gin.RouterGroup) {
	group.GET("/timelines", func(ctx *gin.Context) {
		out, err := c.service.ListTimelines(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/timelines", func(ctx *gin.Context) { // an empty body is an empty sequence
		body, err := optionalBody[dto.TimelineCreate](ctx)
		var out dto.Timeline
		if body == nil {
			body = &dto.TimelineCreate{}
		}
		if err == nil {
			out, err = c.service.CreateTimeline(ctx.Request.Context(), common.MustCaller(ctx), body)
		}
		replyTimeline(ctx, http.StatusCreated, out, err)
	})
	group.GET("/timelines/:id", func(ctx *gin.Context) {
		out, err := c.service.GetTimeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.PUT("/timelines/:id", timelineIDBody(c.service.UpdateTimeline, http.StatusOK))
	group.PATCH("/timelines/:id", timelineIDBody(c.service.PatchTimeline, http.StatusOK))
	group.DELETE("/timelines/:id", func(ctx *gin.Context) {
		if err := c.service.DeleteTimeline(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
			network.SendMixedError(ctx, err)
			return
		}
		network.SendNoContent(ctx)
	})
	group.POST("/timelines/:id/clips", timelineIDBody(c.service.AddTimelineClips, http.StatusOK))
	group.POST("/timelines/:id/undo", timelineIDBody(c.service.UndoTimeline, http.StatusOK))
	group.POST("/timelines/:id/redo", timelineIDBody(c.service.RedoTimeline, http.StatusOK))
	group.POST("/timelines/:id/shots", timelineIDBody(c.service.CreateShot, http.StatusAccepted))
	group.POST("/timelines/:id/rerender", timelineIDBody(c.service.RerenderTimeline, http.StatusAccepted))
	group.POST("/timelines/:id/export/plan", func(ctx *gin.Context) {
		body, err := optionalBody[dto.TimelineExportCreate](ctx)
		var out dto.ExportPlan
		if err == nil {
			out, err = c.service.PlanExport(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/timelines/:id/export", timelineIDBody(c.service.ExportTimeline, http.StatusAccepted))
	group.GET("/timelines/:id/exports", func(ctx *gin.Context) {
		out, err := c.service.ListExports(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.GET("/media-jobs/:job_id", func(ctx *gin.Context) {
		out, err := c.service.GetMediaJob(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("job_id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/media-jobs/:job_id/cancel", func(ctx *gin.Context) {
		out, err := c.service.CancelMediaJob(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("job_id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/takes/:id/frame", func(ctx *gin.Context) {
		body, err := strictBody[dto.TakeFrameRequest](ctx, false)
		var out dto.TakeFrame
		if err == nil {
			out, err = c.service.TakeFrame(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		status := http.StatusOK
		if out.Job != nil {
			status = http.StatusAccepted
		}
		reply(ctx, status, out, err)
	})
}

// timelineIDBody decodes a strict body and answers fn(:id) with status; a broken timeline rule is the 422 with
// problems.
func timelineIDBody[B, T any](fn func(ctx context.Context, caller common.Caller, id string, body *B) (T, error), status int) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, err := strictBody[B](ctx, false)
		var out T
		if err == nil {
			out, err = fn(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		replyTimeline(ctx, status, out, err)
	}
}

// replyTimeline answers out, a TimelineError as its 422 body, or the error.
func replyTimeline[T any](ctx *gin.Context, status int, out T, err error) {
	var problems *TimelineError
	if errors.As(err, &problems) {
		network.SendJSON(ctx, http.StatusUnprocessableEntity, problems)
		return
	}
	reply(ctx, status, out, err)
}
