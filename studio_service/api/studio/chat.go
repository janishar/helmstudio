package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// The director's chat (contracts/README.md "The director and its chat", API 1.15): studio keeps the messages
// (batch_chat, row-level security on owner_id); a user's message is stored and cmd.pipeline.chat written in one
// transaction; the director's replies and notices come back as evt.pipeline.chat. New messages reach the browser on
// the batch event stream as chat events.

const (
	// MaxChatMessages is how many messages GET /chat returns (the newest) and how many user messages one batch's chat
	// takes; the director's replies and notices never count against the user's (bounded by max_wakes instead).
	MaxChatMessages = 500
	// ChatPerMinute is how many user messages a batch takes in a minute.
	ChatPerMinute = 6
)

// chatRow is a batch_chat row.
type chatRow struct {
	ID        string
	Role      string
	Text      string
	At        time.Time
	Wake      *int
	ToolCalls json.RawMessage
	Rows      []int
}

const chatCols = `id::text, role, text, at, wake, tool_calls, rows`

func (r chatRow) dto() dto.ChatMessage {
	out := dto.ChatMessage{ID: r.ID, Role: r.Role, Text: r.Text, At: r.At, Wake: r.Wake, ToolCalls: []json.RawMessage{},
		Rows: r.Rows}
	_ = json.Unmarshal(r.ToolCalls, &out.ToolCalls)
	if out.Rows == nil {
		out.Rows = []int{}
	}
	return out
}

// chatBatch reads the caller's batch with a director (lock: FOR UPDATE); another user's, or one without a director, is
// errNotFound.
func chatBatch(ctx context.Context, tx pgx.Tx, batchID string, lock bool) (dto.Batch, dto.BatchDirector, error) {
	if !utility.IsUUID(batchID) {
		return dto.Batch{}, dto.BatchDirector{}, errNotFound
	}
	batch, err := getBatch(ctx, tx, batchID, lock)
	if err != nil {
		return batch, dto.BatchDirector{}, err
	}
	var director dto.BatchDirector
	if json.Unmarshal(batch.Director, &director) != nil || director.Node == "" {
		return batch, director, errNotFound
	}
	return batch, director, nil
}

// Chat answers GET /batches/{id}/chat: the director's state and the newest MaxChatMessages messages, oldest first.
func (s *Service) Chat(ctx context.Context, caller common.Caller, batchID string) (dto.ChatPage, error) {
	out := dto.ChatPage{BatchID: batchID, Messages: []dto.ChatMessage{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, director, err := chatBatch(ctx, tx, batchID, false)
		if err != nil {
			return err
		}
		out.Director = director
		rows, err := collect[chatRow](ctx, tx, `SELECT * FROM (SELECT `+chatCols+` FROM batch_chat WHERE batch_id = $1
			ORDER BY at DESC, id DESC LIMIT $2) newest ORDER BY at, id`,
			batchID, MaxChatMessages)
		for _, row := range rows {
			out.Messages = append(out.Messages, row.dto())
		}
		return err
	})
	return out, err
}

