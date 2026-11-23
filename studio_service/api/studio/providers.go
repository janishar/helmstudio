package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/registry"
)

// Model providers (contracts/README.md "Model sources", API 1.15): the admin's local servers and remote providers,
// deployment settings in the providers table (no owner; only the admin API writes them). Users see names and models
// only. Studio never calls a provider: a test is pipelines_service's (pipelines.providers.test).

// Lookup resolves a host name (net.Resolver.LookupNetIP's shape); tests replace it.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// ProviderTestAsk asks pipelines_service to test a provider; an error is no answer.
type ProviderTestAsk func(ctx context.Context, req message.ProviderTestRequest) (message.ProviderTestReply, error)

// SetRemote sets the deployment's remote switch (STUDIO_REMOTE_MODELS; off by default).
func (s *Service) SetRemote(on bool) { s.remoteOn = on }

// SetLookup sets how a server's host name is resolved for the host rule.
func (s *Service) SetLookup(fn Lookup) { s.lookup = fn }

// SetProviderTest sets how the service reaches pipelines' provider test; without one the test answers 503.
func (s *Service) SetProviderTest(fn ProviderTestAsk) { s.providerTest = fn }

// ProviderKeyAsk asks pipelines_service whether a key file is there (API 1.51); an error is no answer.
type ProviderKeyAsk func(ctx context.Context, req message.ProviderKeyRequest) (message.ProviderKeyReply, error)

// SetProviderKey sets how the service reaches pipelines' key check; without one the route answers 503.
func (s *Service) SetProviderKey(fn ProviderKeyAsk) { s.providerKey = fn }

// ProviderKeyWriteAsk asks pipelines_service to save or remove a key file (API 1.52); an error is no answer.
type ProviderKeyWriteAsk func(ctx context.Context, req message.ProviderKeyWriteRequest) (message.ProviderKeyWriteReply, error)

// SetProviderKeyWrite sets how the service reaches pipelines' key writer; without one the routes answer 503.
func (s *Service) SetProviderKeyWrite(fn ProviderKeyWriteAsk) { s.providerKeyWrite = fn }

// errAdmin is the 403 of every admin route for anyone else.
var errAdmin = network.NewForbiddenError("admin role required", nil)

// providerRow is a providers row.
type providerRow struct {
	Name         string
	Kind         string
	API          string
	Title        *string
	BaseURL      string
	Models       []string
	DefaultModel string
	Enabled      bool
	TimeoutS     int
	Host         *string
	MemoryGB     *float64
	KeySecret    *string
	MaxInFlight  *int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Compat       *dto.ProviderCompat // API 1.51
	Preset       *string
	Thinking     map[string]string // API 1.53: {model: level}
}

const providerCols = `name, kind, api, title, base_url, models, default_model, enabled, timeout_s, host, memory_gb,
	key_secret, max_in_flight, created_at, updated_at, compat, preset, thinking`

func (p providerRow) dto() dto.Provider {
	return dto.Provider{Name: p.Name, Kind: p.Kind, API: p.API, Title: p.Title, BaseURL: p.BaseURL, Models: p.Models,
		DefaultModel: p.DefaultModel, Enabled: p.Enabled, TimeoutS: p.TimeoutS, Host: p.Host, MemoryGB: p.MemoryGB,
		KeySecret: p.KeySecret, MaxInFlight: p.MaxInFlight, Compat: p.Compat, Preset: p.Preset, CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt, Thinking: p.Thinking, ThinkingLevels: thinkingLevels(p.Preset, p.Models)}
}

// snapshot is defs providerSnapshot: the provider as a batch calls it.
func (p providerRow) snapshot() map[string]any {
	out := map[string]any{"kind": p.Kind, "api": p.API, "base_url": p.BaseURL, "timeout_s": p.TimeoutS, "key_secret": p.KeySecret}
	if p.Kind == "server" {
		out["host"], out["memory_gb"] = p.Host, p.MemoryGB
	} else if p.Kind == "remote" {
		out["max_in_flight"] = cmp.Or(deref(p.MaxInFlight), 2)
	}
	if p.Compat != nil {
		out["compat"] = p.Compat
	}
	if t := thinkingSnapshot(p.Preset, p.Models, p.Thinking); t != nil { // API 1.53
		out["thinking"] = t
	}
	return out
}

