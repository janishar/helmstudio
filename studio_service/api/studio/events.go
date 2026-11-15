package studio

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
)

// PollEvery is how often an open event stream looks for new events; KeepAlive how often it pings. Variables so
// tests can shorten them. ponytail: polling; LISTEN/NOTIFY from the projector if a second of latency matters.
var (
	PollEvery = time.Second
	KeepAlive = 15 * time.Second
)

// Events returns the session's stored events with seq > since (at most 500), oldest first; an output event's key
// gets its signed url, and one whose output is gone is left out. With nothing new it reads only the session's event_seq, so an idle stream's poll stays cheap.
func (s *Service) Events(ctx context.Context, caller common.Caller, sessionID string, since int64) (dto.EventPage, error) {
	out := dto.EventPage{Events: []dto.SessionEvent{}, LastSeq: since}
	if !utility.IsUUID(sessionID) {
		return out, errNotFound
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var last int64
		if err := tx.QueryRow(ctx, `SELECT event_seq FROM sessions WHERE id = $1`, sessionID).Scan(&last); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil || last <= since {
			return err
		}
		events, err := collect[model.Event](ctx, tx, `SELECT seq, type, run_id::text, ts, data FROM run_events
			WHERE session_id = $1 AND seq > $2 ORDER BY seq LIMIT 500`, sessionID, since)
		if err != nil {
			return err
		}
		live, err := liveKeys(ctx, tx, events)
		for _, event := range events {
			out.LastSeq = event.Seq
			if mapped, ok := s.eventDTO(event, live); ok {
				out.Events = append(out.Events, mapped)
			}
		}
		return err
	})
	return out, err
}

// liveKeys returns the keys of the output events' outputs that still have a run output row: a deleted take or a
// retried attempt has none.
func liveKeys(ctx context.Context, tx pgx.Tx, events []model.Event) (map[string]bool, error) {
	var runs, keys []string
	for _, event := range events {
		var data struct{ Key string }
		if event.Type == "output" && json.Unmarshal(event.Data, &data) == nil {
			runs, keys = append(runs, event.RunID), append(keys, data.Key)
		}
	}
	live := map[string]bool{}
	if len(keys) == 0 {
		return live, nil
	}
	found, err := collect[struct{ Key string }](ctx, tx, `SELECT key FROM run_outputs WHERE run_id = ANY($1::uuid[]) AND key = ANY($2)`,
		runs, keys)
	for _, row := range found {
		live[row.Key] = true
	}
	return live, err
}

// eventDTO maps one stored event; an output's key (and a video's poster key) is signed now, so the link is fresh. An
// output that is gone (not in live) is left out: false.
func (s *Service) eventDTO(event model.Event, live map[string]bool) (dto.SessionEvent, bool) {
	var data map[string]any
	_ = json.Unmarshal(event.Data, &data)
	if event.Type == "output" {
		key, _ := data["key"].(string)
		if !live[key] {
			return dto.SessionEvent{}, false
		}
		data["url"] = s.signer.Sign(key)
		if video, ok := data["video"]; ok {
			raw, _ := json.Marshal(video)
			data["video"] = gallerydto.Info(raw, s.signer.Sign)
		}
	}
	return dto.SessionEvent{Seq: event.Seq, Type: event.Type, RunID: event.RunID, TS: event.TS, Data: data}, true
}
