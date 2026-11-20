package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/lora"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// User workflows (contracts/README.md "User workflows"): templates are the registry's workflows (slug ids, read-only);
// a user's own live in user_workflows (UUID ids, row-level security on owner_id).

// MaxWorkflows is how many workflows one user may have.
const MaxWorkflows = 200

// WorkflowError is the 422 of a save the graph rules block: the detail and every problem.
type WorkflowError struct {
	Detail   string        `json:"detail"`
	Problems []dto.Problem `json:"problems"`
}

func (e *WorkflowError) Error() string { return e.Detail }

var (
	errStarting = network.NewServiceUnavailableError("studio is starting; try again", nil)
	errTemplate = network.NewConflictError("templates cannot be changed; save a copy", nil)
)

// ready is errStarting until the registry watches have delivered: the palette would read as empty.
func (s *Service) ready() error {
	if !s.registry.Ready() {
		return errStarting
	}
	return nil
}

// palette is every kind live hosts publish now.
func (s *Service) palette() map[string]registry.PaletteNode { return s.registry.Palette(time.Now()) }

// schemasOf returns the registry schema (live or not) of every kind of nodes the registry has.
func (s *Service) schemasOf(nodes map[string]registry.Node) map[string]registry.NodeSchema {
	all, out := s.registry.Schemas(), map[string]registry.NodeSchema{}
	for _, node := range nodes {
		if schema, ok := all[node.Kind]; ok {
			out[node.Kind] = schema
		}
	}
	return out
}

// rowDoc is a stored workflow's document.
func rowDoc(row model.UserWorkflow) dto.WorkflowDoc {
	doc := dto.WorkflowDoc{Title: row.Title, Family: row.Family, Env: row.Env}
	_ = json.Unmarshal(row.Nodes, &doc.Nodes)
	_ = json.Unmarshal(row.Studio, &doc.Studio) // null leaves it nil
	return doc
}

// templateDoc is a registry workflow as a document.
func templateDoc(workflow registry.Workflow) dto.WorkflowDoc {
	doc := dto.WorkflowDoc{Title: workflow.Title, Family: workflow.Family, Env: workflow.Env, Nodes: workflow.Nodes}
	_ = json.Unmarshal(workflow.Studio, &doc.Studio) // absent leaves it nil
	return doc
}

// templateDTO maps a template (its media as published); problems nil leaves them out.
func (s *Service) templateDTO(workflow registry.Workflow, problems *[]dto.Problem) dto.Workflow {
	return dto.Workflow{ID: workflow.ID, Title: workflow.Title, Action: workflow.Action, Family: workflow.Family, Env: workflow.Env,
		Media: cmp.Or(workflow.Media, "image"), Nodes: workflow.Nodes, NodeSchemas: workflow.NodeSchemas, Source: "template",
		Studio: templateDoc(workflow).Studio, Problems: problems, Keyframes: timeline.ShotKeyframes(workflow.Nodes, workflow.NodeSchemas)}
}

// mineDTO maps a stored workflow (its media computed from its graph); problems nil leaves them out.
func (s *Service) mineDTO(row model.UserWorkflow, problems *[]dto.Problem) dto.Workflow {
	doc := rowDoc(row)
	schemas := s.schemasOf(doc.Nodes)
	return dto.Workflow{ID: row.ID, Title: doc.Title, Family: doc.Family, Env: doc.Env, Nodes: doc.Nodes,
		Media: workflowMedia(doc.Nodes, schemas), NodeSchemas: schemas, Source: "mine", Studio: doc.Studio, BasedOn: row.BasedOn,
		Version: &row.Version, CreatedAt: &row.CreatedAt, UpdatedAt: &row.UpdatedAt, Problems: problems,
		Keyframes: timeline.ShotKeyframes(doc.Nodes, schemas)}
}