// readProviders reads every provider in tx (enabledOnly: those enabled).
func readProviders(ctx context.Context, tx pgx.Tx, enabledOnly bool) ([]providerRow, error) {
	query := `SELECT ` + providerCols + ` FROM providers`
	if enabledOnly {
		query += ` WHERE enabled`
	}
	return collect[providerRow](ctx, tx, query+` ORDER BY name`)
}

// ruleEnv is what the pipeline rules read besides the documents: the enabled providers, the remote switch and the
// registered custom nodes.
func (s *Service) ruleEnv(ctx context.Context, tx pgx.Tx) (ruleEnv, error) {
	rows, err := readProviders(ctx, tx, true)
	env := ruleEnv{RemoteOn: s.remoteOn, Custom: s.registry.Custom()}
	for _, p := range rows {
		env.Providers = append(env.Providers, providerInfo{Name: p.Name, Kind: p.Kind, Models: p.Models,
			DefaultModel: p.DefaultModel, Host: deref(p.Host), MemoryGB: deref(p.MemoryGB)})
	}
	return env, err
}

// ------------------------------------------------------------------ the host rule

// numericHost is a host written as numbers in any form (2130706433, 0x7f000001, 0177.0.0.1, 127.1): resolvers read
// these differently, so only the canonical dotted-decimal form is taken.
var numericHost = regexp.MustCompile(`(?i)^(0x[0-9a-f]*|[0-9]+)(\.(0x[0-9a-f]*|[0-9]+))*\.?$`)

// allowedServer reports whether a server may live at addr: loopback (127.0.0.0/8, ::1) or private (10.0.0.0/8,
// 172.16.0.0/12, 192.168.0.0/16, fc00::/7); never link-local (169.254.0.0/16, fe80::/10: cloud metadata) or anything
// else. An IPv4-mapped IPv6 address counts as its IPv4.
func allowedServer(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast() && (addr.IsLoopback() || addr.IsPrivate())
}

// dockerHost is the name every Docker engine gives the host machine inside a container. OrbStack answers 0.250.250.254
// (neither loopback nor private), Docker Desktop and Linux host-gateway a private address; whatever it resolves to at
// check time is the host, so a server may live there (by that name, another name or the literal).
const dockerHost = "host.docker.internal"

// dockerAddr reports whether addr is one of docker (host.docker.internal's addresses), never a link-local one.
func dockerAddr(addr netip.Addr, docker []netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	for _, d := range docker {
		if d.Unmap() == addr {
			return true
		}
	}
	return false
}

// urlHost parses a base_url's host, refusing ambiguous forms: userinfo, a query or fragment, numeric hosts other than
// canonical IPv4 dotted decimal, an IPv6 zone.
func urlHost(raw string) (string, *netip.Addr, string) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Opaque != "" || u.Hostname() == "" {
		return "", nil, "base_url must be http(s)://host[:port][/path], without user, query or fragment"
	}
	host := u.Hostname()
	if strings.Contains(host, "%") {
		return "", nil, "base_url: an IPv6 zone is not allowed"
	}
	if addr, err := netip.ParseAddr(host); err == nil { // canonical only: ParseAddr refuses 0177.0.0.1 and 127.1
		return host, &addr, ""
	}
	if numericHost.MatchString(host) {
		return "", nil, "base_url: write the address in dotted decimal (a.b.c.d) or use a host name"
	}
	return strings.ToLower(strings.TrimSuffix(host, ".")), nil, ""
}

