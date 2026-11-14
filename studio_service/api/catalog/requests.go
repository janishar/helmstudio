package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/ora/orago/network"

	"github.com/ora/catalog_service/api/catalog/dto"
	"github.com/ora/catalog_service/api/catalog/message"
	"github.com/ora/catalog_service/api/catalog/model"
	"github.com/ora/catalog_service/common"
	"github.com/ora/catalog_service/registry"
)

// whatICanDo is the guide for text the interpreter could not map.
const whatICanDo = "Run, approve, change poses or download. For example: “run the rest of the rings”, “add a macro detail pose”, “approve the rings”."

// TypedRequest asks agents (catalog.interpret) to map typed text to one action, then carries it out through the same
// rules as its endpoint. What those rules refuse (404, 409, 422) becomes action reply with the reason; text the
// interpreter can't map is not_understood and spends nothing. An action that queues a run (run, feedback, a pose
// change that renders new shots) is rolled back and answered with its quote unless confirm_credits matches it, so the
// credits are shown first. 409 only before Start.
func (s *Service) TypedRequest(ctx context.Context, caller common.Caller, sessionID string, body *dto.TypedRequest) (dto.TypedResponse, error) {
	lib, prices, err := library()
	if err != nil {
		return dto.TypedResponse{}, err
	}
	var request *message.InterpretRequest
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		st, err := loadState(ctx, tx, sessionID, false)
		if err != nil {
			return err
		}
		if st.session.Stage != "ready" {
			return network.NewConflictError("start processing first", nil)
		}
		request = interpretRequest(st, lib, body)
		return nil
	})
	if err != nil {
		return dto.TypedResponse{}, err
	}
	reply, err := askAgents[message.InterpretRequest, message.InterpretReply](ctx, s.agents, message.SubjectInterpret, request)
	if err != nil {
		return dto.TypedResponse{}, err
	}
	if reply.Error != "" {
		return dto.TypedResponse{}, network.NewServiceUnavailableError("agents could not read the request; try again", errors.New(reply.Error))
	}
	var out dto.TypedResponse
	err = s.withState(ctx, caller, sessionID, func(tx pgx.Tx, st *state) error {
		out, err = s.act(ctx, tx, st, lib, prices, reply, body.ImageIDs, request.State.LatestRunImageIDs)
		if run := out.Update.Run; err == nil && run != nil && (body.ConfirmCredits == nil || *body.ConfirmCredits != run.CreditsQuoted) {
			quote := &dto.Quote{Images: len(out.Update.Images), Credits: run.CreditsQuoted}
			for _, image := range out.Update.Images {
				if sku := st.sku(image.SkuID); sku != nil {
					_, worst := imageCredits(lib, prices, *sku)
					quote.WorstCredits += worst
				}
			}
			out = dto.TypedResponse{Action: out.Action, Update: dto.Update{Quote: quote},
				Reply: fmt.Sprintf("That's %d %s for %s credits. Go ahead?", quote.Images, plural("image", quote.Images),
					strconv.FormatFloat(quote.Credits, 'f', -1, 64))}
			return errQuoted // roll back: nothing is queued or saved until the client confirms
		}
		return err
	})
	if errors.Is(err, errQuoted) {
		return out, nil
	}
	var refused *network.APIError
	if errors.As(err, &refused) && (refused.Code == 404 || refused.Code == 409 || refused.Code == 422) {
		return dto.TypedResponse{Action: "reply", Reply: refused.Message}, nil
	}
	return out, err
}

// errQuoted rolls back a typed request that answers with a quote.
var errQuoted = errors.New("quoted")

