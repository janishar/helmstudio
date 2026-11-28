package studio

import (
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

	"github.com/helmstudio/studio_service/api/magic"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// Magic boards (contracts/README.md "Magic", API 1.80; plan 39 phase C): a document edited by named ops (api/magic)
// under the row lock, all or nothing, tried first with dry_run, stored as revisions (the last 100 and every
// snapshot's) with the undo and redo pointers, as a sequence is.

// MaxBoards is how many Magic boards one user may have.
const MaxBoards = 100

type boardRow struct {
	ID, OwnerID, Name    string
	Doc                  json.RawMessage
	Revision             int
	CreatedAt, UpdatedAt time.Time
	SessionID            *string // API 1.81: made at its first run
	TitleSource          string  // API 1.91: default | prompt | user | model, as a sequence's name
	NamedBy              *string
	NameAsked            bool
}

const boardCols = `id::text, owner_id::text, name, doc, revision, created_at, updated_at, session_id::text,
	title_source, named_by, name_asked`

func (r boardRow) doc() magic.Doc {
	d, err := magic.Parse(r.Doc)
	if err != nil { // never: every stored document was checked
		return magic.Empty(magic.DefaultTarget)
	}
	return d
}

// MagicEditError is /edits' 422 for an op that cannot apply.
type MagicEditError struct {
	Detail  string `json:"detail"`
	OpIndex int    `json:"op_index"`
}

func (e *MagicEditError) Error() string { return e.Detail }
func (*MagicEditError) unprocessable()  {}

// readBoard reads one live board (lock: FOR UPDATE); unknown, malformed, deleted or another user's is 404.
func readBoard(ctx context.Context, tx pgx.Tx, id string, lock bool) (boardRow, error) {
	if !utility.IsUUID(id) {
		return boardRow{}, errNotFound
	}
	q := `SELECT ` + boardCols + ` FROM magic_boards WHERE id = $1 AND deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE`
	}
	return one[boardRow](ctx, tx, q, id)
}

func staleBoard(row boardRow) error {
	return network.NewConflictError(fmt.Sprintf("board changed since you opened it (now revision %d)", row.Revision), nil)
}

// checkTakes refuses take ids doc names that before did not unless they are the owner's ready, undeleted takes.
func checkTakes(ctx context.Context, tx pgx.Tx, ownerID string, before, doc magic.Doc) error {
	had := magic.TakeIDs(before)
	var fresh []string
	for _, t := range magic.TakeIDs(doc) {
		if !slices.Contains(had, t) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	type idRow struct{ ID string }
	found, err := collect[idRow](ctx, tx, `SELECT id::text FROM takes WHERE id = ANY($1::uuid[]) AND owner_id = $2
		AND deleted_at IS NULL AND ready`, fresh, ownerID)
	if err != nil {
		return err
	}
	for _, t := range fresh {
		if !slices.ContainsFunc(found, func(r idRow) bool { return r.ID == t }) {
			return network.NewUnprocessableError("take "+t+" not found", nil)
		}
	}
	return nil
}

// takesSince is the take ids the revisions after base added, ok only when every one of them is a run's or morph's
// take (studio's "take" write, magicFill / morphFill) and all are kept: an edit at base then applies on top.
func takesSince(ctx context.Context, tx pgx.Tx, row boardRow, base int) ([]string, bool, error) {
	if base > row.Revision {
		return nil, false, nil
	}
	var n int
	var onlyTakes *bool
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT count(*), bool_and(by = 'studio' AND ops_summary = 'take'),
		(SELECT doc FROM magic_board_revisions WHERE board_id = $1 AND revision = $2)
		FROM magic_board_revisions WHERE board_id = $1 AND revision > $2 AND revision <= $3`,
		row.ID, base, row.Revision).Scan(&n, &onlyTakes, &raw)
	if err != nil || n != row.Revision-base || onlyTakes == nil || !*onlyTakes || raw == nil {
		return nil, false, err
	}
	was, err := magic.Parse(raw)
	if err != nil {
		return nil, false, nil
	}
	had := magic.TakeIDs(was)
	var out []string
	for _, t := range magic.TakeIDs(row.doc()) {
		if !slices.Contains(had, t) {
			out = append(out, t)
		}
	}
	return out, true, nil
}

// boardPointers are a revision's undo_to and redo_to.
func boardPointers(ctx context.Context, tx pgx.Tx, id string, revision int) (*int, *int, error) {
	var undo, redo *int
	err := tx.QueryRow(ctx, `SELECT undo_to, redo_to FROM magic_board_revisions WHERE board_id = $1 AND revision = $2`,
		id, revision).Scan(&undo, &redo)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return undo, redo, err
}

// boardKept reports whether revision p of board id is kept.
func boardKept(ctx context.Context, tx pgx.Tx, id string, p *int) (bool, error) {
	if p == nil {
		return false, nil
	}
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM magic_board_revisions WHERE board_id = $1 AND revision = $2)`,
		id, *p).Scan(&found)
	return found, err
}

