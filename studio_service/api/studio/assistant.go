package studio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/messaging"
)

// The in-app assistant (contracts/README.md "Assistant", API 1.44; plan 18, 22.3). Studio keeps the chats (threads),
// their entries and events, roles, caps and the admin's settings; pipelines runs the agent. One input = one
// command: an input is stored and its cmd.assistant.turn written in one transaction (owner from the thread row, roles
// from the caller's token); each evt.assistant.step is stored as entries and sets the thread's state.

const (
	// MaxAssistantThreads is how many chats a user keeps.
	MaxAssistantThreads = 200
	maxAssistantEntries = 200
	maxAssistantEvents  = 500
	maxScreenJSON       = 16000
)

var (
	clientIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	assistantCallID   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[1-9][0-9]{0,5}:([0-9]|[1-9][0-9])$`)
	screenRoute       = regexp.MustCompile(`^/[^\s]{0,199}$`)
	screenIDKey       = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)
	roleNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	actionNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,47}$`)
	providerNameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	providerModelRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
)

// ------------------------------------------------------------------ settings

// AssistantLimits is assistant.schema.json limits: the caps per user.
type AssistantLimits struct {
	ModelCallsPerDay  *int `json:"model_calls_per_day"`
	TokensPerDay      *int `json:"tokens_per_day"`
	LookAtPerHour     *int `json:"look_at_per_hour"`
	MessagesPerMinute *int `json:"messages_per_minute"`
}

func intp(n int) *int { return &n }

// defaultLimits are the caps before the admin sets any.
func defaultLimits() AssistantLimits {
	return AssistantLimits{intp(2000), intp(20000000), intp(200), intp(30)}
}

// check is the 422 detail of limits out of range.
func (l AssistantLimits) check() string {
	in := func(p *int, lo, hi int) bool { return p != nil && *p >= lo && *p <= hi }
	switch {
	case !in(l.ModelCallsPerDay, 1, 100000):
		return "limits.model_calls_per_day must be 1 to 100000"
	case !in(l.TokensPerDay, 1000, 100000000):
		return "limits.tokens_per_day must be 1000 to 100000000"
	case !in(l.LookAtPerHour, 0, 1000):
		return "limits.look_at_per_hour must be 0 to 1000"
	case !in(l.MessagesPerMinute, 1, 60):
		return "limits.messages_per_minute must be 1 to 60"
	}
	return ""
}

// AssistantSettings is GET/PUT /admin/assistant's answer.
type AssistantSettings struct {
	Enabled        bool            `json:"enabled"`
	Provider       *string         `json:"provider"`
	Model          *string         `json:"model"`
	WebSearch      bool            `json:"web_search"`
	SearchProvider *string         `json:"search_provider"`
	Limits         AssistantLimits `json:"limits"`
	RemoteEnabled  bool            `json:"remote_enabled"`
	UpdatedAt      *time.Time      `json:"updated_at"`
}

// AssistantSettingsPut is PUT /admin/assistant's body.
type AssistantSettingsPut struct {
	Enabled        *bool            `json:"enabled"`
	Provider       *string          `json:"provider"`
	Model          *string          `json:"model"`
	WebSearch      *bool            `json:"web_search"`
	SearchProvider *string          `json:"search_provider"`
	Limits         *AssistantLimits `json:"limits"`
}

// Validate checks the shape; the providers are checked by the service.
func (b *AssistantSettingsPut) Validate() string {
	switch {
	case b.Enabled == nil:
		return "enabled is required"
	case b.Provider != nil && !providerNameRe.MatchString(*b.Provider):
		return "provider must be a provider's name"
	case b.Model != nil && !providerModelRe.MatchString(*b.Model):
		return "model must be a model name"
	case b.SearchProvider != nil && !providerNameRe.MatchString(*b.SearchProvider):
		return "search_provider must be a provider's name"
	case b.Limits != nil:
		return b.Limits.check()
	}
	return ""
}

// readAssistantSettings reads the settings row (off, with the default caps, before the first PUT).
func readAssistantSettings(ctx context.Context, tx pgx.Tx) (AssistantSettings, error) {
	out := AssistantSettings{Limits: defaultLimits()}
	err := tx.QueryRow(ctx, `SELECT enabled, provider, model, web_search, search_provider, limits, updated_at
		FROM assistant_settings`).Scan(&out.Enabled, &out.Provider, &out.Model, &out.WebSearch, &out.SearchProvider,
		&out.Limits, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}

// GetAssistantSettings is GET /admin/assistant.
func (s *Service) GetAssistantSettings(ctx context.Context, caller common.Caller) (AssistantSettings, error) {
	if !caller.IsAdmin() {
		return AssistantSettings{}, errAdmin
	}
	var out AssistantSettings
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		out, err = readAssistantSettings(ctx, tx)
		return err
	})
	out.RemoteEnabled = s.remoteOn
	return out, err
}

