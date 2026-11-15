package studio

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// Memory (contracts/README.md "Memory", plan 16.6): pipelines owns it (LangGraph's store in its own database,
// namespace <owner_id>/pipeline/<pipeline_id>); studio checks the pipeline is the caller's (row-level security: another
// user's is a 404), then asks pipelines.memory as the caller, so a request never leaves the caller's namespace.

// MemoryAsk asks pipelines_service about a pipeline's memory (pipelines.memory); an error is no answer.
type MemoryAsk func(ctx context.Context, req message.MemoryRequest) (message.MemoryReply, error)

// SetMemory sets how the service reaches pipelines' memory; without one the memory endpoints answer 503.
func (s *Service) SetMemory(fn MemoryAsk) { s.memory = fn }

// askMemory sends req and maps the reply's code: not_found 404, full 409, invalid 422; no answer or internal 503.
func (s *Service) askMemory(ctx context.Context, req message.MemoryRequest) (message.MemoryReply, error) {
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.memory == nil {
		return message.MemoryReply{}, unavailable
	}
	reply, err := s.memory(ctx, req)
	switch {
	case err != nil || reply.Code == "internal":
		return reply, unavailable
	case reply.Code == "not_found":
		return reply, errNotFound
	case reply.Code == "full":
		return reply, network.NewConflictError(reply.Error, nil)
	case reply.Code != "":
		return reply, network.NewUnprocessableError(reply.Error, nil)
	}
	return reply, nil
}

// ownPipeline is errNotFound unless id is one of the caller's pipelines.
func (s *Service) ownPipeline(ctx context.Context, caller common.Caller, id string) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, err := readPipeline(ctx, tx, id, false)
		return err
	})
}

// memoryDTOs maps items, their takes as signed refs of the caller's takes (deleted ones left out).
func (s *Service) memoryDTOs(ctx context.Context, caller common.Caller, items []message.MemoryItem) ([]dto.MemoryItem, error) {
	var ids []string
	for _, item := range items {
		ids = append(append(ids, item.Kept...), item.Rejected...)
	}
	refs := map[string]gallerydto.TakeRef{}
	if len(ids) > 0 {
		err := s.tx(ctx, caller, func(tx pgx.Tx) error {
			type found struct{ ID, Kind, Title, Key string }
			rows, err := collect[found](ctx, tx, `SELECT t.id::text, t.kind, t.title, coalesce(t.video->'poster'->>'key', t.key)
				FROM takes t WHERE t.id = ANY($1::uuid[]) AND t.deleted_at IS NULL AND t.ready`, uuids(ids))
			for _, row := range rows {
				refs[row.ID] = gallerydto.TakeRef{ID: row.ID, Kind: row.Kind, Title: row.Title, ThumbURL: s.signer.Sign(row.Key)}
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	takes := func(ids []string) []gallerydto.TakeRef {
		out := []gallerydto.TakeRef{}
		for _, id := range ids {
			if ref, ok := refs[id]; ok {
				out = append(out, ref)
			}
		}
		return out
	}
	out := make([]dto.MemoryItem, len(items))
	for i, item := range items {
		out[i] = dto.MemoryItem{ID: item.ID, Kind: item.Kind, PipelineID: item.PipelineID, Key: item.Key, Text: item.Text,
			Kept: takes(item.Kept), Rejected: takes(item.Rejected), Agreed: item.Agreed}
		_ = json.Unmarshal(item.Source, &out[i].Source)
		out[i].CreatedAt, _ = time.Parse(time.RFC3339Nano, item.CreatedAt)
		out[i].UpdatedAt, _ = time.Parse(time.RFC3339Nano, item.UpdatedAt)
	}
	return out, nil
}

// ListMemory lists one of the caller's pipelines' memory, newest first (kind: one kind).
func (s *Service) ListMemory(ctx context.Context, caller common.Caller, pipelineID, kind string) (dto.MemoryList, error) {
	out := dto.MemoryList{PipelineID: pipelineID, Items: []dto.MemoryItem{}, Counts: map[string]int{"judge_example": 0, "note": 0}}
	if kind != "" && kind != "judge_example" && kind != "note" {
		return out, network.NewUnprocessableError("kind must be judge_example or note", nil)
	}
	if err := s.ownPipeline(ctx, caller, pipelineID); err != nil {
		return out, err
	}
	req := message.MemoryRequest{OwnerID: caller.ID, Op: "list", PipelineID: pipelineID}
	if kind != "" {
		req.Kind = &kind
	}
	reply, err := s.askMemory(ctx, req)
	if err != nil {
		return out, err
	}
	if out.Items, err = s.memoryDTOs(ctx, caller, reply.Items); err != nil {
		return out, err
	}
	out.Truncated = reply.Truncated
	for _, item := range out.Items { // ponytail: counted over the answer, so a kind filter zeroes the other count
		out.Counts[item.Kind]++
	}
	return out, nil
}

// CreateMemoryNote adds a note typed by the caller to one of their pipelines' memory.
func (s *Service) CreateMemoryNote(ctx context.Context, caller common.Caller, body *dto.MemoryCreate) (dto.MemoryItem, error) {
	if err := s.ownPipeline(ctx, caller, body.PipelineID); err != nil {
		return dto.MemoryItem{}, err
	}
	text := strings.TrimSpace(body.Text)
	reply, err := s.askMemory(ctx, message.MemoryRequest{OwnerID: caller.ID, Op: "create", PipelineID: body.PipelineID,
		Text: &text, Key: body.Key})
	if err != nil {
		return dto.MemoryItem{}, err
	}
	if reply.Item == nil {
		return dto.MemoryItem{}, network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	}
	items, err := s.memoryDTOs(ctx, caller, []message.MemoryItem{*reply.Item})
	if err != nil {
		return dto.MemoryItem{}, err
	}
	return items[0], nil
}

// DeleteMemoryItem deletes one item of the caller's memory. The path names only the item, so studio asks each of the
// caller's pipelines' namespaces until one has it. ponytail: up to 100 requests; one request once the contract lets
// pipelines.memory delete name no pipeline_id (reported as a contract defect).
func (s *Service) DeleteMemoryItem(ctx context.Context, caller common.Caller, itemID string) error {
	if !utility.IsUUID(itemID) {
		return errNotFound
	}
	var pipelines []string
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		rows, _ := tx.Query(ctx, `SELECT id::text FROM user_pipelines ORDER BY updated_at DESC, id`)
		pipelines, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return err
	}
	for _, pipelineID := range pipelines {
		_, err := s.askMemory(ctx, message.MemoryRequest{OwnerID: caller.ID, Op: "delete", PipelineID: pipelineID, ItemID: &itemID})
		if err != errNotFound {
			return err
		}
	}
	return errNotFound
}

// clearMemory asks pipelines to clear a deleted pipeline's memory; it is the deletion's afterthought, so a failure is
// only logged.
func (s *Service) clearMemory(ctx context.Context, ownerID, pipelineID string) {
	if _, err := s.askMemory(ctx, message.MemoryRequest{OwnerID: ownerID, Op: "clear", PipelineID: pipelineID}); err != nil &&
		err != errNotFound {
		log.Printf("memory: could not clear pipeline %s: %v", pipelineID, err)
	}
}