// boardDTO is the board as answered, with whether undo and redo can go and (API 1.81) its runs, takes and statuses.
func (s *Service) boardDTO(ctx context.Context, tx pgx.Tx, row boardRow) (dto.MagicBoard, error) {
	out := dto.MagicBoard{ID: row.ID, Name: row.Name, Revision: row.Revision, Doc: row.doc(), CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, TitleSource: row.TitleSource, NamedBy: row.NamedBy}
	undo, redo, err := boardPointers(ctx, tx, row.ID, row.Revision)
	if err == nil {
		out.CanUndo, err = boardKept(ctx, tx, row.ID, undo)
	}
	if err == nil {
		out.CanRedo, err = boardKept(ctx, tx, row.ID, redo)
	}
	if err == nil {
		err = s.boardExtras(ctx, tx, row, &out)
	}
	return out, err
}

// storeBoard writes doc as the next revision with its pointers and prunes revisions past the last 100 that no
// snapshot points at.
func storeBoard(ctx context.Context, tx pgx.Tx, row *boardRow, doc magic.Doc, w write, undo, redo *int) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	next := row.Revision + 1
	updated, err := one[boardRow](ctx, tx, `UPDATE magic_boards SET doc = $2, revision = $3, updated_at = clock_timestamp()
		WHERE id = $1 RETURNING `+boardCols, row.ID, data, next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO magic_board_revisions (board_id, owner_id, revision, doc, by, via, ops_summary,
		undo_to, redo_to) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9)`, row.ID, row.OwnerID, next, data, w.by,
		w.via, w.ops, undo, redo); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM magic_board_revisions r WHERE r.board_id = $1 AND r.revision <= $2
		AND NOT EXISTS (SELECT 1 FROM magic_snapshots s WHERE s.board_id = r.board_id AND s.revision = r.revision)
		AND NOT EXISTS (SELECT 1 FROM magic_sends m WHERE m.board_id = r.board_id AND m.revision = r.revision
			AND m.removed_at IS NULL)`, // API 1.82: a listed send's revision is kept
		row.ID, next-keptRevisions)
	*row = updated
	return err
}

// ListMagicBoards is a page of the caller's boards, newest updated first; API 1.92: q matches the name.
func (s *Service) ListMagicBoards(ctx context.Context, caller common.Caller, page Page) (dto.MagicBoardList, error) {
	out := dto.MagicBoardList{Boards: []dto.MagicBoardSummary{}}
	args := sqlArgs{}
	match := matchSQL(page.Words, &args, `name`)
	tail, err := keysetSQL(page, &args, `updated_at`, `id`)
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[boardRow](ctx, tx, `SELECT `+boardCols+` FROM magic_boards WHERE deleted_at IS NULL`+match+tail, args...)
		rows, out.NextCursor = pageOf(rows, page.Limit, func(r boardRow) (time.Time, string) { return r.UpdatedAt, r.ID })
		for _, row := range rows {
			d := row.doc()
			out.Boards = append(out.Boards, dto.MagicBoardSummary{ID: row.ID, Name: row.Name, Revision: row.Revision,
				Lanes: len(d.Lanes), Blocks: len(d.Blocks), Frames: d.Duration(), FPS: d.Target.FPS, UpdatedAt: row.UpdatedAt})
		}
		return err
	})
	return out, err
}