// PutAssistantSettings is PUT /admin/assistant: the provider an enabled server or remote one with that model, the
// search provider an enabled one of kind search; remote and web search need the deployment's switch (409).
func (s *Service) PutAssistantSettings(ctx context.Context, caller common.Caller, body *AssistantSettingsPut) (AssistantSettings, error) {
	if !caller.IsAdmin() {
		return AssistantSettings{}, errAdmin
	}
	unprocessable := func(format string, args ...any) error {
		return network.NewUnprocessableError(fmt.Sprintf(format, args...), nil)
	}
	off := network.NewConflictError("remote models are off on this deployment", nil)
	var out AssistantSettings
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		now, err := readAssistantSettings(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := readProviders(ctx, tx, true)
		if err != nil {
			return err
		}
		find := func(name *string) (providerRow, bool) {
			at := slices.IndexFunc(rows, func(p providerRow) bool { return name != nil && p.Name == *name })
			if at < 0 {
				return providerRow{}, false
			}
			return rows[at], true
		}
		webSearch := body.WebSearch != nil && *body.WebSearch
		switch p, ok := find(body.Provider); {
		case *body.Enabled && body.Provider == nil:
			return unprocessable("turning the assistant on needs a provider")
		case body.Provider != nil && (!ok || p.Kind == "search"):
			return unprocessable("provider %s is not an enabled server or remote provider", *body.Provider)
		case body.Model != nil && body.Provider == nil:
			return unprocessable("model needs a provider")
		case body.Model != nil && !slices.Contains(p.Models, *body.Model):
			return unprocessable("model %s is not one of %s's models", *body.Model, p.Name)
		case p.Kind == "remote" && !s.remoteOn:
			return off
		}
		switch q, ok := find(body.SearchProvider); {
		case body.SearchProvider != nil && (!ok || q.Kind != "search"):
			return unprocessable("search_provider %s is not an enabled provider of kind search", *body.SearchProvider)
		case webSearch && body.SearchProvider == nil:
			return unprocessable("web search needs a search_provider")
		case (webSearch || body.SearchProvider != nil) && !s.remoteOn:
			return off
		}
		limits := now.Limits
		if body.Limits != nil {
			limits = *body.Limits
		}
		return tx.QueryRow(ctx, `INSERT INTO assistant_settings (id, enabled, provider, model, web_search, search_provider, limits)
			VALUES (true, $1, $2, $3, $4, $5, $6) ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled,
			provider = excluded.provider, model = excluded.model, web_search = excluded.web_search,
			search_provider = excluded.search_provider, limits = excluded.limits, updated_at = clock_timestamp()
			RETURNING enabled, provider, model, web_search, search_provider, limits, updated_at`,
			*body.Enabled, body.Provider, body.Model, webSearch, body.SearchProvider, limits).Scan(&out.Enabled, &out.Provider,
			&out.Model, &out.WebSearch, &out.SearchProvider, &out.Limits, &out.UpdatedAt)
	})
	out.RemoteEnabled = s.remoteOn
	return out, err
}

// AssistantStatus is what every user may know: on or off, where the model runs, web search, the caps, whether it thinks.
type AssistantStatus struct {
	Enabled   bool               `json:"enabled"`
	Provider  *AssistantProvider `json:"provider"`
	WebSearch *string            `json:"web_search"`
	Limits    AssistantLimits    `json:"limits"`
	Thinks    bool               `json:"thinks"` // API 1.54 (plan 29): the model thinks (modelThinks)
}

// AssistantProvider names where the model runs.
type AssistantProvider struct {
	Title  string `json:"title"`
	Remote bool   `json:"remote"`
}

// assistantConfig is the settings as an input sees them: the status and the command's settings snapshot. The
// assistant is on only while its provider is still enabled with that model (a remote one under the switch); web
// search only while its provider is and the switch is on.
func (s *Service) assistantConfig(ctx context.Context, tx pgx.Tx) (AssistantStatus, message.AssistantSettings, error) {
	set, err := readAssistantSettings(ctx, tx)
	if err != nil {
		return AssistantStatus{}, message.AssistantSettings{}, err
	}
	rows, err := readProviders(ctx, tx, true)
	if err != nil {
		return AssistantStatus{}, message.AssistantSettings{}, err
	}
	status := AssistantStatus{Limits: set.Limits}
	snap := message.AssistantSettings{Providers: map[string]map[string]any{}, Limits: toJSON(set.Limits)}
	for _, p := range rows {
		model := cmp.Or(deref(set.Model), p.DefaultModel)
		if !set.Enabled || p.Name != deref(set.Provider) || !slices.Contains(p.Models, model) ||
			!(p.Kind == "server" || p.Kind == "remote" && s.remoteOn) {
			continue
		}
		status.Enabled = true
		status.Provider = &AssistantProvider{Title: cmp.Or(deref(p.Title), p.Name), Remote: p.Kind == "remote"}
		status.Thinks = modelThinks(p.Preset, model, p.Thinking)
		snap.Source = map[string]any{"kind": p.Kind, "provider": p.Name, "model": model}
		if p.Kind == "server" {
			snap.Source["host"] = deref(p.Host)
		}
		snap.Providers[p.Name] = p.snapshot()
	}
	for _, q := range rows {
		if status.Enabled && set.WebSearch && s.remoteOn && q.Kind == "search" && q.Name == deref(set.SearchProvider) {
			title := cmp.Or(deref(q.Title), q.Name)
			status.WebSearch, snap.Search = &title, &q.Name
			snap.Providers[q.Name] = q.snapshot()
		}
	}
	return status, snap, nil
}

// ------------------------------------------------------------------ threads, entries, events

// AssistantThread is the HTTP AssistantThread.
type AssistantThread struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	State     string          `json:"state"`
	ClientID  *string         `json:"client_id"`
	Pending   json.RawMessage `json:"pending"`
	Caps      json.RawMessage `json:"caps"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// AssistantEntry is one stored entry of a chat.
type AssistantEntry struct {
	ID    string          `json:"id"`
	Turn  int             `json:"turn"`
	Kind  string          `json:"kind"`
	At    time.Time       `json:"at"`
	Text  *string         `json:"text,omitempty"`
	Line  json.RawMessage `json:"line,omitempty"`
	Card  json.RawMessage `json:"card,omitempty"`
	Error json.RawMessage `json:"error,omitempty"`
	Steer *string         `json:"steer,omitempty"` // API 1.59: waiting, read or next (a message sent while a turn worked)
}

const entryCols = `id::text, turn, kind, at, text, line, card, error, steer`

// threadRow is an assistant_threads row.
type threadRow struct {
	ID        string
	OwnerID   string
	Title     string
	State     string
	ClientID  *string
	Pending   json.RawMessage
	Caps      json.RawMessage
	Roles     []string
	Actions   []string
	Turn      int
	StepSeq   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

const threadCols = `id::text, owner_id::text, title, state, client_id, pending, caps, roles, actions, turn, step_seq,
	created_at, updated_at`

func (t threadRow) dto() AssistantThread {
	pending := t.Pending
	if len(pending) == 0 {
		pending = json.RawMessage("null")
	}
	return AssistantThread{ID: t.ID, Title: t.Title, State: t.State, ClientID: t.ClientID, Pending: pending, Caps: t.Caps,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// pending is AssistantPending.
type pending struct {
	Turn    int               `json:"turn"`
	Actions []json.RawMessage `json:"actions"`
	Cards   []string          `json:"cards"`
}

// actionIDs are the ids of the pending actions.
func (p pending) actionIDs() []string {
	ids := []string{}
	for _, raw := range p.Actions {
		var a struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &a)
		ids = append(ids, a.ID)
	}
	return ids
}

// lockThread reads the caller's thread for update: unknown, malformed or another user's is 404 (row-level security).
func lockThread(ctx context.Context, tx pgx.Tx, id string) (threadRow, error) {
	if !utility.IsUUID(id) {
		return threadRow{}, errNotFound
	}
	return one[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads WHERE id = $1 FOR UPDATE`, id)
}

