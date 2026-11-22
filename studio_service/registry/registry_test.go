package registry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixture reads a canonical registry value from contracts/fixtures/registry.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", name+".valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// present writes a live hosts key for each "<host>.<env>": a host's registry keys are read only while it has one.
func present(r *Registry, sources ...string) {
	for _, source := range sources {
		host, env, _ := strings.Cut(source, ".")
		_ = r.SetHost(source, []byte(`{"host": "`+host+`", "env": "`+env+`", "state": "idle", "run_id": null, "updated_at": "`+
			time.Now().UTC().Format(time.RFC3339Nano)+`"}`))
	}
}

// From API 1.18 a host's registry keys are read only while it has a present hosts key (any env): they lapse with it.
func TestRegistryKeysLapseWithTheHost(t *testing.T) {
	var r Registry
	_ = r.SetRegistry("gcp.torch.workflows", fixture(t, "qwen-image.workflows"))
	_ = r.SetRegistry("gcp.torch.models", fixture(t, "qwen-image.models"))
	if r.Publishes("gcp.torch", "qwen-image") || len(r.Published()) != 0 {
		t.Fatal("no hosts key: nothing read")
	}
	present(&r, "gcp.torch")
	if _, ok := r.Model("gcp.torch", "qwen-image"); !ok || !r.Publishes("gcp.torch", "qwen-image") || len(r.Published()) != 1 {
		t.Fatal("present: read")
	}
	_ = r.SetHost("gcp.torch", nil)
	if _, ok := r.Model("gcp.torch", "qwen-image"); ok || len(r.LoraBlocks()) != 0 {
		t.Fatal("gone again")
	}
}

// The fake runner's values give the echo workflow with the schemas of its three kinds; a workflow with a kind its
// runner did not publish is left out.
func TestWorkflows(t *testing.T) {
	var r Registry
	present(&r, "fake.fake", "other.fake")
	if len(r.Workflows()) != 0 {
		t.Fatal("empty registry")
	}
	if err := r.SetRegistry("fake.fake.workflows", fixture(t, "workflows")); err != nil {
		t.Fatal(err)
	}
	if len(r.Workflows()) != 0 {
		t.Fatal("workflows without their node schemas are not runnable")
	}
	_ = r.SetRegistry("fake.fake.nodes", fixture(t, "nodes"))
	_ = r.SetRegistry("fake.fake.models", fixture(t, "models"))
	workflow, ok := r.Workflow("echo")
	if !ok || workflow.Env != "fake" || len(workflow.NodeSchemas) != 3 || workflow.NodeSchemas["echo/echo"].Params[0].Name != "steps" {
		t.Fatalf("%+v", workflow)
	}
	_ = r.SetRegistry("other.fake.workflows", []byte(`{"workflows": [{"id": "echo", "env": "fake", "nodes": {"a": {"kind": "prompt"}}},
		{"id": "lost", "env": "fake", "nodes": {"a": {"kind": "nope"}}}]}`))
	ids := []string{}
	for _, w := range r.Workflows() {
		ids = append(ids, w.ID)
	}
	if list := r.Workflows(); !slices.Equal(ids, []string{"echo", "echo-a2v", "echo-ask", "echo-dialogue", "echo-extend", "echo-image-to-video",
		"echo-retake", "echo-tiny", "echo-video", "echo-voice-over"}) || list[2].Media != "text" || !IsText(list[2].Nodes) || IsText(list[0].Nodes) ||
		list[6].NodeSchemas["echo/retake"].Params[0].Accepts[0] != "video" {
		t.Fatalf("the echo workflows (echo-ask text), no lost: %v", ids)
	}
	if _, ok := r.Workflow("lost"); ok {
		t.Fatal("lost has no schema for its kind")
	}
}

