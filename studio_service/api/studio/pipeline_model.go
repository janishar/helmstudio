package studio

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// The pipelines default model (contracts/README.md "Model sources: the pipelines default model", API 1.74, plan 33):
// what a write, decide or agent node's `auto` runs on. local (the default) changes nothing; remote names a shared
// provider and model; assistant (Same as Assistant) follows the Assistant's own provider and model.

// readPipelineModel reads the pipeline_model row (absent: local).
func readPipelineModel(ctx context.Context, tx pgx.Tx) (namesRow, error) {
	row, err := one[namesRow](ctx, tx, `SELECT mode, provider, model, updated_at FROM pipeline_model`)
	if errors.Is(err, errNotFound) {
		return namesRow{Mode: "local"}, nil
	}
	return row, err
}

// pipelineAuto is what auto resolves to now for a write, decide or agent node: the provider row and model, ok false
// for the local LLM (local, or a setting that names nothing usable now: a disabled, deleted, Only-you or plugin-off
// provider, a model it no longer lists, a remote one while the deployment's switch is off).
func (s *Service) pipelineAuto(ctx context.Context, tx pgx.Tx) (providerRow, string, bool, error) {
	set, err := readPipelineModel(ctx, tx)
	if err != nil || set.Mode == "local" {
		return providerRow{}, "", false, err
	}
	provider, model := deref(set.Provider), deref(set.Model)
	if set.Mode == "assistant" { // the deployment's Assistant, never an admin's Only-you choice
		var p, m *string
		err := tx.QueryRow(ctx, `SELECT provider, model FROM assistant_settings`).Scan(&p, &m)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && p == nil) {
			return providerRow{}, "", false, nil
		} else if err != nil {
			return providerRow{}, "", false, err
		}
		provider, model = *p, deref(m)
	}
	p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1 AND enabled AND owner_id IS NULL AND `+pluginOn, provider)
	if errors.Is(err, errNotFound) {
		return providerRow{}, "", false, nil
	} else if err != nil {
		return providerRow{}, "", false, err
	}
	model = cmp.Or(model, p.DefaultModel)
	if (p.Kind != "server" && p.Kind != "remote") || !slices.Contains(p.Models, model) || (p.Kind == "remote" && !s.remoteOn) {
		return providerRow{}, "", false, nil
	}
	return p, model, true, nil
}

// pipelineAutoWid is pipelineAuto as a resolved model (`server:<name>/<model>`, `remote:<name>/<model>`), "" for local.
func (s *Service) pipelineAutoWid(ctx context.Context, tx pgx.Tx) (string, error) {
	p, model, ok, err := s.pipelineAuto(ctx, tx)
	if !ok {
		return "", err
	}
	return p.Kind + ":" + p.Name + "/" + model, nil
}

// GetPipelineModel is GET /admin/pipeline-model.
func (s *Service) GetPipelineModel(ctx context.Context, caller common.Caller) (dto.PipelineModel, error) {
	if !caller.IsAdmin() {
		return dto.PipelineModel{}, errAdmin
	}
	var set namesRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		set, err = readPipelineModel(ctx, tx)
		return err
	})
	return s.pipelineModelDTO(set), err
}

func (s *Service) pipelineModelDTO(set namesRow) dto.PipelineModel {
	return dto.PipelineModel{Mode: set.Mode, Provider: set.Provider, Model: set.Model, RemoteEnabled: s.remoteOn,
		UpdatedAt: set.UpdatedAt}
}

// PutPipelineModel is PUT /admin/pipeline-model: local; remote (an enabled shared server or remote provider and one of
// its models; kind remote needs the deployment's switch); assistant (the Assistant has a shared provider, under the switch).
func (s *Service) PutPipelineModel(ctx context.Context, caller common.Caller, body *dto.PipelineModelPut) (dto.PipelineModel, error) {
	if !caller.IsAdmin() {
		return dto.PipelineModel{}, errAdmin
	}
	off := network.NewConflictError("remote models are off on this deployment", nil)
	var set namesRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		switch body.Mode {
		case "remote":
			p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1 AND enabled AND `+pluginOn, *body.Provider)
			if errors.Is(err, errNotFound) || (err == nil && p.Kind == "search") {
				return network.NewUnprocessableError(*body.Provider+" is not an enabled provider", nil)
			} else if err != nil {
				return err
			} else if p.OwnerID != nil { // pipelines run for every account
				return network.NewUnprocessableError(p.Name+" is only yours; pick a shared provider", nil)
			}
			if body.Model == nil {
				body.Model = &p.DefaultModel
			}
			if !slices.Contains(p.Models, *body.Model) {
				return network.NewUnprocessableError(fmt.Sprintf("%s is not a model of %s", *body.Model, p.Name), nil)
			}
			if p.Kind == "remote" && !s.remoteOn {
				return off
			}
		case "assistant":
			var name *string
			err := tx.QueryRow(ctx, `SELECT provider FROM assistant_settings`).Scan(&name)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && name == nil) {
				return network.NewUnprocessableError("the Assistant is not set up", nil)
			} else if err != nil {
				return err
			}
			p, err := one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1`, *name)
			if errors.Is(err, errNotFound) {
				return network.NewUnprocessableError("the Assistant is not set up", nil)
			} else if err != nil {
				return err
			} else if p.OwnerID != nil {
				return network.NewUnprocessableError("the Assistant's provider is only yours; pick a shared provider", nil)
			} else if p.Kind == "remote" && !s.remoteOn {
				return off
			}
		}
		var err error
		set, err = one[namesRow](ctx, tx, `INSERT INTO pipeline_model (mode, provider, model) VALUES ($1, $2, $3)
			ON CONFLICT (one) DO UPDATE SET mode = excluded.mode, provider = excluded.provider, model = excluded.model,
			updated_at = clock_timestamp() RETURNING mode, provider, model, updated_at`, body.Mode, body.Provider, body.Model)
		return err
	})
	return s.pipelineModelDTO(set), err
}
