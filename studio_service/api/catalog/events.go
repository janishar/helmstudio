package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/catalog/dto"
	"github.com/helmstudio/studio_service/api/catalog/model"
	"github.com/helmstudio/studio_service/common"
)

// Events returns the session's stored events with seq > since (at most 500), oldest first, each mapped to the
// contract's SessionEvent: question, skus, image and run events carry the rows as they are now. With nothing new it
// reads only the session's event_seq, so an idle stream's poll stays cheap.
func (s *Service) Events(ctx context.Context, caller common.Caller, sessionID string, since int64) (dto.EventPage, error) {
	out := dto.EventPage{Events: []dto.SessionEvent{}, LastSeq: since}
	if !utility.IsUUID(sessionID) {
		return out, errNotFound
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var last int64
		if err := tx.QueryRow(ctx, `SELECT event_seq FROM catalog_sessions WHERE id = $1`, sessionID).Scan(&last); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil || last <= since {
			return err
		}
		st, err := loadState(ctx, tx, sessionID, false)
		if err != nil {
			return err
		}
		events, err := collect[model.Event](ctx, tx, `SELECT seq, type, work_id::text, ts, data FROM catalog_events
			WHERE session_id = $1 AND seq > $2 ORDER BY seq LIMIT 500`, st.session.ID, since)
		for _, event := range events {
			out.Events = append(out.Events, s.eventDTO(st, event))
			out.LastSeq = event.Seq
		}
		return err
	})
	return out, err
}

// eventDTO maps one stored event: rows it names are read from st as they are now (eventData).
func (s *Service) eventDTO(st *state, event model.Event) dto.SessionEvent {
	return dto.SessionEvent{Seq: event.Seq, Type: event.Type, WorkID: event.WorkID, TS: event.TS,
		Data: s.eventData(st, event.Type, event.Data)}
}

// PollEvery is how often an open event stream looks for new events; KeepAlive how often it pings. Variables so
// tests can shorten them. ponytail: polling; LISTEN/NOTIFY from the projector if a second of latency matters.
var (
	PollEvery = time.Second
	KeepAlive = 15 * time.Second
)