// CreateMagicBoard stores a new board (revision 1): empty at body.Target (else the default), or body.Doc checked.
func (s *Service) CreateMagicBoard(ctx context.Context, caller common.Caller, body *dto.MagicBoardCreate) (dto.MagicBoard, error) {
	var out dto.MagicBoard
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		// the cap holds for two creates at once (as the library's)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('magic_boards:' || $1::text))`, caller.ID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM magic_boards WHERE deleted_at IS NULL`).Scan(&n); err != nil {
			return err
		}
		if n >= MaxBoards {
			return network.NewConflictError(fmt.Sprintf("You have %d boards; delete some first.", MaxBoards), nil)
		}
		doc := magic.Empty(orElse(body.Target, magic.DefaultTarget))
		if body.Doc != nil {
			doc = body.Doc.Clone()
			magic.SortBlocks(&doc)
			if why := magic.Check(doc); why != "" {
				return network.NewUnprocessableError("doc: "+why, nil)
			}
			if err := checkTakes(ctx, tx, caller.ID, magic.Doc{}, doc); err != nil {
				return err
			}
		}
		name, source := "Untitled board", "default"
		if body.Name != nil {
			name, source = strings.TrimSpace(*body.Name), "user"
		}
		data, _ := json.Marshal(doc)
		row, err := one[boardRow](ctx, tx, `INSERT INTO magic_boards (id, owner_id, name, doc, title_source)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+boardCols, utility.NewID(), caller.ID, name, data, source)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO magic_board_revisions (board_id, owner_id, revision, doc, by, via, ops_summary)
			VALUES ($1, $2, 1, $3, 'user', $4, 'create')`, row.ID, caller.ID, data, common.ViaOf(caller.Via)); err != nil {
			return err
		}
		if err := askBoardName(ctx, tx, s.remoteOn, &row); err != nil { // API 1.91
			return err
		}
		out, err = s.boardDTO(ctx, tx, row)
		return err
	})
	return out, err
}

// GetMagicBoard is one board at its current revision.
func (s *Service) GetMagicBoard(ctx context.Context, caller common.Caller, id string) (dto.MagicBoard, error) {
	var out dto.MagicBoard
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, false)
		if err == nil {
			out, err = s.boardDTO(ctx, tx, row)
		}
		return err
	})
	return out, err
}

// GetMagicAgent is GET …/{id}?view=agent.
func (s *Service) GetMagicAgent(ctx context.Context, caller common.Caller, id string) (dto.MagicAgentView, error) {
	var out dto.MagicAgentView
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, false)
		if err != nil {
			return err
		}
		d := row.doc()
		out = dto.MagicAgentView{ID: row.ID, Name: row.Name, Revision: row.Revision, Agent: magic.AgentView(d)}
		secs, err := takeSeconds(ctx, tx, row.OwnerID, d)
		if err != nil {
			return err
		}
		modes := magic.JoinModes(d, secs) // API 1.106: a sound join's blend or dip
		for i := range out.Transitions {
			out.Transitions[i].Mode = modes[out.Transitions[i].ID]
		}
		runs, err := boardRuns(ctx, tx, row.ID) // API 1.81: the derived status
		status := blockStatuses(d, runs)
		for i := range out.Blocks {
			st := status[out.Blocks[i].ID]
			out.Blocks[i].Status, out.Blocks[i].Error, out.Blocks[i].Turns = st.Status, st.Error, st.Turns // API 1.93
		}
		return err
	})
	return out, err
}

// takeSeconds is a lookup of the seconds of d's takes (API 1.106: whether a sound join blends).
func takeSeconds(ctx context.Context, tx pgx.Tx, ownerID string, d magic.Doc) (func(string) (float64, bool), error) {
	rules, _, err := loadTakes(ctx, tx, ownerID, magic.TakeIDs(d))
	return func(id string) (float64, bool) {
		t, ok := rules[id]
		return t.DurationS, ok && t.DurationS > 0
	}, err
}

