package studio

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/model"
)

// cellIn is one cell as pipelines reports it (defs rowCell).
type cellIn struct {
	Node     string `json:"node"`
	Kind     string `json:"kind"`
	Attempts []struct {
		Visit      int      `json:"visit"`
		Status     string   `json:"status"`
		RunIDs     []string `json:"run_ids"`
		TakeIDs    []string `json:"take_ids"`
		Candidates []string `json:"candidates"`
		Seeds      []int64  `json:"seeds"`
		Error      *string  `json:"error"`
	} `json:"attempts"`
}

// runTimes is what a cell shows of a run.
type runTimes struct {
	ID         string
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// rowsDTO maps rows; a pipeline row adds node, parked and its cells, with seconds, thumbnails and queued from the
// runs and takes they name (read in two queries for the whole page).
func (s *Service) rowsDTO(ctx context.Context, tx pgx.Tx, rows []model.BatchRow) ([]dto.BatchRow, error) {
	out := make([]dto.BatchRow, len(rows))
	cells := make([][]cellIn, len(rows))
	var runIDs, takeIDs []string
	for i, row := range rows {
		out[i] = s.rowDTO(row)
		if !row.Pipeline {
			continue
		}
		_ = json.Unmarshal(row.Cells, &cells[i])
		for _, cell := range cells[i] {
			for _, a := range cell.Attempts {
				runIDs = append(runIDs, a.RunIDs...)
				takeIDs = append(append(takeIDs, a.TakeIDs...), a.Candidates...)
			}
		}
	}
	runs := map[string]runTimes{}
	if len(runIDs) > 0 {
		found, err := collect[runTimes](ctx, tx, `SELECT id::text, status, started_at, finished_at FROM runs
			WHERE id = ANY($1::uuid[])`, uuids(runIDs))
		if err != nil {
			return nil, err
		}
		for _, run := range found {
			runs[run.ID] = run
		}
	}
	keys, err := takeKeys(ctx, tx, takeIDs)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		if !row.Pipeline {
			continue
		}
		out[i].Node = row.Node
		var parked *dto.BatchRowParked
		_ = json.Unmarshal(row.Parked, &parked)
		out[i].Parked = parked
		shown := []dto.BatchCell{}
		for _, cell := range cells[i] {
			c := dto.BatchCell{Node: cell.Node, Kind: cell.Kind, Attempts: []dto.BatchCellAttempt{}}
			for _, a := range cell.Attempts {
				attempt := dto.BatchCellAttempt{Visit: a.Visit, Status: a.Status, RunIDs: nonNil(a.RunIDs),
					TakeIDs: nonNil(a.TakeIDs), Candidates: a.Candidates, Seeds: a.Seeds, Error: a.Error}
				if attempt.Seeds == nil {
					attempt.Seeds = []int64{}
				}
				started := false
				for _, id := range a.RunIDs {
					run := runs[id]
					started = started || (run.Status != "" && run.Status != "queued")
					if run.StartedAt != nil && run.FinishedAt != nil {
						seconds := run.FinishedAt.Sub(*run.StartedAt).Seconds()
						if attempt.Seconds != nil {
							seconds += *attempt.Seconds
						}
						attempt.Seconds = &seconds
					}
				}
				if cell.Kind == "step" && attempt.Status == "running" && !started {
					attempt.Status = "queued"
				}
				for _, id := range append(append([]string{}, a.TakeIDs...), a.Candidates...) {
					if key, ok := keys[id]; ok {
						attempt.ThumbURL = s.signer.SignPtr(&key)
						break
					}
				}
				c.Attempts = append(c.Attempts, attempt)
			}
			shown = append(shown, c)
		}
		out[i].Cells = shown
	}
	return out, nil
}

// takeKeys maps each of ids that is the caller's undeleted take to its object key.
func takeKeys(ctx context.Context, tx pgx.Tx, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	type found struct{ ID, Key string }
	rows, err := collect[found](ctx, tx, `SELECT id::text, key FROM takes WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL
		AND ready`, uuids(ids))
	for _, row := range rows {
		out[row.ID] = row.Key
	}
	return out, err
}

// uuids keeps the ids that are uuids (a cast of anything else fails the query).
func uuids(ids []string) []string {
	var out []string
	for _, id := range ids {
		if utility.IsUUID(id) {
			out = append(out, id)
		}
	}
	return out
}

// nonNil is list, or an empty list.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