// Publishes and Model answer per runner; a model's variant counts only when present, and a model without variants
// always has it.
func TestPublishesAndModels(t *testing.T) {
	var r Registry
	present(&r, "mac.torch", "fake.fake", "old.fake")
	_ = r.SetRegistry("mac.torch.workflows", fixture(t, "qwen-image.workflows"))
	_ = r.SetRegistry("mac.torch.models", []byte(`{"models": [{"id": "qwen-image-2.1", "family": "qwen-image", "variants": [
		{"id": "base", "presence": "present", "precisions": ["bf16"]}, {"id": "turbo", "presence": "missing", "precisions": ["bf16"]}]}]}`))
	_ = r.SetRegistry("fake.fake.models", fixture(t, "models"))
	if !r.Publishes("mac.torch", "qwen-image") || r.Publishes("mac.torch", "echo") || r.Publishes("gpu.torch", "qwen-image") {
		t.Fatal("Publishes")
	}
	qwen, ok := r.Model("mac.torch", "qwen-image")
	if !ok || qwen.ID != "qwen-image-2.1" || !qwen.Has("base") || qwen.Has("turbo") || qwen.Has("nope") {
		t.Fatalf("%+v", qwen)
	}
	if echo, ok := r.Model("fake.fake", "echo"); !ok || echo.Has("tiny") || echo.Has("anything") {
		t.Fatalf("the fake's tiny variants start missing: %+v", echo)
	}
	_ = r.SetRegistry("old.fake.models", []byte(`{"models": [{"id": "echo", "family": "echo"}]}`))
	if echo, ok := r.Model("old.fake", "echo"); !ok || !echo.Has("anything") {
		t.Fatalf("no variants: always has it %+v", echo)
	}
	if _, ok := r.Model("mac.torch", "echo"); ok {
		t.Fatal("no echo on mac")
	}
	_ = r.SetRegistry("mac.torch.models", nil)
	if _, ok := r.Model("mac.torch", "qwen-image"); ok {
		t.Fatal("a deleted models key is gone")
	}
}

// A value speaks only for its key's runner: a hosts value naming another host or env, and a workflow of another env
// or one whose id a runner of another env publishes, are refused. A deleted key is gone at once.
func TestValuesAreTiedToTheirKeys(t *testing.T) {
	var r Registry
	now := time.Now()
	if err := r.SetHost("fake.fake", []byte(`{"host": "gpu1", "env": "qwen", "state": "idle", "updated_at": "`+
		now.Format(time.RFC3339)+`"}`)); err == nil || len(r.Hosts()) != 0 {
		t.Fatal("a hosts value for another host was taken")
	}
	_ = r.SetHost("fake.fake", []byte(`{"host": "fake", "env": "fake", "state": "idle", "updated_at": "`+now.Format(time.RFC3339)+`"}`))
	if hosts := r.Hosts(); len(hosts) != 1 || !hosts[0].Live(now) || hosts[0].Live(now.Add(Stale+time.Second)) {
		t.Fatal("its own value, live until Stale")
	}
	_ = r.SetHost("fake.fake", nil)
	if len(r.Hosts()) != 0 {
		t.Fatal("a deleted hosts key is offline at once")
	}

	if err := r.SetRegistry("fake.fake.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "qwen", "nodes": {}}]}`)); err == nil {
		t.Fatal("a workflow of another env was taken")
	}
	present(&r, "gpu1.qwen", "aaaa.fake")
	_ = r.SetRegistry("gpu1.qwen.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "qwen", "nodes": {}}]}`))
	_ = r.SetRegistry("aaaa.fake.workflows", []byte(`{"workflows": [{"id": "qwen.edit", "env": "fake", "nodes": {}}]}`))
	if workflow, ok := r.Workflow("qwen.edit"); !ok || workflow.Env != "qwen" {
		t.Fatalf("another env took the id: %+v", workflow)
	}
	_ = r.SetRegistry("gpu1.qwen.workflows", nil)
	if _, ok := r.Workflow("qwen.edit"); ok {
		t.Fatal("a deleted registry key is gone")
	}
}

// The torch runner's canonical values give the qwen-image workflow; its hosts value carries memory and resident.
func TestQwenAndHostMemory(t *testing.T) {
	var r Registry
	present(&r, "mac.torch")
	_ = r.SetRegistry("mac.torch.nodes", fixture(t, "qwen-image.nodes"))
	_ = r.SetRegistry("mac.torch.workflows", fixture(t, "qwen-image.workflows"))
	workflow, ok := r.Workflow("qwen-image")
	if !ok || len(workflow.NodeSchemas) != 10 || workflow.Env != "torch" { // + qwen-image/enhance (API 1.36)
		t.Fatalf("%+v", workflow)
	}
	host3, _ := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "registry", "host.valid.3.json"))
	if err := r.SetHost("mac.torch", host3); err != nil {
		t.Fatal(err)
	}
	host := r.Hosts()[0]
	if host.Memory == nil || host.Memory.BudgetGB != 52 || len(host.Resident) != 2 || !host.Resident[0].Pinned ||
		host.Resident[0].Precision != "int8" {
		t.Fatalf("%+v", host)
	}
	if host.RunID == nil || host.Attempt != nil {
		t.Fatalf("busy with a run, attempt absent: %+v", host)
	}
}

