package studio

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/common"
)

// Plugins: takes (API 1.70; contracts/README.md "Plugins: takes"): studio signs each plugin tool call's pass for
// pipelines (studio.plugins.pass) and checks it itself on /v1/studio/plugin/*, so a plugin gets one owner's takes for
// one call and nothing else: never a user token, never the database.

// PassTTL is how long a pass is good for.
const PassTTL = 300 * time.Second

// PassRequest is req.studio.plugins.pass's request.
type PassRequest struct {
	OwnerID string   `json:"owner_id"`
	Plugin  string   `json:"plugin"`
	CallID  string   `json:"call_id"`
	TakeIDs []string `json:"take_ids,omitempty"` // after the call: which of these did it make (reply: made)
}

// PassReply is its reply: ok (pass, api; no pass for a plugin without takes), made (take_ids) or error and code.
type PassReply struct {
	Pass    string    `json:"pass,omitempty"`
	API     string    `json:"api,omitempty"`
	TakeIDs *[]string `json:"take_ids,omitempty"`
	Error   string    `json:"error,omitempty"`
	Code    string    `json:"code,omitempty"`
}

// passBody is what a pass signs.
type passBody struct {
	Owner  string `json:"owner"`
	Plugin string `json:"plugin"`
	Call   string `json:"call"`
	Exp    int64  `json:"exp"`
}

var callIDRe = regexp.MustCompile(`^.{1,120}$`)

// pluginUsable reports whether a plugin is on, usable, of kind assistant-tools, and (takes) declares takes.
func (s *Service) pluginUsable(ctx context.Context, name string, takes bool) (bool, error) {
	var ok bool
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := readPlugins(ctx, tx, `WHERE name = $1 AND enabled AND blocked = '' AND manifest->>'kind' = 'assistant-tools'`, name)
		ok = err == nil && len(rows) == 1 && (!takes || rows[0].Manifest.Takes)
		return err
	})
	return ok, err
}

// PluginPass answers studio.plugins.pass: the call's pass, for a plugin that is on (none for a plugin without takes);
// with take_ids, the ones this plugin made under this call (made).
func (s *Service) PluginPass(ctx context.Context, req *PassRequest) PassReply {
	if !utility.IsUUID(req.OwnerID) || !pluginNameRe.MatchString(req.Plugin) || !callIDRe.MatchString(req.CallID) ||
		len(req.TakeIDs) > 8 {
		return PassReply{Error: "owner_id, plugin and call_id are required", Code: "invalid"}
	}
	if req.TakeIDs != nil {
		return s.madeBy(ctx, req)
	}
	ok, err := s.pluginUsable(ctx, req.Plugin, false)
	if err == nil && ok {
		var takes bool
		if takes, err = s.pluginUsable(ctx, req.Plugin, true); err == nil && !takes {
			return PassReply{API: s.signer.BaseURL()} // ponytail: two reads of one row; one query if it ever matters
		}
	}
	switch {
	case err != nil:
		log.Printf("studio.plugins.pass %s: %v", req.Plugin, err)
		return PassReply{Error: "internal error", Code: "internal"}
	case !ok:
		return PassReply{Error: "the plugin is off", Code: "not_found"}
	}
	body, _ := json.Marshal(passBody{Owner: req.OwnerID, Plugin: req.Plugin, Call: req.CallID, Exp: time.Now().Add(PassTTL).Unix()})
	return PassReply{Pass: s.signer.Pass(body), API: s.signer.BaseURL()}
}

// madeBy is the made reply: those of req.TakeIDs that are the owner's live takes with source tool that req.Plugin made
// under req.CallID (takes.tool's plugin and call, as CreatePluginTake records them from the pass).
func (s *Service) madeBy(ctx context.Context, req *PassRequest) PassReply {
	made := []string{}
	err := common.WithUser(ctx, s.db, common.Caller{ID: req.OwnerID}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text FROM takes WHERE id::text = ANY($1) AND owner_id = $2 AND source = 'tool'
			AND deleted_at IS NULL AND tool->>'plugin' = $3 AND tool->>'call' = $4`, req.TakeIDs, req.OwnerID, req.Plugin, req.CallID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			made = append(made, id)
		}
		return rows.Err()
	})
	if err != nil {
		log.Printf("studio.plugins.pass %s made: %v", req.Plugin, err)
		return PassReply{Error: "internal error", Code: "internal"}
	}
	return PassReply{TakeIDs: &made}
}

var errPass = errors.New("invalid or expired plugin pass")

// PluginCaller is the caller a pass stands for: its owner as the plugin (Caller.Plugin), while the pass is signed,
// unexpired and its plugin is on with `takes`; else errPass (an outage of the database is another error).
func (s *Service) PluginCaller(ctx context.Context, pass string) (common.Caller, error) {
	raw, ok := s.signer.PassBody(pass)
	var body passBody
	if !ok || json.Unmarshal(raw, &body) != nil || !utility.IsUUID(body.Owner) || time.Now().Unix() >= body.Exp {
		return common.Caller{}, errPass
	}
	usable, err := s.pluginUsable(ctx, body.Plugin, true)
	if err != nil {
		return common.Caller{}, err
	}
	if !usable {
		return common.Caller{}, errPass
	}
	return common.Caller{ID: body.Owner, Plugin: body.Plugin, Call: body.Call, Via: "api"}, nil
}

// IsPassError reports whether err is a refused pass (401), not an outage.
func IsPassError(err error) bool { return errors.Is(err, errPass) }
