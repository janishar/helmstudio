package studio

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// The Models screen (contracts/README.md "Models and model jobs", API 1.16): every model the runners publish, each
// variant's components and its state on every host. Built from the registry's models keys of hosts with a present
// hosts key, and studio's unfinished download jobs.

// ListModels answers GET /models (for everyone; admins also see whether each host has a Hub token file).
func (s *Service) ListModels(ctx context.Context, caller common.Caller) (dto.ModelList, error) {
	if !s.registry.Ready() {
		return dto.ModelList{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	downloading := map[string]dto.VariantJob{}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		type open struct {
			ID, State, Host, Model, Variant string
			Done                            int64
			Total                           *int64
		}
		// an add-on's job (API 1.36) is "<host> <model> +<addon>"
		rows, err := collect[open](ctx, tx, `SELECT id::text, state, host, model, coalesce(variant, '+' || addon, ''),
			coalesce((progress->>'done_bytes')::bigint, 0), (progress->>'total_bytes')::bigint
			FROM model_jobs WHERE kind = 'download' AND state IN ('queued', 'running')`)
		for _, r := range rows {
			downloading[r.Host+" "+r.Model+" "+r.Variant] = dto.VariantJob{ID: r.ID, State: r.State, DoneBytes: r.Done, TotalBytes: r.Total}
		}
		return err
	})
	if err != nil {
		return dto.ModelList{}, err
	}
	return buildModels(s.registry.Hosts(), s.registry.Published(), downloading, time.Now(), caller.IsAdmin()), nil
}