// editBoard runs fn on the locked board and answers it as it is then.
func (s *Service) editBoard(ctx context.Context, caller common.Caller, id string, fn func(tx pgx.Tx, row *boardRow) error) (dto.MagicBoard, error) {
	var out dto.MagicBoard
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err == nil {
			err = fn(tx, &row)
		}
		if err == nil { // API 1.91: an undo, redo or restore may bring a prompt
			err = askBoardName(ctx, tx, s.remoteOn, &row)
		}
		if err == nil {
			out, err = s.boardDTO(ctx, tx, row)
		}
		return err
	})
	return out, err
}

// PatchMagicBoard renames the board (no revision): the name is the user's (API 1.91), so the namer never replaces it,
// nor its session's title.
func (s *Service) PatchMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicBoardPatch) (dto.MagicBoard, error) {
	return s.editBoard(ctx, caller, id, func(tx pgx.Tx, row *boardRow) error {
		if row.SessionID != nil {
			if _, err := tx.Exec(ctx, `UPDATE sessions SET title_source = 'user', named_by = NULL WHERE id = $1
				AND title_source IN ('default', 'prompt')`, *row.SessionID); err != nil {
				return err
			}
		}
		updated, err := one[boardRow](ctx, tx, `UPDATE magic_boards SET name = $2, title_source = 'user', named_by = NULL,
			updated_at = clock_timestamp() WHERE id = $1 RETURNING `+boardCols, row.ID, strings.TrimSpace(*body.Name))
		*row = updated
		return err
	})
}

// DeleteMagicBoard soft-deletes the board with its revisions and snapshots.
func (s *Service) DeleteMagicBoard(ctx context.Context, caller common.Caller, id string) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readBoard(ctx, tx, id, true); err != nil {
			return err
		}
		for _, q := range []string{`UPDATE magic_boards SET deleted_at = clock_timestamp() WHERE id = $1`,
			`DELETE FROM magic_snapshots WHERE board_id = $1`, `DELETE FROM magic_board_revisions WHERE board_id = $1`} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// EditMagicBoard applies body.Ops in order to the board at body.BaseRevision and stores the result as one user
// revision, or (dry run) answers it without storing.
func (s *Service) EditMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicEdits) (dto.MagicEditResult, error) {
	var out dto.MagicEditResult
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		var arrived []string // API 1.103: takes runs added since base_revision, which the edit must keep
		if body.BaseRevision != row.Revision {
			var ok bool
			if arrived, ok, err = takesSince(ctx, tx, row, body.BaseRevision); err != nil {
				return err
			} else if !ok {
				return staleBoard(row)
			}
		}
		before := row.doc()
		doc := before
		for i, op := range body.Ops {
			r := magic.Apply(doc, op)
			if r.Error != "" {
				return &MagicEditError{Detail: r.Error, OpIndex: i}
			}
			doc = r.Doc
		}
		if kept := magic.TakeIDs(doc); slices.ContainsFunc(arrived, func(t string) bool { return !slices.Contains(kept, t) }) {
			return staleBoard(row) // the edit drops a take its caller never saw
		}
		if why := magic.Check(doc); why != "" {
			return network.NewUnprocessableError(why, nil)
		}
		if err := checkTakes(ctx, tx, row.OwnerID, before, doc); err != nil {
			return err
		}
		secs, err := takeSeconds(ctx, tx, row.OwnerID, doc)
		if err != nil {
			return err
		}
		out = dto.MagicEditResult{Revision: row.Revision, DryRun: body.DryRun, Diff: magic.DiffDocs(before, doc),
			Warnings: append(magic.Warnings(doc), magic.DipWarnings(doc, secs)...)}
		if body.DryRun {
			out.Board, err = s.boardDTO(ctx, tx, row)
			out.Board.Doc = doc // what it would be: nothing is written
			return err
		}
		var names []string
		for _, op := range body.Ops {
			if !slices.Contains(names, op.Name) {
				names = append(names, op.Name)
			}
		}
		summary := strings.Join(names, ", ")
		if common.ViaOf(caller.Via) == "assistant" { // API 1.87: History says what the Assistant changed
			summary = magic.Describe(before, doc)
		}
		undo := row.Revision
		if err := storeBoard(ctx, tx, &row, doc, userWrite(caller, summary), &undo, nil); err != nil {
			return err
		}
		if err := askBoardName(ctx, tx, s.remoteOn, &row); err != nil { // API 1.91
			return err
		}
		out.Revision = row.Revision
		out.Board, err = s.boardDTO(ctx, tx, row)
		return err
	})
	return out, err
}

