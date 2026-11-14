package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ora/orago/micro"
	"github.com/ora/orago/utility"

	"github.com/ora/catalog_service/api/catalog/message"
	"github.com/ora/catalog_service/common"
	"github.com/ora/catalog_service/gen"
	"github.com/ora/catalog_service/messaging"
)

// Projector applies evt.catalog.<session_id>.<type> messages to the catalog_* tables and appends each to catalog_events, the
// session's event log the events route serves. Object keys stay in the tables; a stored event of a row-backed type
// only names the rows it changed (ids), so links are signed when it is read.
type Projector struct {
	pool *pgxpool.Pool
	stop Stopper // tells agents again to stop a stopped run's job, or to skip its stopped images, when it reports in
}

// NewProjector returns the projector writing through pool.
func NewProjector(pool *pgxpool.Pool, stop Stopper) *Projector {
	return &Projector{pool: pool, stop: stop}
}

// MountNatsJetStream registers one handler per evt.catalog.* type.
func (p *Projector) MountNatsJetStream(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.catalog.*.status", handler(p, "status", p.applyStatus))
	group.AddHandler("evt.catalog.*.log", handler(p, "log", storeAsIs))
	group.AddHandler("evt.catalog.*.step", handler(p, "step", storeAsIs))
	group.AddHandler("evt.catalog.*.usage", handler(p, "usage", storeAsIs))
	group.AddHandler("evt.catalog.*.question", handler(p, "question", applyQuestion))
	group.AddHandler("evt.catalog.*.skus", handler(p, "skus", applySkus))
	group.AddHandler("evt.catalog.*.image", handler(p, "image", applyImage))
	group.AddHandler("evt.catalog.*.run", handler(p, "run", applyRun))
}

// based is an event's data: every one embeds message.Base.
type based interface{ Common() message.Base }

// applyFunc projects one event in the system transaction and returns what catalog_events stores for it; nil skips the
// event (it names a row that isn't this session's).
type applyFunc[D based] func(ctx context.Context, tx pgx.Tx, event *gen.Message[D], raw json.RawMessage) (any, error)

// handler decodes the envelope (an undecodable one is dropped), projects it and acks; a database failure naks it.
func handler[D based](p *Projector, typ string, apply applyFunc[D]) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		raw, _ := micro.JsonToMsg[gen.Envelope](msg.Data())
		if err := project(ctx, p.pool, typ, event, raw.Data, apply); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// project applies the event once per (job_id, seq) when its session belongs to the envelope's owner, then appends
// it to the session's events with the next seq. Anything else is logged and skipped (acked).
func project[D based](ctx context.Context, pool *pgxpool.Pool, typ string, event *gen.Message[D], raw json.RawMessage,
	apply applyFunc[D]) error {
	base := event.Data.Common()
	if event.JobID == nil || !utility.IsUUID(base.SessionID) {
		log.Printf("projector: dropping %s without a job_id or session_id", event.Type)
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		var owner string
		err := tx.QueryRow(ctx, `SELECT owner_id::text FROM catalog_sessions WHERE id = $1 FOR UPDATE`, base.SessionID).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != event.OwnerID) {
			log.Printf("projector: %s for unknown session %s (owner %s)", event.Type, base.SessionID, event.OwnerID)
			return nil
		} else if err != nil {
			return err
		}
		if first, err := messaging.FirstSeen(ctx, tx, *event.JobID, base.Seq); err != nil || !first {
			return err
		}
		stored, err := apply(ctx, tx, event, raw)
		if err != nil || stored == nil {
			return err
		}
		return appendEvent(ctx, tx, base.SessionID, owner, typ, *event.JobID, event.OccurredAt, stored)
	})
}

// appendEvent adds one event to the session's log under the session's next seq (the events route serves it).
func appendEvent(ctx context.Context, tx pgx.Tx, sessionID, owner, typ, workID string, ts time.Time, data any) error {
	_, err := tx.Exec(ctx, `WITH next AS (UPDATE catalog_sessions SET event_seq = event_seq + 1 WHERE id = $1 RETURNING event_seq)
		INSERT INTO catalog_events (session_id, seq, owner_id, type, work_id, ts, data)
		SELECT $1, event_seq, $2, $3, $4, $5, $6 FROM next`, sessionID, owner, typ, workID, ts, data)
	return err
}

// storeAsIs stores a log, step or usage event as published (they carry no keys).
func storeAsIs(_ context.Context, _ pgx.Tx, _ *gen.Message[message.Base], raw json.RawMessage) (any, error) {
	return raw, nil
}

// final holds the statuses that end a job.
var final = map[string]bool{"succeeded": true, "failed": true}