// buildModels is the Models screen from the present hosts keys, what their runners publish and the unfinished
// downloads ("<host> <model> <variant>" → job). Descriptive fields come from the freshest live key that publishes
// them, else the freshest key; a variant's state on a host from that host's first env by name listing the model.
func buildModels(values []registry.Host, published map[string]registry.Published, downloading map[string]dto.VariantJob,
	now time.Time, admin bool) dto.ModelList {
	out := dto.ModelList{Hosts: []dto.ModelHost{}, Licences: []registry.Licence{}, Models: []dto.Model{}}
	keys := map[string]registry.Host{}
	byHost := map[string][]registry.Host{} // env order
	for _, v := range values {
		keys[v.Host+"."+v.Env] = v
		byHost[v.Host] = append(byHost[v.Host], v)
	}
	hostNames := slices.Sorted(maps.Keys(byHost))
	for _, name := range hostNames {
		slices.SortFunc(byHost[name], func(a, b registry.Host) int { return strings.Compare(a.Env, b.Env) })
		host := dto.ModelHost{Host: name, Envs: []string{}}
		var disk *registry.ModelsDisk
		var diskAt time.Time
		token := false
		for _, k := range byHost[name] {
			host.Live = host.Live || k.Live(now)
			host.Envs = append(host.Envs, k.Env)
			if k.ModelsDisk != nil && (disk == nil || k.UpdatedAt.After(diskAt)) {
				disk, diskAt = k.ModelsDisk, k.UpdatedAt
			}
			token = token || k.HubToken
		}
		if disk != nil {
			host.ModelsRoot, host.FreeBytes, host.TotalBytes = &disk.Root, &disk.FreeBytes, &disk.TotalBytes
		}
		if admin {
			host.HubToken = &token
		}
		out.Hosts = append(out.Hosts, host)
	}
	var sources []string // live first, then freshest
	for source := range published {
		if _, present := keys[source]; present && len(published[source].Models) > 0 {
			sources = append(sources, source)
		}
	}
	slices.SortFunc(sources, func(a, b string) int {
		ka, kb := keys[a], keys[b]
		return cmp.Or(cmp.Compare(btoi(kb.Live(now)), btoi(ka.Live(now))), kb.UpdatedAt.Compare(ka.UpdatedAt), strings.Compare(a, b))
	})
	find := func(source, model, variant string) (registry.Variant, bool, bool) { // variant, found, model listed
		for _, m := range published[source].Models {
			if m.ID == model {
				i := slices.IndexFunc(m.Variants, func(v registry.Variant) bool { return v.ID == variant })
				if i < 0 {
					return registry.Variant{}, false, true
				}
				return m.Variants[i], true, true
			}
		}
		return registry.Variant{}, false, false
	}
	described := map[string]registry.Model{}
	var variantIDs = map[string][]string{} // model → its variant ids, the freshest key's order then the others'
	for _, source := range sources {
		for _, m := range published[source].Models {
			if _, seen := described[m.ID]; !seen {
				described[m.ID] = m
			}
			for _, v := range m.Variants {
				if !slices.Contains(variantIDs[m.ID], v.ID) {
					variantIDs[m.ID] = append(variantIDs[m.ID], v.ID)
				}
			}
		}
	}
	licences := map[string]registry.Licence{}
	for _, source := range sources {
		for _, l := range published[source].Licences {
			if _, seen := licences[l.ID]; !seen {
				licences[l.ID] = l
			}
		}
	}
	used := map[string]bool{}
	for id, m := range described {
		model := dto.Model{ID: id, Family: m.Family, Title: m.Title, Media: orEmpty(m.Media),
			Runtimes: orEmpty(m.Runtimes), Variants: []dto.ModelVariant{}, Addons: []dto.ModelAddon{}}
		for _, vid := range variantIDs[id] {
			var desc registry.Variant
			for _, source := range sources {
				if v, ok, _ := find(source, id, vid); ok {
					desc = v
					break
				}
			}
			variant := dto.ModelVariant{ID: vid, Title: cmp.Or(desc.Title, vid), Precisions: orEmpty(desc.Precisions),
				Devices: desc.Devices, Components: []dto.VariantComponent{}, Hosts: []dto.ModelVariantHost{}}
			if desc.Dir != "" {
				variant.Dir = &desc.Dir
			}
			if src := desc.Source; src != nil {
				variant.Licence = &dto.VariantLicence{ID: src.Licence, MustAccept: src.MustAccept}
				variant.Gated, variant.DownloadBytes, used[src.Licence] = src.Gated, src.Bytes, true
			}
			for _, c := range desc.Components {
				component := dto.VariantComponent{Component: c.Component, FromVariant: c.FromVariant, Precisions: c.Precisions}
				for _, source := range sources { // its size and stored dtype, each from the freshest key that has it
					v, _, _ := find(source, id, vid)
					if i := slices.IndexFunc(v.Components, func(o registry.Component) bool { return o.Component == c.Component }); i >= 0 {
						if component.Bytes == nil {
							component.Bytes = v.Components[i].Bytes
						}
						if component.Stored == nil && v.Components[i].Stored != "" {
							component.Stored = &v.Components[i].Stored
						}
					}
					if component.Bytes != nil && component.Stored != nil {
						break
					}
				}
				variant.Components = append(variant.Components, component)
			}
			for _, name := range hostNames {
				var on *registry.Variant // nil: the host does not publish this variant (API 1.33: base-fp8 off CUDA)
				for _, k := range byHost[name] {
					if v, ok, has := find(k.Host+"."+k.Env, id, vid); has {
						if ok {
							on = &v
						}
						break
					}
				}
				if on == nil {
					continue
				}
				state := dto.ModelVariantHost{Host: name, State: cmp.Or(on.Presence, "missing")}
				if state.State != "missing" {
					state.Bytes = on.Bytes
				}
				if state.State == "mismatch" {
					state.Mismatch = on.Mismatch
				}
				for _, c := range on.Components {
					if c.Separate {
						state.Separate = append(state.Separate, c.Component)
					}
				}
				if job, ok := downloading[name+" "+id+" "+vid]; ok {
					state.State, state.Job = "downloading", &job
				}
				variant.Hosts = append(variant.Hosts, state)
			}
			model.Variants = append(model.Variants, variant)
		}
		model.Addons = buildAddons(id, sources, published, hostNames, byHost, downloading, used)
		out.Models = append(out.Models, model)
	}
	slices.SortFunc(out.Models, func(a, b dto.Model) int {
		return cmp.Or(strings.Compare(a.Title, b.Title), strings.Compare(a.ID, b.ID))
	})
	for _, id := range slices.Sorted(maps.Keys(used)) {
		if l, ok := licences[id]; ok {
			out.Licences = append(out.Licences, l)
		}
	}
	return out
}