// The palette is every kind a live host publishes, with its envs; GET /nodes groups it core first, then by family
// title. A stale host's kinds leave the palette but keep their schemas.
func TestPalette(t *testing.T) {
	var r Registry
	t0 := time.Now()
	_ = r.SetRegistry("fake.fake.nodes", fixture(t, "nodes"))
	_ = r.SetRegistry("mac.torch.nodes", fixture(t, "qwen-image.nodes"))
	_ = r.SetRegistry("mac.torch.models", fixture(t, "qwen-image.models"))
	host := func(name, env string, at time.Time) []byte {
		return []byte(`{"host": "` + name + `", "env": "` + env + `", "state": "idle", "run_id": null, "updated_at": "` +
			at.UTC().Format(time.RFC3339Nano) + `"}`)
	}
	_ = r.SetHost("fake.fake", host("fake", "fake", t0))
	_ = r.SetHost("mac.torch", host("mac", "torch", t0))
	palette := r.Palette(t0)
	if prompt := palette["prompt"]; len(prompt.Envs) != 2 || prompt.Envs[0] != "fake" || prompt.Envs[1] != "torch" {
		t.Fatalf("%+v", prompt)
	}
	groups := r.PaletteGroups(t0, "")
	if len(groups) != 3 || groups[0].Title != "Core" || groups[0].Family != nil || groups[1].Title != "Qwen Image 2.1" ||
		*groups[2].Family != "echo" || groups[2].Title != "echo" { // a family title from its live models entry, else its id
		t.Fatalf("%+v", groups)
	}
	if torch := r.PaletteGroups(t0, "torch"); len(torch) != 2 || torch[1].Nodes[0].Kind != "qwen-image/model" {
		t.Fatalf("%+v", torch)
	}
	if !r.PublishesKinds("mac.torch", []string{"prompt", "qwen-image/sample"}) || r.PublishesKinds("mac.torch", []string{"echo/echo"}) {
		t.Fatal("PublishesKinds")
	}
	_ = r.SetHost("mac.torch", host("mac", "torch", t0.Add(-time.Minute)))
	if _, ok := r.Palette(t0)["qwen-image/sample"]; ok {
		t.Fatal("a stale host's kinds are not in the palette")
	}
	if _, ok := r.Schemas()["qwen-image/sample"]; !ok {
		t.Fatal("but the registry still has their schemas")
	}
}

