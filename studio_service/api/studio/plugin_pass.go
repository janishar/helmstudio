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
	OwnerID string `json:"owner_id"`
	Plugin  string `json:"plugin"`
	CallID  string `json:"call_id"`
}

// PassReply is its reply: ok (pass, api) or error and code.
type PassReply struct {
	Pass  string `json:"pass,omitempty"`
	API   string `json:"api,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
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

// PluginPass answers studio.plugins.pass: the call's pass, for a plugin that is on.
func (s *Service) PluginPass(ctx context.Context, req *PassRequest) PassReply {
	if !utility.IsUUID(req.OwnerID) || !pluginNameRe.MatchString(req.Plugin) || !callIDRe.MatchString(req.CallID) {
		return PassReply{Error: "owner_id, plugin and call_id are required", Code: "invalid"}
	}
	ok, err := s.pluginUsable(ctx, req.Plugin, false)
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
	return common.Caller{ID: body.Owner, Plugin: body.Plugin, Via: "api"}, nil
}

// IsPassError reports whether err is a refused pass (401), not an outage.
func IsPassError(err error) bool { return errors.Is(err, errPass) }