// hostRule checks a provider's base_url (contracts/README.md, Model sources: the host rule): a server's host is, or
// resolves only to, loopback or private addresses or what host.docker.internal resolves to here (pipelines checks
// again at every call, after DNS); a remote one is https to a public host (a literal address must be public; pipelines
// resolves a name at every call). The detail is the 422's.
func (s *Service) hostRule(ctx context.Context, kind, raw string) string {
	host, addr, detail := urlHost(raw)
	if detail != "" {
		return detail
	}
	if kind != "server" { // remote and (API 1.44) search
		if !strings.HasPrefix(raw, "https://") {
			return "a remote provider's base_url must be https"
		}
		// a literal address must be public; a name is resolved and checked again by pipelines at every call
		public := addr == nil || (addr.Unmap().IsGlobalUnicast() && !addr.Unmap().IsPrivate())
		if !public || host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return "a remote provider's base_url must not be a loopback, private or link-local address"
		}
		return ""
	}
	lookup := s.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs := []netip.Addr{}
	if addr != nil {
		addrs = append(addrs, *addr)
	} else {
		found, err := lookup(lctx, host)
		if err != nil || len(found) == 0 {
			return fmt.Sprintf("base_url: %s does not resolve here", host)
		}
		addrs = found
	}
	var docker []netip.Addr // what host.docker.internal resolves to here, looked up only when needed
	looked := false
	for _, a := range addrs {
		if allowedServer(a) {
			continue
		}
		if !looked {
			docker, _ = lookup(lctx, dockerHost)
			looked = true
		}
		if !dockerAddr(a, docker) {
			return fmt.Sprintf("base_url: a local server must be on a loopback, private or Docker host address; %s is %s",
				host, a.Unmap())
		}
	}
	return ""
}

// ------------------------------------------------------------------ the admin API

// ListProviders answers GET /admin/providers.
func (s *Service) ListProviders(ctx context.Context, caller common.Caller) (dto.ProviderList, error) {
	out := dto.ProviderList{RemoteEnabled: s.remoteOn, Providers: []dto.Provider{}}
	if !caller.IsAdmin() {
		return out, errAdmin
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := readProviders(ctx, tx, false)
		for _, p := range rows {
			out.Providers = append(out.Providers, p.dto())
		}
		return err
	})
	return out, err
}

// PutProvider creates (201) or replaces (200) a provider after the host rule; kind remote only while the deployment's
// switch is on, and a provider's kind never changes in place.
func (s *Service) PutProvider(ctx context.Context, caller common.Caller, name string, body *dto.ProviderPut) (dto.Provider, bool, error) {
	if !caller.IsAdmin() {
		return dto.Provider{}, false, errAdmin
	}
	if !dto.ProviderName.MatchString(name) {
		return dto.Provider{}, false, errNotFound
	}
	if body.Kind != "server" && !s.remoteOn { // remote and (API 1.44) search
		return dto.Provider{}, false, network.NewConflictError("remote models are off on this deployment", nil)
	}
	if detail := s.hostRule(ctx, body.Kind, body.BaseURL); detail != "" {
		return dto.Provider{}, false, network.NewUnprocessableError(detail, nil)
	}
	if detail := thinkingProblem(body); detail != "" {
		return dto.Provider{}, false, network.NewUnprocessableError(detail, nil)
	}
	thinkingSet := body.Thinking
	if thinkingSet == nil {
		thinkingSet = map[string]string{}
	}
	var out dto.Provider
	created := false
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM providers WHERE name = $1 FOR UPDATE`, name).Scan(&kind)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			created = true
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM providers`).Scan(&count); err != nil {
				return err
			} else if count >= 32 {
				return network.NewConflictError("there are 32 providers; delete one first", nil)
			}
		case err != nil:
			return err
		case kind != body.Kind:
			return network.NewConflictError(fmt.Sprintf("%s is a %s; delete it first", name, kind), nil)
		}
		enabled := body.Enabled == nil || *body.Enabled
		row, err := one[providerRow](ctx, tx, `INSERT INTO providers (name, kind, title, base_url, models, default_model,
			enabled, timeout_s, host, memory_gb, key_secret, max_in_flight, api, compat, preset, thinking) VALUES ($1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (name) DO UPDATE SET api = excluded.api, title = excluded.title, base_url = excluded.base_url, models = excluded.models,
			default_model = excluded.default_model, enabled = excluded.enabled, timeout_s = excluded.timeout_s,
			host = excluded.host, memory_gb = excluded.memory_gb, key_secret = excluded.key_secret,
			max_in_flight = excluded.max_in_flight, compat = excluded.compat, preset = excluded.preset,
			thinking = excluded.thinking, updated_at = clock_timestamp() RETURNING `+providerCols,
			name, body.Kind, body.Title, body.BaseURL, body.Models, cmp.Or(deref(body.DefaultModel), body.Models[0]), enabled,
			cmp.Or(deref(body.TimeoutS), 120), body.Host, body.MemoryGB, body.KeySecret, maxInFlight(body),
			cmp.Or(deref(body.API), "openai"), body.Compat, body.Preset, thinkingSet)
		out = row.dto()
		return err
	})
	return out, created, err
}