// putAssistantEvent adds one event to the chat's stream.
func putAssistantEvent(ctx context.Context, tx pgx.Tx, threadID, ownerID string, body map[string]any) error {
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE assistant_threads SET event_seq = event_seq + 1 WHERE id = $1 RETURNING event_seq`,
		threadID).Scan(&seq); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO assistant_events (thread_id, seq, owner_id, body) VALUES ($1, $2, $3, $4)`, threadID,
		seq, ownerID, toJSON(body))
	return err
}

// threadEvent sends the thread's state as it is now.
func threadEvent(ctx context.Context, tx pgx.Tx, id string) (threadRow, error) {
	t, err := one[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads WHERE id = $1`, id)
	if err == nil {
		err = putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "thread", "thread": t.dto()})
	}
	return t, err
}

// putEntry stores an entry under key (a line's or card's call id replaces the one before) and sends it.
func putEntry(ctx context.Context, tx pgx.Tx, t threadRow, key string, turn int, kind string, text *string, line, card, failure any) (AssistantEntry, error) {
	nullable := func(v any) any {
		if v == nil {
			return nil
		}
		return toJSON(v)
	}
	entry, err := one[AssistantEntry](ctx, tx, `INSERT INTO assistant_entries (id, thread_id, owner_id, key, turn, kind, text,
		line, card, error) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (thread_id, key) DO UPDATE SET
		text = excluded.text, line = excluded.line, card = excluded.card, error = excluded.error RETURNING `+entryCols,
		utility.NewID(), t.ID, t.OwnerID, key, turn, kind, text, nullable(line), nullable(card), nullable(failure))
	if err == nil {
		err = putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "entry", "entry": entry})
	}
	return entry, err
}

// AssistantThreadList is GET /assistant/threads.
type AssistantThreadList struct {
	Status  AssistantStatus   `json:"status"`
	Threads []AssistantThread `json:"threads"`
}

// ListAssistantThreads is GET /assistant/threads: the caller's chats, newest first, and the status.
func (s *Service) ListAssistantThreads(ctx context.Context, caller common.Caller) (AssistantThreadList, error) {
	out := AssistantThreadList{Threads: []AssistantThread{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var err error
		if out.Status, _, err = s.assistantConfig(ctx, tx); err != nil {
			return err
		}
		rows, err := collect[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads ORDER BY updated_at DESC, id DESC
			LIMIT $1`, MaxAssistantThreads)
		for _, t := range rows {
			out.Threads = append(out.Threads, t.dto())
		}
		return err
	})
	return out, err
}

// AssistantThreadCreate is POST /assistant/threads's body.
type AssistantThreadCreate struct {
	ClientID string `json:"client_id"`
}

// Validate checks client_id.
func (b *AssistantThreadCreate) Validate() string {
	if !clientIDPattern.MatchString(b.ClientID) {
		return "client_id must be 8-64 letters, digits, _ or -"
	}
	return ""
}

var errAssistantOff = network.NewConflictError("the assistant is off", nil)

// CreateAssistantThread is POST /assistant/threads (New chat): the caller's newest idle chat with a message not
// summarized since gets its summary asked for in the same transaction.
func (s *Service) CreateAssistantThread(ctx context.Context, caller common.Caller, body *AssistantThreadCreate) (AssistantThread, error) {
	var out AssistantThread
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		status, snap, err := s.assistantConfig(ctx, tx)
		if err != nil {
			return err
		} else if !status.Enabled {
			return errAssistantOff
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM assistant_threads`).Scan(&count); err != nil {
			return err
		} else if count >= MaxAssistantThreads {
			return network.NewConflictError(fmt.Sprintf("you have %d chats; delete one first", MaxAssistantThreads), nil)
		}
		previous, err := one[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads WHERE state = 'idle'
			AND last_message_at IS NOT NULL AND summarized_at IS NULL ORDER BY last_message_at DESC LIMIT 1 FOR UPDATE`)
		if err == nil {
			err = summarize(ctx, tx, previous, snap)
		}
		if err != nil && !errors.Is(err, errNotFound) {
			return err
		}
		t, err := one[threadRow](ctx, tx, `INSERT INTO assistant_threads (id, owner_id, client_id) VALUES ($1, $2, $3)
			RETURNING `+threadCols, utility.NewID(), caller.ID, body.ClientID)
		out = t.dto()
		return err
	})
	return out, err
}

// summarize asks for a finished chat's summary (cmd.assistant.turn input summarize; tx holds the thread).
func summarize(ctx context.Context, tx pgx.Tx, t threadRow, snap message.AssistantSettings) error {
	turn := t.Turn + 1
	if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET turn = $2, summarized_at = clock_timestamp() WHERE id = $1`,
		t.ID, turn); err != nil {
		return err
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: fmt.Sprintf("%s:%d", t.ID, turn),
		Data: message.AssistantTurn{ThreadID: t.ID, Turn: turn, ClientID: cmp.Or(deref(t.ClientID), "summarize"),
			Roles: t.Roles, Tools: []string{}, Actions: []string{}, Settings: snap, Input: map[string]any{"kind": "summarize"}}})
}

// SummarizeIdle asks for the summary of every chat whose last message is 24 h old (plan 18, 22.8), at most 50 a
// pass; nothing while the assistant is off.
func (s *Service) SummarizeIdle(ctx context.Context) (int, error) {
	done := 0
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		status, snap, err := s.assistantConfig(ctx, tx)
		if err != nil || !status.Enabled {
			return err
		}
		rows, err := collect[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads WHERE state = 'idle'
			AND summarized_at IS NULL AND last_message_at < clock_timestamp() - interval '24 hours'
			ORDER BY last_message_at LIMIT 50 FOR UPDATE SKIP LOCKED`)
		for _, t := range rows {
			if err == nil {
				err = summarize(ctx, tx, t, snap)
				done++
			}
		}
		return err
	})
	return done, err
}

