package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// The hosts admin (contracts/README.md "Hosts admin", API 1.16): a host's residency settings and pins, stored in
// host_settings (the truth) and copied to KV host_settings key <host> on every change and at start; the admin's
// unloads go through the dispatcher (dispatcher_admin.go).

// settingsRow is a host_settings row.
type settingsRow struct {
	Host          string
	Version       int
	AfterRun      string
	IdleUnloadMin int
	ReserveGB     float64
	ReserveMode   string
	Pins          json.RawMessage
	Folders       []string // API 1.78: the model folders' paths in priority order; nil = never set
	Downloads     *int     // API 1.78: the downloads folder's index in Folders
	UpdatedAt     time.Time
}

const settingsCols = `host, version, after_run, idle_unload_min, reserve_gb, reserve_mode, pins, folders, downloads,
	updated_at`

// FolderID is a model folder's id (API 1.78, contracts/README.md "Model folders"): the first 12 hex of the SHA-256 of
// its path as stored.
func FolderID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:12]
}

// dto is the row with applied: every fresh key of the host reports this version in effect.
func (r settingsRow) dto(values []registry.Host, now time.Time) dto.HostSettings {
	out := dto.HostSettings{Host: r.Host, Version: r.Version, AfterRun: r.AfterRun, IdleUnloadMin: r.IdleUnloadMin,
		ReserveGB: r.ReserveGB, ReserveMode: r.ReserveMode, Pins: []dto.PinRef{}, UpdatedAt: r.UpdatedAt, Applied: true}
	_ = json.Unmarshal(r.Pins, &out.Pins)
	if out.Pins == nil {
		out.Pins = []dto.PinRef{}
	}
	if len(r.Folders) > 0 {
		for _, p := range r.Folders {
			out.Folders = append(out.Folders, dto.HostFolder{ID: FolderID(p), Path: p})
		}
		id := FolderID(r.Folders[min(deref(r.Downloads), len(r.Folders)-1)])
		out.Downloads = &id
	}
	for _, v := range values {
		if v.Host == r.Host && v.Env != "server" && v.Live(now) && (v.Policy == nil || deref(v.Policy.SettingsVersion) != r.Version) {
			out.Applied = false
		}
	}
	return out
}

// kv is the row as registry.schema.json $defs/settings.
func (r settingsRow) kv() []byte {
	v := map[string]any{"schema_version": 1, "host": r.Host, "version": r.Version,
		"after_run": r.AfterRun, "idle_unload_min": r.IdleUnloadMin, "reserve_gb": r.ReserveGB, "pins": r.Pins,
		"updated_at": r.UpdatedAt.UTC()}
	if r.ReserveMode == "auto" { // absent = custom: a runner older than 1.75 keeps taking custom values
		v["reserve_mode"] = "auto"
	}
	if len(r.Folders) > 0 { // API 1.78: only when set, so a runner older than 1.78 keeps its HELMSTUDIO_MODELS
		v["folders"], v["downloads"] = r.Folders, deref(r.Downloads)
	}
	value, _ := json.Marshal(v)
	return value
}

// presentKeys are host's present hosts keys of runners that hold weights, by env (a local server's `server` env
// holds none: settings, pins and unloads do not concern it, and it answers no runner.unload); none is a 404.
func (s *Service) presentKeys(host string) ([]registry.Host, error) {
	var out []registry.Host
	for _, v := range s.registry.Hosts() {
		if v.Host == host && v.Env != "server" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, errNotFound
	}
	return out, nil
}

// publish writes the row to KV host_settings, called under the host's lock so the KV never ends on an older version;
// a failure is a 503 (the change is rolled back).
func (s *Service) publish(ctx context.Context, row settingsRow) error {
	if s.putSettings == nil {
		return nil
	}
	if err := s.putSettings(ctx, row.Host, row.kv()); err != nil {
		return network.NewServiceUnavailableError("the settings did not reach the runners; try again", err)
	}
	return nil
}

// PublishSettings writes every stored host's settings to KV host_settings (at start), each read and written under
// its host's lock.
func (s *Service) PublishSettings(ctx context.Context) error {
	var hosts []struct{ Host string }
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		hosts, err = collect[struct{ Host string }](ctx, tx, `SELECT host FROM host_settings ORDER BY host`)
		return err
	})
	for _, h := range hosts {
		if err != nil {
			break
		}
		err = common.System(ctx, s.db, func(tx pgx.Tx) error {
			if err := lockHost(ctx, tx, h.Host); err != nil {
				return err
			}
			row, err := one[settingsRow](ctx, tx, `SELECT `+settingsCols+` FROM host_settings WHERE host = $1`, h.Host)
			if err != nil {
				return err
			}
			return s.publish(ctx, row)
		})
	}
	return err
}