// Family detection prefers a family a non-fake env publishes: LoraBlocks marks the fake-only ones, and a shared LoRA
// that a fake host and a torch host both index takes the torch host's family.
func TestLoraFamiliesPreferNonFakeEnvs(t *testing.T) {
	var r Registry
	t0 := time.Now()
	_ = r.SetRegistry("fake.fake.models", fixture(t, "models"))
	_ = r.SetRegistry("mac.torch.models", fixture(t, "qwen-image.models"))
	_ = r.SetRegistry("other.fake.models", fixture(t, "qwen-image.models"))
	present(&r, "fake.fake", "mac.torch", "other.fake")
	blocks := r.LoraBlocks()
	if !blocks["echo"].Fake || blocks["qwen-image"].Fake || len(blocks["qwen-image"].Modules) == 0 {
		t.Fatalf("%+v", blocks)
	}
	// M9: a torch runner publishes ltx without a lora block (LoRAs are MLX's only): the mlx runner's block is used,
	// though gcp.torch sorts first
	_ = r.SetRegistry("gcp.torch.models", []byte(`{"models": [{"id": "ltx-2.5", "family": "ltx", "title": "LTX-2.5"}]}`))
	_ = r.SetRegistry("mac.mlx.models", fixture(t, "ltx.models"))
	present(&r, "gcp.torch", "mac.mlx")
	if b := r.LoraBlocks()["ltx"]; len(b.Renames) == 0 || b.Fake {
		t.Fatalf("%+v", b)
	}
	host := func(name, env string) []byte {
		return []byte(`{"host": "` + name + `", "env": "` + env + `", "state": "idle", "run_id": null, "updated_at": "` +
			t0.UTC().Format(time.RFC3339Nano) + `"}`)
	}
	lora := func(family string) []byte {
		return []byte(`{"loras": [{"id": "` + strings.Repeat("a", 64) + `", "name": "x", "family": "` + family +
			`", "rank": 4, "bytes": 1, "key_style": "peft", "licence": null, "modified_at": "2026-11-14T10:00:00Z"}]}`)
	}
	_ = r.SetHost("fake.fake", host("fake", "fake"))
	_ = r.SetHost("mac.torch", host("mac", "torch"))
	_ = r.SetRegistry("fake.fake.loras", lora("echo"))
	_ = r.SetRegistry("mac.torch.loras", lora("qwen-image"))
	if got := r.Loras(t0, "")[strings.Repeat("a", 64)]; got.Family != "qwen-image" || len(got.Hosts) != 2 {
		t.Fatalf("%+v", got)
	}
	_ = r.SetRegistry("mac.torch.loras", lora("unknown"))
	if got := r.Loras(t0, "")[strings.Repeat("a", 64)]; got.Family != "echo" {
		t.Fatalf("%+v", got)
	}
}

// Starter looks: the union over live hosts; one id on several hosts is the highest version (a tie: a non-fake env's
// first), a LoRA id the first non-null for that version and file, created at the oldest published_at; a host gone
// stale drops its looks.
func TestStarterLooks(t *testing.T) {
	var r Registry
	now := time.Now()
	host := func(name, env string, age time.Duration) []byte {
		return []byte(`{"host": "` + name + `", "env": "` + env + `", "state": "idle", "run_id": null, "updated_at": "` +
			now.Add(-age).UTC().Format(time.RFC3339Nano) + `"}`)
	}
	looks := func(at, version, loraID string) []byte {
		return []byte(`{"published_at": "` + at + `", "looks": [{"id": "qwen-image/ink", "family": "qwen-image", "group": "film",
			"name": "Ink v` + version + `", "text": "", "version": ` + version + `, "loras": [{"file": "ink.safetensors",
			"name": "ink", "strength": 0.5, "lora_id": ` + loraID + `}], "thumb": null}]}`)
	}
	models := []byte(`{"models": [{"id": "echo", "family": "echo", "title": "Echo"}, {"id": "qwen-image", "family": "qwen-image", "title": "Qwen"}]}`)
	_ = r.SetHost("fake.fake", host("fake", "fake", 0))
	_ = r.SetHost("mac.torch", host("mac", "torch", 0))
	_ = r.SetHost("pc.torch", host("pc", "torch", 0))
	for _, source := range []string{"fake.fake", "mac.torch", "pc.torch"} {
		_ = r.SetRegistry(source+".models", models)
	}
	_ = r.SetRegistry("fake.fake.looks", fixture(t, "looks"))
	_ = r.SetRegistry("mac.torch.looks", looks("2026-11-14T12:00:00Z", "1", "null"))
	_ = r.SetRegistry("pc.torch.looks", looks("2026-11-14T10:00:00Z", "1", `"`+strings.Repeat("b", 64)+`"`))
	got := r.Looks(now)
	ink := got["qwen-image/ink"]
	if len(got) != 3 || got["echo/soft-light"].Text != "soft diffuse light" || ink.Loras[0].LoraID == nil ||
		*ink.Loras[0].LoraID != strings.Repeat("b", 64) || !ink.PublishedAt.Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", got)
	}
	// a fake env never replaces a non-fake env's entry, whatever its version
	_ = r.SetRegistry("fake.fake.looks", looks("2026-11-14T09:00:00Z", "2", "null"))
	if ink = r.Looks(now)["qwen-image/ink"]; ink.Name != "Ink v1" || ink.Loras[0].LoraID == nil ||
		!ink.PublishedAt.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", ink)
	}
	// with only fake hosts left, the highest version wins
	_ = r.SetHost("mac.torch", host("mac", "torch", time.Minute))
	_ = r.SetHost("pc.torch", host("pc", "torch", time.Minute))
	if ink = r.Looks(now)["qwen-image/ink"]; ink.Name != "Ink v2" || ink.Loras[0].LoraID != nil {
		t.Fatalf("%+v", ink)
	}
}

