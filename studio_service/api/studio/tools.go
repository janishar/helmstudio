package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// Agent tools studio serves (contracts/README.md "Tools", API 1.15; messages/req.studio.tools): gallery.search,
// library.read, rows.add and rows.edit, each strictly as the batch's owner: the owner comes from the batch record and
// must equal the request's (a forged owner is not_found), every read runs under row-level security as that owner, and
// a row goes through Submit's checks. Idempotent on call_id: the first final reply is kept (tool_calls).

const (
	// MaxAddedRows is how many rows agents may add to one batch.
	MaxAddedRows = 100
	// MaxToolResults caps gallery.search and library.read.
	MaxToolResults = 20
)

// inputName is a pipeline input's name.
var inputName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// callID is a tool call's id: <the turn's idempotency key>:<call index> (req.studio.tools call_id).
var callID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:(0|[1-9][0-9]{0,2}):[1-9][0-9]{0,3}:[a-z][a-z0-9_]{0,63}:(0|[1-9][0-9]{0,4}):t[0-9]{1,2}r?:[0-3]$`)

// toolErr is a refusal told to the model: {error, code}.
func toolErr(code, format string, args ...any) message.ToolsReply {
	text := fmt.Sprintf(format, args...)
	if utf8.RuneCountInString(text) > 2000 {
		text = string([]rune(text)[:2000])
	}
	return message.ToolsReply{Error: text, Code: code}
}

// strictArgs decodes a tool's args into out, closed (an unknown field is invalid).
func strictArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

// Tool answers one studio.tools request.
func (s *Service) Tool(ctx context.Context, req *message.ToolsRequest) message.ToolsReply {
	if !utility.IsUUID(req.OwnerID) || !utility.IsUUID(req.BatchID) || req.Row < 0 || req.Row > MaxRows ||
		!callID.MatchString(req.CallID) || !strings.HasPrefix(req.CallID, fmt.Sprintf("%s:%d:", req.BatchID, req.Row)) ||
		strings.Split(req.CallID, ":")[3] != req.Node {
		return toolErr("invalid", "owner_id, batch_id, row, node and call_id (<turn key>:<index> of this batch, row and node) are required")
	}
	if !s.registry.Ready() {
		return toolErr("internal", "studio is starting; try again")
	}
	// the batch and its owner as stored: never the request's word
	var stored storedPipeline
	var owner, status, control string
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT owner_id::text, status, control, pipeline FROM batches WHERE id = $1`, req.BatchID).
			Scan(&owner, &status, &control, &raw)
		if err != nil {
			return err
		}
		if raw == nil {
			return errNotFound
		}
		return json.Unmarshal(raw, &stored)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errNotFound) || (err == nil && owner != req.OwnerID):
		return toolErr("not_found", "not found")
	case err != nil:
		log.Printf("studio.tools %s: %v", req.CallID, err)
		return toolErr("internal", "internal error")
	}
	node, known := stored.Doc.Nodes[req.Node]
	switch {
	case !known || node.Kind != "agent" || !slices.Contains(node.Tools, req.Tool):
		return toolErr("invalid", "%s does not declare %s", req.Node, req.Tool)
	case req.Row == 0 && !node.IsDirector():
		return toolErr("invalid", "row 0 is the director's chat")
	case req.Tool == "rows.edit" && req.Row != 0:
		return toolErr("invalid", "rows.edit is only for the director's chat")
	}
	caller := common.Caller{ID: owner} // a plain user: row-level security limits everything to the owner
	var out message.ToolsReply
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		var first []byte
		if err := tx.QueryRow(ctx, `SELECT reply FROM tool_calls WHERE call_id = $1 AND batch_id = $2`, req.CallID,
			req.BatchID).Scan(&first); err == nil {
			return json.Unmarshal(first, &out) // asked again: the first reply, nothing done twice
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if req.Row > 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM batch_rows WHERE batch_id = $1 AND n = $2)`, req.BatchID,
				req.Row).Scan(&exists); err != nil {
				return err
			} else if !exists {
				out = toolErr("not_found", "not found")
				return nil
			}
		}
		var err error
		switch req.Tool {
		case "gallery.search":
			out, err = gallerySearch(ctx, tx, req.Args)
		case "library.read":
			out, err = s.libraryRead(ctx, caller, req.Args)
		case "rows.add", "rows.edit":
			if ended(status) || control == "stop" {
				out = toolErr("conflict", "the batch has ended")
				return nil
			}
			out, err = s.rowsTool(ctx, tx, stored, req)
		default:
			out = toolErr("invalid", "studio does not serve %s", req.Tool)
		}
		if err != nil || out.Code == "internal" {
			return err
		}
		// ponytail: a rejected row (invalid, cap, conflict) is final too, so its reply is kept like a result
		_, err = tx.Exec(ctx, `INSERT INTO tool_calls (call_id, batch_id, owner_id, tool, reply) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (call_id) DO NOTHING`, req.CallID, req.BatchID, owner, req.Tool, toJSON(out))
		return err
	})
	if err != nil {
		log.Printf("studio.tools %s: %v", req.CallID, err)
		return toolErr("internal", "internal error")
	}
	return out
}

// gallerySearch is gallery.search: the owner's completed, undeleted takes (tx is the owner's), newest first.
func gallerySearch(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (message.ToolsReply, error) {
	var args struct {
		Text       *string `json:"text"`
		Kind       *string `json:"kind"`
		Collection *string `json:"collection"`
		Starred    *bool   `json:"starred"`
		Limit      *int    `json:"limit"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return toolErr("invalid", "args: %v", err), nil
	}
	limit := 10
	switch {
	case args.Text != nil && utf8.RuneCountInString(*args.Text) > 200:
		return toolErr("invalid", "text: at most 200 characters"), nil
	case args.Kind != nil && *args.Kind != "image" && *args.Kind != "video":
		return toolErr("invalid", "kind must be image or video"), nil
	case args.Collection != nil && (*args.Collection == "" || utf8.RuneCountInString(*args.Collection) > 80):
		return toolErr("invalid", "collection must be 1-80 characters"), nil
	case args.Limit != nil && (*args.Limit < 1 || *args.Limit > MaxToolResults):
		return toolErr("invalid", "limit must be 1 to 20"), nil
	case args.Limit != nil:
		limit = *args.Limit
	}
	conds, params := []string{`t.deleted_at IS NULL AND t.ready AND t.kind IN ('image', 'video')`}, []any{}
	where := func(cond string, value any) {
		params = append(params, value)
		conds = append(conds, strings.ReplaceAll(cond, "$?", fmt.Sprintf("$%d", len(params))))
	}
	if args.Text != nil && *args.Text != "" {
		where(`(t.title ILIKE $? OR t.made_with->>'prompt' ILIKE $?)`, "%"+likeEscape(*args.Text)+"%")
	}
	if args.Kind != nil {
		where(`t.kind = $?`, *args.Kind)
	}
	if args.Starred != nil {
		where(`t.starred = $?`, *args.Starred)
	}
	if args.Collection != nil {
		where(`b.collection = $?`, *args.Collection)
	}
	params = append(params, limit+1)
	type found struct {
		ID         string
		Kind       string
		Title      string
		Prompt     *string
		Width      *int
		Height     *int
		Collection *string
		Starred    bool
		CreatedAt  time.Time
	}
	rows, err := collect[found](ctx, tx, `SELECT t.id::text, t.kind, t.title, t.made_with->>'prompt', t.width, t.height,
		b.collection, t.starred, t.created_at FROM takes t LEFT JOIN batches b ON b.id = t.batch_id
		WHERE `+strings.Join(conds, " AND ")+fmt.Sprintf(` ORDER BY t.created_at DESC, t.id DESC LIMIT $%d`, len(params)), params...)
	if err != nil {
		return message.ToolsReply{}, err
	}
	takes := []map[string]any{}
	for i, r := range rows {
		if i == limit {
			break
		}
		if r.Prompt != nil {
			*r.Prompt = cutRunes(*r.Prompt, 500)
		}
		takes = append(takes, map[string]any{"take_id": r.ID, "kind": r.Kind, "title": cutRunes(r.Title, 200), "prompt": r.Prompt,
			"width": r.Width, "height": r.Height, "collection": r.Collection, "starred": r.Starred,
			"created_at": r.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return message.ToolsReply{Result: map[string]any{"takes": takes, "more": len(rows) > limit}}, nil
}

// likeEscape makes text match itself in ILIKE.
func likeEscape(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}

// libraryRead is library.read: the owner's elements and the looks the owner can use, by kind, group and name.
func (s *Service) libraryRead(ctx context.Context, caller common.Caller, raw json.RawMessage) (message.ToolsReply, error) {
	var args struct {
		Kind  *string `json:"kind"`
		Group *string `json:"group"`
		Text  *string `json:"text"`
		Limit *int    `json:"limit"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return toolErr("invalid", "args: %v", err), nil
	}
	limit := 10
	if args.Limit != nil {
		if *args.Limit < 1 || *args.Limit > MaxToolResults {
			return toolErr("invalid", "limit must be 1 to 20"), nil
		}
		limit = *args.Limit
	}
	if args.Text != nil && utf8.RuneCountInString(*args.Text) > 100 {
		return toolErr("invalid", "text: at most 100 characters"), nil
	}
	query, detail := ParseLibraryQuery(deref(args.Kind), deref(args.Group), deref(args.Text), fmt.Sprint(limit+1))
	if detail != "" {
		return toolErr("invalid", "%s", detail), nil
	}
	list, err := s.ListLibrary(ctx, caller, query)
	if err != nil {
		return message.ToolsReply{}, err
	}
	items := []map[string]any{}
	for i, item := range list.Items {
		if i == limit {
			break
		}
		handle := item.Handle
		if item.Kind == "look" {
			handle = nil
		}
		items = append(items, map[string]any{"id": item.ID, "kind": item.Kind, "group": item.Group, "name": cutRunes(item.Name, 80),
			"handle": handle, "text": cutRunes(item.Text, 500)})
	}
	return message.ToolsReply{Result: map[string]any{"items": items, "more": len(list.Items) > limit}}, nil
}

// rowsTool is rows.add (append one row) and rows.edit (merge inputs into a row not yet started), each through Submit's
// checks for that row; tx is the owner's. The reply carries the stored row for pipelines to start or update its
// thread, and the row event is appended for the run table.
func (s *Service) rowsTool(ctx context.Context, tx pgx.Tx, stored storedPipeline, req *message.ToolsRequest) (message.ToolsReply, error) {
	var args struct {
		Row    *int           `json:"row"`
		Inputs map[string]any `json:"inputs"`
	}
	if err := strictArgs(req.Args, &args); err != nil {
		return toolErr("invalid", "args: %v", err), nil
	}
	edit := req.Tool == "rows.edit"
	switch {
	case len(args.Inputs) < 1 || len(args.Inputs) > 16:
		return toolErr("invalid", "inputs: 1 to 16 inputs by name"), nil
	case edit && (args.Row == nil || *args.Row < 1 || *args.Row > MaxRows):
		return toolErr("invalid", "row: a row number from 1"), nil
	case !edit && args.Row != nil:
		return toolErr("invalid", "args: unknown field row"), nil
	}
	for name, value := range args.Inputs {
		if text, ok := value.(string); !inputName.MatchString(name) || (ok && utf8.RuneCountInString(text) > 4000) {
			return toolErr("invalid", "inputs.%s: a name like prompt and a text of at most 4000 characters", name), nil
		}
	}
	// the batch is locked: one row change at a time, and the numbers stay dense
	var added, count int
	if err := tx.QueryRow(ctx, `SELECT rows_added, (SELECT count(*) FROM batch_rows WHERE batch_id = $1) FROM batches
		WHERE id = $1 FOR UPDATE`, req.BatchID).Scan(&added, &count); err != nil {
		return message.ToolsReply{}, err
	}
	root := stored.root()
	inputs := map[string]any{}
	n, seeds := count+1, map[string]int64{}
	if edit {
		n = *args.Row
		var status string
		var started bool
		err := tx.QueryRow(ctx, `SELECT br.status, br.run_id IS NOT NULL OR EXISTS (SELECT 1 FROM runs r WHERE r.batch_id = br.batch_id
			AND r.batch_row = br.n), br.inputs, br.seeds FROM batch_rows br WHERE br.batch_id = $1 AND br.n = $2 FOR UPDATE`,
			req.BatchID, n).Scan(&status, &started, &inputs, &seeds)
		if errors.Is(err, pgx.ErrNoRows) {
			return toolErr("not_found", "row %d not found", n), nil
		} else if err != nil {
			return message.ToolsReply{}, err
		}
		if status != "queued" || started {
			return toolErr("conflict", "row %d has started; only rows not yet started can change", n), nil
		}
	} else {
		switch {
		case added >= MaxAddedRows:
			return toolErr("cap", "agents have added %d rows to this batch; at most %d", added, MaxAddedRows), nil
		case count >= MaxRows:
			return toolErr("cap", "the batch has %d rows; at most %d", count, MaxRows), nil
		}
	}
	for name, value := range args.Inputs {
		inputs[root+"."+name] = value
	}
	detail, err := s.checkRow(ctx, tx, stored, req.BatchID, req.OwnerID, inputs, n)
	if err != nil {
		return message.ToolsReply{}, err
	} else if detail != "" {
		return toolErr("invalid", "row: %s", detail), nil
	}
	drawn := map[string]int64{}
	for _, step := range stored.steps() {
		if stored.SeedParams[step] != "" {
			old, had := seeds[step]
			drawn[step] = stored.baseSeed(step, inputs, func() int64 {
				if had {
					return old // an edit keeps a drawn seed
				}
				return randomBase()
			})
		}
	}
	if edit {
		_, err = tx.Exec(ctx, `UPDATE batch_rows SET inputs = $3, seeds = $4 WHERE batch_id = $1 AND n = $2`, req.BatchID, n,
			inputs, drawn)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO batch_rows (batch_id, n, owner_id, inputs, seeds, cells) VALUES ($1, $2, $3, $4, $5, '[]')`,
			req.BatchID, n, req.OwnerID, inputs, drawn)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE batches SET rows_added = rows_added + 1 WHERE id = $1`, req.BatchID)
		}
	}
	if err == nil {
		err = batchChanged(ctx, tx, req.BatchID, n)
	}
	if err != nil {
		return message.ToolsReply{}, err
	}
	return message.ToolsReply{Result: map[string]any{"row": n, "status": "queued"},
		Row: &message.ToolsRow{Row: n, Inputs: inputs, Seeds: drawn}}, nil
}

// checkRow is Submit's checks for one row of a running batch (row n): every input of its type (typedRow, in place),
// the owner's takes, each step's graph resolving with it, its mentions among the batch's elements, and the batch's
// inputs within MaxBatchInputs. The detail is the refusal's ("inputs.prompt: a value is required").
func (s *Service) checkRow(ctx context.Context, tx pgx.Tx, stored storedPipeline, batchID, ownerID string, inputs map[string]any, n int) (string, error) {
	if detail := typedRow(stored.inputTypes(), inputs, randomBase); detail != "" {
		return detail, nil
	}
	var refs []imageAt
	for _, key := range slices.Sorted(maps.Keys(inputs)) {
		if ref, ok := inputs[key].(map[string]any); ok && stored.inputTypes()[key] == "Image" {
			refs = append(refs, imageAt{Path: key, TakeID: fmt.Sprint(ref["take_id"])})
		}
	}
	bad, err := vetTakes(ctx, tx, s.store, ownerID, refs, false)
	if err != nil {
		return "", err
	} else if detail := firstBad(refs, bad); detail != "" {
		return detail, nil
	}
	var copied []element
	var size int
	if err := tx.QueryRow(ctx, `SELECT coalesce(elements, '[]'), length(pipeline::text) + coalesce((SELECT sum(length(inputs::text))
		FROM batch_rows WHERE batch_id = $1 AND n <> $2), 0) FROM batches WHERE id = $1`, batchID, n).Scan(&copied, &size); err != nil {
		return "", err
	}
	var graphs []stepGraph
	for _, step := range stored.steps() {
		workflow := stored.Workflows[stored.Doc.Nodes[step].Workflow]
		nodes, detail := resolve(workflow, stored.stepInputs(step, inputs, 0))
		if detail != "" {
			return step + ": " + detail, nil
		}
		graphs = append(graphs, stepGraph{step, nodes, workflow.NodeSchemas, workflow.Env})
	}
	for _, g := range graphs {
		for _, p := range prompts(g.Nodes, g.Schemas) {
			for _, handle := range mentioned(p.Parts) {
				if !slices.ContainsFunc(copied, func(e element) bool { return e.Handle == handle }) {
					return fmt.Sprintf("%s: %s.%s: @%s is not an element this batch was started with", g.Where, p.Node, p.Param.Name, handle), nil
				}
			}
		}
	}
	if size+len(toJSON(inputs)) > MaxBatchInputs {
		return "batch inputs are too large; use a shorter row", nil
	}
	return "", nil
}