// PutHostSettings answers PUT /admin/hosts/{host}/settings.
func (s *Service) PutHostSettings(ctx context.Context, caller common.Caller, host string, body *dto.HostSettingsPut) (dto.HostSettings, error) {
	if !caller.IsAdmin() {
		return dto.HostSettings{}, errAdmin
	}
	keys, err := s.presentKeys(host)
	if err != nil {
		return dto.HostSettings{}, err
	}
	ram := -1.0 // the smallest memory a key cuts its budget from: VRAM on cuda, else RAM
	for _, k := range keys {
		total := -1.0
		if k.Device != nil && k.Device.Kind == "cuda" {
			total = k.Device.MemoryTotalGB
		} else if k.Machine != nil {
			total = k.Machine.RAMGB
		}
		if total >= 0 && (ram < 0 || total < ram) {
			ram = total
		}
	}
	return s.storeSettings(ctx, host, func(row *settingsRow) error {
		row.AfterRun, row.IdleUnloadMin, row.ReserveGB = body.AfterRun, *body.IdleUnloadMin, *body.ReserveGB
		if body.ReserveMode != "" { // absent: unchanged
			row.ReserveMode = body.ReserveMode
		}
		if ram >= 0 && row.ReserveGB >= ram && row.ReserveMode != "auto" { // auto does not use reserve_gb
			return network.NewUnprocessableError("reserve_gb leaves no memory on "+host, nil)
		}
		return nil
	}, keys)
}

// PutHostPins answers PUT /admin/hosts/{host}/pins: the pins replaced (duplicates collapsed, first kept).
func (s *Service) PutHostPins(ctx context.Context, caller common.Caller, host string, body *dto.HostPins) (dto.HostSettings, error) {
	if !caller.IsAdmin() {
		return dto.HostSettings{}, errAdmin
	}
	keys, err := s.presentKeys(host)
	if err != nil {
		return dto.HostSettings{}, err
	}
	published := s.registry.Published()
	pins, seen := []dto.PinRef{}, map[string]bool{}
	for _, p := range body.Pins {
		if !slices.ContainsFunc(keys, func(k registry.Host) bool { return k.Env == p.Env }) {
			return dto.HostSettings{}, network.NewUnprocessableError(fmt.Sprintf("env %s is not on host %s", p.Env, host), nil)
		}
		if !slices.ContainsFunc(published[host+"."+p.Env].Models, func(m registry.Model) bool { return m.ID == p.Model }) {
			return dto.HostSettings{}, network.NewUnprocessableError(fmt.Sprintf("%s is not published by %s.%s", p.Model, host, p.Env), nil)
		}
		if !seen[p.Key()] {
			seen[p.Key()] = true
			pins = append(pins, p)
		}
	}
	raw, _ := json.Marshal(pins)
	return s.storeSettings(ctx, host, func(row *settingsRow) error { row.Pins = raw; return nil }, keys)
}

// FoldersCheckAsk asks the supervising runner of a host what each model folder path is there (req.runner.folders.check,
// API 1.78); an error is no answer.
type FoldersCheckAsk func(ctx context.Context, host string, req message.FoldersCheckRequest) (message.FoldersCheckReply, error)

// SetFoldersCheck sets how folder paths are checked on their host (nil: 503).
func (s *Service) SetFoldersCheck(fn FoldersCheckAsk) { s.foldersCheck = fn }

