package studio

import (
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
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
	"github.com/helmstudio/studio_service/registry"
)

// Session names (contracts/README.md "Sessions: titles and naming", API 1.43): the admin's setting, the
// cmd.pipeline.name a session's first succeeded run sends, the hidden local naming run pipelines asks for, and the
// evt.pipeline.<session>.named the projector applies. Pipelines owns the template and the answer checks.

const (
	// NameWorkflow is the local namer's ask workflow (the llm pack); LocalNamer its short name.
	NameWorkflow = "llm-ask"
	LocalNamer   = "Qwen3.8"
	// maxNamePrompt is how much of the prompt is sent.
	maxNamePrompt = 2000
)

// imageTag is an `<imageN>` reference in a prompt: never sent to the namer.
var imageTag = regexp.MustCompile(`<image[0-9]*>`)

// nameKey is the hidden naming run's idempotency key: name:<session_id>.
var nameKey = regexp.MustCompile(`^name:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// namesRow is the session_names row (absent: off).
type namesRow struct {
	Mode      string
	Provider  *string
	Model     *string
	UpdatedAt *time.Time
}

func readNames(ctx context.Context, tx pgx.Tx) (namesRow, error) {
	row, err := one[namesRow](ctx, tx, `SELECT mode, provider, model, updated_at FROM session_names`)
	if errors.Is(err, errNotFound) {
		return namesRow{Mode: "off"}, nil
	}
	return row, err
}

// namer is the setting resolved now: the short name and the provider row (remote); ok false when it names nothing
// usable (off, a disabled or deleted provider, a model it no longer lists).
func namer(ctx context.Context, tx pgx.Tx, set namesRow) (string, *providerRow, bool, error) {
	switch set.Mode {
	case "local":
		return LocalNamer, nil, true, nil
	case "remote":
		p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1 AND enabled AND owner_id IS NULL AND `+pluginOn, deref(set.Provider))
		if errors.Is(err, errNotFound) || (err == nil && !slices.Contains(p.Models, deref(set.Model))) {
			return "", nil, false, nil
		}
		return cmp.Or(deref(p.Title), p.Name), &p, err == nil, err
	}
	return "", nil, false, nil
}

// naming is SessionList.naming: the setting as a user may see it.
func naming(ctx context.Context, tx pgx.Tx) (dto.SessionNaming, error) {
	out := dto.SessionNaming{Mode: "off"}
	set, err := readNames(ctx, tx)
	if err != nil {
		return out, err
	}
	name, p, ok, err := namer(ctx, tx, set)
	if err != nil || !ok {
		return out, err
	}
	out.Mode, out.NamedBy, out.Remote = set.Mode, &name, p != nil && p.Kind == "remote"
	return out, nil
}

// localAvailable reports whether a live host can run the local namer's workflow now.
func (s *Service) localAvailable() bool {
	workflow, ok := s.registry.Workflow(NameWorkflow)
	if !ok {
		return false
	}
	nodes, detail := resolve(workflow, map[string]any{})
	if detail != "" {
		return false
	}
	graph, _ := json.Marshal(nodes)
	_, _, reason := eligible(s.registry, s.registry.Hosts(), model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: graph}, time.Now())
	return reason == ""
}

// GetSessionNames is GET /admin/session-names.
func (s *Service) GetSessionNames(ctx context.Context, caller common.Caller) (dto.SessionNames, error) {
	if !caller.IsAdmin() {
		return dto.SessionNames{}, errAdmin
	}
	var set namesRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		set, err = readNames(ctx, tx)
		return err
	})
	return s.namesDTO(set), err
}

func (s *Service) namesDTO(set namesRow) dto.SessionNames {
	return dto.SessionNames{Mode: set.Mode, Provider: set.Provider, Model: set.Model, LocalAvailable: s.localAvailable(),
		RemoteEnabled: s.remoteOn, UpdatedAt: set.UpdatedAt}
}