// RunAssistantSweep runs SummarizeIdle every 10 minutes until ctx ends.
func (s *Service) RunAssistantSweep(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := s.SummarizeIdle(ctx); err != nil && ctx.Err() == nil {
				log.Printf("assistant sweep: %v", err)
			}
		}
	}
}

// AssistantThreadDetail is GET /assistant/threads/{id}.
type AssistantThreadDetail struct {
	AssistantThread
	Entries []AssistantEntry `json:"entries"`
}

// GetAssistantThread is GET /assistant/threads/{id}: the chat with its newest 200 entries, oldest first.
func (s *Service) GetAssistantThread(ctx context.Context, caller common.Caller, id string) (AssistantThreadDetail, error) {
	var out AssistantThreadDetail
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if !utility.IsUUID(id) {
			return errNotFound
		}
		t, err := one[threadRow](ctx, tx, `SELECT `+threadCols+` FROM assistant_threads WHERE id = $1`, id)
		if err != nil {
			return err
		}
		out.AssistantThread = t.dto()
		out.Entries, err = collect[AssistantEntry](ctx, tx, `SELECT * FROM (SELECT `+entryCols+` FROM assistant_entries
			WHERE thread_id = $1 ORDER BY at DESC, id DESC LIMIT $2) e ORDER BY at, id`, id, maxAssistantEntries)
		return err
	})
	return out, err
}

// DeleteAssistantThread is DELETE /assistant/threads/{id}: its rows go, and cmd.assistant.delete in the same
// transaction drops its checkpoints and summary in pipelines.
func (s *Service) DeleteAssistantThread(ctx context.Context, caller common.Caller, id string) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		t, err := lockThread(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM assistant_threads WHERE id = $1`, t.ID); err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: t.ID + ":delete",
			Data: message.AssistantDelete{ThreadID: t.ID}})
	})
}

// AssistantEventPage is GET …/events's JSON.
type AssistantEventPage struct {
	Events  []json.RawMessage `json:"events"`
	LastSeq int64             `json:"last_seq"`
}

// AssistantEvents is the chat's events after since (at most 500).
func (s *Service) AssistantEvents(ctx context.Context, caller common.Caller, id string, since int64) (AssistantEventPage, []sseEvent, error) {
	out := AssistantEventPage{Events: []json.RawMessage{}, LastSeq: since}
	var events []sseEvent
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if !utility.IsUUID(id) {
			return errNotFound
		}
		if _, err := one[struct{ ID string }](ctx, tx, `SELECT id::text FROM assistant_threads WHERE id = $1`, id); err != nil {
			return err
		}
		type row struct {
			Seq  int64
			Body map[string]any
		}
		rows, err := collect[row](ctx, tx, `SELECT seq, body FROM assistant_events WHERE thread_id = $1 AND seq > $2
			ORDER BY seq LIMIT $3`, id, since, maxAssistantEvents)
		for _, r := range rows {
			r.Body["seq"] = r.Seq
			out.Events = append(out.Events, toJSON(r.Body))
			events = append(events, sseEvent{r.Seq, fmt.Sprint(r.Body["type"]), r.Body})
			out.LastSeq = r.Seq
		}
		return err
	})
	return out, events, err
}

// ------------------------------------------------------------------ inputs

// AssistantScreen is assistant.schema.json screen, checked field by field (pipelines refuses an off-contract command).
type AssistantScreen struct {
	Route   *string           `json:"route"`
	IDs     map[string]string `json:"ids"`
	Roles   []string          `json:"roles"`
	AsOf    *time.Time        `json:"as_of"`
	Hidden  *bool             `json:"hidden"`
	Actions []string          `json:"actions"`
	Fields  map[string]any    `json:"fields"`
}

// check is the 422 detail.
func (sc *AssistantScreen) check() string {
	switch {
	case sc.Route == nil || !screenRoute.MatchString(*sc.Route):
		return "screen.route must be a path"
	case sc.IDs == nil || len(sc.IDs) > 16:
		return "screen.ids: at most 16 ids"
	case sc.Roles == nil || len(sc.Roles) > 8:
		return "screen.roles: at most 8 roles"
	case sc.AsOf == nil || sc.Hidden == nil || sc.Fields == nil:
		return "screen needs as_of, hidden and fields"
	case sc.Actions == nil || len(sc.Actions) > 64 || len(slices.Compact(slices.Sorted(slices.Values(sc.Actions)))) < len(sc.Actions):
		return "screen.actions: at most 64 actions, none twice"
	}
	for key, value := range sc.IDs {
		if !screenIDKey.MatchString(key) || utf8.RuneCountInString(value) > 80 {
			return "screen.ids." + key + ": a name like session_id and at most 80 characters"
		}
	}
	for _, role := range sc.Roles {
		if !roleNamePattern.MatchString(role) {
			return "screen.roles: each like admin"
		}
	}
	for _, name := range sc.Actions {
		if !actionNamePattern.MatchString(name) {
			return "screen.actions: each like navigate"
		}
	}
	return ""
}

// AssistantResult is assistant.schema.json result: what one action did in the tab.
type AssistantResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Line   *struct {
		Text string `json:"text"`
		Undo *bool  `json:"undo"`
	} `json:"line"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *string         `json:"error,omitempty"`
}

// resultLine maps a result's status to its line's.
var resultLine = map[string]string{"done": "done", "failed": "failed", "did_not_run": "did_not_run", "cancelled": "cancelled",
	"still_running": "running"}

// AssistantPost is POST …/messages's body: a message, results, an answer or continue.
type AssistantPost struct {
	Kind     string            `json:"kind"`
	ClientID string            `json:"client_id"`
	Text     *string           `json:"text"`
	Screen   json.RawMessage   `json:"screen"`
	Turn     *int              `json:"turn"`
	Results  []AssistantResult `json:"results"`
	CardID   *string           `json:"card_id"`
	Answer   *string           `json:"answer"`
	screen   *AssistantScreen
}