// readWorkflow reads one of the caller's workflows in tx (lock: FOR UPDATE); another user's is errNotFound.
func readWorkflow(ctx context.Context, tx pgx.Tx, id string, lock bool) (model.UserWorkflow, error) {
	if !utility.IsUUID(id) {
		return model.UserWorkflow{}, errNotFound
	}
	query := `SELECT ` + model.UserWorkflowCols + ` FROM user_workflows WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	return one[model.UserWorkflow](ctx, tx, query, id)
}

// FindWorkflow returns a template or one of the caller's workflows with the registry schemas of its kinds, and the
// stored version (nil for a template); errNotFound when there is none. The gallery's reuse and "Use in…" read it.
func (s *Service) FindWorkflow(ctx context.Context, caller common.Caller, id string) (registry.Workflow, *int, error) {
	if !utility.IsUUID(id) {
		workflow, ok := s.registry.Workflow(id)
		if !ok {
			return workflow, nil, errNotFound
		}
		return workflow, nil, nil
	}
	var row model.UserWorkflow
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = readWorkflow(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return registry.Workflow{}, nil, err
	}
	doc := rowDoc(row)
	return registry.Workflow{ID: row.ID, Title: doc.Title, Family: doc.Family, Env: doc.Env, Nodes: doc.Nodes,
		Studio: row.Studio, NodeSchemas: s.schemasOf(doc.Nodes)}, &row.Version, nil
}

// runnable returns the workflow a run or batch names, as the caller sees it in tx: a template, or the caller's own
// as stored now if it passes every graph rule against the live palette (positions dropped, the live schemas), with its
// version. Unknown (or another user's) is 422 unknown workflow; a problem is 422 workflow cannot run.
func (s *Service) runnable(ctx context.Context, tx pgx.Tx, id string) (registry.Workflow, *int, error) {
	unknown := network.NewUnprocessableError("unknown workflow "+id, nil)
	if !utility.IsUUID(id) {
		workflow, ok := s.registry.Workflow(id)
		if !ok {
			return workflow, nil, unknown
		}
		return workflow, nil, nil
	}
	row, err := readWorkflow(ctx, tx, id, false)
	if errors.Is(err, errNotFound) {
		return registry.Workflow{}, nil, unknown
	} else if err != nil {
		return registry.Workflow{}, nil, err
	}
	doc, palette := rowDoc(row), s.palette()
	if problems := graphProblems(doc, palette, nil); len(problems) > 0 { // LoRAs: checked once inputs are merged
		return registry.Workflow{}, nil, network.NewUnprocessableError("workflow cannot run: "+problems[0].Message, nil)
	}
	workflow := registry.Workflow{ID: row.ID, Title: doc.Title, Family: doc.Family, Env: doc.Env,
		Nodes: map[string]registry.Node{}, NodeSchemas: map[string]registry.NodeSchema{}}
	for nodeID, node := range doc.Nodes {
		node.Position = nil
		workflow.Nodes[nodeID] = node
		workflow.NodeSchemas[node.Kind] = palette[node.Kind].NodeSchema
	}
	return workflow, &row.Version, nil
}

// refuseText is the 422 of Create, a workflow batch and "Use in…" for a text (ask) workflow (API 1.14): only a
// pipeline's judge, write and decide nodes run one.
func refuseText(workflow registry.Workflow) error {
	if registry.IsText(workflow.Nodes) {
		return network.NewUnprocessableError(fmt.Sprintf("workflow %s answers text; only a judge, write or decide node runs it",
			workflow.ID), nil)
	}
	return nil
}

// kinds lists the kinds of a graph, each once, sorted.
func kinds(nodes map[string]registry.Node) []string {
	var out []string
	for _, node := range nodes {
		if !slices.Contains(out, node.Kind) {
			out = append(out, node.Kind)
		}
	}
	slices.Sort(out)
	return out
}

// ------------------------------------------------------------------ the endpoints

// ListWorkflows returns the templates by title, then the caller's own newest first; source keeps one kind, media
// those whose takes are images, videos or (API 1.30) audio.
func (s *Service) ListWorkflows(ctx context.Context, caller common.Caller, source, media string) (dto.WorkflowList, error) {
	out := dto.WorkflowList{Workflows: []dto.Workflow{}}
	if source != "" && source != "template" && source != "mine" {
		return out, network.NewUnprocessableError("source must be template or mine", nil)
	}
	if media != "" && media != "image" && media != "video" && media != "audio" {
		return out, network.NewUnprocessableError("media must be image, video or audio", nil)
	}
	keep := func(workflow dto.Workflow) {
		if workflow.Media != "text" && (media == "" || workflow.Media == media) { // text workflows are internal
			out.Workflows = append(out.Workflows, workflow)
		}
	}
	if err := s.ready(); err != nil {
		return out, err
	}
	if source != "mine" {
		templates := s.registry.Workflows()
		slices.SortStableFunc(templates, func(a, b registry.Workflow) int { return strings.Compare(a.Title, b.Title) })
		for _, workflow := range templates {
			keep(s.templateDTO(workflow, nil))
		}
	}
	if source == "template" {
		return out, nil
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.UserWorkflow](ctx, tx, `SELECT `+model.UserWorkflowCols+` FROM user_workflows
			ORDER BY updated_at DESC, id LIMIT $1`, MaxWorkflows)
		for _, row := range rows {
			keep(s.mineDTO(row, nil))
		}
		return err
	})
	return out, err
}

// Offerable is what "Use in…" may offer (API 1.19; contracts/README.md, Media inputs: offers): the templates a live host
// could run now with their own values (eligible), by family then in their pack's order, then the caller's own non-text
// workflows (GET /workflows order).
func (s *Service) Offerable(ctx context.Context, caller common.Caller) ([]registry.Workflow, error) {
	return s.offerable(ctx, caller, true)
}

// offerable is Offerable; mine false leaves the caller's own workflows out (a Timeline clip's actions).
func (s *Service) offerable(ctx context.Context, caller common.Caller, mine bool) ([]registry.Workflow, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var out []registry.Workflow
	templates, now := s.registry.Workflows(), time.Now()
	slices.SortStableFunc(templates, func(a, b registry.Workflow) int { // by family, then its pack's order
		return cmp.Or(strings.Compare(a.Family, b.Family), cmp.Compare(a.Order, b.Order))
	})
	for _, workflow := range templates {
		graph := map[string]registry.Node{}
		for id, node := range workflow.Nodes {
			node.Params = merged(workflow.NodeSchemas[node.Kind], node.Params)
			graph[id] = node
		}
		nodes, _ := json.Marshal(graph)
		probe := model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: nodes}
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), probe, now); reason == "" && !registry.IsText(workflow.Nodes) {
			out = append(out, workflow)
		}
	}
	if !mine {
		return out, nil
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.UserWorkflow](ctx, tx, `SELECT `+model.UserWorkflowCols+` FROM user_workflows
			ORDER BY updated_at DESC, id LIMIT $1`, MaxWorkflows)
		for _, row := range rows {
			doc := rowDoc(row)
			schemas := s.schemasOf(doc.Nodes)
			if media := workflowMedia(doc.Nodes, schemas); media != "text" {
				out = append(out, registry.Workflow{ID: row.ID, Title: doc.Title, Family: doc.Family, Env: doc.Env,
					Media: media, Nodes: doc.Nodes, Studio: row.Studio, NodeSchemas: schemas})
			}
		}
		return err
	})
	return out, err
}

// GetWorkflow returns a template or one of the caller's workflows with its problems against the live palette.
func (s *Service) GetWorkflow(ctx context.Context, caller common.Caller, id string) (dto.Workflow, error) {
	if err := s.ready(); err != nil {
		return dto.Workflow{}, err
	}
	if !utility.IsUUID(id) {
		workflow, ok := s.registry.Workflow(id)
		if !ok {
			return dto.Workflow{}, errNotFound
		}
		problems := graphProblems(templateDoc(workflow), s.palette(), map[string]string{}) // a template holds no LoRA
		return s.templateDTO(workflow, &problems), nil
	}
	var out dto.Workflow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readWorkflow(ctx, tx, id, false)
		if err != nil {
			return err
		}
		problems, err := s.problems(ctx, tx, caller, rowDoc(row))
		out = s.mineDTO(row, &problems)
		return err
	})
	return out, err
}

// problems checks doc with every graph rule, against the caller's LoRA library now (the ids it names).
func (s *Service) problems(ctx context.Context, tx pgx.Tx, caller common.Caller, doc dto.WorkflowDoc) ([]dto.Problem, error) {
	usable, err := lora.Usable(ctx, tx, s.registry, caller.ID, doc.Env, docLoraIDs(doc))
	library := map[string]string{}
	for id, info := range usable {
		library[id] = info.Family
	}
	return graphProblems(doc, s.palette(), library), err
}

// checkSave returns the document's problems, or the 422 WorkflowError when one blocks saving.
func (s *Service) checkSave(ctx context.Context, tx pgx.Tx, caller common.Caller, doc dto.WorkflowDoc) ([]dto.Problem, error) {
	problems, err := s.problems(ctx, tx, caller, doc)
	if err != nil {
		return nil, err
	}
	if blocksSave(problems) {
		first := problems[slices.IndexFunc(problems, func(p dto.Problem) bool { return p.Blocks == "save" })]
		return nil, &WorkflowError{Detail: fmt.Sprintf("workflow has %d problems: %s", len(problems), first.Message),
			Problems: problems}
	}
	return problems, nil
}

// CreateWorkflow saves a new workflow of the caller's: the document given, or a copy of based_on.
func (s *Service) CreateWorkflow(ctx context.Context, caller common.Caller, body *dto.WorkflowCreate) (dto.Workflow, error) {
	if err := s.ready(); err != nil {
		return dto.Workflow{}, err
	}
	var out dto.Workflow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		doc := body.WorkflowDoc
		if body.BasedOn != nil { // a source the caller cannot read is a 404 before anything else
			var source dto.WorkflowDoc
			if utility.IsUUID(*body.BasedOn) {
				row, err := readWorkflow(ctx, tx, *body.BasedOn, false)
				if err != nil {
					return err
				}
				source = rowDoc(row)
			} else if workflow, ok := s.registry.Workflow(*body.BasedOn); ok {
				source = templateDoc(workflow)
			} else {
				return errNotFound
			}
			if body.IsCopy() {
				doc, doc.Title = source, cmp.Or(body.Title, cutRunes(source.Title+" copy", 80))
			}
		}
		doc.Title = strings.TrimSpace(doc.Title)
		// one create at a time per user, so concurrent creates can't all pass the cap
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('workflows:' || $1::text))`, caller.ID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_workflows`).Scan(&count); err != nil {
			return err
		} else if count >= MaxWorkflows {
			return network.NewConflictError(fmt.Sprintf("You have %d workflows; delete some first.", MaxWorkflows), nil)
		}
		problems, err := s.checkSave(ctx, tx, caller, doc)
		if err != nil {
			return err
		}
		row, err := one[model.UserWorkflow](ctx, tx, `INSERT INTO user_workflows (id, owner_id, title, family, env, nodes,
			studio, based_on) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+model.UserWorkflowCols,
			utility.NewID(), caller.ID, doc.Title, doc.Family, doc.Env, doc.Nodes, doc.Studio, body.BasedOn)
		out = s.mineDTO(row, &problems)
		return err
	})
	return out, err
}