// maxInFlight is a remote provider's in-flight limit (default 2); nil for a server.
func maxInFlight(body *dto.ProviderPut) *int {
	if body.Kind != "remote" {
		return nil
	}
	n := cmp.Or(deref(body.MaxInFlight), 2)
	return &n
}

// DeleteProvider deletes a provider: 404 unknown, 409 while an unfinished batch names it.
func (s *Service) DeleteProvider(ctx context.Context, caller common.Caller, name string) error {
	if !caller.IsAdmin() {
		return errAdmin
	}
	return common.System(ctx, s.db, func(tx pgx.Tx) error { // every user's batches count
		if _, err := one[struct{ Name string }](ctx, tx, `SELECT name FROM providers WHERE name = $1 FOR UPDATE`, name); err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM batches WHERE $1 = ANY(providers)
			AND status IN ('queued', 'running', 'paused'))`, name).Scan(&used); err != nil {
			return err
		} else if used {
			return network.NewConflictError(name+" is in use by an unfinished batch", nil)
		}
		_, err := tx.Exec(ctx, `DELETE FROM providers WHERE name = $1`, name)
		return err
	})
}

// TestProvider asks pipelines to test a provider (30 s, else 503); a failed test is 200 with ok false.
func (s *Service) TestProvider(ctx context.Context, caller common.Caller, name, model string) (dto.ProviderTest, error) {
	if !caller.IsAdmin() {
		return dto.ProviderTest{}, errAdmin
	}
	var row providerRow
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		row, err = one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1`, name)
		return err
	})
	if err != nil {
		return dto.ProviderTest{}, err
	}
	req := message.ProviderTestRequest{Name: name, Provider: row.snapshot()}
	if model != "" {
		req.Model = &model
	}
	return s.askTest(ctx, req, row.Preset)
}

// askTest asks pipelines.providers.test (30 s, else 503); a failed test is 200 with ok false. From API 1.53 the listed
// models' thinking under preset.
func (s *Service) askTest(ctx context.Context, req message.ProviderTestRequest, preset *string) (dto.ProviderTest, error) {
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.providerTest == nil {
		return dto.ProviderTest{}, unavailable
	}
	reply, err := s.providerTest(ctx, req)
	if err != nil || reply.Code == "internal" {
		return dto.ProviderTest{}, unavailable
	}
	out := dto.ProviderTest{OK: reply.Code == "", Models: reply.Models, Answer: reply.Answer, Seconds: reply.Seconds}
	if out.Models == nil {
		out.Models = []string{}
	}
	if !out.OK {
		out.Error, out.Failure = &reply.Error, &reply.Code
	} else {
		out.Thinking = testThinking(preset, reply)
	}
	return out, nil
}

// ------------------------------------------------------------------ presets and key files (API 1.51, plan 25)

// presets is the embedded contracts/providers/presets.json (gen.ProviderPresets), parsed once.
var presets = sync.OnceValue(func() []dto.ProviderPreset {
	var file struct {
		Presets []dto.ProviderPreset `json:"presets"`
	}
	if err := json.Unmarshal(gen.ProviderPresets, &file); err != nil {
		panic(err) // the file is checked by make contracts-check and its copy by gen's test
	}
	return file.Presets
})

// ProviderPresets answers GET /admin/provider-presets: the file's presets in its order, with the remote switch.
func (s *Service) ProviderPresets(caller common.Caller) (dto.ProviderPresetList, error) {
	if !caller.IsAdmin() {
		return dto.ProviderPresetList{}, errAdmin
	}
	return dto.ProviderPresetList{RemoteEnabled: s.remoteOn, Presets: presets()}, nil
}