// applyStatus moves the process or run row on (a finished one never reopens or turns into the other final status, a
// stopped one keeps its error). A finished run marks its images still generating as failed, so no SKU stays Running; a
// finished process job runs afterProcess. A stopped run that reports queued or running was picked up by agents after
// the stop: agents is told to stop it again; a running run with stopped images is told to skip them again (the skip
// may have reached no replica).
func (p *Projector) applyStatus(ctx context.Context, tx pgx.Tx, event *gen.Message[message.Status], raw json.RawMessage) (any, error) {
	data := event.Data
	table := "catalog_processes"
	if data.Work == "run" {
		table = "catalog_runs"
	}
	tag, err := tx.Exec(ctx, `UPDATE `+table+` SET status = $3, error = $4
		WHERE id = $1 AND session_id = $2 AND (status NOT IN ('succeeded', 'failed') OR
		  ($5 AND status = $3 AND error IS DISTINCT FROM $6))`,
		*event.JobID, data.SessionID, data.Status, data.Error, final[data.Status], stoppedError)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if data.Work == "run" && !final[data.Status] {
			var stopped bool
			err := tx.QueryRow(ctx, `SELECT error IS NOT DISTINCT FROM $2 FROM catalog_runs WHERE id = $1`, *event.JobID,
				stoppedError).Scan(&stopped)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if stopped {
				p.stop.Run(*event.JobID)
			}
		}
		return nil, nil
	}
	if data.Work == "run" && data.Status == "running" {
		var skipped []string
		if err := tx.QueryRow(ctx, `SELECT stopped_image_ids::text[] FROM catalog_runs WHERE id = $1`, *event.JobID).Scan(&skipped); err != nil {
			return nil, err
		}
		p.stop.Images(*event.JobID, skipped)
	}
	if data.Work == "process" && final[data.Status] {
		if err := afterProcess(ctx, tx, *event.JobID, data.SessionID, data.Status == "succeeded"); err != nil {
			return nil, err
		}
	}
	if data.Work == "run" && final[data.Status] {
		if _, err := tx.Exec(ctx, `UPDATE catalog_runs SET finished_at = clock_timestamp() WHERE id = $1`, *event.JobID); err != nil {
			return nil, err
		}
		reason := "the run ended before this image was made"
		if data.Error != nil {
			reason = *data.Error
		}
		if _, err := tx.Exec(ctx, `UPDATE catalog_images SET status = 'failed', error = $2, updated_at = clock_timestamp()
			WHERE run_id = $1 AND status = 'generating'`, *event.JobID, reason); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// afterProcess runs when a processing job ends. A job that succeeded ran every check over every SKU, so the open
// questions it did not raise again are gone (the input that caused them changed): they are deleted, and Start no
// longer waits for them. An input changed while it ran (recheck) queues the checks-only job it waited for.
func afterProcess(ctx context.Context, tx pgx.Tx, jobID, sessionID string, succeeded bool) error {
	if succeeded {
		if _, err := tx.Exec(ctx, `DELETE FROM catalog_questions WHERE session_id = $1 AND answer IS NULL AND id::text NOT IN (
			SELECT data->>'question_id' FROM catalog_events WHERE session_id = $1 AND work_id = $2 AND type = 'question')`,
			sessionID, jobID); err != nil {
			return err
		}
	}
	var pending bool
	err := tx.QueryRow(ctx, `UPDATE catalog_sessions SET recheck = false WHERE id = $1 AND recheck RETURNING true`, sessionID).Scan(&pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	st, err := loadState(ctx, tx, sessionID, false)
	if err != nil || !st.inSetup() || st.processing() || len(st.skus) == 0 {
		return err
	}
	_, err = enqueueProcess(ctx, tx, st, []message.Ingest{}, nil)
	return err
}

// applyQuestion upserts the question on (session_id, key); an answered one is never re-opened or changed.
func applyQuestion(ctx context.Context, tx pgx.Tx, event *gen.Message[message.Question], _ json.RawMessage) (any, error) {
	data := event.Data
	options, _ := json.Marshal(data.Options)
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO catalog_questions (id, owner_id, session_id, key, check_id, text, options, blocking, sku_codes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (session_id, key) DO UPDATE SET check_id = $5, text = $6, options = $7, blocking = $8, sku_codes = $9
		WHERE catalog_questions.answer IS NULL RETURNING id::text`, utility.NewID(), event.OwnerID, data.SessionID, data.Key,
		data.Check, data.Text, options, data.Blocking, nonNil(data.SkuCodes)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // answered already
	}
	return map[string]string{"question_id": id}, err
}

// applySkus upserts the SKUs by code (a confirmed SKU stays confirmed), links colour variants by code and stores the
// rules read from the documents.
func applySkus(ctx context.Context, tx pgx.Tx, event *gen.Message[message.Skus], _ json.RawMessage) (any, error) {
	data := event.Data
	var ids []string
	for _, sku := range data.Skus {
		keys := []string{}
		for _, key := range sku.RawKeys {
			if common.Owns(event.OwnerID, key) {
				keys = append(keys, key)
			}
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO catalog_skus (id, owner_id, session_id, code, name, category, subcategory,
			group_key, group_label, attributes, confirmed, brief, raw_keys)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, '{"model": null, "background": null, "poses": [], "resolution": null, "ratio": null}', $12)
			ON CONFLICT (session_id, code) DO UPDATE SET name = $5, category = $6, subcategory = $7, group_key = $8,
			  group_label = $9, attributes = $10, confirmed = catalog_skus.confirmed OR $11, raw_keys = $12, updated_at = clock_timestamp()
			RETURNING id::text`, utility.NewID(), event.OwnerID, data.SessionID, sku.Code, sku.Name, sku.Category,
			sku.Subcategory, sku.GroupKey, sku.GroupLabel, sku.Attributes, sku.Confirmed, keys).Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	for _, sku := range data.Skus {
		if _, err := tx.Exec(ctx, `UPDATE catalog_skus s SET variant_of = (SELECT id FROM catalog_skus WHERE session_id = $1 AND code = $3)
			WHERE s.session_id = $1 AND s.code = $2`, data.SessionID, sku.Code, sku.VariantOf); err != nil {
			return nil, err
		}
	}
	if len(data.Rules) > 0 && string(data.Rules) != "null" {
		if _, err := tx.Exec(ctx, `UPDATE catalog_sessions SET rules = $2, updated_at = clock_timestamp() WHERE id = $1`,
			data.SessionID, data.Rules); err != nil {
			return nil, err
		}
	}
	return map[string]any{"sku_ids": nonNil(ids), "found": data.Found, "expected": data.Expected}, nil
}

// applyImage records a render: its key and every try's key (all under the owner's prefix only, else the event is
// skipped), review, status, attempts and first_pass. Only a generating image changes, so a late event never undoes a
// review.
func applyImage(ctx context.Context, tx pgx.Tx, event *gen.Message[message.Image], _ json.RawMessage) (any, error) {
	data := event.Data
	owned := data.Key == nil || common.Owns(event.OwnerID, *data.Key)
	for _, try := range data.AttemptsDetail {
		owned = owned && common.Owns(event.OwnerID, try.Key)
	}
	if !owned {
		log.Printf("projector: image %s with a key outside its owner's prefix", data.ImageID)
		return nil, nil
	}
	review := data.Review
	if len(review) == 0 || string(review) == "null" {
		review = nil
	}
	var tries any // SQL NULL keeps the stored tries
	if data.AttemptsDetail != nil {
		tries = data.AttemptsDetail
	}
	tag, err := tx.Exec(ctx, `UPDATE catalog_images SET status = $4, key = coalesce($5, key), review = coalesce($6::jsonb, review),
		error = $7, attempts = $8, first_pass = $9, tries = coalesce($10::jsonb, tries), updated_at = clock_timestamp()
		WHERE id = $1 AND session_id = $2 AND run_id = $3 AND status = 'generating'`,
		data.ImageID, data.SessionID, *event.JobID, data.Status, data.Key, nilText(review), data.Error, data.Attempts,
		data.FirstPass, tries)
	if err != nil || tag.RowsAffected() == 0 {
		return nil, err
	}
	return map[string]string{"image_id": data.ImageID}, nil
}

// applyRun marks the run running and stores the progress line; progress of a run already ended (stopped) is dropped.
func applyRun(ctx context.Context, tx pgx.Tx, event *gen.Message[message.RunProgress], _ json.RawMessage) (any, error) {
	data := event.Data
	tag, err := tx.Exec(ctx, `UPDATE catalog_runs SET status = 'running'
		WHERE id = $1 AND session_id = $2 AND status IN ('queued', 'running')`, *event.JobID, data.SessionID)
	if err != nil || tag.RowsAffected() == 0 {
		return nil, err
	}
	return map[string]any{"run_id": *event.JobID, "sku_id": data.SkuID, "sku_status": data.SkuStatus,
		"images_done": data.ImagesDone, "images_total": data.ImagesTotal, "line": data.Line}, nil
}

// nilText is raw JSON as text, or SQL NULL.
func nilText(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

// eventDTO turns a stored event into its SessionEvent data: rows named by id are read from st and mapped as the
// other routes map them (signed links); status, log and step pass as published.
func (s *Service) eventData(st *state, typ string, stored json.RawMessage) any {
	var ids struct {
		QuestionID string   `json:"question_id"`
		SkuIDs     []string `json:"sku_ids"`
		ImageID    string   `json:"image_id"`
		RunID      string   `json:"run_id"`
	}
	_ = json.Unmarshal(stored, &ids)
	switch typ {
	case "question":
		for _, question := range st.questions {
			if question.ID == ids.QuestionID {
				return questionDTO(st, question)
			}
		}
	case "skus":
		var counts map[string]any
		_ = json.Unmarshal(stored, &counts)
		return map[string]any{"skus": s.skusDTO(st, nonNil(ids.SkuIDs)), "found": counts["found"], "expected": counts["expected"]}
	case "image":
		if image := st.image(ids.ImageID); image != nil {
			return s.imageDTO(*image)
		}
	case "run":
		var progress map[string]any
		_ = json.Unmarshal(stored, &progress)
		for _, run := range st.runs {
			if run.ID == ids.RunID {
				out := map[string]any{}
				body, _ := json.Marshal(runDTO(run))
				_ = json.Unmarshal(body, &out)
				for _, key := range []string{"sku_id", "sku_status", "images_done", "images_total", "line"} {
					out[key] = progress[key]
				}
				return out
			}
		}
	default:
		return stored
	}
	return map[string]any{} // the row is gone (merged SKU, reset): nothing left to show
}