// ReplaceWorkflow saves an edited document over one of the caller's workflows when body.Version is the stored one.
func (s *Service) ReplaceWorkflow(ctx context.Context, caller common.Caller, id string, body *dto.WorkflowUpdate) (dto.Workflow, error) {
	if err := s.ready(); err != nil {
		return dto.Workflow{}, err
	}
	if err := s.notTemplate(id); err != nil {
		return dto.Workflow{}, err
	}
	var out dto.Workflow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readWorkflow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if row.Version != body.Version {
			return network.NewConflictError(fmt.Sprintf("workflow changed since you opened it (now version %d)", row.Version), nil)
		}
		doc := body.WorkflowDoc
		doc.Title = strings.TrimSpace(doc.Title)
		problems, err := s.checkSave(ctx, tx, caller, doc)
		if err != nil {
			return err
		}
		row, err = one[model.UserWorkflow](ctx, tx, `UPDATE user_workflows SET title = $2, family = $3, env = $4, nodes = $5,
			studio = $6, version = version + 1, updated_at = clock_timestamp() WHERE id = $1 RETURNING `+model.UserWorkflowCols,
			id, doc.Title, doc.Family, doc.Env, doc.Nodes, doc.Studio)
		out = s.mineDTO(row, &problems)
		return err
	})
	return out, err
}

