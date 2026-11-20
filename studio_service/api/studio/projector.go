package studio

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/gallery"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// Projector applies evt.run.<host>.<session_id>.<kind> to the runs and run_outputs tables and appends each applied event to
// the session's log, as System.
type Projector struct {
	pool *pgxpool.Pool
	// Workflows names a take's workflow and model (made_with); nil leaves them as the run's ids.
	Workflows *registry.Registry
}

// NewProjector returns the projector writing through pool.
func NewProjector(pool *pgxpool.Pool) *Projector {
	return &Projector{pool: pool}
}

// MountNatsJetStream registers one handler per evt.run.* kind.
func (p *Projector) MountNatsJetStream(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.run.*.*.status", handler(p, "status", applyStatus))
	group.AddHandler("evt.run.*.*.progress", handler(p, "progress", applyProgress))
	group.AddHandler("evt.run.*.*.output", handler(p, "output", p.applyOutput))
}

// based is an event's data: every one has a seq.
type based interface{ Common() message.Base }

// applyFunc projects one event onto its run (locked, open or not) and returns what run_events stores for it; nil
// stores nothing (an event the run's state no longer takes).
type applyFunc[D based] func(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[D]) (any, error)

// handler decodes the envelope (an undecodable one is dropped), projects it and acks; a database failure naks it.
func handler[D based](p *Projector, typ string, apply applyFunc[D]) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		host, session := routeOf(msg.Subject())
		if err := project(ctx, p.pool, typ, host, session, event, apply); err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// routeOf is the <host> and <session_id> of evt.run.<host>.<session_id>.<kind> ("" for another shape). The host's
// NATS user may publish only evt.run.<its host>.>, so the subject names the publisher (contracts/README.md).
func routeOf(subject string) (string, string) {
	parts := strings.Split(subject, ".")
	if len(parts) != 5 {
		return "", ""
	}
	return parts[2], parts[3]
}

