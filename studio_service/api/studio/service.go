// Package studio: every /v1/studio route — sessions, workflows (from the registry), runs (outbox → cmd.run.execute,
// cancel through the KV cancels), the event projector and stream, hosts, the queue — plus the dispatcher.
package studio

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/storage"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/lora"
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
// reaches the runners, store is read to check a run's reference images.
type Service struct {
	db               *pgxpool.Pool
	signer           *common.Signer
	registry         *registry.Registry
	cancel           Cancel
	store            storage.Store
	stateGet         StateGet            // pipelines.state.get; nil answers 503
	pluginDirs       []string            // API 1.61: where plugins/*/plugin.json are read (read-only mounts)
	userPluginDir    string              // API 1.64: ~/.helm/plugins as mounted (read-only); "" = none
	scanMu           sync.Mutex          // API 1.64: one plugins scan at a time (Rescan, start, the watch)
	runsMu           sync.Mutex          // API 1.65: one write of the plugins to run at a time
	runHosts         map[string]bool     // API 1.65: hosts whose <host>.plugins key studio wrote (to empty it)
	memory           MemoryAsk           // pipelines.memory; nil answers 503
	lookup           Lookup              // resolves a server provider's host (nil: the system resolver)
	providerTest     ProviderTestAsk     // pipelines.providers.test; nil answers 503
	providerKey      ProviderKeyAsk      // pipelines.providers.key (API 1.51); nil answers 503
	providerKeyWrite ProviderKeyWriteAsk // pipelines.providers.key.write (API 1.52); nil answers 503
	judgeTest        JudgeTestAsk        // pipelines.judge.test (API 1.28); nil answers 503
	remoteOn         bool                // the deployment's remote switch (STUDIO_REMOTE_MODELS, off by default; API 1.15)
	// the dispatcher whose skip counts positions use; nil: none
	dispatcher *Dispatcher
	// API 1.16: purge drops a CMD subject's messages (a stale released download's command); putSettings writes KV
	// host_settings key <host>. nil: not reachable (tests)
	purge       func(ctx context.Context, subject string) error
	putSettings func(ctx context.Context, host string, value []byte) error
	// API 1.44: the assistant's memory in pipelines (nil answers 503) and the in-process engine its read tools ask
	assistantMemory AssistantMemoryAsk
	local           http.Handler
}

// SetLocal sets the in-process engine the assistant's read tools ask (common.LocalEngine).
func (s *Service) SetLocal(h http.Handler) { s.local = h }

// SetModelOps sets the NATS calls of model jobs and host settings (API 1.16).
func (s *Service) SetModelOps(purge func(ctx context.Context, subject string) error, putSettings func(ctx context.Context, host string, value []byte) error) {
	s.purge, s.putSettings = purge, putSettings
}

// SetDispatcher lets positions use the dispatcher's current skip counts.
func (s *Service) SetDispatcher(d *Dispatcher) { s.dispatcher = d }

// NewService returns the service.
func NewService(db *pgxpool.Pool, signer *common.Signer, registry *registry.Registry, cancel Cancel, store storage.Store) *Service {
	return &Service{db: db, signer: signer, registry: registry, cancel: cancel, store: store}
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
		row, err := one[model.Session](ctx, tx, `INSERT INTO sessions (id, owner_id, title, title_source) VALUES ($1, $2, $3, $4)
			RETURNING `+model.SessionCols, utility.NewID(), caller.ID, body.TitleOrDefault(), body.TitleSource())
		out = sessionDTO(row)
		return err
	})
	return out, err
}

// ListSessions returns the caller's newest 100 sessions and how new ones are named (API 1.43).
func (s *Service) ListSessions(ctx context.Context, caller common.Caller) (dto.SessionList, error) {
	out := dto.SessionList{Sessions: []dto.Session{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := collect[model.Session](ctx, tx, `SELECT `+model.SessionCols+` FROM sessions
			ORDER BY created_at DESC LIMIT 100`)
		for _, row := range rows {
			out.Sessions = append(out.Sessions, sessionDTO(row))
		}
		if err == nil {
			out.Naming, err = naming(ctx, tx)
		}
		if out.Naming.Remote && !s.remoteOn { // the projector asks nothing while the switch is off: say nothing
			out.Naming = dto.SessionNaming{Mode: "off"}
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

// RenameSession sets the title; it is the user's from then on (the namer never replaces it).
func (s *Service) RenameSession(ctx context.Context, caller common.Caller, id string, body *dto.SessionPatch) (dto.Session, error) {
	if !utility.IsUUID(id) {
		return dto.Session{}, errNotFound
	}
	var out dto.Session
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[model.Session](ctx, tx, `UPDATE sessions SET title = $2, title_source = 'user', named_by = NULL,
			updated_at = clock_timestamp() WHERE id = $1 RETURNING `+model.SessionCols, id, (&dto.SessionCreate{Title: body.Title}).TitleOrDefault())
		out = sessionDTO(row)
		return err
	})
	return out, err
}

// DeleteSession deletes the session with its runs and events; 409 while one of its runs is queued or running, or one
// of its batches is unfinished. Its finished batches stay (session_id null). Its hidden naming run does not count: it
// is cancelled first (API 1.43).
func (s *Service) DeleteSession(ctx context.Context, caller common.Caller, id string) error {
	if !utility.IsUUID(id) {
		return errNotFound
	}
	return s.tx(ctx, caller, func(tx pgx.Tx) error {
		// Lock the session first, then look at its runs in a new statement: its snapshot then sees a run that
		// CreateRun committed while this waited for the lock (a subquery of the locking statement would not).
		var busy bool
		err := tx.QueryRow(ctx, `SELECT 1 FROM sessions WHERE id = $1 FOR UPDATE`, id).Scan(new(int))
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE session_id = $1 AND status IN ('queued', 'running')
				AND purpose IS NULL)`, id).Scan(&busy)
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return errNotFound
		case err != nil:
			return err
		case busy:
			return network.NewConflictError("a run of this session is still queued or running; cancel it first", nil)
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM batches WHERE session_id = $1
			AND status IN ('queued', 'running', 'paused'))`, id).Scan(&busy); err != nil {
			return err
		} else if busy {
			return network.NewConflictError("a batch of this session is still queued, running or paused; stop it first", nil)
		}
		hidden, err := collect[model.Run](ctx, tx, `SELECT `+model.RunCols+` FROM runs WHERE session_id = $1
			AND purpose IS NOT NULL AND status IN ('queued', 'running') FOR UPDATE`, id)
		for _, run := range hidden {
			if err == nil {
				_, err = s.cancelRun(ctx, tx, run)
			}
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
		}
		return err
	})
}