// PutHostFolders answers PUT /admin/hosts/{host}/folders (API 1.78): the list replaced after the host's runner checked
// it, version + 1, published like the settings.
func (s *Service) PutHostFolders(ctx context.Context, caller common.Caller, host string, body *dto.ModelFoldersPut) (dto.HostSettings, error) {
	if !caller.IsAdmin() {
		return dto.HostSettings{}, errAdmin
	}
	keys, err := s.presentKeys(host)
	if err != nil {
		return dto.HostSettings{}, err
	}
	paths := make([]string, len(body.Folders))
	for i, f := range body.Folders {
		paths[i] = f.Path
		if len(f.Path) > 1 {
			paths[i] = strings.TrimRight(f.Path, "/")
		}
	}
	offline := network.NewServiceUnavailableError(host+" is offline", nil)
	if s.foldersCheck == nil {
		return dto.HostSettings{}, offline
	}
	reply, err := s.foldersCheck(ctx, host, message.FoldersCheckRequest{Paths: paths})
	if err != nil || len(reply.Folders) != len(paths) {
		return dto.HostSettings{}, offline
	}
	for i, f := range reply.Folders {
		switch {
		case !f.Connected && !f.Drive: // a drive's folder may be listed while unplugged
			return dto.HostSettings{}, network.NewUnprocessableError(paths[i]+" is not a folder on "+host, nil)
		case i == *body.Downloads && !f.Connected:
			return dto.HostSettings{}, network.NewUnprocessableError("downloads must be a connected folder", nil)
		case i == *body.Downloads && !f.Writable:
			return dto.HostSettings{}, network.NewUnprocessableError(paths[i]+" is not writable on "+host, nil)
		}
	}
	log.Printf("hosts admin: %s set %d model folders on %s", caller.ID, len(paths), host)
	return s.storeSettings(ctx, host, func(row *settingsRow) error {
		row.Folders, row.Downloads = paths, body.Downloads
		return nil
	}, keys)
}

// storeSettings applies change to the host's row (a new row starts from the freshest key's policy, no pins),
// version + 1, and publishes it before the commit.
func (s *Service) storeSettings(ctx context.Context, host string, change func(row *settingsRow) error, keys []registry.Host) (dto.HostSettings, error) {
	var row settingsRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockHost(ctx, tx, host); err != nil {
			return err
		}
		found, err := one[settingsRow](ctx, tx, `SELECT `+settingsCols+` FROM host_settings WHERE host = $1 FOR UPDATE`, host)
		switch {
		case errors.Is(err, errNotFound):
			row = defaults(host, keys)
		case err != nil:
			return err
		default:
			row = found
		}
		if err := change(&row); err != nil {
			return err
		}
		row, err = one[settingsRow](ctx, tx, `INSERT INTO host_settings (host, version, after_run, idle_unload_min, reserve_gb, reserve_mode, pins, folders, downloads)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (host) DO UPDATE SET version = excluded.version,
			after_run = excluded.after_run, idle_unload_min = excluded.idle_unload_min, reserve_gb = excluded.reserve_gb,
			reserve_mode = excluded.reserve_mode, pins = excluded.pins, folders = excluded.folders,
			downloads = excluded.downloads, updated_at = clock_timestamp() RETURNING `+settingsCols,
			host, row.Version+1, row.AfterRun, row.IdleUnloadMin, row.ReserveGB, row.ReserveMode, row.Pins, row.Folders, row.Downloads)
		if err != nil {
			return err
		}
		return s.publish(ctx, row) // under the lock: two quick changes reach the KV in version order
	})
	if err != nil {
		return dto.HostSettings{}, err
	}
	return row.dto(s.registry.Hosts(), time.Now()), nil
}

// defaults is a new host's row: the freshest key's policy (else keep_warm, never idle-unload, the reserve its memory
// implies), no pins, version 0.
func defaults(host string, keys []registry.Host) settingsRow {
	row := settingsRow{Host: host, AfterRun: "keep_warm", ReserveMode: "custom", Pins: json.RawMessage(`[]`)}
	var freshest *registry.Host
	for i, k := range keys {
		if freshest == nil || k.UpdatedAt.After(freshest.UpdatedAt) {
			freshest = &keys[i]
		}
	}
	switch {
	case freshest.Policy != nil:
		row.AfterRun, row.IdleUnloadMin, row.ReserveGB = freshest.Policy.AfterRun, freshest.Policy.IdleUnloadMin, freshest.Policy.ReserveGB
	case freshest.Machine != nil && freshest.Memory != nil:
		row.ReserveGB = max(0, math.Round((freshest.Machine.RAMGB-freshest.Memory.BudgetGB)*2)/2)
	}
	return row
}