// buildAddons is a model's optional add-ons on the Models screen (API 1.36): each add-on's descriptive fields from the
// freshest key that lists it (sources' order), its state on every host whose first env listing the model lists it.
func buildAddons(id string, sources []string, published map[string]registry.Published, hostNames []string,
	byHost map[string][]registry.Host, downloading map[string]dto.VariantJob, used map[string]bool) []dto.ModelAddon {
	model := func(source string) (registry.Model, bool) {
		i := slices.IndexFunc(published[source].Models, func(m registry.Model) bool { return m.ID == id })
		if i < 0 {
			return registry.Model{}, false
		}
		return published[source].Models[i], true
	}
	out := []dto.ModelAddon{}
	seen := map[string]bool{}
	for _, source := range sources {
		m, _ := model(source)
		for _, a := range m.Addons {
			if seen[a.Component] {
				continue
			}
			seen[a.Component] = true
			addon := dto.ModelAddon{Component: a.Component, Title: a.Title, Help: a.Help, Hosts: []dto.ModelVariantHost{}}
			if a.Dir != "" {
				addon.Dir = &a.Dir
			}
			if src := a.Source; src != nil {
				addon.Licence = &dto.VariantLicence{ID: src.Licence, MustAccept: src.MustAccept}
				addon.Gated, addon.DownloadBytes, used[src.Licence] = src.Gated, src.Bytes, true
			}
			for _, name := range hostNames {
				var on *registry.Addon
				for _, k := range byHost[name] {
					if m, listed := model(k.Host + "." + k.Env); listed {
						if found, ok := m.Addon(a.Component); ok {
							on = &found
						}
						break
					}
				}
				if on == nil {
					continue
				}
				state := dto.ModelVariantHost{Host: name, State: cmp.Or(on.Presence, "missing")}
				if state.State != "missing" {
					state.Bytes = on.Bytes
				}
				if state.State == "mismatch" {
					state.Mismatch = on.Mismatch
				}
				if job, ok := downloading[name+" "+id+" +"+a.Component]; ok {
					state.State, state.Job = "downloading", &job
				}
				addon.Hosts = append(addon.Hosts, state)
			}
			out = append(out, addon)
		}
	}
	return out
}

// ------------------------------------------------------------------ what a model job may do

// weights is one look at what the runners publish, for a model job's refusals.
type weights struct {
	now       time.Time
	values    []registry.Host
	published map[string]registry.Published
}

func (s *Service) weights() weights {
	return weights{now: time.Now(), values: s.registry.Hosts(), published: s.registry.Published()}
}

// known reports whether any present key's models key lists model.
func (w weights) known(model string) bool {
	for _, v := range w.values {
		if slices.ContainsFunc(w.published[v.Host+"."+v.Env].Models, func(m registry.Model) bool { return m.ID == model }) {
			return true
		}
	}
	return false
}

// envFor is the first env by name of host whose models key lists model, with that model.
func (w weights) envFor(host, model string) (string, registry.Model, bool) {
	env, found := "", registry.Model{}
	for _, v := range w.values {
		if v.Host != host || (env != "" && v.Env > env) {
			continue
		}
		for _, m := range w.published[v.Host+"."+v.Env].Models {
			if m.ID == model {
				env, found = v.Env, m
			}
		}
	}
	return env, found, env != ""
}

// key is the present hosts key of host.env.
func (w weights) key(host, env string) (registry.Host, bool) {
	i := slices.IndexFunc(w.values, func(v registry.Host) bool { return v.Host == host && v.Env == env })
	if i < 0 {
		return registry.Host{}, false
	}
	return w.values[i], true
}

// writable is a 409 when host.env's models root is bind-mounted read-only (API 1.18: models_disk.writable false; a
// GPU host that takes no model jobs).
func (w weights) writable(host, env string) error {
	if key, ok := w.key(host, env); ok && key.ModelsDisk != nil && key.ModelsDisk.Writable != nil && !*key.ModelsDisk.Writable {
		return network.NewConflictError("models root on "+host+" is read-only", nil)
	}
	return nil
}

// variantTitle is `<model title> <variant title>`.
func variantTitle(m registry.Model, v registry.Variant) string {
	return m.Title + " " + cmp.Or(v.Title, v.ID)
}

// download is what a download job takes: its env, model, variant (or add-on, API 1.36) and the licence accepted.
type download struct {
	env     string
	model   registry.Model
	variant registry.Variant
	addon   *registry.Addon
	licence *dto.JobLicence
}

