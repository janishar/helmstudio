// Package studio: every /v1/studio route — sessions, workflows (from the registry), runs (outbox → cmd.run.execute,
// cancel through the KV cancels), the event projector and stream, hosts — plus the lease sweep.
package studio

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/orago/network"
	"github.com/helmstudio/orago/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// errNotFound answers an unknown, malformed or another user's id alike.
var errNotFound = network.NewNotFoundError("not found", nil)

// Cancel asks the runner to stop a run: it writes the run id as a key in the KV cancels.
type Cancel func(ctx context.Context, runID string) error

// Service owns the studio tables. registry is what the runners publish, signer turns output keys into links, cancel
// reaches the runners.
type Service struct {
	db       *pgxpool.Pool
	signer   *common.Signer
	registry *registry.Registry
	cancel   Cancel
}

// NewService returns the service.
func NewService(db *pgxpool.Pool, signer *common.Signer, registry *registry.Registry, cancel Cancel) *Service {
	return &Service{db: db, signer: signer, registry: registry, cancel: cancel}
}

// tx runs fn in a transaction scoped to the caller as a plain user: an admin sees only their own rows here too.
func (s *Service) tx(ctx context.Context, caller common.Caller, fn func(tx pgx.Tx) error) error {
	return common.WithUser(ctx, s.db, common.Caller{ID: caller.ID}, fn)
}

// collect runs query and scans every row into T by position.
func collect[T any](ctx context.Context, tx pgx.Tx, query string, args ...any) ([]T, error) {
	rows, _ := tx.Query(ctx, query, args...)
	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

// one runs query and scans its row into T; no row is errNotFound.
func one[T any](ctx context.Context, tx pgx.Tx, query string, args ...any) (T, error) {
	rows, _ := tx.Query(ctx, query, args...)
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[T])
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	return row, err
}

// ------------------------------------------------------------------ sessions

// CreateSession makes an empty session.
func (s *Service) CreateSession(ctx context.Context, caller common.Caller, body *dto.SessionCreate) (dto.Session, error) {
	var out dto.Session
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[model.Session](ctx, tx, `INSERT INTO sessions (id, owner_id, title) VALUES ($1, $2, $3)
			RETURNING `+model.SessionCols, utility.NewID(), caller.ID, body.TitleOrDefault())
		out = sessionDTO(row)
		return err
	})
	return out, err
}

// ListSessions returns the caller's newest 100 sessions.
func (s *Service) ListSessions(ctx context.Context, caller common.Caller) (dto.SessionList, error) {
	out := dto.SessionList{Sessions: []dto.Session{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.Session](ctx, tx, `SELECT `+model.SessionCols+` FROM sessions
			ORDER BY created_at DESC LIMIT 100`)
		for _, row := range rows {
			out.Sessions = append(out.Sessions, sessionDTO(row))
		}
		return err
	})
	return out, err
}

// GetSession returns one session.
func (s *Service) GetSession(ctx context.Context, caller common.Caller, id string) (dto.Session, error) {
	if !utility.IsUUID(id) {
		return dto.Session{}, errNotFound
	}
	var out dto.Session
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[model.Session](ctx, tx, `SELECT `+model.SessionCols+` FROM sessions WHERE id = $1`, id)
		out = sessionDTO(row)
		return err
	})
	return out, err
}

// RenameSession sets the title.
func (s *Service) RenameSession(ctx context.Context, caller common.Caller, id string, body *dto.SessionPatch) (dto.Session, error) {
	if !utility.IsUUID(id) {
		return dto.Session{}, errNotFound
	}
	var out dto.Session
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[model.Session](ctx, tx, `UPDATE sessions SET title = $2, updated_at = clock_timestamp()
			WHERE id = $1 RETURNING `+model.SessionCols, id, (&dto.SessionCreate{Title: body.Title}).TitleOrDefault())
		out = sessionDTO(row)
		return err
	})
	return out, err
}

// DeleteSession deletes the session with its runs and events; 409 while one of its runs is queued or running.
func (s *Service) DeleteSession(ctx context.Context, caller common.Caller, id string) error {
	if !utility.IsUUID(id) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		var busy bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE session_id = s.id AND status IN ('queued', 'running'))
			FROM sessions s WHERE id = $1 FOR UPDATE`, id).Scan(&busy)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return errNotFound
		case err != nil:
			return err
		case busy:
			return network.NewConflictError("a run of this session is still queued or running; cancel it first", nil)
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
		return err
	})
}

// sessionDTO maps a row.
func sessionDTO(row model.Session) dto.Session {
	return dto.Session{ID: row.ID, Title: row.Title, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// ------------------------------------------------------------------ workflows and hosts

// Workflows returns every runnable workflow the runners published.
func (s *Service) Workflows() dto.WorkflowList {
	return dto.WorkflowList{Workflows: s.registry.Workflows()}
}

// Workflow returns one workflow with its node schemas.
func (s *Service) Workflow(id string) (registry.Workflow, error) {
	workflow, ok := s.registry.Workflow(id)
	if !ok {
		return workflow, errNotFound
	}
	return workflow, nil
}

// Hosts returns every runner's state, redacted: never the run or its owner.
func (s *Service) Hosts() dto.HostList {
	out := dto.HostList{Hosts: []dto.Host{}}
	now := time.Now()
	for _, host := range s.registry.Hosts() {
		state := host.State
		if !host.Live(now) {
			state = "offline"
		}
		out.Hosts = append(out.Hosts, dto.Host{Host: host.Host, Env: host.Env, State: state, UpdatedAt: host.UpdatedAt})
	}
	return out
}