// A published starter look is kept only with an id <family>/<slug> of a family its host serves and no control
// characters; a thumb key outside packs/<family>/looks/ is dropped, never signed.
func TestStarterLookChecks(t *testing.T) {
	var r Registry
	now := time.Now()
	_ = r.SetHost("pc.torch", []byte(`{"host": "pc", "env": "torch", "state": "idle", "run_id": null, "updated_at": "`+
		now.UTC().Format(time.RFC3339Nano)+`"}`))
	_ = r.SetRegistry("pc.torch.models", []byte(`{"models": [{"id": "echo", "family": "echo", "title": "Echo"}]}`))
	look := func(id, family, text, thumb string) string {
		return `{"id": "` + id + `", "family": "` + family + `", "group": "film", "name": "N", "text": "` + text +
			`", "version": 1, "loras": [], "thumb": ` + thumb + `}`
	}
	key := func(k string) string {
		return `{"key": "` + k + `", "content_type": "image/png", "width": 1, "height": 1, "bytes": 1}`
	}
	_ = r.SetRegistry("pc.torch.looks", []byte(`{"published_at": "2026-11-14T10:00:00Z", "looks": [`+strings.Join([]string{
		look("echo/ok", "echo", `a\nb`, key("packs/echo/looks/ok-0123456789abcdef.png")),
		look("echo/victim", "echo", "x", key("u/10000000-0000-4000-8000-000000000001/takes/a.png")),
		look("echo/other", "echo", "x", key("packs/qwen-image/looks/other-0123456789abcdef.png")),
		look("qwen-image/golden", "echo", "x", "null"),       // id not of its family
		look("qwen-image/golden", "qwen-image", "x", "null"), // a family the host does not serve
		look("echo/nul", "echo", `a\u0000b`, "null"),
	}, ",")+`]}`))
	got := r.Looks(now)
	if len(got) != 3 || got["echo/ok"].Thumb == nil || got["echo/ok"].Text != "a\nb" || got["echo/victim"].Thumb != nil ||
		got["echo/other"].Thumb != nil {
		t.Fatalf("%+v", got)
	}
}