// AdminUnload answers POST /admin/hosts/{host}/unload: one req.runner.unload (by admin) per env concerned, asked now
// when the env runs nothing, else after its run.
func (s *Service) AdminUnload(ctx context.Context, caller common.Caller, host string, body *dto.HostUnload) (dto.HostUnloadAccepted, error) {
	out := dto.HostUnloadAccepted{Host: host, Requests: []dto.UnloadRequest{}}
	if !caller.IsAdmin() {
		return out, errAdmin
	}
	keys, err := s.presentKeys(host)
	if err != nil {
		return out, err
	}
	if s.dispatcher == nil {
		return out, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	onHost := func(env string) error {
		if !slices.ContainsFunc(keys, func(k registry.Host) bool { return k.Env == env }) {
			return network.NewUnprocessableError(fmt.Sprintf("env %s is not on host %s", env, host), nil)
		}
		return nil
	}
	requests := map[string]message.Unload{}
	switch {
	case body.All != nil:
		for _, k := range keys {
			requests[k.Env] = message.Unload{ForEnv: k.Env}
		}
	case body.CachedEnv != nil:
		if err := onHost(*body.CachedEnv); err != nil {
			return out, err
		}
		requests[*body.CachedEnv] = message.Unload{ForEnv: *body.CachedEnv, CachedOnly: true}
	default:
		for _, p := range body.Entries {
			if err := onHost(p.Env); err != nil {
				return out, err
			}
			request := requests[p.Env]
			request.ForEnv = p.Env
			c := message.Component{Component: p.Component, Model: p.Model, Variant: p.Variant, Precision: p.Precision, Compute: p.Compute}
			for _, l := range p.Loras {
				c.Loras = append(c.Loras, message.ComponentLora{LoraID: l.LoraID, Strength: l.Strength})
			}
			request.Components = append(request.Components, c)
			requests[p.Env] = request
		}
	}
	running := map[string]bool{}
	err = common.System(ctx, s.db, func(tx pgx.Tx) error {
		envs, err := collect[struct{ Env string }](ctx, tx, `SELECT DISTINCT env FROM runs WHERE host = $1
			AND released_at IS NOT NULL AND status IN ('queued', 'running')`, host)
		for _, e := range envs {
			running[e.Env] = true
		}
		return err
	})
	if err != nil {
		return out, err
	}
	now := time.Now()
	for _, env := range slices.Sorted(maps.Keys(requests)) {
		state := "asked"
		if running[env] {
			state = "after_run"
		}
		s.dispatcher.AdminUnload(host, env, requests[env], running[env], now)
		out.Requests = append(out.Requests, dto.UnloadRequest{Env: env, State: state})
	}
	log.Printf("hosts admin: %s asked %s to unload (%d envs)", caller.ID, host, len(out.Requests))
	return out, nil
}

// hostsAdmin is what admins see of each env: "<host>.<env>" → HostAdmin. tx is System.
func (s *Service) hostsAdmin(ctx context.Context, tx pgx.Tx, values []registry.Host, now time.Time) (map[string]*dto.HostAdmin, error) {
	settings, err := collect[settingsRow](ctx, tx, `SELECT `+settingsCols+` FROM host_settings`)
	if err != nil {
		return nil, err
	}
	type open struct {
		ID, Owner, Lane, Host, Env string
		Progress                   json.RawMessage
	}
	runs, err := collect[open](ctx, tx, `SELECT id::text, owner_id::text, lane, host, env, progress FROM runs
		WHERE released_at IS NOT NULL AND status IN ('queued', 'running') AND host IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	type ended struct {
		Host string
		At   time.Time
	}
	ends, err := collect[ended](ctx, tx, `SELECT host, max(finished_at) FROM runs WHERE host IS NOT NULL
		AND finished_at IS NOT NULL GROUP BY host`)
	if err != nil {
		return nil, err
	}
	out := map[string]*dto.HostAdmin{}
	for _, v := range values {
		admin := &dto.HostAdmin{Policy: v.Policy, HubToken: v.HubToken, UnloadPending: s.dispatcher.UnloadPending(v.Host, v.Env)}
		if i := slices.IndexFunc(settings, func(r settingsRow) bool { return r.Host == v.Host }); i >= 0 {
			hs := settings[i].dto(values, now)
			admin.Settings = &hs
		}
		busy := false
		for _, r := range runs {
			busy = busy || r.Host == v.Host
			if r.Host == v.Host && r.Env == v.Env {
				run := &dto.HostRun{RunID: r.ID, OwnerID: r.Owner, Lane: r.Lane}
				_ = json.Unmarshal(r.Progress, run)
				run.RunID, run.OwnerID, run.Lane = r.ID, r.Owner, r.Lane
				admin.Run = run
			}
		}
		if i := slices.IndexFunc(ends, func(e ended) bool { return e.Host == v.Host }); i >= 0 && !busy {
			admin.IdleSince = &ends[i].At
		}
		out[v.Host+"."+v.Env] = admin
	}
	return out, nil
}