// Validate checks the body of its kind (every other field absent).
func (b *AssistantPost) Validate() string {
	has := map[string]bool{"text": b.Text != nil, "screen": b.Screen != nil, "turn": b.Turn != nil, "results": b.Results != nil,
		"card_id": b.CardID != nil, "answer": b.Answer != nil}
	allowed, known := map[string][]string{"message": {"text", "screen"}, "results": {"turn", "results", "screen"},
		"answer": {"card_id", "answer"}, "continue": {}, "stop": {}}[b.Kind]
	if !known {
		return "kind must be message, results, answer, continue or stop"
	}
	if !clientIDPattern.MatchString(b.ClientID) {
		return "client_id must be 8-64 letters, digits, _ or -"
	}
	for field, set := range has {
		if set && !slices.Contains(allowed, field) {
			return "unknown field " + field
		}
	}
	if b.Screen != nil {
		if len(b.Screen) > maxScreenJSON {
			return fmt.Sprintf("screen: at most %d characters", maxScreenJSON)
		}
		b.screen = new(AssistantScreen)
		decoder := json.NewDecoder(bytes.NewReader(b.Screen))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(b.screen); err != nil {
			return "screen is not a screen summary"
		} else if detail := b.screen.check(); detail != "" {
			return detail
		}
	}
	switch b.Kind {
	case "message":
		if b.Text != nil {
			*b.Text = strings.TrimSpace(*b.Text)
		}
		switch {
		case b.Text == nil || *b.Text == "" || utf8.RuneCountInString(*b.Text) > 4000:
			return "text must be 1-4000 characters"
		case b.screen == nil:
			return "screen is required"
		}
	case "results":
		if b.Turn == nil || *b.Turn < 1 || len(b.Results) < 1 || len(b.Results) > 30 {
			return "turn and 1 to 30 results are required"
		}
		for _, r := range b.Results {
			switch {
			case !assistantCallID.MatchString(r.ID):
				return "results: each id is a call id"
			case resultLine[r.Status] == "":
				return "results: status must be done, failed, did_not_run, cancelled or still_running"
			case r.Line == nil || r.Line.Undo == nil || r.Line.Text == "" || utf8.RuneCountInString(r.Line.Text) > 300:
				return "results: line needs text (1-300 characters) and undo"
			case r.Error != nil && utf8.RuneCountInString(*r.Error) > 500:
				return "results: error is at most 500 characters"
			case r.Result != nil && !bytes.HasPrefix(bytes.TrimSpace(r.Result), []byte("{")):
				return "results: result must be an object"
			}
		}
	case "answer":
		switch {
		case b.CardID == nil || !assistantCallID.MatchString(*b.CardID):
			return "card_id must be a card's id"
		case b.Answer == nil || (*b.Answer != "yes" && *b.Answer != "not_now"):
			return "answer must be yes or not_now"
		}
	}
	return ""
}

// AssistantAccepted is POST …/messages's 202.
type AssistantAccepted struct {
	Thread AssistantThread `json:"thread"`
	Entry  *AssistantEntry `json:"entry"`
	Steer  bool            `json:"steer,omitempty"` // API 1.59: the message joined the running answer
}

// cleanRoles are the token's roles as a command carries them.
func cleanRoles(roles []string) []string {
	out := []string{}
	for _, r := range slices.Compact(slices.Sorted(slices.Values(roles))) {
		if roleNamePattern.MatchString(r) && len(out) < 8 {
			out = append(out, r)
		}
	}
	return out
}