// RenameWorkflow sets the title of one of the caller's workflows (version + 1).
func (s *Service) RenameWorkflow(ctx context.Context, caller common.Caller, id string, body *dto.WorkflowPatch) (dto.Workflow, error) {
	if err := s.notTemplate(id); err != nil {
		return dto.Workflow{}, err
	}
	var out dto.Workflow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if !utility.IsUUID(id) {
			return errNotFound
		}
		row, err := one[model.UserWorkflow](ctx, tx, `UPDATE user_workflows SET title = $2, version = version + 1,
			updated_at = clock_timestamp() WHERE id = $1 RETURNING `+model.UserWorkflowCols, id, strings.TrimSpace(body.Title))
		out = s.mineDTO(row, nil)
		return err
	})
	return out, err
}

// DeleteWorkflow deletes one of the caller's workflows: 409 while a run of it is queued or running, or a batch of it is
// queued, running or paused. Runs, batches and takes keep their snapshots.
func (s *Service) DeleteWorkflow(ctx context.Context, caller common.Caller, id string) error {
	if err := s.notTemplate(id); err != nil {
		return err
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		// lock the workflow first, then look at its runs in new statements (DeleteSession explains why)
		if _, err := readWorkflow(ctx, tx, id, true); err != nil {
			return err
		}
		var runs, batches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE workflow_id = $1 AND status IN ('queued', 'running')),
			EXISTS (SELECT 1 FROM batches WHERE workflow_id = $1 AND status IN ('queued', 'running', 'paused'))`, id).
			Scan(&runs, &batches); err != nil {
			return err
		}
		switch {
		case runs:
			return network.NewConflictError("a run of this workflow is unfinished; cancel it or wait", nil)
		case batches:
			return network.NewConflictError("a batch of this workflow is unfinished; stop it or wait", nil)
		}
		_, err := tx.Exec(ctx, `DELETE FROM user_workflows WHERE id = $1`, id)
		return err
	})
}

// notTemplate is the 409 for a template id; any other non-UUID id is a 404 later.
func (s *Service) notTemplate(id string) error {
	if _, ok := s.registry.Workflow(id); ok && !utility.IsUUID(id) {
		return errTemplate
	}
	return nil
}

// ValidateWorkflow checks a document with the graph rules and saves nothing.
func (s *Service) ValidateWorkflow(ctx context.Context, caller common.Caller, doc dto.WorkflowDoc) (dto.WorkflowValidation, error) {
	if err := s.ready(); err != nil {
		return dto.WorkflowValidation{}, err
	}
	var problems []dto.Problem
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		problems, err = s.problems(ctx, tx, caller, doc)
		return err
	})
	return dto.WorkflowValidation{Problems: problems, CanSave: !blocksSave(problems), CanRun: len(problems) == 0}, err
}

// Nodes is the palette: every kind live hosts publish, grouped by family; env keeps the kinds that env publishes.
func (s *Service) Nodes(env string) (dto.NodePalette, error) {
	if err := s.ready(); err != nil {
		return dto.NodePalette{}, err
	}
	return dto.NodePalette{Groups: s.registry.PaletteGroups(time.Now(), env)}, nil
}

// cutRunes cuts text to at most n characters.
func cutRunes(text string, n int) string {
	runes := []rune(text)
	return string(runes[:min(n, len(runes))])
}