// interpretRequest describes the session to the interpreter: categories with their current poses, SKUs with status,
// and the images of the latest run still waiting for review.
func interpretRequest(st *state, lib *registry.Library, body *dto.TypedRequest) *message.InterpretRequest {
	request := &message.InterpretRequest{OwnerID: st.session.OwnerID, SessionID: st.session.ID, Text: body.Text,
		SelectedImageIDs: nonNil(body.ImageIDs), State: message.InterpretState{Categories: []message.CategoryState{},
			Skus: []message.SkuState{}, LatestRunImageIDs: []string{}}}
	for _, work := range categoryWorks(st, lib) {
		poses, known := categoryPoses(st, lib, work.id), knownCustomPoses(st)
		var custom []message.CustomPose
		for _, pose := range poses {
			if c, ok := known[pose]; ok {
				custom = append(custom, message.CustomPose(c))
			}
		}
		request.State.Categories = append(request.State.Categories, message.CategoryState{Category: work.id,
			Name: work.name, Poses: poses, CustomPoses: custom})
	}
	for _, sku := range st.skus {
		request.State.Skus = append(request.State.Skus, message.SkuState{SkuID: sku.ID, Code: sku.Code, Name: sku.Name,
			Category: sku.Category, GroupKey: sku.GroupKey, Status: st.status[sku.ID]})
	}
	if len(st.runs) > 0 {
		for _, image := range st.images {
			if image.RunID == st.runs[0].ID && image.Status == "to_review" {
				request.State.LatestRunImageIDs = append(request.State.LatestRunImageIDs, image.ID)
			}
		}
	}
	return request
}

// categoryPoses are a category's current poses: those of its first SKU not Approved (else its first SKU), else the
// category's defaults.
func categoryPoses(st *state, lib *registry.Library, category string) []string {
	var first *model.Sku
	for i := range st.skus {
		sku := &st.skus[i]
		if sku.Category != category {
			continue
		}
		if first == nil {
			first = sku
		}
		if st.status[sku.ID] != "Approved" {
			first = sku
			break
		}
	}
	if first != nil && len(first.Brief.Poses) > 0 {
		return slices.Clone(first.Brief.Poses)
	}
	if found := lib.Category(category); found != nil {
		return slices.Clone(found.DefaultPoses)
	}
	return []string{}
}

// act carries out the interpreter's action inside tx.
func (s *Service) act(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, prices *registry.Prices,
	reply *message.InterpretReply, selected, waiting []string) (dto.TypedResponse, error) {
	args := reply.Args
	out := dto.TypedResponse{Action: reply.Action, Reply: reply.Reply}
	name := func(category string) string {
		if found := lib.Category(category); found != nil {
			return found.Name
		}
		return category
	}
	switch reply.Action {
	case "run":
		accepted, err := s.run(ctx, tx, st, lib, prices, &dto.RunRequest{Scope: args.Scope, Category: args.Category,
			GroupKey: args.GroupKey, SkuIDs: args.SkuIDs})
		if err != nil {
			return out, err
		}
		out.Update = dto.Update{Run: &accepted.Run, Images: accepted.Images, Skus: accepted.Skus}
	case "approve":
		body := &dto.ApproveRequest{ImageIDs: args.ImageIDs, SkuIDs: args.SkuIDs, Category: args.Category}
		if body.ImageIDs == nil && body.SkuIDs == nil && body.Category == "" {
			body.ImageIDs = waiting
		}
		approved, err := s.approve(ctx, tx, st, body)
		if err != nil {
			return out, err
		}
		if approved.Approved == 0 {
			where := "the latest run"
			if args.Category != "" {
				where = name(args.Category)
			}
			return dto.TypedResponse{Action: "reply", Reply: "Nothing in " + where + " is waiting for approval."}, nil
		}
		out.Update = dto.Update{Images: approved.Images, Skus: approved.Skus}
	case "add_pose", "remove_pose", "set_poses":
		poses := categoryPoses(st, lib, args.Category)
		pose := args.PoseID
		poseName := pose
		if found, _, ok := findPose(st, lib, pose); ok {
			poseName = found
		}
		switch reply.Action {
		case "add_pose":
			if slices.Contains(poses, pose) {
				return dto.TypedResponse{Action: "reply", Reply: poseName + " is already in the " + name(args.Category) + " poses."}, nil
			}
			poses = append(poses, pose)
		case "remove_pose":
			if !slices.Contains(poses, pose) {
				return dto.TypedResponse{Action: "reply", Reply: poseName + " isn't in the " + name(args.Category) + " poses."}, nil
			}
			poses = slices.DeleteFunc(poses, func(p string) bool { return p == pose })
		default:
			poses = args.Poses
		}
		return s.setCategoryPoses(ctx, tx, st, lib, prices, args.Category, name(args.Category), reply.Action, poses)
	case "open_pose_editor":
		out.Update.PoseEditor = &dto.PoseEditor{Category: args.Category, Poses: categoryPoses(st, lib, args.Category)}
	case "download":
		entries, skus := approvedEntries(st)
		if len(entries) == 0 {
			return out, network.NewConflictError("Nothing is approved yet.", nil)
		}
		download, err := s.zip(ctx, st.session.OwnerID, entries, skus)
		if err != nil {
			return out, err
		}
		out.Update.Download = &download
		out.Reply = "Packed the approved images into catalog_approved.zip."
	case "feedback":
		ids := selected
		if len(ids) == 0 {
			ids = args.ImageIDs
		}
		if len(ids) == 0 {
			ids = waiting
		}
		if len(ids) == 0 || strings.TrimSpace(args.Feedback) == "" {
			return dto.TypedResponse{Action: "reply", Reply: "Select the images to change first, then write the fix."}, nil
		}
		accepted, err := s.regenerate(ctx, tx, st, lib, prices, ids, strings.TrimSpace(args.Feedback), "")
		if err != nil {
			return out, err
		}
		out.Update = dto.Update{Run: &accepted.Run, Images: accepted.Images, Skus: accepted.Skus}
	case "reply":
	default:
		out.Action = "not_understood"
		if out.Reply == "" {
			out.Reply = whatICanDo
		}
	}
	return out, nil
}

