package visual

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/chrome"
)

// What M7b's screens do, read from the DOM rather than from pixels.
//
// A golden holds what a screen looks like. These hold what a golden cannot:
// that the three facts on a card are three facts and not one derived from
// another; that an entry which does not validate is listed rather than
// offered; that a form edit leaves as a pointer and a value and comes back
// with the author's comments intact; and that the Install button on the
// approval screen is below every command it is asking about.

// screen opens one of the launcher's fixtures and waits for it to settle.
func screen(t *testing.T, ctx context.Context, srv string, name string, w, h int) *chrome.Page {
	t.Helper()
	b := openBrowser(t)
	p, err := b.NewPage(ctx, w, h, "dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	if err := p.Navigate(ctx, srv+"/fixtures/launcher.html?theme=dark&screen="+name); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitFor(ctx, `document.documentElement.dataset.ready === "1"`); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLibraryCardsStateThreeFacts(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "catalogue", 1280, 1400)

	for _, c := range []struct {
		name string
		expr string
		want float64
	}{
		{
			// Source and level are independent: the local override is
			// Unverified and a registry entry is Unverified too. A card that
			// derived one from the other would have to disagree with one of
			// these two.
			"source and level are two facts, not one",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "wan studio");
				const chips = [...card.querySelectorAll(".helm-card-facts .helm-chip")].map(c => c.textContent);
				return chips.join("|") === "Local|Unverified" ? 1 : 0;
			})()`,
			1,
		},
		{
			"a registry entry wears the registry chip",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "ltx studio");
				const chips = [...card.querySelectorAll(".helm-card-facts .helm-chip")].map(c => c.textContent);
				return chips.join("|") === "Registry|Unverified" ? 1 : 0;
			})()`,
			1,
		},
		{
			// R2 as amended (M7 Q6): an entry whose manifest does not validate
			// is listed as invalid, with what is wrong. A studio that vanished
			// is worse than one that says what is wrong with it, because only
			// one of them can be fixed.
			"an invalid entry is listed, with its errors",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "zed-studio");
				if (!card) return 0;
				return card.textContent.includes("does not validate") && card.textContent.includes("more than one health probe") ? 1 : 0;
			})()`,
			1,
		},
		{
			// And it offers no Install: there is nothing to install.
			"an invalid entry offers no install",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "zed-studio");
				return [...card.querySelectorAll("button")].filter(b => ["Install", "Launch", "Retry"].includes(b.textContent)).length;
			})()`,
			0,
		},
		{
			// And it is not reported as running. `manifest_loaded` is false for
			// two different things — a studio the daemon is still running
			// without a manifest, and an entry whose manifest never loaded —
			// and only one of them has a process to stop.
			"an invalid entry is not reported as running",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "zed-studio");
				const chip = card.querySelector(".helm-card-actions .helm-chip").textContent;
				return chip === "Manifest invalid" ? 1 : 0;
			})()`,
			1,
		},
		{
			// It does offer the editor, which is the only thing that helps.
			"an invalid entry can still be edited",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "zed-studio");
				return [...card.querySelectorAll("button")].filter(b => b.textContent === "Edit").length;
			})()`,
			1,
		},
		{
			// Revert is offered where there is something underneath to go back
			// to, and nowhere else.
			"revert is offered only over something",
			`(() => {
				const has = (label) => {
					const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === label);
					return [...card.querySelectorAll("button")].some(b => b.textContent === "Revert");
				};
				return (has("zed-studio") && has("wan studio") && !has("ltx studio") && !has("h3 studio")) ? 1 : 0;
			})()`,
			1,
		},
		{
			// The checkpoint is chosen on the card, before Launch. The approval
			// screen offers it only when an approval is needed, so a studio
			// whose approval is current would otherwise never offer a choice.
			"a stopped studio offers its checkpoints, with nothing pretended",
			`(() => {
				const sel = document.getElementById("checkpoint-auk-studio");
				if (!sel || sel.disabled) return 0;
				const chosen = sel.options[sel.selectedIndex];
				return chosen.textContent === "— choose —" && sel.options.length === 3 ? 1 : 0;
			})()`,
			1,
		},
		{
			// While work is in flight the choice is shown and cannot be made:
			// the daemon refuses it, and a control that looked live would be
			// an error waiting to be clicked.
			"a busy studio shows its checkpoint and does not offer a change",
			`(() => {
				const sel = document.getElementById("checkpoint-iris-studio");
				return sel && sel.disabled && sel.value === "flux_klein_4b" ? 1 : 0;
			})()`,
			1,
		},
		{
			// One checkpoint is not a choice.
			"a studio with no choice to make offers none",
			`document.getElementById("checkpoint-ltx-studio") === null ? 1 : 0`,
			1,
		},
		{
			// Where a Local entry came from is a note, and it is on the card.
			"a local entry says where it came from",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "wan studio");
				return card.textContent.includes("Imported from https://example.com/wan-studio.yaml") ? 1 : 0;
			})()`,
			1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got float64
			if err := p.Eval(ctx, c.expr, &got); err != nil {
				t.Fatalf("evaluating %s: %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The launcher redraws only when what it draws has moved, so a field a card
// states and the change signature leaves out is a field that never updates.
// A Revert changes a card's source and nothing else.
func TestTheLibraryRedrawsWhenOnlyACardsFactsChange(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "catalogue", 1000, 800)

	for _, field := range []string{"source", "level", "manifest_valid", "selection", "provenance", "approval_required", "rebuild_needed", "errors"} {
		var moved bool
		expr := `(async () => {
			const { signature, newStore } = await import("/web/app.js");
			const { studios } = await import("/fixtures/fake.js");
			const before = newStore();
			before.studios = structuredClone(studios);
			const after = newStore();
			after.studios = structuredClone(studios);
			const wan = after.studios.find(s => s.id === "wan-studio");
			const change = {
				source: () => { wan.source = "registry"; },
				level: () => { wan.level = "draft"; },
				manifest_valid: () => { wan.manifest_valid = false; },
				selection: () => { wan.selection = "other"; },
				provenance: () => { wan.provenance = { kind: "duplicated" }; },
				approval_required: () => { wan.approval_required = !wan.approval_required; },
				rebuild_needed: () => { wan.rebuild_needed = !wan.rebuild_needed; },
				errors: () => { wan.errors = [{ pointer: "/id", message: "x" }]; },
			}["` + field + `"];
			change();
			return signature({ store: before }, "#/studios") !== signature({ store: after }, "#/studios");
		})()`
		if err := p.Eval(ctx, expr, &moved); err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if !moved {
			t.Errorf("changing only %s leaves the redraw signature the same, so the card would not update", field)
		}
	}
}

func TestTheEditorSendsAPointerAndAValue(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "editor", 1280, 1600)

	// Every field in the map that the schema describes is drawn. A form that
	// quietly lost one would still look like a form.
	var drawn float64
	if err := p.Eval(ctx, `document.querySelectorAll(".helm-editor .helm-field").length`, &drawn); err != nil {
		t.Fatal(err)
	}
	if drawn < 20 {
		t.Errorf("the form drew %v fields; the map names more than that", drawn)
	}

	// A required field says so; an optional one inside an optional object does
	// not. `python.version` is required *within* `python`, and nothing
	// requires `python`.
	var marks string
	if err := p.Eval(ctx, `(() => {
		const label = (id) => document.querySelector('label[for="' + id + '"]').textContent;
		return [label("f-id"), label("f-python-version"), label("f-license")].join("|");
	})()`, &marks); err != nil {
		t.Fatal(err)
	}
	if marks != "Id *|Python|Licence" {
		t.Errorf("required marks are %q, want %q", marks, "Id *|Python|Licence")
	}

	// The round trip: change one field, and the text comes back with the
	// author's comment and key order intact. Everything about this assertion
	// happened on the daemon — the page sent a pointer and a value.
	var after string
	const edit = `(async () => {
		const box = document.getElementById("f-description");
		box.value = "Wan 2.6, described by somebody who got it running.";
		box.dispatchEvent(new Event("change"));
		for (let i = 0; i < 100; i++) {
			await new Promise(r => setTimeout(r, 50));
			const t = document.querySelector("textarea.helm-yaml").value;
			if (t.includes("got it running")) return t;
		}
		return document.querySelector("textarea.helm-yaml").value;
	})()`
	if err := p.Eval(ctx, edit, &after); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"description: Wan 2.6, described by somebody who got it running.",
		"# wan-studio — written by hand", // the author's comment survived
		"# No test.profile yet",          // including one inside a process
	} {
		if !strings.Contains(after, want) {
			t.Errorf("after one field edit the text does not contain %q:\n%s", want, after)
		}
	}
	// And the id is still the first key: an edit is applied to the node tree,
	// not a decode and re-encode.
	if !strings.Contains(after, "id: wan-studio\nname: wan studio") {
		t.Errorf("the key order changed:\n%s", after)
	}
}

func TestTheApprovalScreenPutsItsButtonAfterTheCommands(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "approve", 1280, 1600)

	// The whole shape of this screen is that you reach Install by scrolling
	// past what it would run. A button beside the title would let someone
	// approve a screen they never read.
	var below float64
	if err := p.Eval(ctx, `(() => {
		const cmds = [...document.querySelectorAll(".helm-step-command")];
		if (!cmds.length) return 0;
		const last = Math.max(...cmds.map(c => c.getBoundingClientRect().bottom));
		const go = [...document.querySelectorAll("button")].find(b => b.textContent.startsWith("Install"));
		return go && go.getBoundingClientRect().top > last ? 1 : 0;
	})()`, &below); err != nil {
		t.Fatal(err)
	}
	if below != 1 {
		t.Error("the Install button is not below every command the screen is asking about")
	}

	// Every command, grouped by when it runs — including the process command,
	// which never passed through install and runs at every launch.
	var groups string
	if err := p.Eval(ctx, `[...document.querySelectorAll(".helm-section-label")].map(e => e.textContent).join("|")`, &groups); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Runs once, when you install it",
		"Runs once, the first time it starts",
		"Runs every time you launch it",
	} {
		if !strings.Contains(groups, want) {
			t.Errorf("the commands are not grouped by when they run: %q has no %q", groups, want)
		}
	}

	// A check nobody ran is never a pass. The two that build and run the
	// studio are exactly the two this screen is asking permission for.
	var notRun float64
	if err := p.Eval(ctx, `(() => {
		const text = document.body.textContent;
		return text.includes("Not run before install: it builds and runs the studio") ? 1 : 0;
	})()`, &notRun); err != nil {
		t.Fatal(err)
	}
	if notRun != 1 {
		t.Error("the smoke test is not listed as not run, with its reason")
	}
}

func TestImportNamesACollisionRatherThanResolvingIt(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "import", 1280, 1000)

	// Three documents, three verdicts, and the one that collides adds nothing
	// until somebody chooses. Silently replacing an entry a person already has
	// is the one thing import must not do.
	var state string
	if err := p.Eval(ctx, `(() => {
		const dlg = document.querySelector("dialog");
		const rows = [...dlg.querySelectorAll(".helm-import-row")].map(r => r.querySelector(".helm-chip").textContent);
		const go = [...dlg.querySelectorAll("button")].find(b => b.textContent.startsWith("Add "));
		return rows.join("|") + " :: " + go.textContent + " :: " + (go.disabled ? "disabled" : "enabled");
	})()`, &state); err != nil {
		t.Fatal(err)
	}
	want := "valid|collides|not a manifest :: Add 1 to library :: enabled"
	if state != want {
		t.Errorf("the report reads %q, want %q", state, want)
	}

	// And it says so, rather than leaving someone to infer it from a count.
	var says float64
	if err := p.Eval(ctx, `document.querySelector("dialog").textContent.includes("Nothing is installed by importing") ? 1 : 0`, &says); err != nil {
		t.Fatal(err)
	}
	if says != 1 {
		t.Error("the import dialog does not say that adding is not installing")
	}
}
