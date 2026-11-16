package gallery

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/common"
)

// controller serves the gallery and upload routes of /v1/studio.
type controller struct {
	micro.Controller
	service *Service
}

// NewController returns the controller; the module's prefix guard authenticates all of /v1/.
func NewController(service *Service) micro.Controller {
	return &controller{Controller: micro.NewController("/v1/studio", nil, nil), service: service}
}

// MountRoutes registers the takes and uploads routes of contracts/openapi.yaml. The session path keeps :id, the
// wildcard name the studio controller uses at that position.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/takes", c.listHandler)
	group.GET("/takes/:id", c.getHandler)
	group.PATCH("/takes/:id", c.updateHandler)
	group.DELETE("/takes/:id", c.deleteHandler)
	group.POST("/takes/:id/reuse", c.reuseHandler)
	group.POST("/takes/:id/use", c.useHandler)
	group.GET("/sessions/:id/takes", c.sessionTakesHandler)
	group.POST("/uploads", c.createUploadHandler)
	group.POST("/uploads/:id/complete", c.completeUploadHandler)
	group.POST("/admin/imports/takes", c.createImportHandler)
	group.POST("/admin/imports/takes/:id/complete", c.completeImportHandler)
}

// createImportHandler: POST /admin/imports/takes — 201 a new key, 200 one seen before; 403 before the body is read.
func (c *controller) createImportHandler(ctx *gin.Context) {
	caller := common.MustCaller(ctx)
	if !caller.IsAdmin() {
		network.SendMixedError(ctx, errAdmin)
		return
	}
	// a legacy snapshot alone may be 64 KB: ReqBody's limit is too small
	body := new(dto.ImportTakeCreate)
	raw, err := io.ReadAll(http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 256<<10))
	if err == nil {
		err = json.Unmarshal(raw, body)
	}
	if err != nil {
		network.SendMixedError(ctx, network.NewUnprocessableError("invalid JSON body", err))
		return
	}
	if detail := body.Validate(); detail != "" {
		network.SendMixedError(ctx, network.NewUnprocessableError(detail, nil))
		return
	}
	out, created, err := c.service.CreateImport(ctx.Request.Context(), caller, body)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	reply(ctx, status, out, err)
}

// completeImportHandler: POST /admin/imports/takes/{take_id}/complete.
func (c *controller) completeImportHandler(ctx *gin.Context) {
	out, err := c.service.CompleteImport(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// reply sends out with status, or the error.
func reply[T any](ctx *gin.Context, status int, out T, err error) {
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, status, out)
}

// listHandler: GET /takes.
func (c *controller) listHandler(ctx *gin.Context) {
	filter, detail := ParseFilter(ctx.Request.URL.Query(), true)
	if detail != "" {
		network.SendError(ctx, http.StatusUnprocessableEntity, detail)
		return
	}
	out, err := c.service.List(ctx.Request.Context(), common.MustCaller(ctx), filter)
	reply(ctx, http.StatusOK, out, err)
}

// sessionTakesHandler: GET /sessions/{id}/takes — only cursor and limit apply; 404 before any 422.
func (c *controller) sessionTakesHandler(ctx *gin.Context) {
	query := ctx.Request.URL.Query()
	filter, detail := ParseFilter(map[string][]string{"cursor": query["cursor"], "limit": query["limit"]}, false)
	caller, id := common.MustCaller(ctx), ctx.Param("id")
	if detail != "" {
		if _, err := c.service.SessionTakes(ctx.Request.Context(), caller, id, Filter{Limit: 1}); err != nil {
			network.SendMixedError(ctx, err)
			return
		}
		network.SendError(ctx, http.StatusUnprocessableEntity, detail)
		return
	}
	out, err := c.service.SessionTakes(ctx.Request.Context(), caller, id, filter)
	reply(ctx, http.StatusOK, out, err)
}

// getHandler: GET /takes/{id}.
func (c *controller) getHandler(ctx *gin.Context) {
	out, err := c.service.Get(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// updateHandler: PATCH /takes/{id}.
func (c *controller) updateHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.TakePatch](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.Update(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	reply(ctx, http.StatusOK, out, err)
}

// deleteHandler: DELETE /takes/{id}.
func (c *controller) deleteHandler(ctx *gin.Context) {
	if err := c.service.Delete(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// reuseHandler: POST /takes/{id}/reuse.
func (c *controller) reuseHandler(ctx *gin.Context) {
	out, err := c.service.Reuse(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// useHandler: POST /takes/{id}/use.
func (c *controller) useHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.TakeUse](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.Use(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	reply(ctx, http.StatusOK, out, err)
}

// createUploadHandler: POST /uploads — 201 with the PUT link.
func (c *controller) createUploadHandler(ctx *gin.Context) {
	body, err := network.ReqBody[dto.UploadCreate](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.CreateUpload(ctx.Request.Context(), common.MustCaller(ctx), body)
	reply(ctx, http.StatusCreated, out, err)
}

// completeUploadHandler: POST /uploads/{id}/complete.
func (c *controller) completeUploadHandler(ctx *gin.Context) {
	out, err := c.service.CompleteUpload(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}