// sessionDTO maps a row.
func sessionDTO(row model.Session) dto.Session {
	return dto.Session{ID: row.ID, Title: row.Title, TitleSource: row.TitleSource, NamedBy: row.NamedBy,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// ------------------------------------------------------------------ hosts

// Hosts returns every runner's state, redacted: never the run or its owner, and a LoRA baked into a resident
// component that is another user's own shows only as its size. Each entry carries its host's memory: the smallest
// budget among the host's keys and the sum of every resident component of them (what admission counts).
func (s *Service) Hosts(ctx context.Context, caller common.Caller) (dto.HostList, error) {
	out := dto.HostList{Hosts: []dto.Host{}}
	now := time.Now()
	hosts := s.registry.Hosts()
	var ids []string
	for _, host := range hosts {
		for _, r := range host.Resident {
			for _, l := range r.Loras {
				if l.LoraID != nil && utility.IsUUID(*l.LoraID) {
					ids = append(ids, *l.LoraID)
				}
			}
		}
	}
	shared := map[string]*dto.HostMemory{}
	used := map[string]int{} // hundredths
	kinds := map[string]string{}
	for _, host := range hosts {
		used[host.Host] += held(host) // its device entries, or what it measured when larger (caches the admission counts too)
		if m := host.Memory; m != nil && (shared[host.Host] == nil || hundredths(m.BudgetGB) < hundredths(shared[host.Host].BudgetGB)) {
			shared[host.Host] = &dto.HostMemory{BudgetGB: m.BudgetGB}
		}
		if host.Device != nil && kinds[host.Host] == "" {
			kinds[host.Host] = map[string]string{"cuda": "vram", "cpu": "ram"}[host.Device.Kind]
		}
	}
	for name, m := range shared {
		m.BudgetGB, m.UsedGB = math.Round(m.BudgetGB*10)/10, math.Round(float64(used[name])/10)/10
		m.Kind = cmp.Or(kinds[name], "unified") // absent from older runners: unified
	}
	mine := map[string]bool{}
	if len(ids) > 0 {
		err := common.WithUser(ctx, s.db, common.Caller{ID: caller.ID}, func(tx pgx.Tx) (err error) {
			mine, err = lora.Owned(ctx, tx, ids)
			return err
		})
		if err != nil {
			return out, err
		}
	}
	var admin map[string]*dto.HostAdmin
	if caller.IsAdmin() {
		err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
			admin, err = s.hostsAdmin(ctx, tx, hosts, now)
			return err
		})
		if err != nil {
			return out, err
		}
	}
	for _, host := range hosts {
		state := host.State
		if !host.Live(now) {
			state = "offline"
		}
		resident := slices.Clone(host.Resident)
		declared := 0
		for i, r := range resident {
			if registry.OnDevice(r.Device) {
				declared += hundredths(r.GB)
			}
			if len(r.Loras) == 0 {
				continue
			}
			r.Loras = slices.Clone(r.Loras)
			for j, l := range r.Loras {
				switch {
				case l.LoraID == nil:
					r.Loras[j] = registry.ResidentLora{Bytes: l.Bytes}
				case utility.IsUUID(*l.LoraID) && !mine[*l.LoraID] && caller.IsAdmin(): // admins: id and strength, never the name
					r.Loras[j] = registry.ResidentLora{LoraID: l.LoraID, Strength: l.Strength, Bytes: l.Bytes}
				case utility.IsUUID(*l.LoraID) && !mine[*l.LoraID]:
					r.Loras[j] = registry.ResidentLora{Bytes: l.Bytes}
				}
			}
			resident[i] = r
		}
		entry := dto.Host{Host: host.Host, Env: host.Env, State: state, UpdatedAt: host.UpdatedAt,
			Memory: host.Memory, HostMemory: shared[host.Host], Resident: resident, Machine: host.Machine,
			Admin: admin[host.Host+"."+host.Env], Device: host.Device, HostRAM: host.HostRAM}
		if host.Memory != nil {
			cached := math.Round(float64(max(0, hundredths(host.Memory.UsedGB)-declared))/10) / 10
			entry.CachedGB = &cached
		}
		out.Hosts = append(out.Hosts, entry)
	}
	return out, nil
}
