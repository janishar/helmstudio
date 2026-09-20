package visual

import (
	"context"
	"net/url"
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
	return open(t, ctx, srv+"/fixtures/launcher.html?theme=dark&screen="+name, w, h)
}

// route opens any launcher route over the fixture data and the real daemon.
func route(t *testing.T, ctx context.Context, srv string, hash string) *chrome.Page {
	t.Helper()
	return open(t, ctx, srv+"/fixtures/launcher.html?theme=dark&screen=editor&hash="+url.QueryEscape(hash), 1280, 1600)
}

func open(t *testing.T, ctx context.Context, address string, w, h int) *chrome.Page {
	t.Helper()
	b := openBrowser(t)
	p, err := b.NewPage(ctx, w, h, "dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	if err := p.Navigate(ctx, address); err != nil {
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
			// Plain text since the launcher redesign (03 §6, amended), and still
			// each its own element.
			"source and level are two facts, not one",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "wan studio");
				const facts = [...card.querySelectorAll("[data-fact]")].map(c => c.textContent);
				return facts.join("|") === "Local|Unverified" ? 1 : 0;
			})()`,
			1,
		},
		{
			"a registry entry says it is from the registry",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "ltx studio");
				const facts = [...card.querySelectorAll("[data-fact]")].map(c => c.textContent);
				return facts.join("|") === "Registry|Unverified" ? 1 : 0;
			})()`,
			1,
		},
		{
			// 03 §1, amended: at most one accent on a screen, and on Studios it
			// is the running studio's Open.
			"the screen has one accent, and it is the running studio's Open",
			`(() => {
				const accents = [...document.querySelectorAll("main .helm-btn-primary")];
				if (accents.length !== 1) return accents.length + 10;
				return accents[0].textContent === "Open" && accents[0].closest(".helm-card").getAttribute("aria-label") === "ltx studio" ? 1 : 0;
			})()`,
			1,
		},
		{
			// Where the code comes from is stated, never switched (03 §6).
			"a row says where its code comes from",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "h3 studio");
				return card.querySelector(".helm-studio-facts").textContent.includes("clones github.com/janishar/h3c-studio at main") ? 1 : 0;
			})()`,
			1,
		},
		{
			// A stripe is the studio's own hue, never the accent (03 §2).
			"a row's stripe is its studio's hue",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "h3 studio");
				return getComputedStyle(card.querySelector(".helm-card-stripe")).backgroundColor === "rgb(224, 163, 60)" ? 1 : 0;
			})()`,
			1,
		},
		{
			// While a download runs, its bar and numbers take the facts line's
			// place rather than being added under it.
			"a download replaces the facts line with its progress",
			`(() => {
				const card = [...document.querySelectorAll(".helm-card")].find(c => c.getAttribute("aria-label") === "iris studio");
				return card.querySelector(".helm-studio-progress [role=progressbar]") && !card.querySelector(".helm-studio-facts") ? 1 : 0;
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

	for _, field := range []string{"source", "level", "manifest_valid", "selection", "provenance", "approval_required", "rebuild_needed", "errors", "repo", "hue"} {
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
				repo: () => { wan.repo = "https://github.com/someone-else/wan"; },
				hue: () => { wan.hue = { dark: "#000000", light: "#ffffff" }; },
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

	// Every field in the map that the schema describes is drawn, one section
	// at a time (03 §13a, amended). A form that quietly lost one would still
	// look like a form.
	var drawn float64
	if err := p.Eval(ctx, `(async () => {
		let n = 0;
		for (const section of ["identity", "runtime", "host", "platform"]) {
			n += (await `+visit+`(section)).querySelectorAll(".helm-editor .helm-field").length;
		}
		return n;
	})()`, &drawn); err != nil {
		t.Fatal(err)
	}
	if drawn < 20 {
		t.Errorf("the form drew %v fields; the map names more than that", drawn)
	}

	// A required field says so; an optional one inside an optional object does
	// not. `python.version` is required *within* `python`, and nothing
	// requires `python`.
	var marks string
	if err := p.Eval(ctx, `(async () => {
		const label = async (section, id) => (await `+visit+`(section)).querySelector('label[for="' + id + '"]').textContent;
		return [await label("identity", "f-id"), await label("runtime", "f-python-version"), await label("identity", "f-license")].join("|");
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
	if err := p.Eval(ctx, editField+`("identity", "f-description", "Wan 2.6, described by somebody who got it running.", "got it running")`, &after); err != nil {
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
		const cmds = [...document.querySelectorAll(".helm-command")];
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

// visit opens one of the editor's sections the way its menu does, by address,
// and resolves to the page once the section is drawn.
const visit = `(async (section) => {
	location.hash = document.querySelector(".helm-editor-link").getAttribute("href").split("?")[0] + "?section=" + section;
	for (let i = 0; i < 100; i++) {
		const current = document.querySelector('.helm-editor-link[aria-current="true"]');
		if (current && current.getAttribute("href").endsWith("section=" + section)) return document;
		await new Promise(r => setTimeout(r, 20));
	}
	throw new Error("section " + section + " was never drawn");
})`

// editField changes one form field the way a person does, in the section that
// holds it, and waits in the text section for what the daemon hands back.
const editField = `(async (section, id, value, expect) => {
	const box = (await ` + visit + `(section)).getElementById(id);
	box.value = value;
	box.dispatchEvent(new Event("change"));
	await ` + visit + `("yaml");
	for (let i = 0; i < 100; i++) {
		await new Promise(r => setTimeout(r, 50));
		const t = document.querySelector("textarea.helm-yaml").value;
		if (t.includes(expect)) return t;
	}
	return document.querySelector("textarea.helm-yaml").value;
})`

// Every bundled studio is a registry pointer with its manifest inline, and
// Override opens one. A form that edited the pointer rather than the manifest
// it carries showed empty fields, and the first thing typed into one made the
// entry invalid: a pointer allows no `description`.
func TestOverridingARegistryEntryEditsTheManifestItCarries(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/edit/ptr-studio")

	var shown string
	if err := p.Eval(ctx, `document.getElementById("f-description").value`, &shown); err != nil {
		t.Fatal(err)
	}
	if shown != "A registry entry, overridden." {
		t.Fatalf("the form shows description %q, not the inline manifest's", shown)
	}

	var after string
	if err := p.Eval(ctx, editField+`("identity", "f-description", "Overridden, on purpose.", "Overridden, on purpose.")`, &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "manifest:\n  id: ptr-studio\n  name: ptr studio\n  description: Overridden, on purpose.") {
		t.Errorf("the description did not land in the inline manifest:\n%s", after)
	}

	// id, repo and ref are stated twice and must agree, so one form edit
	// writes both copies and the entry stays valid.
	if err := p.Eval(ctx, editField+`("identity", "f-repo", "https://github.com/someone-else/ptr", "someone-else")`, &after); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(after, "repo: https://github.com/someone-else/ptr"); n != 2 {
		t.Errorf("the repository was written %d times, want both copies:\n%s", n, after)
	}
	var verdict string
	if err := p.Eval(ctx, `(async () => {
		for (let i = 0; i < 40; i++) {
			const c = document.querySelector(".helm-editor-verdict .helm-chip");
			if (c && c.textContent === "Valid") return "Valid";
			await new Promise(r => setTimeout(r, 50));
		}
		return document.querySelector(".helm-editor-verdict .helm-chip").textContent;
	})()`, &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict != "Valid" {
		t.Errorf("after editing the repository the entry reads %q", verdict)
	}
}

// The editor exists to fix invalid documents. When the daemon sent no decoded
// document for one, the form guessed that every parent was missing — and a
// guessed parent is written as an empty map over the real one. Changing the
// minimum memory replaced all of \`requires\`.
func TestAFormEditOnAnInvalidManifestKeepsWhatItDidNotTouch(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/edit/bad-studio")

	var after string
	if err := p.Eval(ctx, editField+`("host", "f-requires-ram_gb", "48", "ram_gb: 48")`, &after); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ram_gb: 48", "os: [darwin]", "arch: [arm64]", "tools: [git]", "# Half written: no processes yet."} {
		if !strings.Contains(after, want) {
			t.Errorf("after setting the minimum memory the text has no %q:\n%s", want, after)
		}
	}
}

// The launcher redesign's reason for drawing in place: a redraw keeps what a
// person has open and where their focus is. Replacing the page on every poll
// closed a menu under the pointer and dropped focus to the body, so the page
// redrew as seldom as it could.
func TestARedrawKeepsAnOpenMenuAndFocus(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "catalogue", 1280, 1400)

	var got string
	if err := p.Eval(ctx, `(async () => {
		const id = "more-ltx-studio";
		document.querySelector('[popovertarget="' + id + '"]').click();
		for (let i = 0; i < 50 && document.activeElement.getAttribute("role") !== "menuitem"; i++) {
			await new Promise(r => setTimeout(r, 20));
		}
		const focused = document.activeElement;
		// A redraw with nothing changed, as a poll's.
		window.dispatchEvent(new HashChangeEvent("hashchange"));
		await new Promise(r => setTimeout(r, 100));
		const menu = document.getElementById(id);
		return [
			menu.matches(":popover-open") ? "open" : "closed",
			document.activeElement === focused ? "focus kept" : "focus lost to " + document.activeElement.tagName,
			document.querySelector('[popovertarget="' + id + '"]').getAttribute("aria-expanded"),
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "open|focus kept|true" {
		t.Errorf("after a redraw the menu reads %q, want open|focus kept|true", got)
	}

	// Escape closes it and gives focus back to the button that opened it.
	if err := p.Eval(ctx, `(async () => {
		document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		await new Promise(r => setTimeout(r, 50));
		return [
			document.getElementById("more-ltx-studio").matches(":popover-open") ? "open" : "closed",
			document.activeElement.getAttribute("aria-label"),
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "closed|More actions for ltx studio" {
		t.Errorf("after Escape the menu reads %q", got)
	}
}

// The skip link was a link to #main, which the router read as a route and
// answered with Studios. It moves focus, and goes nowhere.
func TestTheSkipLinkMovesFocusWithoutNavigating(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "models", 1280, 900)

	var got string
	if err := p.Eval(ctx, `(async () => {
		const before = location.href;
		document.querySelector(".helm-skip-link").click();
		await new Promise(r => setTimeout(r, 100));
		return (location.href === before ? "stayed" : "went to " + location.href) + "|" + document.activeElement.id + "|" + document.querySelector("main h1").textContent;
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "stayed|main|Models & disk" {
		t.Errorf("after the skip link: %q, want stayed|main|Models & disk", got)
	}

	// And a change of page puts focus on its title, so a screen reader says
	// where it arrived.
	if err := p.Eval(ctx, `(async () => {
		location.hash = "#/settings";
		await new Promise(r => setTimeout(r, 200));
		return document.activeElement.tagName + "|" + document.activeElement.textContent;
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "H1|Settings" {
		t.Errorf("after moving to Settings focus is on %q, want the page's title", got)
	}
}

// Add a studio checks a row when it is submitted, and says what is wrong under
// the field, tied to it — rather than a button that stayed disabled until a box
// had something in it, and never said why (03 §13, amended).
func TestAddAStudioSaysWhatIsWrongUnderTheField(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "add", 1280, 1000)

	var got string
	if err := p.Eval(ctx, `(async () => {
		const forms = document.querySelectorAll("form.helm-add-form");
		forms[1].querySelector("input").value = "code/wan";
		forms[1].requestSubmit();
		await new Promise(r => setTimeout(r, 100));
		const box = document.getElementById("add-folder-path");
		const said = document.getElementById("add-folder-path-error");
		return [
			box.getAttribute("aria-invalid"),
			(box.getAttribute("aria-describedby") || "").split(" ").includes("add-folder-path-error") ? "described" : "not described",
			said ? said.textContent : "no error",
			document.activeElement === box ? "focused" : "not focused",
			document.querySelectorAll("main .helm-btn-primary").length,
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	want := "true|described|code/wan is not an absolute path. Start it at the root, as in /Users/you/code/wan.|focused|1"
	if got != want {
		t.Errorf("after submitting a relative folder:\n got %q\nwant %q", got, want)
	}
}

// Models & disk: a linked folder is the user's, so it is not counted and it is
// unlinked rather than deleted; and red appears only in a confirmation, never
// on a row (03 §12, amended).
func TestModelsAndDiskStatesWhatItOwns(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "models", 1280, 900)

	var got string
	if err := p.Eval(ctx, `(() => {
		const row = [...document.querySelectorAll("tbody tr")].find(r => r.textContent.includes("Lightricks/LTX-2.5"));
		return [
			row.querySelector("td:nth-child(3)").textContent,
			[...row.querySelectorAll("button")].map(b => b.textContent).join(","),
			document.querySelectorAll("main .helm-btn-danger, main .helm-btn-danger-fill").length,
			document.querySelector(".helm-page-header .helm-meta").textContent,
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	// 60 GB, 4.1 GB and a 9.2 GB partial download: managed bytes, partial
	// files included, and never the linked folder's.
	want := "not counted|Copy,Unlink|0|73 GB used · 285 GB free"
	if got != want {
		t.Errorf("models read:\n got %q\nwant %q", got, want)
	}
}

// Remove is drawn only when there is a token to remove. It was hidden with
// `hidden`, which a button's own display overrides, so it showed with none.
func TestSettingsOffersRemoveOnlyWithAToken(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "settings", 1280, 900)

	var got string
	if err := p.Eval(ctx, `(async () => {
		const { settings } = await import("/web/settings.js");
		const { about } = await import("/fixtures/fake.js");
		const removes = (hfToken) => {
			const node = settings({ store: { hfToken, about, studios: [] }, client: {}, redraw() {} });
			return [...node.querySelectorAll("button")].filter(b => b.textContent === "Remove").length;
		};
		return removes({ present: true, added_at: "2026-09-14T09:31:00Z" }) + "|" + removes({ present: false });
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "1|0" {
		t.Errorf("Remove drawn with a token and without one: %q, want 1|0", got)
	}
}

// A criterion about a field links to the section that holds it, since a form
// one section at a time would otherwise leave "Declares its licence: fail"
// with nowhere to go.
func TestACriterionLinksToItsSection(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/edit/wan-studio?section=criteria")

	var got string
	if err := p.Eval(ctx, `(async () => {
		for (let i = 0; i < 100 && !document.querySelector(".helm-criterion"); i++) {
			await new Promise(r => setTimeout(r, 50));
		}
		const find = (n) => [...document.querySelectorAll(".helm-criterion")].find(c => c.textContent.includes(n + ". "));
		const href = (n) => { const a = find(n).querySelector("a"); return a ? a.getAttribute("href") : "none"; };
		return [href(8), href(2), href(3)].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	want := "#/edit/wan-studio?section=identity|#/edit/wan-studio?section=host|none"
	if got != want {
		t.Errorf("criteria link to:\n got %q\nwant %q", got, want)
	}
}

// The two states of Studios no golden poses: before the daemon has answered
// at all, and when it stops answering. A blank list is not the same as an
// empty library, and one failed poll must not throw away what the last one
// said (03 §6, amended).
func TestStudiosDrawsLoadingAndUnreachable(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := screen(t, ctx, srv.URL, "catalogue", 1280, 900)

	var got string
	if err := p.Eval(ctx, `(async () => {
		const { newContext, newStore, shell } = await import("/web/app.js");
		const { studios } = await import("/fixtures/fake.js");
		const draw = (tweak) => {
			const store = newStore();
			tweak(store);
			const ctx = newContext({ client: {}, store, redraw() {} });
			const host = document.createElement("div");
			host.append(...shell(ctx, "#/studios", "127.0.0.1:8700"));
			return host;
		};

		// Before the first answer: placeholder rows, and no count.
		const loading = draw(() => {});
		const first = [
			loading.querySelectorAll(".helm-skeleton").length,
			loading.querySelector(".helm-studios").getAttribute("aria-busy"),
			loading.querySelector(".helm-page-header .helm-meta") ? "counted" : "no count",
			loading.textContent.includes("No studios yet") ? "said empty" : "said nothing about empty",
		].join(",");

		// Unreachable after a first answer: the rows it last served, greyed
		// and inert, under one sentence.
		const stale = draw((store) => {
			store.studios = structuredClone(studios);
			store.loaded = true;
			store.error = "The daemon could not be reached. Connection refused.";
		});
		const screen = stale.querySelector(".helm-screen");
		const second = [
			stale.querySelector(".helm-unreachable") ? "said" : "silent",
			screen.hasAttribute("inert") ? "inert" : "live",
			screen.classList.contains("helm-stale") ? "greyed" : "bright",
			screen.querySelectorAll(".helm-studio:not(.helm-skeleton)").length,
		].join(",");

		// And before any answer, an unreachable daemon has no rows to keep.
		const never = draw((store) => { store.error = "The daemon could not be reached."; });
		const third = [
			never.querySelector(".helm-unreachable") ? "said" : "silent",
			never.querySelector(".helm-screen") ? "drew a screen" : "drew no screen",
		].join(",");
		return [first, second, third].join(" | ");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	want := "3,true,no count,said nothing about empty | said,inert,greyed,6 | said,drew no screen"
	if got != want {
		t.Errorf("Studios while loading and unreachable:\n got %q\nwant %q", got, want)
	}
}

// Weights are a form section, not text (03 §13a, amended 2026-09-18): the
// weight a studio should take from a folder this Mac already holds is the one
// thing the editor could not say, because the generated form could draw a
// field and not a list of them.
func TestTheEditorDrawsWeightsAsEntriesWithTheirLocalDirectory(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/edit/wan-studio?section=weights")

	var drawn string
	if err := p.Eval(ctx, `(async () => {
		for (let i = 0; i < 100 && !document.querySelector(".helm-entry"); i++) {
			await new Promise(r => setTimeout(r, 50));
		}
		const entries = [...document.querySelectorAll(".helm-entry")];
		return [
			entries.length,
			entries.map(e => e.querySelector(".helm-entry-name").textContent).join(","),
			document.getElementById("f-weights-0-local_path") ? "has a local directory" : "no local directory",
			document.getElementById("f-weights-1-repo").value,
		].join("|");
	})()`, &drawn); err != nil {
		t.Fatal(err)
	}
	want := "2|weight wan_t2v_5b,weight wan_t2v_14b|has a local directory|Wan-AI/Wan2.6-T2V-14B"
	if drawn != want {
		t.Errorf("the weights section reads:\n got %q\nwant %q", drawn, want)
	}

	// Setting one goes out as a pointer and a value, like every other field,
	// and comes back in the text with the author's file otherwise untouched.
	var after string
	if err := p.Eval(ctx, editField+`("weights", "f-weights-0-local_path", "/Volumes/Models/wan-5b", "/Volumes/Models/wan-5b")`, &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "local_path: /Volumes/Models/wan-5b") {
		t.Errorf("the local directory did not land in the text:\n%s", after)
	}
	if !strings.Contains(after, "name: wan_t2v_14b") {
		t.Errorf("the second weight did not survive the edit:\n%s", after)
	}
}

// The process group is where someone lands after launching, so the page the
// studio serves is reachable from it. It was not: the screen offered Details
// and Stop group and no way in.
func TestTheProcessGroupOpensTheStudio(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/studios/ltx-studio/processes")

	var got string
	if err := p.Eval(ctx, `(async () => {
		for (let i = 0; i < 100 && !document.querySelector(".helm-page-header"); i++) {
			await new Promise(r => setTimeout(r, 50));
		}
		const header = document.querySelector(".helm-page-header");
		const open = [...header.querySelectorAll("button")].find(b => b.textContent === "Open");
		return [
			open ? "offers Open" : "no way in",
			open && open.classList.contains("helm-btn-primary") ? "accent" : "not the accent",
			document.querySelectorAll("main .helm-btn-primary").length,
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "offers Open|accent|1" {
		t.Errorf("the process group header reads %q, want offers Open|accent|1", got)
	}

	// While it is still starting there is nothing to open, and nothing is
	// offered: a page that is not up yet opens a browser tab on a refusal.
	p2 := screen(t, ctx, srv.URL, "processes", 1280, 900)
	if err := p2.Eval(ctx, `(() => {
		const header = document.querySelector(".helm-page-header");
		return [...header.querySelectorAll("button")].some(b => b.textContent === "Open") ? "offers Open" : "no Open";
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "no Open" {
		t.Errorf("a group that is still starting %s", got)
	}
}

// A studio's own page, inside helmstudio (docs/decisions.md 2026-09-18). The
// frame is the studio's own origin — a different port — so this asserts what
// the launcher controls: that it points at exactly the address that studio
// serves, that a redraw does not reload it, and that the studio's own window
// is still one click away.
func TestAStudioOpensInsideHelmstudio(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/studios/ltx-studio/open")

	var got string
	if err := p.Eval(ctx, `(async () => {
		for (let i = 0; i < 100 && !document.querySelector("iframe.helm-embed"); i++) {
			await new Promise(r => setTimeout(r, 25));
		}
		const frame = document.querySelector("iframe.helm-embed");
		if (!frame) return "no frame";
		// The poll redraws every two seconds; a frame rebuilt or re-pointed on
		// each one reloads the studio's page under whoever is using it.
		frame.dataset.mark = "kept";
		window.dispatchEvent(new HashChangeEvent("hashchange"));
		await new Promise(r => setTimeout(r, 150));
		const after = document.querySelector("iframe.helm-embed");
		const tab = [...document.querySelectorAll("button")].find(b => b.textContent === "Open in a tab");
		// It fills the page. An iframe whose chain to the window is broken
		// anywhere falls back to its own 150px and shows the studio through a
		// letterbox.
		const page = document.querySelector(".helm-page-full").getBoundingClientRect().height;
		const tall = frame.getBoundingClientRect().height;
		return [
			frame.getAttribute("src"),
			after === frame ? "same frame" : "rebuilt",
			after && after.dataset.mark === "kept" ? "not reloaded" : "reloaded",
			frame.getAttribute("allow"),
			tab ? "tab offered" : "no tab",
			tall > page - 120 ? "fills the page" : Math.round(tall) + "px of " + Math.round(page) + "px",
		].join("|");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	want := "http://127.0.0.1:8720/|same frame|not reloaded|fullscreen; clipboard-write; microphone|tab offered|fills the page"
	if got != want {
		t.Errorf("the embedded studio reads:\n got %q\nwant %q", got, want)
	}

	// Full screen hands the display to the studio itself — the frame, not the
	// launcher's page around it — and Escape is the browser's way back.
	if err := p.Eval(ctx, `(async () => {
		const frame = document.querySelector("iframe.helm-embed");
		let asked = null;
		frame.requestFullscreen = function () { asked = this; return Promise.resolve(); };
		const button = [...document.querySelectorAll("button")].find(b => b.textContent === "Full screen");
		if (!button) return "no button";
		button.click();
		await new Promise(r => setTimeout(r, 50));
		return asked === frame ? "asks for the frame" : "asks for " + (asked && asked.tagName);
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "asks for the frame" {
		t.Errorf("full screen %q", got)
	}

	// A studio that is still starting has no page to show yet, so it is not
	// framed: a frame would show a browser error inside helmstudio.
	p2 := screen(t, ctx, srv.URL, "processes", 1280, 900)
	if err := p2.Eval(ctx, `(async () => {
		location.hash = "#/studios/ltx-studio/open";
		await new Promise(r => setTimeout(r, 200));
		return (document.querySelector("iframe.helm-embed") ? "framed" : "not framed") + "|" +
			(document.body.textContent.includes("Starting ltx studio") ? "says it is starting" : "says nothing");
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "not framed|says it is starting" {
		t.Errorf("a studio that is starting: %q", got)
	}
}

// The Gallery in the nav (03 §10, amended 2026-09-19). What a golden cannot
// hold: that a chip narrows the same component rather than drawing a second
// one, that an export says "timeline" rather than naming the studio that ran
// it, and that the launcher draws no star on work it may not change.
func TestTheGalleryIsEveryStudiosWork(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	for _, c := range []struct {
		name string
		hash string
		expr string
		want float64
	}{
		{
			"every studio's items, with no scope chosen",
			"#/gallery",
			`document.querySelector("helm-gallery").shadowRoot.querySelectorAll(".item").length`,
			6,
		},
		{
			"the launcher's gallery asks for every studio",
			"#/gallery",
			`document.querySelector("helm-gallery").getAttribute("scope") === "all" ? 1 : 0`,
			1,
		},
		{
			// The chip is a link to a scope, and the component it narrows is
			// the same element: a second <helm-gallery> would reload from
			// nothing and lose whatever was open in it.
			"a chip narrows the gallery to one studio",
			"#/gallery?studio=h3-studio",
			`(() => {
				const g = document.querySelector("helm-gallery");
				const items = [...g.shadowRoot.querySelectorAll(".item .facts")].map(f => f.textContent);
				return g.getAttribute("studio") === "h3-studio" ? items.length : 0;
			})()`,
			3,
		},
		{
			// 03 §10, amended M8 Q17: an item with a timeline_id is a
			// sequence's export, and it says "timeline" rather than naming a
			// studio. h3's three items are two takes and one export.
			"an export is labelled timeline, not by the studio that ran it",
			"#/gallery?studio=h3-studio",
			`(() => {
				const g = document.querySelector("helm-gallery");
				const origins = [...g.shadowRoot.querySelectorAll(".item .origin")].map(o => o.textContent).sort();
				return JSON.stringify(origins) === JSON.stringify(["h3-studio", "h3-studio", "timeline"]) ? 1 : 0;
			})()`,
			1,
		},
		{
			"the chosen chip is the current one, and it is the only one",
			"#/gallery?studio=h3-studio",
			`(() => {
				const on = [...document.querySelectorAll(".helm-scope[aria-current=true]")];
				return on.length === 1 && on[0].textContent === "h3 studio" ? 1 : 0;
			})()`,
			1,
		},
		{
			// An item is another studio's: the launcher may not star it, so
			// there is no star to press (04 §9).
			"the launcher draws no star",
			"#/gallery",
			`document.querySelector("helm-gallery").shadowRoot.querySelectorAll(".star").length`,
			0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := route(t, ctx, srv.URL, c.hash)
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

// Adding a clip from any studio (03 §11, amended 2026-09-19). What a golden
// cannot hold: that "Add" reaches a picker over every studio rather than one
// studio's own work, and that what it picks is what is appended.
func TestTheTimelineAddsFromEveryStudio(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/timeline?id=01JBTM00000000000000SEQ01A")

	// The editor asks; the page answers with the gallery, over every studio.
	if err := p.Eval(ctx, `(() => {
		const editor = document.querySelector("helm-timeline");
		[...editor.shadowRoot.querySelectorAll("button")].find(b => b.textContent.startsWith("Add clip")).click();
		return 1;
	})()`, new(float64)); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitFor(ctx, `!!document.querySelector("dialog.helm-picker helm-gallery")
		&& document.querySelector("dialog.helm-picker helm-gallery").shadowRoot.querySelectorAll(".item").length === 6`); err != nil {
		t.Fatalf("waiting for the picker: %v", err)
	}
	var scope string
	if err := p.Eval(ctx, `document.querySelector("dialog.helm-picker helm-gallery").getAttribute("scope")`, &scope); err != nil {
		t.Fatal(err)
	}
	if scope != "all" {
		t.Errorf("the picker's scope is %q; a picker that offered one studio's work is the thing this screen exists to replace", scope)
	}

	// ltx studio's plate, chosen from h3's sequence.
	if err := p.Eval(ctx, `(() => {
		const g = document.querySelector("dialog.helm-picker helm-gallery");
		const item = [...g.shadowRoot.querySelectorAll(".item")].find(i => i.textContent.includes("rain street plate"));
		item.click();
		[...g.shadowRoot.querySelectorAll("button")].find(b => b.textContent === "Use this").click();
		return 1;
	})()`, new(float64)); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitFor(ctx, `globalThis.appended.length === 1`); err != nil {
		t.Fatalf("waiting for the clip to be appended: %v", err)
	}
	var added string
	if err := p.Eval(ctx, `JSON.stringify(globalThis.appended[0])`, &added); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(added, `"asset_id":"as_2"`) || !strings.Contains(added, `"timeline_id":"01JBTM00000000000000SEQ01A"`) {
		t.Errorf("appended %s; want ltx studio's asset on this sequence", added)
	}

	// And the picker is gone: it answered one question and closed.
	var open float64
	if err := p.Eval(ctx, `document.querySelectorAll("dialog.helm-picker").length`, &open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Errorf("%v pickers are still open after choosing", open)
	}
}

// A sequence assembled here reads as what it is: four studios, each clip in
// the hue of the one that made it, and the one whose studio cannot be learnt
// neutral and labelled (03 §11, §17). Inside a studio every other studio's
// clip is neutral, because the studio API serves no other studio's hue; the
// launcher is the page that has read the library.
func TestClipsWearTheirStudiosHues(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p := route(t, ctx, srv.URL, "#/timeline?id=01JBTM00000000000000SEQ01A")

	var got string
	if err := p.Eval(ctx, `(() => {
		const editor = document.querySelector("helm-timeline");
		return JSON.stringify([...editor.shadowRoot.querySelectorAll(".clip")].map(c => [
			c.querySelector(".who").textContent,
			getComputedStyle(c).getPropertyValue("--_hue").trim(),
		]));
	})()`, &got); err != nil {
		t.Fatal(err)
	}
	// The hue a page gave is substituted as written; the neutral one is
	// --helm-status-idle, which resolves to its value.
	want := `[["h3-studio","light-dark(#a06a10, #e0a33c)"],["ltx-studio","light-dark(#2f5fc4, #5b8def)"],["iris-studio","light-dark(#b12f68, #d8558f)"],["another studio","#716b5e"],["auk-studio","light-dark(#5c4bc4, #8b7cf0)"]]`
	if got != want {
		t.Errorf("clips are\n%s\nwant\n%s", got, want)
	}
}

// A studio's page is the page there (03 §7a, amended 2026-09-19): the nav is
// not drawn above it, and the way back is this screen's own link.
func TestAStudiosScreenDrawsNoNav(t *testing.T) {
	srv := fixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := route(t, ctx, srv.URL, "#/studios/ltx-studio/open")
	var got string
	if err := p.Eval(ctx, `JSON.stringify({
		nav: document.querySelectorAll(".helm-nav-link").length,
		back: !!document.querySelector(".helm-back"),
		topbar: !!document.querySelector(".helm-topbar"),
		rows: getComputedStyle(document.getElementById("app")).gridTemplateRows.split(" ").length,
	})`, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"nav":0`) || !strings.Contains(got, `"back":true`) ||
		!strings.Contains(got, `"topbar":true`) || !strings.Contains(got, `"rows":2`) {
		t.Errorf("a studio's screen is %s; want no nav, a back link, the top bar, and two rows", got)
	}

	// And every other screen still has it.
	q := route(t, ctx, srv.URL, "#/gallery")
	var links float64
	if err := q.Eval(ctx, `document.querySelectorAll(".helm-nav-link").length`, &links); err != nil {
		t.Fatal(err)
	}
	if links != 5 {
		t.Errorf("the Gallery screen has %v nav links; want the five the nav has", links)
	}
}