// A remote host (gcp) never replaces what the stack's own host (mac) publishes: not a workflow template by id, not a
// node schema however late its published_at, not a look by a higher version; a remote workflow under an id an own
// host's other env publishes never refuses the own host's. A hosts key's age is the KV entry's server time.
func TestRemoteHostsNeverShadowOwnHosts(t *testing.T) {
	var r Registry
	present(&r, "mac.torch", "mac.mlx", "gcp.torch")
	forged := func(name string) []byte {
		s := strings.Replace(string(fixture(t, name)), `"Qwen Image"`, `"Forged"`, 1)
		s = strings.Replace(s, `"The text the workflow starts from."`, `"Forged"`, 1)
		return []byte(strings.Replace(s, `"2026-11-14T10:00:00Z"`, `"2099-01-01T00:00:00Z"`, 1))
	}
	_ = r.SetRegistry("gcp.torch.workflows", forged("qwen-image.workflows"))
	_ = r.SetRegistry("gcp.torch.nodes", forged("qwen-image.nodes"))
	_ = r.SetRegistry("gcp.torch.workflows", []byte(`{"workflows": [{"id": "qwen-image", "title": "Forged", "env": "torch",
		"nodes": {"prompt": {"kind": "prompt"}}}, {"id": "ltx-text-to-video", "title": "Forged", "env": "torch",
		"nodes": {"prompt": {"kind": "prompt"}}}]}`))
	_ = r.SetRegistry("mac.torch.workflows", fixture(t, "qwen-image.workflows"))
	_ = r.SetRegistry("mac.torch.nodes", fixture(t, "qwen-image.nodes"))
	_ = r.SetRegistry("mac.mlx.workflows", fixture(t, "ltx.workflows"))
	_ = r.SetRegistry("mac.mlx.nodes", fixture(t, "ltx.nodes"))
	if w, ok := r.Workflow("qwen-image"); !ok || w.Title != "Qwen Image" {
		t.Fatalf("mac's template: %+v", w)
	}
	if w, ok := r.Workflow("ltx-text-to-video"); !ok || w.Env != "mlx" || w.Title == "Forged" {
		t.Fatalf("mac.mlx's, never refused for gcp's: %+v", w)
	}
	// API 1.40: a cross-node values_by keeps its note (the Timeline's Extend before line)
	if ps := r.Schemas()["ltx/extend"].Params; !slices.ContainsFunc(ps, func(p Param) bool {
		return p.ValuesBy != nil && strings.HasPrefix(p.ValuesBy.Note, "Dev: the only model")
	}) {
		t.Fatal("ltx/extend direction: values_by note dropped")
	}
	if help := r.Schemas()["prompt"].Help; help == "Forged" {
		t.Fatal("gcp's schema dated 2099 must not replace mac's")
	}
	models := []byte(`{"models": [{"id": "qwen-image", "family": "qwen-image", "title": "Qwen"}]}`)
	look := func(name, version string) []byte {
		return []byte(`{"published_at": "2099-01-01T00:00:00Z", "looks": [{"id": "qwen-image/ink", "family": "qwen-image",
			"group": "film", "name": "` + name + `", "text": "", "version": ` + version + `, "loras": [], "thumb": null}]}`)
	}
	for _, source := range []string{"mac.torch", "gcp.torch"} {
		_ = r.SetRegistry(source+".models", models)
	}
	_ = r.SetRegistry("gcp.torch.looks", look("Forged", "999"))
	_ = r.SetRegistry("mac.torch.looks", look("Ink", "1"))
	if ink := r.Looks(time.Now())["qwen-image/ink"]; ink.Name != "Ink" || ink.PublishedAt.After(time.Now()) {
		t.Fatalf("mac's look, published_at no later than receipt: %+v", ink)
	}

	stale := []byte(`{"host": "gcp", "env": "torch", "state": "idle", "run_id": null, "updated_at": "2001-01-01T00:00:00Z"}`)
	if err := r.SetHostAt("gcp.torch", stale, time.Now()); err != nil || !r.Hosts()[0].Live(time.Now()) {
		t.Fatal("the server's time decides, not the runner's clock", err)
	}
}

// Starter voices off registry.schema.json are dropped, and so is one whose id is also a starter look's.
func TestStarterVoiceChecks(t *testing.T) {
	var r Registry
	now := time.Now()
	_ = r.SetHost("pc.torch", []byte(`{"host": "pc", "env": "torch", "state": "idle", "run_id": null, "updated_at": "`+
		now.UTC().Format(time.RFC3339Nano)+`"}`))
	_ = r.SetRegistry("pc.torch.models", []byte(`{"models": [{"id": "echo", "family": "echo", "title": "Echo"}]}`))
	_ = r.SetRegistry("pc.torch.looks", []byte(`{"published_at": "2026-11-14T10:00:00Z", "looks": [{"id": "echo/same",
		"family": "echo", "group": "film", "name": "N", "text": "x", "version": 1, "loras": [], "thumb": null}]}`))
	voice := func(id, name, speaker, language string) string {
		return `{"id": "` + id + `", "family": "echo", "name": "` + name + `", "speaker": "` + speaker +
			`", "language": "` + language + `", "version": 1, "preview": null}`
	}
	_ = r.SetRegistry("pc.torch.voices", []byte(`{"published_at": "2026-11-14T10:00:00Z", "voices": [`+strings.Join([]string{
		voice("echo/ok", "Ada", "Ryan", "en-US"),
		voice("echo/same", "Ada", "Ryan", "en"),          // a starter look's id
		voice("echo/speaker", "Ada", "Ry an", "en"),      // speaker off its pattern
		voice("echo/language", "Ada", "Ryan", "English"), // not BCP 47
		voice("echo/noname", "", "Ryan", "en"),           // name 1-80
		voice("echo/long", strings.Repeat("a", 81), "Ryan", "en"),
	}, ",")+`]}`))
	if got := r.Voices(now); len(got) != 1 || got["echo/ok"].Name != "Ada" {
		t.Fatalf("%+v", got)
	}
}