// PostAssistant is POST …/messages: the input is stored and its command written in one transaction (202).
func (s *Service) PostAssistant(ctx context.Context, caller common.Caller, id string, body *AssistantPost) (AssistantAccepted, error) {
	var out AssistantAccepted
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		t, err := lockThread(ctx, tx, id)
		if err != nil {
			return err
		}
		// Continue here: the chat's actions run in this tab now; no command. updated_at moves so a tab replaying the
		// chat's events never takes the snapshot before the move for the newer one
		if body.Kind == "continue" {
			if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET client_id = $2, updated_at = clock_timestamp() WHERE id = $1`, t.ID, body.ClientID); err != nil {
				return err
			}
			t, err = threadEvent(ctx, tx, t.ID)
			out.Thread = t.dto()
			return err
		}
		out.Thread = t.dto()
		var wait *pending
		if len(t.Pending) > 0 && string(t.Pending) != "null" {
			wait = new(pending)
			if err := json.Unmarshal(t.Pending, wait); err != nil {
				return err
			}
		}
		// the turn runs: it works, or waits on its tab's actions (a wait only on a server card runs nothing)
		running := t.State == "working" || t.State == "waiting" && wait != nil && len(wait.Actions) > 0
		// Stop (API 1.59): the running turn ends at its next step; a chat not running has nothing to stop. Each Stop
		// has its own id (pipelines' inbox keeps ids for good: a second Stop after its restart must still arrive)
		if body.Kind == "stop" {
			if !running {
				return nil
			} else if deref(t.ClientID) != body.ClientID {
				return network.NewConflictError("this chat is working in another tab", nil)
			}
			if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET stopped_turn = $2 WHERE id = $1`, t.ID, t.Turn); err != nil {
				return err
			}
			return messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: fmt.Sprintf("%s:stop:%s", t.ID, utility.NewID()),
				Data: message.AssistantSteer{ThreadID: t.ID, Turn: t.Turn, Stop: true, At: time.Now().UTC().Format(time.RFC3339Nano)}})
		}
		status, snap, err := s.assistantConfig(ctx, tx)
		if err != nil {
			return err
		} else if !status.Enabled {
			return errAssistantOff
		}
		turn, actions := t.Turn+1, t.Actions
		if body.screen != nil {
			actions = body.screen.Actions
		}
		roles := cleanRoles(caller.Roles)
		lookAt := false
		if _, ok := s.askWorkflow(); ok && *status.Limits.LookAtPerHour > 0 {
			lookAt = true
		}
		input := map[string]any{"kind": body.Kind}
		set := `turn = $2, state = 'working', client_id = $3, pending = NULL, roles = $4, actions = $5, updated_at = clock_timestamp()`
		switch body.Kind {
		case "message":
			var recent int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM assistant_entries WHERE owner_id = $1 AND kind = 'user'
				AND at > clock_timestamp() - interval '1 minute'`, t.OwnerID).Scan(&recent); err != nil {
				return err
			} else if n := *status.Limits.MessagesPerMinute; recent >= n {
				return network.NewTooManyRequestsError(fmt.Sprintf("at most %d messages a minute", n), nil)
			}
			messageID := utility.NewID()
			input["message_id"], input["text"], input["screen"] = messageID, *body.Text, body.Screen
			input["at"] = time.Now().UTC().Format(time.RFC3339Nano)
			// steer (API 1.59): from the tab running the turn, while it works or runs that tab's actions, the message
			// joins the running answer; `next` keeps the turn it starts if the answer ends before reading it
			mine := deref(t.ClientID) == body.ClientID
			if t.State == "working" && !mine {
				return network.NewConflictError("this chat is working in another tab", nil)
			}
			if mine && running {
				next := message.AssistantTurn{ClientID: body.ClientID, Roles: roles, Tools: offeredTools(roles, snap.Search != nil, lookAt),
					Actions: append([]string{}, actions...), Settings: snap, Input: map[string]any{"kind": "message", "screen": body.Screen}}
				entry, err := one[AssistantEntry](ctx, tx, `INSERT INTO assistant_entries (id, thread_id, owner_id, key, turn, kind, text,
					steer, next) VALUES ($1, $2, $3, $4, $5, 'user', $6, 'waiting', $7) RETURNING `+entryCols,
					utility.NewID(), t.ID, t.OwnerID, "message:"+messageID, t.Turn, *body.Text, toJSON(next))
				if err == nil {
					err = putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "entry", "entry": entry})
				}
				if err != nil {
					return err
				}
				out.Entry, out.Steer = &entry, true
				return messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: fmt.Sprintf("%s:steer:%s", t.ID, messageID),
					Data: message.AssistantSteer{ThreadID: t.ID, Turn: t.Turn, SteerID: messageID, Text: *body.Text, At: input["at"].(string)}})
			}
			if wait != nil { // parked on a tab that went away: what it waits on never ran
				results := []map[string]any{}
				for _, aid := range wait.actionIDs() {
					results = append(results, map[string]any{"id": aid, "status": "did_not_run", "error": "the tab was closed",
						"line": map[string]any{"text": "Did not run", "undo": false}})
				}
				if len(results) > 0 {
					input["results"] = results
				}
				if err := cancelCards(ctx, tx, t, wait.Cards); err != nil {
					return err
				}
			}
			// steers still waiting (the turn ended or parked on a card before reading them) go first, in order: never
			// answered later as an out-of-order turn
			steers, _, err := takeSteers(ctx, tx, t, "next")
			if err != nil {
				return err
			} else if len(steers) > 0 {
				text := []rune(strings.Join(append(steers, *body.Text), "\n\n"))
				input["text"] = string(text[max(0, len(text)-4000):]) // the newest words win
			}
			entry, err := putEntry(ctx, tx, t, "message:"+messageID, turn, "user", body.Text, nil, nil, nil)
			if err != nil {
				return err
			}
			out.Entry = &entry
			set += `, caps = '{"model_calls": 0, "tool_calls": 0, "memory_writes": 0}', last_message_at = clock_timestamp(),
				summarized_at = NULL, title = CASE WHEN turn = 0 OR title = 'New chat' THEN $6 ELSE title END`
		case "results", "answer":
			if deref(t.ClientID) != body.ClientID {
				return network.NewConflictError("this chat is working in another tab", nil)
			}
			if body.Kind == "answer" {
				if wait == nil || t.State != "waiting" || !slices.Contains(wait.Cards, *body.CardID) {
					return network.NewConflictError("nothing waits on "+*body.CardID, nil)
				}
				if err := setCard(ctx, tx, t, *body.CardID, *body.Answer); err != nil {
					return err
				}
				input["card_id"], input["answer"] = *body.CardID, *body.Answer
				break
			}
			for _, r := range body.Results {
				if wait == nil || t.State != "waiting" || !slices.Contains(wait.actionIDs(), r.ID) || *body.Turn != wait.Turn {
					return network.NewConflictError("nothing waits on "+r.ID, nil)
				}
			}
			for _, r := range body.Results {
				line := map[string]any{"id": r.ID, "status": resultLine[r.Status], "text": r.Line.Text, "undo": *r.Line.Undo}
				if _, err := putEntry(ctx, tx, t, r.ID, wait.Turn, "line", nil, line, nil, nil); err != nil {
					return err
				}
			}
			input["results"] = body.Results
			if body.screen != nil {
				input["screen"] = body.Screen
			}
		}
		args := []any{t.ID, turn, body.ClientID, roles, actions}
		if body.Kind == "message" {
			args = append(args, cutRunes(*body.Text, 120))
		}
		if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET `+set+` WHERE id = $1`, args...); err != nil {
			return err
		}
		if err := messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: fmt.Sprintf("%s:%d", t.ID, turn),
			Data: message.AssistantTurn{ThreadID: t.ID, Turn: turn, ClientID: body.ClientID, Roles: roles,
				Tools: offeredTools(roles, snap.Search != nil, lookAt), Actions: append([]string{}, actions...), Settings: snap,
				Input: input}}); err != nil {
			return err
		}
		t, err = threadEvent(ctx, tx, t.ID)
		out.Thread = t.dto()
		return err
	})
	return out, err
}

// setCard sets a card entry's state (yes, not_now, cancelled) and sends it.
func setCard(ctx context.Context, tx pgx.Tx, t threadRow, cardID, state string) error {
	entry, err := one[AssistantEntry](ctx, tx, `UPDATE assistant_entries SET card = jsonb_set(card, '{state}', to_jsonb($3::text))
		WHERE thread_id = $1 AND key = $2 AND kind = 'card' RETURNING `+entryCols, t.ID, cardID, state)
	if errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "entry", "entry": entry})
}