// PutSessionNames is PUT /admin/session-names: off, local (a live host can run llm-ask) or remote (an enabled
// provider and one of its models; kind remote needs the deployment's switch, a server on this network does not).
func (s *Service) PutSessionNames(ctx context.Context, caller common.Caller, body *dto.SessionNamesPut) (dto.SessionNames, error) {
	if !caller.IsAdmin() {
		return dto.SessionNames{}, errAdmin
	}
	if body.Mode == "local" && !s.localAvailable() {
		return dto.SessionNames{}, network.NewUnprocessableError("Qwen3.8 27B is not downloaded", nil)
	}
	var set namesRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if body.Mode == "remote" {
			p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1 AND enabled`, *body.Provider)
			if errors.Is(err, errNotFound) {
				return network.NewUnprocessableError(*body.Provider+" is not an enabled provider", nil)
			} else if err != nil {
				return err
			} else if p.OwnerID != nil { // API 1.61: naming runs for every account
				return network.NewUnprocessableError(p.Name+" is only yours; pick a shared provider", nil)
			}
			if body.Model == nil {
				body.Model = &p.DefaultModel
			}
			if !slices.Contains(p.Models, *body.Model) {
				return network.NewUnprocessableError(fmt.Sprintf("%s is not a model of %s", *body.Model, p.Name), nil)
			}
			if p.Kind == "remote" && !s.remoteOn {
				return network.NewConflictError("remote models are off on this deployment", nil)
			}
		}
		var err error
		set, err = one[namesRow](ctx, tx, `INSERT INTO session_names (mode, provider, model) VALUES ($1, $2, $3)
			ON CONFLICT (one) DO UPDATE SET mode = excluded.mode, provider = excluded.provider, model = excluded.model,
			updated_at = clock_timestamp() RETURNING mode, provider, model, updated_at`, body.Mode, body.Provider, body.Model)
		return err
	})
	return s.namesDTO(set), err
}

// namePrompt is what the namer is sent from run: its workflow's first unwired text param as typed (node id, then
// schema order), <imageN> tags removed, spaces collapsed, cut to 2000 characters; "" when it has none.
func namePrompt(reg *registry.Registry, run model.Run) string {
	nodes := map[string]registry.Node{}
	_ = json.Unmarshal(run.Nodes, &nodes)
	if registry.IsText(nodes) { // an ask run (a judge test): its prompt is a template, not the user's
		return ""
	}
	found := prompts(nodes, reg.Schemas())
	if len(found) == 0 {
		return ""
	}
	text := found[0].Value
	var exp expansion
	_ = json.Unmarshal(run.Mentions, &exp)
	if typed, ok := exp.Typed[found[0].Node+"."+found[0].Param.Name].(string); ok {
		text = typed
	}
	return cleanPrompt(text)
}

// cleanPrompt is a prompt as the namer is sent it: <imageN> tags removed, spaces collapsed, cut to 2000 characters.
func cleanPrompt(text string) string {
	text = strings.Join(strings.Fields(imageTag.ReplaceAllString(text, " ")), " ")
	if utf8.RuneCountInString(text) > maxNamePrompt {
		text = string([]rune(text)[:maxNamePrompt])
	}
	return text
}

// promptTitle is a session title from a prompt: its first 5 words, trailing punctuation dropped, the first letter
// upper-cased, at most 60 characters ("old fisherman on a pier, at dawn" → "Old fisherman on a pier").
func promptTitle(prompt string) string {
	words := strings.Fields(prompt)
	title := strings.TrimRight(strings.Join(words[:min(5, len(words))], " "), ",.;:!?-")
	if r := []rune(title); len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
		title = strings.TrimSpace(string(r[:min(60, len(r))]))
	}
	return title
}

// askName writes cmd.pipeline.name in the transaction that made run succeed, when run is the session's first
// succeeded Create run with a prompt, the session's title is nobody's and the setting names something usable; the
// session is marked asked, so it is asked once.
func (p *Projector) askName(ctx context.Context, tx pgx.Tx, run model.Run) error {
	if p.Workflows == nil || run.Purpose != nil || run.BatchID != nil || run.Lane != "interactive" {
		return nil
	}
	prompt := namePrompt(p.Workflows, run)
	if prompt == "" {
		return nil // the next run with a prompt may ask
	}
	// an Untitled session takes the prompt's first words at once (owner, 2026-11-25): with no namer it keeps them, a
	// namer may still replace them (title_source prompt)
	if title := promptTitle(prompt); title != "" {
		tag, err := tx.Exec(ctx, `UPDATE sessions SET title = $2, title_source = 'prompt' WHERE id = $1 AND NOT name_asked
			AND title_source = 'default'`, run.SessionID, title)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			if err = appendEvent(ctx, tx, run, "session", time.Now(),
				map[string]any{"title": title, "title_source": "prompt", "named_by": nil}); err != nil {
				return err
			}
		}
	}
	// marked first, whatever the setting: only the first succeeded run with a prompt may name (naming turned on later
	// never names an older session)
	tag, err := tx.Exec(ctx, `UPDATE sessions SET name_asked = true WHERE id = $1 AND NOT name_asked
		AND title_source IN ('default', 'prompt')`, run.SessionID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return enqueueName(ctx, tx, p.RemoteOn, run.OwnerID, run.SessionID, run.ID, prompt)
}

// enqueueName writes cmd.pipeline.name for sessionID (named from runID's prompt) when the setting names something
// usable: a remote provider is skipped while the deployment's switch is off.
func enqueueName(ctx context.Context, tx pgx.Tx, remoteOn bool, ownerID, sessionID, runID, prompt string) error {
	set, err := readNames(ctx, tx)
	if err != nil {
		return err
	}
	name, provider, ok, err := namer(ctx, tx, set)
	if err != nil || !ok || (provider != nil && provider.Kind == "remote" && !remoteOn) {
		return err
	}
	data := message.Name{SessionID: sessionID, RunID: runID, Prompt: prompt, Mode: set.Mode, NamedBy: name}
	if provider != nil {
		data.Provider = &message.NameProvider{Name: provider.Name, Model: deref(set.Model), Snapshot: provider.snapshot()}
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: sessionID, OwnerID: ownerID,
		MsgID: "name:" + sessionID, Data: data})
}

// askTimelineName names an untitled sequence once (API 1.76, as askName a session): while its name is default and it
// was never asked, the first take of its document (track and clip order) made by a run with a prompt gives the
// prompt's first words at once (title_source prompt, its session too, with a `session` event there) and, when the
// setting names something, cmd.pipeline.name for the sequence's session; the answer names both (namedHandler).
func askTimelineName(ctx context.Context, tx pgx.Tx, reg *registry.Registry, remoteOn bool, row *timelineRow) error {
	if reg == nil || row.TitleSource != "default" || row.NameAsked || row.SessionID == nil {
		return nil // ponytail: a sequence whose session was deleted waits for its next shot to make one
	}
	ids := docTakeIDs(row.doc())
	if len(ids) == 0 {
		return nil
	}
	runs, err := collect[model.Run](ctx, tx, `WITH t AS (SELECT run_id, min(array_position($1::uuid[], id)) AS pos
		FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND run_id IS NOT NULL GROUP BY run_id)
		SELECT `+model.RunCols+` FROM runs JOIN t ON t.run_id = runs.id WHERE owner_id = $2 AND purpose IS NULL
		ORDER BY t.pos`, ids, row.OwnerID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		prompt := namePrompt(reg, run)
		title := promptTitle(prompt)
		if title == "" {
			continue // an upload, a clone with no text: the next take with a prompt may name it
		}
		named, err := one[timelineRow](ctx, tx, `UPDATE timelines SET name = $2, title_source = 'prompt', name_asked = true
			WHERE id = $1 RETURNING `+timelineCols, row.ID, title)
		if err != nil {
			return err
		}
		*row = named
		if _, err := tx.Exec(ctx, `UPDATE sessions SET title = $2, title_source = 'prompt', name_asked = true WHERE id = $1`,
			*row.SessionID, title); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, model.Run{ID: run.ID, SessionID: *row.SessionID, OwnerID: row.OwnerID}, "session",
			time.Now(), map[string]any{"title": title, "title_source": "prompt", "named_by": nil}); err != nil {
			return err
		}
		return enqueueName(ctx, tx, remoteOn, row.OwnerID, *row.SessionID, run.ID, prompt)
	}
	return nil
}

// boardPrompt is what a Magic board is named from (API 1.91): its first block (lane then time order) with a prompt,
// else a line, else a dialogue's first line with words; "" when none has one.
func boardPrompt(d magic.Doc) string {
	for _, b := range d.Blocks {
		texts := []any{b.Request["prompt"], b.Request["line"]}
		turns, _ := b.Request["turns"].([]any)
		for _, t := range turns {
			if m, ok := t.(map[string]any); ok {
				texts = append(texts, m["text"])
			}
		}
		for _, t := range texts {
			if text, _ := t.(string); cleanPrompt(text) != "" {
				return cleanPrompt(text)
			}
		}
	}
	return ""
}

// askBoardName names an untitled Magic board once (API 1.91, as askTimelineName a sequence): while its name is default
// and it was never asked, the first stored document whose blocks hold a prompt gives the prompt's first words at once
// (title_source prompt; the board's session, made now if it has none, too, marked asked) and, when the setting names
// something, cmd.pipeline.name for that session with the board id as its run id; the answer names both
// (namedHandler).
func askBoardName(ctx context.Context, tx pgx.Tx, remoteOn bool, row *boardRow) error {
	if row.TitleSource != "default" || row.NameAsked {
		return nil
	}
	prompt := boardPrompt(row.doc())
	title := promptTitle(prompt)
	if title == "" {
		return nil
	}
	sessionID, err := boardSession(ctx, tx, row)
	if err != nil {
		return err
	}
	named, err := one[boardRow](ctx, tx, `UPDATE magic_boards SET name = $2, title_source = 'prompt', name_asked = true
		WHERE id = $1 RETURNING `+boardCols, row.ID, title)
	if err != nil {
		return err
	}
	*row = named
	if _, err := tx.Exec(ctx, `UPDATE sessions SET title = $2, title_source = 'prompt', name_asked = true WHERE id = $1`,
		sessionID, "Magic · "+title); err != nil {
		return err
	}
	return enqueueName(ctx, tx, remoteOn, row.OwnerID, sessionID, row.ID, prompt)
}

// nameTimelinesOf is askTimelineName for each sequence run's shot just filled (the projector).
func (p *Projector) nameTimelinesOf(ctx context.Context, tx pgx.Tx, run model.Run) error {
	_, timelines, _, err := shotsOf(ctx, tx, run)
	if err != nil {
		return err
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(timelines))) {
		row, err := one[timelineRow](ctx, tx, `SELECT `+timelineCols+` FROM timelines WHERE id = $1 AND owner_id = $2
			AND deleted_at IS NULL FOR UPDATE`, id, run.OwnerID)
		if errors.Is(err, errNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if err := askTimelineName(ctx, tx, p.Workflows, p.RemoteOn, &row); err != nil {
			return err
		}
	}
	return nil
}

// applyStatusNamed is applyStatus, then askName when the run succeeded.
func (p *Projector) applyStatusNamed(ctx context.Context, tx pgx.Tx, run model.Run, event *gen.Message[message.Status]) (any, error) {
	stored, err := applyStatus(ctx, tx, run, event)
	if err == nil && stored != nil && event.Data.Status == "succeeded" {
		err = p.askName(ctx, tx, run)
	}
	return stored, err
}

// goodTitle reports whether a namer's title may be stored: 1-60 characters on one line, none of the markup
// characters pipelines strips (it checked 2-5 words; studio checks again, it is model text).
func goodTitle(title string) bool {
	n := utf8.RuneCountInString(title)
	return n >= 1 && n <= 60 && !strings.ContainsAny(title, "\n\r\"`<>*_#[]")
}

// namedHandler applies evt.pipeline.<session_id>.named: the title, only to the envelope owner's session while its
// title is nobody's (a rename wins), and a `session` event to its log. Anything else is dropped (acked).
func (p *Projector) namedHandler(ctx context.Context, msg *micro.NatsJetStreamMessage) {
	event, err := micro.JsonToMsg[gen.Message[message.Named]](msg.Data())
	if err != nil {
		micro.TermNatsJetStreamMessage(msg, err)
		return
	}
	parts := strings.Split(msg.Subject(), ".")
	d := event.Data
	title := strings.TrimSpace(d.Title)
	if event.JobID == nil || len(parts) != 4 || parts[2] != *event.JobID || d.SessionID != *event.JobID ||
		!utility.IsUUID(d.SessionID) || !utility.IsUUID(d.RunID) || !goodTitle(title) || d.NamedBy == "" ||
		utf8.RuneCountInString(d.NamedBy) > 60 {
		log.Printf("names: dropping %s on %s", event.Type, msg.Subject())
		micro.AckNatsJetStreamMessage(msg)
		return
	}
	err = common.System(ctx, p.pool, func(tx pgx.Tx) error {
		// API 1.76: a sequence's session names the sequence (asked by askTimelineName, from a take made anywhere)
		seq, err := tx.Exec(ctx, `UPDATE timelines SET name = $2, title_source = 'model', named_by = $3
			WHERE session_id = $1 AND owner_id = $4 AND deleted_at IS NULL AND name_asked
			AND title_source IN ('default', 'prompt')`, d.SessionID, title, d.NamedBy, event.OwnerID)
		if err != nil {
			return err
		}
		// API 1.91: so does a Magic board's (asked by askBoardName, the board id as the run id)
		board, err := tx.Exec(ctx, `UPDATE magic_boards SET name = $2, title_source = 'model', named_by = $3
			WHERE id = $5 AND session_id = $1 AND owner_id = $4 AND deleted_at IS NULL AND name_asked
			AND title_source IN ('default', 'prompt')`, d.SessionID, title, d.NamedBy, event.OwnerID, d.RunID)
		if err != nil {
			return err
		}
		named := seq.RowsAffected() + board.RowsAffected()
		tag, err := tx.Exec(ctx, `UPDATE sessions SET title = $2, title_source = 'model', named_by = $3
			WHERE id = $1 AND owner_id = $4 AND title_source IN ('default', 'prompt')
			AND ($6 OR EXISTS (SELECT 1 FROM runs WHERE id = $5 AND session_id = $1))`, d.SessionID, title, d.NamedBy,
			event.OwnerID, d.RunID, named > 0)
		if err != nil || (tag.RowsAffected() == 0 && named == 0) {
			return err
		}
		return appendEvent(ctx, tx, model.Run{ID: d.RunID, SessionID: d.SessionID, OwnerID: event.OwnerID}, "session",
			eventTime(event.OccurredAt), map[string]any{"title": title, "title_source": "model", "named_by": d.NamedBy})
	})
	if err != nil {
		micro.NakNatsJetStreamMessage(ctx, msg, err)
		return
	}
	micro.AckNatsJetStreamMessage(msg)
}

// createNameRun answers studio.runs.create with purpose name: the session's hidden llm-ask run, once per session
// (key name:<session_id>), owner and session from the session row, inputs ask.* only; lane batch, no batch.
func (s *Service) createNameRun(ctx context.Context, req *message.RunsCreate) (message.RunsCreated, *message.RunsCreateError) {
	invalid := func(detail string) *message.RunsCreateError {
		return &message.RunsCreateError{Error: detail, Code: "invalid"}
	}
	notFound := &message.RunsCreateError{Error: "no such session or workflow for this owner", Code: "not_found"}
	if req.IdempotencyKey != "name:"+req.SessionID || !nameKey.MatchString(req.IdempotencyKey) || req.Lane != "batch" ||
		req.BatchID != "" || req.Row != 0 || !utility.IsUUID(req.OwnerID) {
		return message.RunsCreated{}, invalid("a naming run is lane batch, no batch or row, key name:<session_id>")
	}
	for key := range req.Inputs {
		if !slices.Contains([]string{"ask.system", "ask.prompt", "ask.schema", "ask.max_tokens"}, key) {
			return message.RunsCreated{}, invalid("a naming run takes ask.system, ask.prompt, ask.schema and ask.max_tokens only")
		}
	}
	if !s.registry.Ready() {
		return message.RunsCreated{}, &message.RunsCreateError{Error: "studio is starting; try again", Code: "internal"}
	}
	workflow, ok := s.registry.Workflow(NameWorkflow)
	if req.WorkflowID != NameWorkflow || !ok {
		return message.RunsCreated{}, notFound
	}
	var out message.RunsCreated
	var refused *message.RunsCreateError
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		var owner string
		err := tx.QueryRow(ctx, `SELECT owner_id::text FROM sessions WHERE id = $1 FOR UPDATE`, req.SessionID).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != req.OwnerID) {
			refused = notFound
			return nil
		} else if err != nil {
			return err
		}
		run, err := one[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE idempotency_key = $1`, req.IdempotencyKey)
		created := errors.Is(err, errNotFound)
		if created {
			graphs, details, _, err := s.runGraphs(ctx, tx, owner, workflow, []map[string]any{req.Inputs}, [][]string{nil}, nil)
			if err != nil {
				return err
			} else if details[0] != "" {
				refused = invalid(details[0])
				return nil
			}
			graph, _ := json.Marshal(graphs[0].Nodes)
			status, failure := "queued", (*string)(nil)
			if _, _, reason := eligible(s.registry, s.registry.Hosts(), model.Run{WorkflowID: workflow.ID, Env: workflow.Env,
				Nodes: graph}, time.Now()); reason != "" {
				status, failure = "failed", &reason
			}
			inputs, _ := json.Marshal(req.Inputs)
			if run, err = one[model.Run](ctx, tx, `INSERT INTO runs (id, owner_id, session_id, workflow_id, env, inputs, status,
				error, nodes, lane, idempotency_key, finished_at, workflow_title, thumbs, purpose) VALUES ($1, $2, $3, $4, $5, $6, $7,
				$8, $9, 'batch', $10, CASE WHEN $7 <> 'queued' THEN clock_timestamp() END, $11, 0, 'name') RETURNING `+model.RunCols,
				utility.NewID(), owner, req.SessionID, workflow.ID, workflow.Env, inputs, status, failure, graph, req.IdempotencyKey,
				workflow.Title); err != nil {
				return err
			}
			if terminal[status] {
				if err := runEnded(ctx, tx, run); err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}
		out = message.RunsCreated{RunID: run.ID, Status: run.Status, Created: created, TakeIDs: []string{}, Error: run.Error}
		if terminal[run.Status] {
			out.Answer = answerOf(run)
		}
		return nil
	})
	if err != nil {
		return out, &message.RunsCreateError{Error: "internal error", Code: "internal"}
	}
	return out, refused
}