// PostChat answers POST /batches/{id}/chat: the message stored and cmd.pipeline.chat written in one transaction (202);
// 409 when the batch ended, the director is off or the chat is full; 429 above ChatPerMinute a minute.
func (s *Service) PostChat(ctx context.Context, caller common.Caller, batchID string, body *dto.ChatPost) (dto.ChatMessage, error) {
	var out dto.ChatMessage
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		batch, director, err := chatBatch(ctx, tx, batchID, true)
		if err != nil {
			return err
		}
		switch {
		case ended(batch.Status) || batch.Control == "stop":
			return network.NewConflictError("the director is off: the batch has ended", nil)
		case director.State == "off":
			return network.NewConflictError(fmt.Sprintf("the director is off: it has woken %d times, its most", director.MaxWakes), nil)
		}
		var total, recent int
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE at > clock_timestamp() - interval '1 minute')
			FROM batch_chat WHERE batch_id = $1 AND role = 'user'`, batchID).Scan(&total, &recent); err != nil {
			return err
		}
		switch {
		case total >= MaxChatMessages:
			return network.NewConflictError("this chat is full", nil)
		case recent >= ChatPerMinute:
			return network.NewAPIError(http.StatusTooManyRequests, fmt.Sprintf("at most %d messages a minute", ChatPerMinute), nil)
		}
		row, err := one[chatRow](ctx, tx, `INSERT INTO batch_chat (id, batch_id, owner_id, role, text) VALUES ($1, $2, $3, 'user', $4)
			RETURNING `+chatCols, utility.NewID(), batchID, caller.ID, body.Text)
		if err != nil {
			return err
		}
		if err := s.command(ctx, tx, batchID, caller.ID, message.Chat{BatchID: batchID, MessageID: row.ID, Text: row.Text,
			At: row.At.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
		out = row.dto()
		return appendChatEvent(ctx, tx, batchID, row.ID)
	})
	return out, err
}

// appendChatEvent adds a chat event (the message) under the batch's next seq.
func appendChatEvent(ctx context.Context, tx pgx.Tx, batchID, messageID string) error {
	_, err := tx.Exec(ctx, `WITH next AS (UPDATE batches SET event_seq = event_seq + 1 WHERE id = $1 RETURNING owner_id, event_seq)
		INSERT INTO batch_events (batch_id, seq, owner_id, type, message_id) SELECT $1, event_seq, owner_id, 'chat', $2 FROM next`,
		batchID, messageID)
	return err
}

// chatMessages reads the messages ids name, by id.
func chatMessages(ctx context.Context, tx pgx.Tx, ids []string) (map[string]dto.ChatMessage, error) {
	out := map[string]dto.ChatMessage{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := collect[chatRow](ctx, tx, `SELECT `+chatCols+` FROM batch_chat WHERE id = ANY($1::uuid[])`, ids)
	for _, row := range rows {
		out[row.ID] = row.dto()
	}
	return out, err
}

// applyPipelineChat stores one message of the director (a republish of its id replaces it when newer) and appends a
// chat event; the rows it names are kept only when they are the batch's. It returns changed false: the chat event is
// its own, no batch or row event follows.
func applyPipelineChat(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineChat) (int, bool, error) {
	m := data.Message
	at, err := time.Parse(time.RFC3339Nano, m.At)
	if (m.Role != "director" && m.Role != "notice") || !utility.IsUUID(m.ID) || utf8.RuneCountInString(m.Text) > 4000 ||
		m.Wake < 1 || len(m.ToolCalls) > 64 || len(m.Rows) > 64 || err != nil {
		log.Printf("batch projector: batch %s: dropping a chat message that is not the contract's", batchID)
		return 0, false, nil
	}
	if m.ToolCalls == nil {
		m.ToolCalls = []json.RawMessage{}
	}
	rows := slices.DeleteFunc(slices.Clone(m.Rows), func(n int) bool { return n < 1 || n > MaxRows })
	tag, err := tx.Exec(ctx, `INSERT INTO batch_chat (id, batch_id, owner_id, role, text, wake, tool_calls, rows, seq, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, coalesce((SELECT array_agg(n ORDER BY n) FROM batch_rows WHERE batch_id = $2
			AND n = ANY($8::int[])), '{}'), $9, $10)
		ON CONFLICT (id) DO UPDATE SET text = excluded.text, wake = excluded.wake, tool_calls = excluded.tool_calls,
		rows = excluded.rows, seq = excluded.seq WHERE batch_chat.seq < excluded.seq AND batch_chat.batch_id = excluded.batch_id
		AND batch_chat.role <> 'user'`,
		m.ID, batchID, ownerID, m.Role, m.Text, m.Wake, toJSON(m.ToolCalls), rows, data.Seq, at)
	if err != nil || tag.RowsAffected() == 0 {
		return 0, false, err
	}
	return 0, false, appendChatEvent(ctx, tx, batchID, m.ID)
}
