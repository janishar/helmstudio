package studio

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// mountMagic registers the Magic board routes (API 1.80, plan 39 phase C).
func (c *controller) mountMagic(group *gin.RouterGroup) {
	group.GET("/magic/boards", func(ctx *gin.Context) {
		if page, ok := pageOr422(ctx, MaxBoards); ok {
			out, err := c.service.ListMagicBoards(ctx.Request.Context(), common.MustCaller(ctx), page)
			reply(ctx, http.StatusOK, out, err)
		}
	})
	group.POST("/magic/boards", func(ctx *gin.Context) { // an empty body is an empty board
		body, err := optionalBody[dto.MagicBoardCreate](ctx)
		var out dto.MagicBoard
		if body == nil {
			body = &dto.MagicBoardCreate{}
		}
		if err == nil {
			out, err = c.service.CreateMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), body)
		}
		reply(ctx, http.StatusCreated, out, err)
	})
	group.GET("/magic/boards/:id", func(ctx *gin.Context) {
		switch view, ok := ctx.GetQuery("view"); {
		case ok && view == "agent":
			out, err := c.service.GetMagicAgent(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
			reply(ctx, http.StatusOK, out, err)
		case ok:
			network.SendMixedError(ctx, network.NewUnprocessableError("view must be agent", nil))
		default:
			out, err := c.service.GetMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
			reply(ctx, http.StatusOK, out, err)
		}
	})
	group.PATCH("/magic/boards/:id", idBody(c.service.PatchMagicBoard, http.StatusOK))
	group.DELETE("/magic/boards/:id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")))
	})
	group.POST("/magic/boards/:id/edits", idBody(c.service.EditMagicBoard, http.StatusOK))
	group.GET("/magic/boards/:id/revisions", func(ctx *gin.Context) {
		var before *int
		limit := 20
		for name, set := range map[string]func(int){"before": func(v int) { before = &v }, "limit": func(v int) { limit = v }} {
			if text, ok := ctx.GetQuery(name); ok {
				v, err := strconv.Atoi(text)
				if err != nil || v < 1 || (name == "limit" && v > 100) {
					network.SendMixedError(ctx, network.NewUnprocessableError("before must be a revision and limit 1 to 100", nil))
					return
				}
				set(v)
			}
		}
		out, err := c.service.ListMagicRevisions(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), before, limit)
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/magic/boards/:id/restore", idBody(c.service.RestoreMagicBoard, http.StatusOK))
	group.POST("/magic/boards/:id/undo", idBody(c.service.UndoMagicBoard, http.StatusOK))
	group.POST("/magic/boards/:id/redo", idBody(c.service.RedoMagicBoard, http.StatusOK))
	group.GET("/magic/boards/:id/snapshots", func(ctx *gin.Context) {
		out, err := c.service.ListMagicSnapshots(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/magic/boards/:id/snapshots", func(ctx *gin.Context) { // an empty body: the current revision
		body, err := optionalBody[dto.MagicSnapshotCreate](ctx)
		var out dto.MagicSnapshot
		if body == nil {
			body = &dto.MagicSnapshotCreate{}
		}
		if err == nil {
			out, err = c.service.CreateMagicSnapshot(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, http.StatusCreated, out, err)
	})
	group.PATCH("/magic/boards/:id/snapshots/:snapshot_id", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicSnapshotPatch](ctx, false)
		var out dto.MagicSnapshot
		if err == nil {
			out, err = c.service.PatchMagicSnapshot(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"),
				ctx.Param("snapshot_id"), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	// API 1.81: the standard request and runs
	group.GET("/magic/kinds", func(ctx *gin.Context) {
		out, err := c.service.MagicKinds(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
	})
	group.GET("/magic/settings", func(ctx *gin.Context) {
		out, err := c.service.GetMagicSettings(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
	})
	group.PUT("/magic/settings", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicSettings](ctx, false)
		var out dto.MagicSettings
		if err == nil {
			out, err = c.service.PutMagicSettings(ctx.Request.Context(), common.MustCaller(ctx), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	group.POST("/magic/boards/:id/run", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicRunCreate](ctx, false)
		var out dto.MagicRunStarted
		if err == nil {
			out, err = c.service.RunMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, http.StatusAccepted, out, err)
	})
	group.POST("/magic/boards/:id/plan", idBody(c.service.PlanMagicBoard, http.StatusOK))
	// API 1.82: Send to Timeline
	group.POST("/magic/boards/:id/send", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicSendCreate](ctx, false)
		var out dto.MagicSendResult
		status := http.StatusOK
		if err == nil {
			out, status, err = c.service.SendMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		reply(ctx, status, out, err)
	})
	group.GET("/magic/boards/:id/sends", func(ctx *gin.Context) {
		out, err := c.service.ListMagicSends(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
		reply(ctx, http.StatusOK, out, err)
	})
	group.DELETE("/magic/boards/:id/sends/:send_id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteMagicSend(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("send_id")))
	})
	// API 1.83: Make views of an asset
	group.POST("/magic/boards/:id/assets/:asset_id/views", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicViewsCreate](ctx, false)
		var out dto.MagicViewsStarted
		if err == nil {
			out, err = c.service.MakeMagicViews(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"),
				ctx.Param("asset_id"), body)
		}
		reply(ctx, http.StatusAccepted, out, err)
	})
	// API 1.102: helm mcp's design_voice and save_voice (no card: the MCP client's ask is the Yes)
	group.POST("/magic/boards/:id/assets/:asset_id/voice/design", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicVoiceDesign](ctx, false)
		var out any
		if err == nil {
			out, err = c.service.MagicVoiceDesign(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("asset_id"), body)
		}
		reply(ctx, http.StatusAccepted, out, err)
	})
	group.POST("/magic/boards/:id/assets/:asset_id/voice", func(ctx *gin.Context) {
		body, err := strictBody[dto.MagicVoiceSave](ctx, false)
		var out any
		if err == nil {
			out, err = c.service.MagicVoiceSave(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), ctx.Param("asset_id"), body)
		}
		reply(ctx, http.StatusOK, out, err)
	})
	group.DELETE("/magic/boards/:id/snapshots/:snapshot_id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteMagicSnapshot(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"),
			ctx.Param("snapshot_id")))
	})
}
