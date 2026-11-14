package studio

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/orago/utility"

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
// gets its signed url. With nothing new it reads only the session's event_seq, so an idle stream's poll stays cheap.
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
		for _, event := range events {
			out.Events = append(out.Events, s.eventDTO(event))
			out.LastSeq = event.Seq
		}
		return err
	})
	return out, err
}

// eventDTO maps one stored event; an output's key is signed now, so the link is fresh.
func (s *Service) eventDTO(event model.Event) dto.SessionEvent {
	var data map[string]any
	_ = json.Unmarshal(event.Data, &data)
	if key, ok := data["key"].(string); ok && event.Type == "output" {
		data["url"] = s.signer.Sign(key)
	}
	return dto.SessionEvent{Seq: event.Seq, Type: event.Type, RunID: event.RunID, TS: event.TS, Data: data}
}
