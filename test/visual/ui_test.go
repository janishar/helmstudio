package visual

import (
	"context"
	"testing"
	"time"
)

// What the components do, read from the DOM rather than from pixels.
//
// The goldens hold what a component looks like. These hold what it does — and
// they hold the two things a golden deliberately cannot: the thumbnail path,
// which is left out of the goldens because a decoded raster makes Chrome's
// compositing of the panel around it vary (see fixtures/ui.html), and
// behaviour that leaves no mark, such as a carriage-return line replacing the
// one before it rather than adding a row.
func TestComponentsBehave(t *testing.T) {
	b := openBrowser(t)
	srv := fixtureServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	page, err := b.NewPage(ctx, 1100, 900, "dark")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	if err := page.Navigate(ctx, srv.URL+"/fixtures/ui.html?theme=dark&thumbs=1"); err != nil {
		t.Fatal(err)
	}
	if err := page.WaitFor(ctx, `document.documentElement.dataset.ready === "1"`); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		expr string
		want float64
	}{
		{
			// Every item gets a thumbnail, through the client's own method.
			"a thumbnail for every item",
			`document.getElementById("gal").shadowRoot.querySelectorAll("img").length`,
			5,
		},
		{
			// Eleven line events arrive, four of them carriage-return progress
			// writes. A terminal that printed each would hold thirteen rows
			// with the step and the gap; one that rewrites in place holds ten.
			"carriage returns rewrite in place",
			`document.getElementById("term").lines.length`,
			10,
		},
		{
			// And the row that survived is the last write, not the first.
			"the surviving progress row is the latest write",
			`document.getElementById("term").lines.filter(l => l.text === "downloading 100%|").length`,
			1,
		},
		{
			// The ANSI escapes became spans: one error, one warning.
			"ANSI colour becomes spans",
			`document.getElementById("term").shadowRoot.querySelectorAll(".row .err, .row .warn").length`,
			2,
		},
		{
			"no escape survives into the copied text",
			`document.getElementById("term").text().includes(String.fromCharCode(27)) ? 1 : 0`,
			0,
		},
		{
			// The parts that need ffmpeg say so rather than being absent.
			"the filmstrip says it is unsupported",
			`document.getElementById("play").shadowRoot.querySelector('[part="unsupported"]').textContent.startsWith("Unsupported") ? 1 : 0`,
			1,
		},
		{
			// A sequence's export is labelled timeline, not by a studio (M8 Q17).
			"a sequence's export is labelled timeline",
			`[...document.getElementById("gal").shadowRoot.querySelectorAll(".origin")].filter(e => e.textContent === "timeline").length`,
			1,
		},
		{
			// Rule 3 from inside the browser: the client a component uses is
			// the one it was handed, not one it made.
			"the component uses the client it was given",
			`document.getElementById("gal").client === document.getElementById("gal")._client ? 1 : 0`,
			1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got float64
			if err := page.Eval(ctx, c.expr, &got); err != nil {
				t.Fatalf("evaluating %s: %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("%s: got %v, want %v", c.expr, got, c.want)
			}
		})
	}

	// Picking resolves to an asset, which is what picker mode is for.
	var picked string
	const pick = `(() => {
		const g = document.getElementById("gal");
		let seen = "";
		g.addEventListener("pick", e => { seen = e.detail.asset; });
		g.select(g.items[1]);
		g.pick();
		return seen;
	})()`
	if err := page.Eval(ctx, pick, &picked); err != nil {
		t.Fatal(err)
	}
	if picked != "as_2" {
		t.Errorf("picking the second item emitted asset %q, want as_2", picked)
	}
}