// checkDownload applies POST …/download's registry refusals (404, 422, 409 already on the host).
func (w weights) checkDownload(host, model string, variant, addon *string, accept bool) (download, error) {
	if !w.known(model) {
		return download{}, errNotFound
	}
	env, m, ok := w.envFor(host, model)
	if !ok {
		return download{}, network.NewUnprocessableError(fmt.Sprintf("host %s has no env for %s", host, model), nil)
	}
	out := download{env: env, model: m}
	if err := w.writable(host, env); err != nil {
		return out, err
	}
	if addon != nil {
		return w.checkAddonDownload(host, out, *addon, accept)
	}
	switch {
	case variant == nil && len(m.Variants) == 0:
		return out, network.NewUnprocessableError(m.Title+" has no download source", nil)
	case variant == nil:
		out.variant = m.Variants[0]
	default:
		i := slices.IndexFunc(m.Variants, func(v registry.Variant) bool { return v.ID == *variant })
		if i < 0 {
			return out, network.NewUnprocessableError(fmt.Sprintf("%s has no variant %s", model, *variant), nil)
		}
		out.variant = m.Variants[i]
	}
	src := out.variant.Source
	switch {
	case src == nil:
		return out, network.NewUnprocessableError(variantTitle(m, out.variant)+" has no download source", nil)
	case src.MustAccept && !accept:
		return out, network.NewUnprocessableError("licence "+src.Licence+" must be accepted", nil)
	case out.variant.Presence == "present":
		return out, network.NewConflictError(fmt.Sprintf("%s is already on %s", variantTitle(m, out.variant), host), nil)
	}
	out.licence = w.accepted(host, env, src)
	return out, nil
}

// checkAddonDownload is checkDownload for an optional add-on (API 1.36).
func (w weights) checkAddonDownload(host string, out download, component string, accept bool) (download, error) {
	m := out.model
	a, ok := m.Addon(component)
	if !ok {
		return out, network.NewUnprocessableError(fmt.Sprintf("%s has no add-on %s", m.ID, component), nil)
	}
	out.addon = &a
	switch src := a.Source; {
	case src == nil:
		return out, network.NewUnprocessableError(m.Title+" "+a.Title+" has no download source", nil)
	case src.MustAccept && !accept:
		return out, network.NewUnprocessableError("licence "+src.Licence+" must be accepted", nil)
	case a.Presence == "present":
		return out, network.NewConflictError(fmt.Sprintf("%s %s is already on %s", m.Title, a.Title, host), nil)
	}
	out.licence = w.accepted(host, out.env, a.Source)
	return out, nil
}

// accepted is the licence a download of src records as accepted (its text as host.env publishes it), or nil.
func (w weights) accepted(host, env string, src *registry.Source) *dto.JobLicence {
	if !src.MustAccept {
		return nil
	}
	text := ""
	if i := slices.IndexFunc(w.published[host+"."+env].Licences, func(l registry.Licence) bool { return l.ID == src.Licence }); i >= 0 {
		text = w.published[host+"."+env].Licences[i].Text
	}
	sum := sha256.Sum256([]byte(text))
	return &dto.JobLicence{ID: src.Licence, SHA256: hex.EncodeToString(sum[:])}
}

// removal is what a delete job removes: its env, the model, the variants deleted, the shared components kept and the
// (component, owner variant) pairs whose files go.
type removal struct {
	env      string
	model    registry.Model
	variants []string
	keeps    []dto.Keep
	removed  map[string]bool // "<component> <from_variant>"
	title    string
}

