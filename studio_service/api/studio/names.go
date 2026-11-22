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
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

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
		p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1 AND enabled`, deref(set.Provider))
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
	text = strings.Join(strings.Fields(imageTag.ReplaceAllString(text, " ")), " ")
	if utf8.RuneCountInString(text) > maxNamePrompt {
		text = string([]rune(text)[:maxNamePrompt])
	}
	return text
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
	set, err := readNames(ctx, tx)
	if err != nil {
		return err
	}
	name, provider, ok, err := namer(ctx, tx, set)
	if err != nil || !ok || (provider != nil && provider.Kind == "remote" && !p.RemoteOn) {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE sessions SET name_asked = true WHERE id = $1 AND NOT name_asked
		AND title_source IN ('default', 'prompt')`, run.SessionID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	data := message.Name{SessionID: run.SessionID, RunID: run.ID, Prompt: prompt, Mode: set.Mode, NamedBy: name}
	if provider != nil {
		data.Provider = &message.NameProvider{Name: provider.Name, Model: deref(set.Model), Snapshot: provider.snapshot()}
	}
	return messaging.Enqueue(ctx, tx, messaging.Command{JobID: run.SessionID, OwnerID: run.OwnerID,
		MsgID: "name:" + run.SessionID, Data: data})
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
		tag, err := tx.Exec(ctx, `UPDATE sessions SET title = $2, title_source = 'model', named_by = $3
			WHERE id = $1 AND owner_id = $4 AND title_source IN ('default', 'prompt')
			AND EXISTS (SELECT 1 FROM runs WHERE id = $5 AND session_id = $1)`, d.SessionID, title, d.NamedBy, event.OwnerID, d.RunID)
		if err != nil || tag.RowsAffected() == 0 {
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
