package lora

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/common"
)

// controller serves /v1/studio/loras.
type controller struct {
	micro.Controller
	service *Service
}

// NewController returns the controller; the module's prefix guard authenticates all of /v1/.
func NewController(service *Service) micro.Controller {
	return &controller{Controller: micro.NewController("/v1/studio", nil, nil), service: service}
}

// MountRoutes registers the LoRA routes of contracts/openapi.yaml.
func (c *controller) MountRoutes(group *gin.RouterGroup) {
	group.GET("/loras", c.listHandler)
	group.POST("/loras", c.createHandler)
	group.GET("/loras/:id", c.getHandler)
	group.PATCH("/loras/:id", c.renameHandler)
	group.DELETE("/loras/:id", c.deleteHandler)
	group.POST("/loras/:id/complete", c.completeHandler)
}

// reply sends out with status, or the error.
func reply[T any](ctx *gin.Context, status int, out T, err error) {
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendJSON(ctx, status, out)
}

// strict decodes a JSON body with no field the schema lacks (additionalProperties: false).
func strict[B any](ctx *gin.Context) (*B, error) {
	body := new(B)
	decoder := json.NewDecoder(ctx.Request.Body) // the module's body limit bounds it
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return nil, network.NewUnprocessableError("invalid JSON body", err)
	}
	return body, nil
}

// listHandler: GET /loras?source=&family=&q=.
func (c *controller) listHandler(ctx *gin.Context) {
	out, err := c.service.List(ctx.Request.Context(), common.MustCaller(ctx),
		Query{Source: ctx.Query("source"), Family: ctx.Query("family"), Q: ctx.Query("q")})
	reply(ctx, http.StatusOK, out, err)
}

// createHandler: POST /loras — 201 with the PUT link.
func (c *controller) createHandler(ctx *gin.Context) {
	body, err := strict[UploadCreate](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.CreateUpload(ctx.Request.Context(), common.MustCaller(ctx), body)
	reply(ctx, http.StatusCreated, out, err)
}

// getHandler: GET /loras/{id}.
func (c *controller) getHandler(ctx *gin.Context) {
	out, err := c.service.Get(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// renameHandler: PATCH /loras/{id}.
func (c *controller) renameHandler(ctx *gin.Context) {
	body, err := strict[Patch](ctx)
	if err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	out, err := c.service.Rename(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"), body)
	reply(ctx, http.StatusOK, out, err)
}

// deleteHandler: DELETE /loras/{id} — 204.
func (c *controller) deleteHandler(ctx *gin.Context) {
	if err := c.service.Delete(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id")); err != nil {
		network.SendMixedError(ctx, err)
		return
	}
	network.SendNoContent(ctx)
}

// completeHandler: POST /loras/{id}/complete.
func (c *controller) completeHandler(ctx *gin.Context) {
	out, err := c.service.Complete(ctx.Request.Context(), common.MustCaller(ctx), ctx.Param("id"))
	reply(ctx, http.StatusOK, out, err)
}

// ------------------------------------------------------------------ the sweep

// Sweep removes, as System, the objects then the rows of LoRAs deleted for more than Grace and of uploads left pending
// for more than PendingTTL, and the objects of rejected ones (their rows stay listed until deleted). It only deletes a
// key under its row's owner prefix.
func Sweep(ctx context.Context, pool *pgxpool.Pool, store storage.Store) (int, error) {
	removed := 0
	err := common.System(ctx, pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `SELECT id::text, owner_id::text, key, state = 'rejected' AND deleted_at IS NULL FROM loras
			WHERE deleted_at < clock_timestamp() - make_interval(secs => $1)
				OR (state = 'pending' AND deleted_at IS NULL AND created_at < clock_timestamp() - make_interval(secs => $2))
				OR (state = 'rejected' AND deleted_at IS NULL AND NOT swept)
			ORDER BY created_at LIMIT 500 FOR UPDATE SKIP LOCKED`, Grace.Seconds(), PendingTTL.Seconds())
		found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
			ID, Owner, Key string
			Keep           bool
		}])
		if err != nil {
			return err
		}
		var gone, kept, keys []string
		for _, r := range found {
			if !common.Owns(r.Owner, r.Key) || strings.HasSuffix(r.Key, "/") {
				log.Printf("lora sweep: LoRA %s key outside u/%s/, left alone", r.ID, r.Owner)
				continue
			}
			keys = append(keys, r.Key)
			if r.Keep {
				kept = append(kept, r.ID)
			} else {
				gone = append(gone, r.ID)
			}
		}
		if len(keys) == 0 {
			return nil
		}
		if err := store.Delete(ctx, keys...); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE loras SET swept = true WHERE id = ANY($1::uuid[])`, kept); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM loras WHERE id = ANY($1::uuid[])`, gone)
		removed = int(tag.RowsAffected())
		return err
	})
	return removed, err
}

// RunSweep sweeps every minute until ctx ends.
func RunSweep(ctx context.Context, pool *pgxpool.Pool, store storage.Store) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		if _, err := Sweep(ctx, pool, store); err != nil && ctx.Err() == nil {
			log.Printf("lora sweep: %v", err)
		}
	}
}