// FetchPresetModels answers POST /admin/provider-presets/{id}/models: the preset's provider lists its models, asked by
// pipelines from the preset's own kind, base_url and compat (never a URL from the browser), timeout 15 s. A remote
// preset while the switch is off is ok false, failure off, without asking.
func (s *Service) FetchPresetModels(ctx context.Context, caller common.Caller, id string, body *dto.ProviderFetch) (dto.ProviderTest, error) {
	if !caller.IsAdmin() {
		return dto.ProviderTest{}, errAdmin
	}
	i := slices.IndexFunc(presets(), func(p dto.ProviderPreset) bool { return p.ID == id })
	if i < 0 {
		return dto.ProviderTest{}, errNotFound
	}
	p := presets()[i]
	if p.Kind == "remote" && !s.remoteOn {
		off, detail := "off", "remote models are off on this deployment"
		return dto.ProviderTest{Models: []string{}, Error: &detail, Failure: &off}, nil
	}
	key := p.KeySecret
	if body != nil && body.KeySecret != nil {
		key = body.KeySecret
	}
	snapshot := map[string]any{"kind": p.Kind, "api": "openai", "base_url": p.BaseURL, "timeout_s": 15, "key_secret": key}
	if p.Compat != nil {
		snapshot["compat"] = p.Compat
	}
	return s.askTest(ctx, message.ProviderTestRequest{Name: p.ID, Provider: snapshot}, &p.ID)
}

// ProviderKey answers GET /admin/provider-keys/{key_secret}: pipelines stats the file (5 s, else 503); 404 for a
// malformed name. The path is the deployment's, composed here: studio never reads the folder.
func (s *Service) ProviderKey(ctx context.Context, caller common.Caller, secret string) (dto.ProviderKey, error) {
	if !caller.IsAdmin() {
		return dto.ProviderKey{}, errAdmin
	}
	if !dto.KeySecretName(secret) {
		return dto.ProviderKey{}, errNotFound
	}
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.providerKey == nil {
		return dto.ProviderKey{}, unavailable
	}
	reply, err := s.providerKey(ctx, message.ProviderKeyRequest{KeySecret: secret})
	if err != nil || reply.Code != "" {
		return dto.ProviderKey{}, unavailable
	}
	return dto.ProviderKey{KeySecret: secret, Path: keyPath(secret), Found: reply.Found, Writable: reply.Writable}, nil
}

// keyPath is a key file's deploy-side path (studio never reads the folder).
func keyPath(secret string) string { return "deploy/secrets/providers/" + secret }

// SaveProviderKey answers PUT /admin/provider-keys/{key_secret} (API 1.52, plan 27): pipelines checks the key on the
// /models of the preset's or the saved provider's URL (never one from the browser), then writes it. 404 malformed name,
// unknown preset or provider; 409 not writable; 503 no answer. The log line names the file and the admin, never the key.
func (s *Service) SaveProviderKey(ctx context.Context, caller common.Caller, secret string, body *dto.ProviderKeySave) (dto.ProviderKeySaved, error) {
	if !caller.IsAdmin() {
		return dto.ProviderKeySaved{}, errAdmin
	}
	if !dto.KeySecretName(secret) {
		return dto.ProviderKeySaved{}, errNotFound
	}
	var snapshot map[string]any
	if body.Preset != nil {
		i := slices.IndexFunc(presets(), func(p dto.ProviderPreset) bool { return p.ID == *body.Preset })
		if i < 0 {
			return dto.ProviderKeySaved{}, errNotFound
		}
		p := presets()[i]
		snapshot = map[string]any{"kind": p.Kind, "api": "openai", "base_url": p.BaseURL}
		if p.Compat != nil {
			snapshot["compat"] = p.Compat
		}
	} else {
		var row providerRow
		err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
			row, err = one[providerRow](ctx, tx, `SELECT `+providerCols+` FROM providers WHERE name = $1`, *body.Provider)
			return err
		})
		if err != nil {
			return dto.ProviderKeySaved{}, err
		}
		snapshot = row.snapshot()
		delete(snapshot, "key_secret") // the write message carries the key itself, never a file name in the snapshot
	}
	snapshot["timeout_s"] = 15
	reply, err := s.askKeyWrite(ctx, message.ProviderKeyWriteRequest{Op: "save", KeySecret: secret, Key: body.Key,
		Provider: snapshot, Force: body.Force})
	if err != nil {
		return dto.ProviderKeySaved{}, err
	}
	switch {
	case reply.Saved:
		log.Printf("provider key %s saved (check %s) by %s", secret, reply.Check, caller.ID)
	case reply.Check == "refused":
		log.Printf("provider key %s refused by the provider, not saved, by %s", secret, caller.ID)
	default:
		log.Printf("provider key %s not saved (could not check) by %s", secret, caller.ID)
	}
	out := dto.ProviderKeySaved{KeySecret: secret, Path: keyPath(secret), Found: reply.Found, Saved: reply.Saved,
		Check: reply.Check}
	if reply.Detail != "" {
		out.Detail = &reply.Detail
	}
	return out, nil
}

