package studio

import (
	"context"
	"errors"
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
		out, err := c.service.ListMagicBoards(ctx.Request.Context(), common.MustCaller(ctx))
		reply(ctx, http.StatusOK, out, err)
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
	group.PATCH("/magic/boards/:id", magicIDBody(c.service.PatchMagicBoard))
	group.DELETE("/magic/boards/:id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteMagicBoard(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")))
	})
	group.POST("/magic/boards/:id/edits", magicIDBody(c.service.EditMagicBoard))
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
	group.POST("/magic/boards/:id/restore", magicIDBody(c.service.RestoreMagicBoard))
	group.POST("/magic/boards/:id/undo", magicIDBody(c.service.UndoMagicBoard))
	group.POST("/magic/boards/:id/redo", magicIDBody(c.service.RedoMagicBoard))
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
	group.POST("/magic/boards/:id/plan", magicIDBody(c.service.PlanMagicBoard))
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
	group.DELETE("/magic/boards/:id/snapshots/:snapshot_id", func(ctx *gin.Context) {
		noContent(ctx, c.service.DeleteMagicSnapshot(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"),
			ctx.Param("snapshot_id")))
	})
}

// magicIDBody decodes a strict body and answers fn(:id) with 200; an op that cannot apply is MagicEditError's 422.
func magicIDBody[B, T any](fn func(ctx context.Context, caller common.Caller, id string, body *B) (T, error)) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, err := strictBody[B](ctx, false)
		var out T
		if err == nil {
			out, err = fn(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
		}
		var failed *MagicEditError
		if errors.As(err, &failed) {
			network.SendJSON(ctx, http.StatusUnprocessableEntity, failed)
			return
		}
		reply(ctx, http.StatusOK, out, err)
	}
}