// ListMagicRevisions is the kept revisions below before (nil: all), newest first, at most limit.
func (s *Service) ListMagicRevisions(ctx context.Context, caller common.Caller, id string, before *int, limit int) (dto.MagicRevisionList, error) {
	out := dto.MagicRevisionList{Revisions: []dto.MagicRevisionItem{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readBoard(ctx, tx, id, false); err != nil {
			return err
		}
		rows, err := collect[dto.MagicRevisionItem](ctx, tx, `SELECT revision, by, via, saved_at, ops_summary,
			coalesce(jsonb_array_length(doc->'blocks'), 0) FROM magic_board_revisions
			WHERE board_id = $1 AND ($2::int IS NULL OR revision < $2) ORDER BY revision DESC LIMIT $3`, id, before, limit)
		out.Revisions = append(out.Revisions, rows...)
		return err
	})
	return out, err
}

// UndoMagicBoard writes the revision undo goes back to as the next one.
func (s *Service) UndoMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicRevision) (dto.MagicBoard, error) {
	return s.restoreBoard(ctx, caller, id, body.Revision, true, nil)
}

// RedoMagicBoard writes the revision redo goes forward to as the next one.
func (s *Service) RedoMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicRevision) (dto.MagicBoard, error) {
	return s.restoreBoard(ctx, caller, id, body.Revision, false, nil)
}

// RestoreMagicBoard writes kept revision body.To forward as the next one.
func (s *Service) RestoreMagicBoard(ctx context.Context, caller common.Caller, id string, body *dto.MagicRestore) (dto.MagicBoard, error) {
	return s.restoreBoard(ctx, caller, id, body.Revision, false, &body.To)
}