// RemoveProviderKey answers DELETE /admin/provider-keys/{key_secret} (API 1.52): pipelines deletes the first key
// folder's file; the answer is the status after it.
func (s *Service) RemoveProviderKey(ctx context.Context, caller common.Caller, secret string) (dto.ProviderKey, error) {
	if !caller.IsAdmin() {
		return dto.ProviderKey{}, errAdmin
	}
	if !dto.KeySecretName(secret) {
		return dto.ProviderKey{}, errNotFound
	}
	reply, err := s.askKeyWrite(ctx, message.ProviderKeyWriteRequest{Op: "remove", KeySecret: secret})
	if err != nil {
		return dto.ProviderKey{}, err
	}
	log.Printf("provider key %s removed by %s", secret, caller.ID)
	return dto.ProviderKey{KeySecret: secret, Path: keyPath(secret), Found: reply.Found, Writable: reply.Writable}, nil
}

// askKeyWrite asks pipelines.providers.key.write: no answer or internal is 503, readonly or a folder of that name 409.
func (s *Service) askKeyWrite(ctx context.Context, req message.ProviderKeyWriteRequest) (message.ProviderKeyWriteReply, error) {
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.providerKeyWrite == nil {
		return message.ProviderKeyWriteReply{}, unavailable
	}
	reply, err := s.providerKeyWrite(ctx, req)
	switch {
	case err != nil || reply.Code == "internal":
		return reply, unavailable
	case reply.Code == "readonly":
		return reply, network.NewConflictError("keys are set in files on this deployment", nil)
	case reply.Code != "":
		return reply, network.NewConflictError(keyPath(req.KeySecret)+" is a folder, not a file", nil)
	}
	return reply, nil
}

// ModelSources answers GET /model-sources: the enabled providers, names and models only; remote ones only while the
// deployment's switch is on.
func (s *Service) ModelSources(ctx context.Context, caller common.Caller) (dto.ModelSourceList, error) {
	out := dto.ModelSourceList{RemoteEnabled: s.remoteOn, Sources: []dto.ModelSource{}}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		rows, err := readProviders(ctx, tx, true)
		for _, p := range rows {
			if p.Kind == "search" || p.Kind == "remote" && !s.remoteOn { // never a search provider (1.44)
				continue
			}
			out.Sources = append(out.Sources, dto.ModelSource{Name: p.Name, Kind: p.Kind, Title: cmp.Or(deref(p.Title), p.Name),
				Models: p.Models, DefaultModel: p.DefaultModel, Thinking: p.Thinking})
		}
		return err
	})
	return out, err
}

// CustomNodes answers GET /custom-nodes: what pipelines registered ([] before it did).
func (s *Service) CustomNodes() (dto.CustomNodeList, error) {
	if !s.registry.Ready() {
		return dto.CustomNodeList{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	out := dto.CustomNodeList{Nodes: s.registry.Custom()}
	if out.Nodes == nil {
		out.Nodes = []registry.CustomNode{}
	}
	return out, nil
}

// reservedOn is the memory (hundredths of a GB) the enabled local servers reserve per host: the dispatcher subtracts it
// from that host's budget in every admission.
func reservedOn(ctx context.Context, tx pgx.Tx) (map[string]int, error) {
	type found struct {
		Host     string
		MemoryGB float64
	}
	rows, err := collect[found](ctx, tx, `SELECT host, memory_gb FROM providers WHERE kind = 'server' AND enabled
		AND host IS NOT NULL AND memory_gb IS NOT NULL`)
	out := map[string]int{}
	for _, r := range rows {
		out[r.Host] += hundredths(r.MemoryGB)
	}
	return out, err
}

// toJSON marshals v (for jsonb columns).
func toJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