// cancelCards cancels the cards a parked turn waits on.
func cancelCards(ctx context.Context, tx pgx.Tx, t threadRow, cards []string) error {
	for _, id := range cards {
		if err := setCard(ctx, tx, t, id, "cancelled"); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------ steps (the projector)

// ApplyAssistantStep stores one evt.assistant.step of ownerID's thread: its words, lines and cards as entries, and,
// for the thread's current turn, its state, pending and caps. An unknown thread, another owner's, or a step at or
// below the last one applied (a republish) is skipped.
func (p *Projector) ApplyAssistantStep(ctx context.Context, ownerID string, step message.AssistantStep) error {
	return common.System(ctx, p.pool, func(tx pgx.Tx) error {
		t, err := lockThread(ctx, tx, step.ThreadID)
		if errors.Is(err, errNotFound) || (err == nil && t.OwnerID != ownerID) {
			log.Printf("assistant projector: step %d for unknown thread %s (owner %s)", step.Seq, step.ThreadID, ownerID)
			return nil
		} else if err != nil {
			return err
		}
		if step.Seq <= t.StepSeq {
			return nil
		}
		key := fmt.Sprintf("step:%d", step.Seq)
		if text := strings.TrimSpace(step.Text); text != "" {
			if _, err := putEntry(ctx, tx, t, key, step.Turn, "assistant", &step.Text, nil, nil, nil); err != nil {
				return err
			}
		}
		for _, raw := range step.Lines {
			var line struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &line) != nil || line.ID == "" {
				continue
			}
			if _, err := putEntry(ctx, tx, t, line.ID, step.Turn, "line", nil, raw, nil, nil); err != nil {
				return err
			}
		}
		cardIDs := []string{}
		for _, raw := range step.Cards {
			var card struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &card) != nil || card.ID == "" {
				continue
			}
			cardIDs = append(cardIDs, card.ID)
			if _, err := putEntry(ctx, tx, t, card.ID, step.Turn, "card", nil, nil, raw, nil); err != nil {
				return err
			}
		}
		if len(step.Error) > 0 && string(step.Error) != "null" {
			var failure struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(step.Error, &failure)
			if _, err := putEntry(ctx, tx, t, key+":error", step.Turn, "notice", &failure.Text, nil, nil, step.Error); err != nil {
				return err
			}
		}
		set, args := `step_seq = $2`, []any{t.ID, step.Seq}
		if step.Turn >= t.Turn { // an older turn's end (a new message came first) leaves the new one's state
			state, wait := "idle", any(nil)
			if !step.Done {
				state = "waiting"
				wait = toJSON(pending{Turn: step.Turn, Actions: append([]json.RawMessage{}, step.Actions...), Cards: cardIDs})
			}
			// a summary's end (summarized_at set; a new message clears it) leaves the chat's place in Chats
			set += `, state = $3, pending = $4, updated_at = CASE WHEN summarized_at IS NULL THEN clock_timestamp() ELSE updated_at END`
			args = append(args, state, wait)
			if len(step.Caps) > 0 {
				set += `, caps = $5`
				args = append(args, step.Caps)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET `+set+` WHERE id = $1`, args...); err != nil {
			return err
		}
		if step.Done && step.Turn >= t.Turn {
			// a stopped turn takes its unread steers as read; else they start the next turn
			var stopped bool
			if err := tx.QueryRow(ctx, `SELECT coalesce(stopped_turn = $2, false) FROM assistant_threads WHERE id = $1`,
				t.ID, step.Turn).Scan(&stopped); err != nil {
				return err
			}
			if stopped {
				_, _, err = takeSteers(ctx, tx, t, "read")
			} else {
				err = nextFromSteers(ctx, tx, t)
			}
			if err != nil {
				return err
			}
		}
		_, err = threadEvent(ctx, tx, t.ID)
		return err
	})
}

// nextFromSteers (API 1.59): the turn ended with steers unread; they become the next message, in order, as one new
// turn (the command kept with the last one, its screen; text their texts joined), and show "sent as the next message".
func nextFromSteers(ctx context.Context, tx pgx.Tx, t threadRow) error {
	texts, last, err := takeSteers(ctx, tx, t, "next")
	if err != nil || len(texts) == 0 {
		return err
	}
	var data message.AssistantTurn
	if err := json.Unmarshal(last, &data); err != nil {
		return err
	}
	data.ThreadID, data.Turn = t.ID, t.Turn+1
	data.Input["message_id"], data.Input["text"] = utility.NewID(), cutRunes(strings.Join(texts, "\n\n"), 4000)
	data.Input["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(ctx, `UPDATE assistant_threads SET turn = $2, state = 'working', pending = NULL, roles = $3, actions = $4,
		caps = '{"model_calls": 0, "tool_calls": 0, "memory_writes": 0}', last_message_at = clock_timestamp(), summarized_at = NULL,
		updated_at = clock_timestamp() WHERE id = $1`, t.ID, data.Turn, data.Roles, data.Actions); err != nil {
		return err
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: t.ID, OwnerID: t.OwnerID, MsgID: fmt.Sprintf("%s:%d", t.ID, data.Turn),
		Data: data})
}

// takeSteers sets the thread's waiting steers to steer (next or read), at now, and sends them; their texts in order
// and the last one's kept command.
func takeSteers(ctx context.Context, tx pgx.Tx, t threadRow, steer string) ([]string, json.RawMessage, error) {
	type steerRow struct {
		Key  string
		Text string
		Next json.RawMessage
	}
	rows, err := collect[steerRow](ctx, tx, `SELECT key, text, next FROM assistant_entries WHERE thread_id = $1
		AND steer = 'waiting' ORDER BY at, id`, t.ID)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	texts := []string{}
	for _, r := range rows {
		texts = append(texts, r.Text)
		entry, err := one[AssistantEntry](ctx, tx, `UPDATE assistant_entries SET steer = $3, at = clock_timestamp()
			WHERE thread_id = $1 AND key = $2 RETURNING `+entryCols, t.ID, r.Key, steer)
		if err == nil {
			err = putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "entry", "entry": entry})
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return texts, rows[len(rows)-1].Next, nil
}

// ApplyAssistantSteered marks the steers a turn read (evt.assistant.steered, API 1.59): read, at now (they show where
// they were read), each sent. An unknown thread, another owner's or a steer not waiting is skipped.
func (p *Projector) ApplyAssistantSteered(ctx context.Context, ownerID string, read message.AssistantSteered) error {
	return common.System(ctx, p.pool, func(tx pgx.Tx) error {
		t, err := lockThread(ctx, tx, read.ThreadID)
		if errors.Is(err, errNotFound) || (err == nil && t.OwnerID != ownerID) {
			return nil
		} else if err != nil {
			return err
		}
		for _, id := range read.SteerIDs {
			entry, err := one[AssistantEntry](ctx, tx, `UPDATE assistant_entries SET steer = 'read', at = clock_timestamp()
				WHERE thread_id = $1 AND key = $2 AND steer = 'waiting' RETURNING `+entryCols, t.ID, "message:"+id)
			if errors.Is(err, errNotFound) {
				continue
			} else if err == nil {
				err = putAssistantEvent(ctx, tx, t.ID, t.OwnerID, map[string]any{"type": "entry", "entry": entry})
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ------------------------------------------------------------------ memory (pipelines.assistant.memory)

// AssistantMemoryAsk asks pipelines about the assistant's memory; an error is no answer.
type AssistantMemoryAsk func(ctx context.Context, req message.AssistantMemoryRequest) (message.AssistantMemoryReply, error)

// SetAssistantMemory sets how the service reaches the assistant's memory; without one the Memory list answers 503.
func (s *Service) SetAssistantMemory(fn AssistantMemoryAsk) { s.assistantMemory = fn }

// AssistantMemoryItem is the HTTP AssistantMemoryItem: the chat that saved it by title, or null.
type AssistantMemoryItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Text      string         `json:"text"`
	Thread    map[string]any `json:"thread"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

// AssistantMemoryList is GET /assistant/memory.
type AssistantMemoryList struct {
	Items  []AssistantMemoryItem `json:"items"`
	Counts map[string]int        `json:"counts"`
}

var memoryKinds = []string{"preference", "project", "worked", "summary"}

// askAssistantMemory sends req in the caller's namespace: no answer or internal is 503, not_found 404, invalid 422.
func (s *Service) askAssistantMemory(ctx context.Context, caller common.Caller, req message.AssistantMemoryRequest) (message.AssistantMemoryReply, error) {
	req.OwnerID = caller.ID
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.assistantMemory == nil {
		return message.AssistantMemoryReply{}, unavailable
	}
	reply, err := s.assistantMemory(ctx, req)
	switch {
	case err != nil || reply.Code == "internal":
		return reply, unavailable
	case reply.Code == "not_found":
		return reply, errNotFound
	case reply.Code != "":
		return reply, network.NewUnprocessableError(cmp.Or(reply.Error, "invalid"), nil)
	}
	return reply, nil
}

// memoryItems names each item's chat (the caller's threads; a deleted one is null).
func (s *Service) memoryItems(ctx context.Context, caller common.Caller, items []message.AssistantMemoryItem) ([]AssistantMemoryItem, error) {
	ids := []string{}
	for _, it := range items {
		if it.ThreadID != nil && utility.IsUUID(*it.ThreadID) {
			ids = append(ids, *it.ThreadID)
		}
	}
	titles := map[string]string{}
	if len(ids) > 0 {
		err := s.tx(ctx, caller, func(tx pgx.Tx) error {
			rows, err := collect[struct{ ID, Title string }](ctx, tx, `SELECT id::text, title FROM assistant_threads
				WHERE id = ANY($1::uuid[])`, ids)
			for _, r := range rows {
				titles[r.ID] = r.Title
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	out := []AssistantMemoryItem{}
	for _, it := range items {
		item := AssistantMemoryItem{ID: it.ID, Kind: it.Kind, Text: it.Text, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
		if title, ok := titles[deref(it.ThreadID)]; ok {
			item.Thread = map[string]any{"id": *it.ThreadID, "title": title}
		}
		out = append(out, item)
	}
	return out, nil
}

// ListAssistantMemory is GET /assistant/memory?kind=.
func (s *Service) ListAssistantMemory(ctx context.Context, caller common.Caller, kind string) (AssistantMemoryList, error) {
	if kind != "" && !slices.Contains(memoryKinds, kind) {
		return AssistantMemoryList{}, network.NewUnprocessableError("kind must be preference, project, worked or summary", nil)
	}
	reply, err := s.askAssistantMemory(ctx, caller, message.AssistantMemoryRequest{Op: "list", Kind: kind})
	if err != nil {
		return AssistantMemoryList{}, err
	}
	out := AssistantMemoryList{Counts: map[string]int{}}
	for _, k := range memoryKinds {
		out.Counts[k] = reply.Counts[k]
	}
	out.Items, err = s.memoryItems(ctx, caller, reply.Items)
	return out, err
}

// ClearAssistantMemory is DELETE /assistant/memory (Clear all).
func (s *Service) ClearAssistantMemory(ctx context.Context, caller common.Caller) error {
	_, err := s.askAssistantMemory(ctx, caller, message.AssistantMemoryRequest{Op: "clear"})
	return err
}

// AssistantMemoryPatch is PATCH /assistant/memory/{id}'s body.
type AssistantMemoryPatch struct {
	Text string `json:"text"`
}

// Validate trims text.
func (b *AssistantMemoryPatch) Validate() string {
	if b.Text = strings.TrimSpace(b.Text); b.Text == "" || utf8.RuneCountInString(b.Text) > 500 {
		return "text must be 1-500 characters"
	}
	return ""
}

// EditAssistantMemory is PATCH /assistant/memory/{id}.
func (s *Service) EditAssistantMemory(ctx context.Context, caller common.Caller, id string, body *AssistantMemoryPatch) (AssistantMemoryItem, error) {
	if !utility.IsUUID(id) {
		return AssistantMemoryItem{}, errNotFound
	}
	reply, err := s.askAssistantMemory(ctx, caller, message.AssistantMemoryRequest{Op: "edit", ItemID: id, Text: body.Text})
	if err != nil {
		return AssistantMemoryItem{}, err
	} else if reply.Item == nil {
		return AssistantMemoryItem{}, network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	}
	items, err := s.memoryItems(ctx, caller, []message.AssistantMemoryItem{*reply.Item})
	if err != nil {
		return AssistantMemoryItem{}, err
	}
	return items[0], nil
}

// DeleteAssistantMemory is DELETE /assistant/memory/{id}.
func (s *Service) DeleteAssistantMemory(ctx context.Context, caller common.Caller, id string) error {
	if !utility.IsUUID(id) {
		return errNotFound
	}
	_, err := s.askAssistantMemory(ctx, caller, message.AssistantMemoryRequest{Op: "delete", ItemID: id})
	return err
}