// restoreBoard writes a kept revision forward: undo's (undo true), redo's, or the given one (target), as the
// Timeline's restore does.
func (s *Service) restoreBoard(ctx context.Context, caller common.Caller, id string, revision int, undo bool, target *int) (dto.MagicBoard, error) {
	return s.editBoard(ctx, caller, id, func(tx pgx.Tx, row *boardRow) error {
		if revision != row.Revision {
			return staleBoard(*row)
		}
		curUndo, curRedo, err := boardPointers(ctx, tx, row.ID, row.Revision)
		if err != nil {
			return err
		}
		to, nothing := curRedo, "nothing to redo"
		switch {
		case target != nil:
			to = target
		case undo:
			to, nothing = curUndo, "nothing to undo"
		}
		if ok, err := boardKept(ctx, tx, row.ID, to); err != nil || !ok {
			switch {
			case err != nil:
			case target != nil:
				err = network.NewUnprocessableError(fmt.Sprintf("revision %d is not kept", *target), nil)
			default:
				err = network.NewConflictError(nothing, nil)
			}
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT doc FROM magic_board_revisions WHERE board_id = $1 AND revision = $2`,
			row.ID, *to).Scan(&raw); err != nil {
			return err
		}
		doc, err := magic.Parse(raw)
		if err == nil { // API 1.81: an undo, redo or restore never takes a run's take back
			doc, err = withRunTakes(ctx, tx, row.ID, doc)
		}
		if err == nil { // API 1.82: nor a morph's
			doc, err = withMorphTakes(ctx, tx, row.ID, doc)
		}
		if err != nil {
			return err
		}
		toUndo, toRedo, err := boardPointers(ctx, tx, row.ID, *to)
		if err != nil {
			return err
		}
		current := row.Revision
		switch {
		case target != nil:
			return storeBoard(ctx, tx, row, doc, userWrite(caller, "restore"), &current, nil)
		case undo:
			return storeBoard(ctx, tx, row, doc, userWrite(caller, "undo"), toUndo, &current)
		}
		return storeBoard(ctx, tx, row, doc, userWrite(caller, "redo"), &current, toRedo)
	})
}

// ------------------------------------------------------------------ snapshots

const snapshotCols = `s.id::text, s.name, s.revision, s.made_at, coalesce(jsonb_array_length(r.doc->'blocks'), 0)`

// ListMagicSnapshots is the board's snapshots, newest first.
func (s *Service) ListMagicSnapshots(ctx context.Context, caller common.Caller, id string) (dto.MagicSnapshotList, error) {
	out := dto.MagicSnapshotList{Snapshots: []dto.MagicSnapshot{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readBoard(ctx, tx, id, false); err != nil {
			return err
		}
		rows, err := collect[dto.MagicSnapshot](ctx, tx, `SELECT `+snapshotCols+` FROM magic_snapshots s
			LEFT JOIN magic_board_revisions r ON r.board_id = s.board_id AND r.revision = s.revision
			WHERE s.board_id = $1 ORDER BY s.made_at DESC, s.id`, id)
		out.Snapshots = append(out.Snapshots, rows...)
		return err
	})
	return out, err
}

func readSnapshot(ctx context.Context, tx pgx.Tx, boardID, id string) (dto.MagicSnapshot, error) {
	if !utility.IsUUID(id) {
		return dto.MagicSnapshot{}, errNotFound
	}
	return one[dto.MagicSnapshot](ctx, tx, `SELECT `+snapshotCols+` FROM magic_snapshots s
		LEFT JOIN magic_board_revisions r ON r.board_id = s.board_id AND r.revision = s.revision
		WHERE s.board_id = $1 AND s.id = $2`, boardID, id)
}

// CreateMagicSnapshot names a kept revision (absent: the current one).
func (s *Service) CreateMagicSnapshot(ctx context.Context, caller common.Caller, id string, body *dto.MagicSnapshotCreate) (dto.MagicSnapshot, error) {
	var out dto.MagicSnapshot
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readBoard(ctx, tx, id, true)
		if err != nil {
			return err
		}
		revision := orElse(body.Revision, row.Revision)
		if ok, err := boardKept(ctx, tx, row.ID, &revision); err != nil || !ok {
			if err == nil {
				err = network.NewUnprocessableError(fmt.Sprintf("revision %d is not kept", revision), nil)
			}
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM magic_snapshots WHERE board_id = $1`, row.ID).Scan(&n); err != nil {
			return err
		}
		name := fmt.Sprintf("Snapshot %d", n+1)
		if body.Name != nil {
			name = strings.TrimSpace(*body.Name)
		}
		snapID := utility.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO magic_snapshots (id, board_id, owner_id, name, revision) VALUES ($1, $2, $3, $4, $5)`,
			snapID, row.ID, row.OwnerID, name, revision); err != nil {
			return err
		}
		out, err = readSnapshot(ctx, tx, row.ID, snapID)
		return err
	})
	return out, err
}

// PatchMagicSnapshot renames a snapshot.
func (s *Service) PatchMagicSnapshot(ctx context.Context, caller common.Caller, id, snapID string, body *dto.MagicSnapshotPatch) (dto.MagicSnapshot, error) {
	var out dto.MagicSnapshot
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readBoard(ctx, tx, id, false); err != nil {
			return err
		}
		if _, err := readSnapshot(ctx, tx, id, snapID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE magic_snapshots SET name = $2 WHERE id = $1`, snapID, strings.TrimSpace(*body.Name)); err != nil {
			return err
		}
		var err error
		out, err = readSnapshot(ctx, tx, id, snapID)
		return err
	})
	return out, err
}

// DeleteMagicSnapshot deletes a snapshot; its revision is pruned with the others from then on.
func (s *Service) DeleteMagicSnapshot(ctx context.Context, caller common.Caller, id, snapID string) error {
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, err := readBoard(ctx, tx, id, false); err != nil {
			return err
		}
		if _, err := readSnapshot(ctx, tx, id, snapID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM magic_snapshots WHERE id = $1`, snapID)
		return err
	})
}
