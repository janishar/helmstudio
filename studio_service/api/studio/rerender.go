package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/timeline"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Re-render (contracts/README.md "Timeline: re-render", API 1.17): one pipeline snapshot with a step per selected clip
// (its take's workflow and settings, a preset laid over them), an edge where a clip starts from another's frame,
// submitted through the batch path as one row; each step's take fills its slot.

// RerenderTimeline builds and submits the re-render of the selected clips (absent: every V1 clip with a take).
func (s *Service) RerenderTimeline(ctx context.Context, caller common.Caller, id string, body *dto.TimelineRerender) (dto.TimelineRerenderAnswer, error) {
	if !s.registry.Ready() {
		return dto.TimelineRerenderAnswer{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.TimelineRerenderAnswer
	tl, err := s.editTimeline(ctx, caller, id, func(tx pgx.Tx, row *timelineRow) error {
		if body.Revision != row.Revision {
			return staleRevision(*row)
		}
		doc := row.doc()
		rules, rows, err := loadTakes(ctx, tx, caller.ID, docTakeIDs(doc))
		if err != nil {
			return err
		}
		if n, probs := timeline.Normalize(doc, rules, &doc); probs == nil {
			doc = *n
		}
		for _, c := range body.Clips {
			if indexOf(doc.Tracks[0].Clips, c) < 0 {
				return network.NewUnprocessableError("unknown clip "+c, nil)
			}
		}
		if _, rerr := timeline.RerenderSteps(doc, rules, body.Clips, nil, nil); rerr != nil {
			return rerenderError(*rerr, doc, rules)
		}
		// each selected clip's workflow, as it runs now
		workflows, versions, keyframes := map[string]registry.Workflow{}, map[string]int{}, map[string]*timeline.Keyframes{}
		media := map[string][]string{} // workflow id → its media param paths (API 1.19)
		selected := map[string]timeline.Clip{}
		for _, c := range doc.Tracks[0].Clips {
			if (body.Clips == nil && c.Take() != "") || slices.Contains(body.Clips, c.ID) {
				selected[c.ID] = c
			}
		}
		for _, c := range selected {
			if c.Shot != nil {
				if status, _, _, err := shotState(ctx, tx, caller.ID, *c.Shot); err != nil {
					return err
				} else if status == "queued" || status == "running" {
					return network.NewConflictError("clip "+c.ID+" already has a shot running", nil)
				}
			}
			wid := rules[c.Take()].MadeWith.WorkflowID
			if _, done := workflows[wid]; done {
				continue
			}
			workflow, version, err := s.runnable(ctx, tx, wid)
			var api *network.APIError
			if errors.As(err, &api) && api.Code < 500 {
				return network.NewUnprocessableError(fmt.Sprintf("clip %s: workflow %s no longer exists", c.ID, wid), nil)
			} else if err != nil {
				return err
			}
			workflows[wid], keyframes[wid] = workflow, timeline.ShotKeyframes(workflow.Nodes, workflow.NodeSchemas)
			media[wid] = slices.Sorted(maps.Keys(mediaParams(workflow.Nodes, workflow.NodeSchemas)))
			if version != nil {
				versions[wid] = *version
			}
		}
		steps, rerr := timeline.RerenderSteps(doc, rules, body.Clips, keyframes, media)
		if rerr != nil {
			return rerenderError(*rerr, doc, rules)
		}
		if err := openBatchCap(ctx, tx, caller.ID); err != nil {
			return err
		}
		title := "Re-render · " + row.Name
		pdoc := dto.PipelineDoc{Title: title, Nodes: map[string]dto.PipelineNode{
			"inputs": {Kind: "inputs", Columns: []dto.PipelineInput{{Name: "timeline", Type: "Text"}}}}}
		stored := storedPipeline{ID: row.ID, Title: title, Version: row.Revision, Workflows: workflows, Versions: versions,
			Presets: map[string]map[string]any{}, SeedParams: map[string]string{}, Models: map[string]string{},
			Sources: map[string]map[string]string{}, Providers: map[string]map[string]any{},
			Remote: pdoc.RemoteFilled(), Custom: map[string]registry.CustomNode{}, Bounds: dto.PipelineBounds{Runs: len(steps)}}
		wdocs, err := s.stepWorkflows(ctx, tx, dto.PipelineDoc{Nodes: stepNodes(workflows)})
		if err != nil {
			return err
		}
		palette := s.palette()
		from := "inputs"
		one := 1
		for _, step := range steps {
			c := selected[step.Clip]
			var made gallerydto.MadeWith
			_ = json.Unmarshal(rows[c.Take()].MadeWith, &made)
			fixed := maps.Clone(rules[c.Take()].MadeWith.Inputs)
			key := seedParam(wdocs[made.WorkflowID], palette)
			if key != "" && made.Seed != nil {
				fixed[key] = float64(*made.Seed) // the recorded seed: the base seed of the step's one run
			}
			node := dto.PipelineNode{Kind: "step", Workflow: made.WorkflowID, Seeds: &one, Fixed: fixed, Bind: step.Binds}
			for _, l := range made.Looks {
				node.Looks = append(node.Looks, l.ID)
			}
			if body.Preset != nil {
				preset, ok := map[string]any(nil), false
				if studio := wdocs[made.WorkflowID].Studio; studio != nil {
					preset, ok = studio.Presets[*body.Preset]
				}
				if !ok {
					return network.NewUnprocessableError(fmt.Sprintf("clip %s: workflow %s has no preset %s", c.ID,
						made.WorkflowID, *body.Preset), nil)
				}
				node.Preset = body.Preset
				stored.Presets[step.Step] = preset
			}
			if key != "" {
				stored.SeedParams[step.Step] = key
			}
			pdoc.Nodes[step.Step] = node
			pdoc.Edges = append(pdoc.Edges, dto.PipelineEdge{From: from, To: step.Step})
			from = step.Step
		}
		pdoc.Nodes["out"] = dto.PipelineNode{Kind: "output", From: from + ".takes", Collection: title}
		pdoc.Edges = append(pdoc.Edges, dto.PipelineEdge{From: from, To: "out"})
		stored.Doc = pdoc
		sessionID, err := s.timelineSession(ctx, tx, row)
		if err != nil {
			return err
		}
		batchRows := []map[string]any{{"inputs.timeline": row.ID}}
		out.Batch, err = s.submitPipeline(ctx, tx, caller, &dto.BatchCreate{SessionID: &sessionID, Title: &title}, stored,
			pipelineInfo{}, batchRows, nil, &row.ID)
		if err != nil {
			return err
		}
		for _, step := range steps {
			k := indexOf(doc.Tracks[0].Clips, step.Clip)
			doc.Tracks[0].Clips[k].Shot = &timeline.ShotRef{BatchID: out.Batch.ID, Step: step.Step}
			if _, err := tx.Exec(ctx, `INSERT INTO timeline_shots (owner_id, timeline_id, clip, batch_id, step)
				VALUES ($1, $2, $3, $4, $5)`, caller.ID, row.ID, step.Clip, out.Batch.ID, step.Step); err != nil {
				return err
			}
		}
		return storeUser(ctx, tx, row, doc)
	})
	out.Timeline = tl
	return out, err
}

// stepNodes is a step per workflow (stepWorkflows reads the caller's own ones).
func stepNodes(workflows map[string]registry.Workflow) map[string]dto.PipelineNode {
	out := map[string]dto.PipelineNode{}
	for id := range workflows {
		out[id] = dto.PipelineNode{Kind: "step", Workflow: id}
	}
	return out
}

// rerenderError is the refusal of a re-render the rules cannot build.
func rerenderError(e timeline.RerenderError, doc timeline.Doc, takes map[string]timeline.Take) error {
	switch e.Code {
	case "too_many":
		return network.NewUnprocessableError(fmt.Sprintf("at most %d clips per re-render", timeline.MaxRerender), nil)
	case "empty":
		return network.NewUnprocessableError("clip "+e.Clip+" is an empty slot", nil)
	case "offline":
		return network.NewUnprocessableError("clip "+e.Clip+" is offline", nil)
	case "not_made":
		return network.NewUnprocessableError("clip "+e.Clip+": its take was not made by a workflow", nil)
	case "rerender_cycle":
		other := e.Clip
		k := indexOf(doc.Tracks[0].Clips, e.Clip)
		if fo := firstFrameSource(doc.Tracks[0].Clips[k], doc, takes); fo != "" {
			other = fo
		}
		return network.NewUnprocessableError(fmt.Sprintf("clips %s and %s start from each other", e.Clip, other), nil)
	}
	return network.NewUnprocessableError(e.Code, nil)
}

// firstFrameSource is another clip c's keyframes come from (for the cycle's message), "" when none is found.
func firstFrameSource(c timeline.Clip, doc timeline.Doc, takes map[string]timeline.Take) string {
	mw := takes[c.Take()].MadeWith
	if mw == nil {
		return ""
	}
	for _, v := range mw.Inputs {
		raw, _ := json.Marshal(v)
		for _, o := range doc.Tracks[0].Clips {
			for _, a := range o.Alternatives {
				for id, t := range takes {
					if t.FrameOf != nil && t.FrameOf.TakeID == a && o.ID != c.ID && strings.Contains(string(raw), id) {
						return o.ID
					}
				}
			}
		}
	}
	return ""
}

func indexOf(clips []timeline.Clip, id string) int {
	return slices.IndexFunc(clips, func(c timeline.Clip) bool { return c.ID == id })
}