// checkDelete applies DELETE …/models/{model}'s registry refusals: 404, 422, 409 not on the host, 409 loaded.
func (w weights) checkDelete(host, model string, variant, addon *string) (removal, error) {
	if !w.known(model) {
		return removal{}, errNotFound
	}
	env, m, ok := w.envFor(host, model)
	if !ok {
		return removal{}, network.NewUnprocessableError(fmt.Sprintf("host %s has no env for %s", host, model), nil)
	}
	out := removal{env: env, model: m, removed: map[string]bool{}, keeps: []dto.Keep{}, title: m.Title}
	if err := w.writable(host, env); err != nil {
		return out, err
	}
	on := func(v registry.Variant) bool { return v.Presence != "missing" && v.Presence != "" }
	if addon != nil { // API 1.36: only the add-on's files
		a, ok := m.Addon(*addon)
		switch {
		case !ok:
			return out, network.NewUnprocessableError(fmt.Sprintf("%s has no add-on %s", model, *addon), nil)
		case a.Presence == "missing" || a.Presence == "":
			return out, network.NewConflictError(fmt.Sprintf("%s %s is not on %s", m.Title, a.Title, host), nil)
		}
		out.title = m.Title + " · " + a.Title
		for _, v := range w.values {
			if v.Host == host && slices.ContainsFunc(v.Resident, func(r registry.Resident) bool { return r.Model == model && r.Component == *addon }) {
				return out, network.NewConflictError(fmt.Sprintf("%s of %s is loaded on %s; unload it first", *addon, m.Title, host), nil)
			}
		}
		return out, nil
	}
	if variant != nil {
		i := slices.IndexFunc(m.Variants, func(v registry.Variant) bool { return v.ID == *variant })
		if i < 0 {
			return out, network.NewUnprocessableError(fmt.Sprintf("%s has no variant %s", model, *variant), nil)
		}
		if !on(m.Variants[i]) {
			return out, network.NewConflictError(fmt.Sprintf("%s is not on %s", variantTitle(m, m.Variants[i]), host), nil)
		}
		out.variants, out.title = []string{*variant}, m.Title+" · "+cmp.Or(m.Variants[i].Title, *variant)
	} else {
		for _, v := range m.Variants {
			if on(v) {
				out.variants = append(out.variants, v.ID)
			}
		}
		if len(out.variants) == 0 {
			return out, network.NewConflictError(fmt.Sprintf("%s is not on %s", m.Title, host), nil)
		}
	}
	for _, v := range m.Variants {
		if slices.Contains(out.variants, v.ID) {
			for _, c := range v.Components {
				if c.FromVariant == v.ID {
					out.removed[c.Component+" "+v.ID] = true
				}
			}
		}
	}
	for _, pair := range slices.Sorted(maps.Keys(out.removed)) { // kept while a variant not deleted loads it
		component, owner, _ := strings.Cut(pair, " ")
		var still []string
		for _, v := range m.Variants {
			if on(v) && !slices.Contains(out.variants, v.ID) && slices.ContainsFunc(v.Components, func(c registry.Component) bool {
				return c.Component == component && c.FromVariant == owner && !c.Separate
			}) {
				still = append(still, v.ID)
			}
		}
		if len(still) > 0 {
			delete(out.removed, pair)
			out.keeps = append(out.keeps, dto.Keep{Component: component, FromVariant: owner, For: still})
		}
	}
	for _, v := range w.values {
		if v.Host != host {
			continue
		}
		for _, r := range v.Resident {
			if r.Model == model && out.removed[r.Component+" "+r.Variant] {
				return out, network.NewConflictError(fmt.Sprintf("%s of %s is loaded on %s; unload it first", r.Component, m.Title, host), nil)
			}
		}
	}
	return out, nil
}

// usedBy reports whether a run not yet terminal on host (placed there, or not placed and an env of the host lists
// the model) has a <family>/model node of the model's family.
func (w weights) usedBy(host string, del removal, runs []runOnHost) bool {
	for _, run := range runs {
		if run.host == nil && !slices.ContainsFunc(w.values, func(v registry.Host) bool {
			return v.Host == host && v.Env == run.env && slices.ContainsFunc(w.published[v.Host+"."+v.Env].Models,
				func(m registry.Model) bool { return m.ID == del.model.ID })
		}) {
			continue
		}
		if slices.ContainsFunc(run.models, func(m runModel) bool { return m.family == del.model.Family }) {
			return true
		}
	}
	return false
}

// loraDelete is what a lora_delete job takes: the env whose loras key lists it, and its name.
type loraDelete struct {
	env, name string
}

// checkLoraDelete applies DELETE /admin/loras/{lora_id}'s registry refusals: 404 not listed, 409 pre-fused or baked
// into a loaded component.
func (w weights) checkLoraDelete(host, id string) (loraDelete, error) {
	out := loraDelete{}
	for _, v := range w.values {
		if v.Host != host || (out.env != "" && v.Env > out.env) {
			continue
		}
		for _, l := range w.published[v.Host+"."+v.Env].Loras {
			if l.ID == id {
				out = loraDelete{env: v.Env, name: l.Name}
			}
		}
	}
	if out.env == "" {
		return out, errNotFound
	}
	for _, v := range w.values {
		if v.Host != host {
			continue
		}
		for _, m := range w.published[v.Host+"."+v.Env].Models {
			if m.Lora != nil && slices.ContainsFunc(m.Lora.Prefused, func(p registry.Prefused) bool { return p.LoraID == id }) {
				return out, network.NewConflictError(fmt.Sprintf("LoRA %s is part of %s (pre-fused); delete the model instead", out.name, m.Title), nil)
			}
		}
		for _, r := range v.Resident {
			if slices.ContainsFunc(r.Loras, func(l registry.ResidentLora) bool { return deref(l.LoraID) == id }) {
				return out, network.NewConflictError(fmt.Sprintf("LoRA %s is baked into a loaded component on %s; unload it first", out.name, host), nil)
			}
		}
	}
	return out, nil
}

// orEmpty is list, or an empty one for nil (a JSON [] rather than null).
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