// setCategoryPoses saves a category's poses on every SKU of it not Approved, then renders the new poses of the SKUs
// already run (their missing shots).
func (s *Service) setCategoryPoses(ctx context.Context, tx pgx.Tx, st *state, lib *registry.Library, prices *registry.Prices,
	category, categoryName, action string, poses []string) (dto.TypedResponse, error) {
	if len(poses) == 0 {
		return dto.TypedResponse{Action: "reply", Reply: categoryName + " needs at least one pose."}, nil
	}
	var names []string
	for _, pose := range poses {
		found, _, ok := findPose(st, lib, pose)
		if !ok {
			return dto.TypedResponse{}, network.NewUnprocessableError("unknown pose "+pose, nil)
		}
		names = append(names, found)
	}
	first := slices.IndexFunc(st.skus, func(sku model.Sku) bool { return sku.Category == category })
	if first < 0 {
		return dto.TypedResponse{Action: "reply", Reply: "There are no " + strings.ToLower(categoryName) + " in this batch."}, nil
	}
	ids, err := s.setBriefs(ctx, tx, st, lib, st.skus[first], message.BriefPatch{Poses: poses}, nil, dto.Nullable{},
		true, nil, nil, nil)
	if err != nil {
		return dto.TypedResponse{}, err
	}
	out := dto.TypedResponse{Action: action, Reply: categoryName + " poses are now: " + strings.Join(names, ", ") + "."}
	var rerun []string
	for _, sku := range st.skus {
		if slices.Contains(ids, sku.ID) && (st.status[sku.ID] == "To review" || st.status[sku.ID] == "Redo") {
			rerun = append(rerun, sku.ID)
		}
	}
	if len(rerun) > 0 {
		accepted, err := s.run(ctx, tx, st, lib, prices, &dto.RunRequest{Scope: "skus", SkuIDs: rerun, MissingOnly: true})
		var conflict *network.APIError
		if errors.As(err, &conflict) && conflict.Code == 409 {
			err = nil // nothing new to render: the poses were only removed
		} else if err == nil {
			out.Update.Run, out.Update.Images = &accepted.Run, accepted.Images
			var codes []string
			for _, sku := range accepted.Skus {
				codes = append(codes, sku.Code)
			}
			out.Reply += " Generating the new poses for " + listCodes(codes) + "."
		}
		if err != nil {
			return dto.TypedResponse{}, err
		}
	}
	st, err = reload(ctx, tx, st)
	if err != nil {
		return dto.TypedResponse{}, err
	}
	out.Update.Skus = s.skusDTO(st, ids)
	return out, nil
}