// project applies the event once per (job_id, attempt, seq) when its run belongs to the envelope's owner and the
// subject's session, the subject's host is the one the run's current attempt was released to and the event is of
// that attempt, then appends it to the session's log. Anything else is logged and skipped (acked). A terminal status
// frees the run's host for the dispatcher's next pass.
func project[D based](ctx context.Context, pool *pgxpool.Pool, typ, host, sessionID string, event *gen.Message[D], apply applyFunc[D]) error {
	seq := event.Data.Common().Seq
	if event.JobID == nil || !utility.IsUUID(*event.JobID) || seq < 1 {
		log.Printf("projector: dropping %s without a job_id or seq", event.Type)
		return nil
	}
	return common.System(ctx, pool, func(tx pgx.Tx) error {
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE id = $1 FOR UPDATE`, *event.JobID)
		if errors.Is(err, errNotFound) || (err == nil && (run.OwnerID != event.OwnerID || run.SessionID != sessionID)) {
			log.Printf("projector: %s for unknown run %s (owner %s, session %s)", event.Type, *event.JobID, event.OwnerID, sessionID)
			return nil
		} else if err != nil {
			return err
		}
		if run.Host == nil || *run.Host != host { // another host (a lost attempt's, or forging): never its runs
			log.Printf("projector: dropping %s of run %s from host %q (released to %s)", event.Type, run.ID, host, deref(run.Host))
			return nil
		}
		attempt := event.Data.Common().Try()
		if attempt != run.Attempt { // a lost attempt's runner still talking: the run has moved on
			log.Printf("projector: dropping %s of run %s attempt %d (now %d)", event.Type, run.ID, attempt, run.Attempt)
			return nil
		}
		if first, err := messaging.FirstSeen(ctx, tx, run.ID, attempt, seq); err != nil || !first {
			return err
		}
		stored, err := apply(ctx, tx, run, event)
		if err != nil || stored == nil {
			return err
		}
		return appendEvent(ctx, tx, run, typ, eventTime(event.OccurredAt), stored)
	})
}

// applyStatus moves the run on when canMove allows it: running sets started_at, a terminal status the error and
// finished_at. The host stays the one written at release (data.host is informational). Anything else (a repeat,
// anything after the end) is ignored.
func applyStatus(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Status]) (any, error) {
	data := event.Data
	if !canMove(run.Status, data.Status) {
		return nil, nil
	}
	if data.Status != "failed" {
		data.Error = nil
	} else if data.Error == nil || *data.Error == "" {
		reason := "the run failed"
		data.Error = &reason
	}
	host := run.Host
	if data.Status != "succeeded" || len(data.Resolved) == 0 {
		data.Resolved = nil
	}
	var answer message.Answer // a text workflow's answer, kept when it has a try (succeeded, or failed on the schema)
	if (data.Status != "succeeded" && data.Status != "failed") || json.Unmarshal(data.Answer, &answer) != nil || len(answer.Tries) == 0 {
		data.Answer = nil
	}
	_, err := tx.Exec(ctx, `UPDATE runs SET status = $2, error = $3, host = $4, updated_at = clock_timestamp(),
		started_at = CASE WHEN $2 = 'running' THEN clock_timestamp() ELSE started_at END,
		finished_at = CASE WHEN $2 = 'running' THEN NULL ELSE clock_timestamp() END,
		resolved = coalesce($5::jsonb, resolved), answer = coalesce($6::jsonb, answer)
		WHERE id = $1`, run.ID, data.Status, data.Error, host, data.Resolved, data.Answer)
	if err != nil {
		return nil, err
	}
	run.Status, run.Error = data.Status, data.Error
	if data.Answer != nil {
		run.Answer = data.Answer
	}
	if terminal[data.Status] {
		err = runEnded(ctx, tx, run)
	} else {
		err = runMoved(ctx, tx, run)
	}
	stored := statusData(data.Status, data.Error, host, run.Attempt)
	if data.Resolved != nil {
		stored["resolved"] = data.Resolved
	}
	return stored, err
}

// applyProgress stores the run's last step; a run already ended takes none.
func applyProgress(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Progress]) (any, error) {
	if terminal[run.Status] {
		return nil, nil
	}
	data := event.Data
	progress := map[string]any{"node": data.Node, "step": data.Step, "of": data.Of}
	if data.Phase != nil {
		progress["phase"] = *data.Phase
	}
	if data.EtaS != nil {
		progress["eta_s"] = *data.EtaS
	}
	_, err := tx.Exec(ctx, `UPDATE runs SET progress = $2, updated_at = clock_timestamp() WHERE id = $1`, run.ID, progress)
	return progress, err
}

// applyOutput records an output stored under the current attempt's prefix (OutputPrefix); a key outside it, a video without its metadata or
// with a poster outside it, an audio output that is not a .wav with its facts (API 1.30), or a run already ended, is
// dropped. Every output is a take, stored in the same transaction
// (gallery.Record). The stored event keeps the keys: they are signed when read.
func (p *Projector) applyOutput(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Output]) (any, error) {
	data := event.Data
	prefix := OutputPrefix(run)
	if terminal[run.Status] || !common.Owns(run.OwnerID, data.Key) || !strings.HasPrefix(data.Key, prefix) ||
		(data.Kind != "image" && data.Kind != "video" && data.Kind != "audio") || data.Index < 0 || badVideo(data, prefix) ||
		badAudio(data) {
		log.Printf("projector: dropping output %d of run %s (%s)", data.Index, run.ID, data.Key)
		return nil, nil
	}
	made, err := withPipeline(ctx, tx, run, madeWith(p.Workflows, run, data.Seed, outputParams(data)))
	if err != nil {
		return nil, err
	}
	lineage := parents(typedView(run.Nodes, run.Mentions))
	if sm := data.SourceMap; sm != nil && (data.Kind != "video" || !slices.Contains(lineage, sm.TakeID)) {
		data.SourceMap = nil // only a video, and only from one of the run's own media takes
	}
	takeID, err := gallery.Record(ctx, tx, gallery.Output{OwnerID: run.OwnerID, SessionID: run.SessionID, RunID: run.ID,
		Index: data.Index, Key: data.Key, Kind: data.Kind, Width: data.Width, Height: data.Height, Bytes: data.Bytes,
		Etag: data.Etag, MadeWith: made, BatchID: run.BatchID, Parents: lineage, Video: data.Video, SourceMap: data.SourceMap,
		Audio: data.Audio})
	if err == nil && data.Index == 0 {
		err = fillShot(ctx, tx, run, takeID) // API 1.17: the run's first take fills its slot
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO run_outputs (run_id, owner_id, idx, key, kind, width, height, bytes, seed, take_id, video,
		audio) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) ON CONFLICT (run_id, idx) DO NOTHING`,
		run.ID, run.OwnerID, data.Index, data.Key, data.Kind, data.Width, data.Height, data.Bytes, data.Seed, takeID, data.Video,
		data.Audio)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET updated_at = clock_timestamp() WHERE id = $1`, run.ID); err != nil {
		return nil, err
	}
	stored := map[string]any{"index": data.Index, "key": data.Key, "kind": data.Kind, "width": data.Width, "height": data.Height,
		"take_id": takeID}
	if data.Seed != nil {
		stored["seed"] = *data.Seed
	}
	if data.Video != nil {
		stored["video"] = data.Video
	}
	if data.Audio != nil {
		stored["audio"] = data.Audio
	}
	return stored, nil
}

// outputParams is an output's resolved.params, nil without.
func outputParams(data message.Output) map[string]int64 {
	if data.Resolved == nil {
		return nil
	}
	return data.Resolved.Params
}

// resolvedParams is runs.resolved's params (null or absent: nil).
func resolvedParams(raw []byte) map[string]int64 {
	var resolved message.Resolved
	_ = json.Unmarshal(raw, &resolved)
	return resolved.Params
}

// badVideo reports whether an output's video part breaks evt.run.output: a video needs its MP4 key, size and metadata
// with a poster under the run's prefix; an image has no video part.
func badVideo(data message.Output, prefix string) bool {
	video := data.Video
	if data.Kind != "video" {
		return video != nil
	}
	if video != nil && video.Last != nil && (!strings.HasPrefix(video.Last.Key, prefix) || // API 1.17: the last frame
		!strings.HasSuffix(video.Last.Key, ".last.png") || strings.Contains(video.Last.Key, "..") || strings.Contains(video.Last.Key, "//")) {
		return true
	}
	return video == nil || !strings.HasSuffix(data.Key, ".mp4") || data.Width == nil || data.Height == nil ||
		video.Frames < 1 || video.FPS <= 0 || video.DurationS <= 0 || !strings.HasPrefix(video.Poster.Key, prefix) ||
		!strings.HasSuffix(video.Poster.Key, ".poster.png") || strings.Contains(video.Poster.Key, "..") ||
		strings.Contains(video.Poster.Key, "//")
}

// badAudio reports whether an output's audio part breaks evt.run.output (API 1.30): an audio output is a .wav with
// its facts and no size; anything else has no audio part.
func badAudio(data message.Output) bool {
	a := data.Audio
	if data.Kind != "audio" {
		return a != nil
	}
	return a == nil || !strings.HasSuffix(data.Key, ".wav") || data.Width != nil || data.Height != nil || a.DurationS <= 0 ||
		a.DurationS > 600 || a.SampleRate == nil || *a.SampleRate < 8000 || *a.SampleRate > 96000
}

// eventTime is when a stored event happened; the zero time (a malformed envelope) is now.
func eventTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
