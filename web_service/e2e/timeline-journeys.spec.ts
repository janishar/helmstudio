// The Timeline as people use it (QA, 2026-11-24): one fresh user on the test stack's fake runner (echo pack). The
// user "already made" three short clips and a still (Echo video / Echo runs through the API, as setup); everything
// after is done in the browser, journeys in order (each builds on the sequence the ones before left). Boards:
// Timeline, TimelineAudio, TimelineDialogs, TimelineTools; plans 23 and 24. Screenshots go to E2E_SHOTS.
import { mkdirSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { newUser, signIn, type Creds } from "./users";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/timeline";
mkdirSync(SHOTS, { recursive: true });

const CLIPS = ["harbour at dawn", "the boat leaves", "gulls overhead"];
const STILL = "a lighthouse at night";

let user: Creds;
let page: Page;

const api = (p: Page) => ({
  get: async (path: string) => {
    const r = await p.request.get(`/api${path}`);
    expect(r.ok(), `GET ${path}: ${r.status()}`).toBeTruthy();
    return r.json();
  },
  post: async (path: string, data: unknown) => {
    const r = await p.request.post(`/api${path}`, { data });
    expect(r.ok(), `POST ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return r.json();
  },
});

/** Runs a workflow in a session and waits for its take. */
async function made(sid: string, workflow_id: string, inputs: Record<string, unknown>) {
  const a = api(page);
  const run = await a.post(`/v1/studio/sessions/${sid}/runs`, { workflow_id, inputs });
  let r = run;
  await expect
    .poll(
      async () => {
        r = await a.get(`/v1/studio/runs/${run.id}`);
        return r.status;
      },
      { timeout: 60_000 },
    )
    .toBe("succeeded");
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(240_000);
  user = await newUser(browser, "timeline");
  page = await browser.newPage();
  await signIn(page, user);
  const s = await api(page).post("/v1/studio/sessions", { title: "Harbour" });
  for (const t of CLIPS)
    await made(s.id, "echo-video", {
      "prompt.text": t,
      "canvas.width": 256,
      "canvas.height": 128,
      "duration.frames": 49,
      "duration.fps": 24,
      "video.steps": 2,
      "video.delay_ms": 50,
    });
  await made(s.id, "echo", { "prompt.text": STILL, "echo.steps": 2, "echo.delay_ms": 50 });
});
test.afterAll(() => page?.close());
test.afterEach(async ({}, info) => {
  if (info.status !== info.expectedStatus)
    await page
      ?.screenshot({ path: `${SHOTS}/${slug(info.title)}-failed.png`, fullPage: true })
      .catch(() => undefined);
});

const slug = (t: string) => t.split(" ")[0].toLowerCase();
const shot = (name: string) => page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });

// ---- what the user sees and does
const v1 = () => page.getByRole("group", { name: "V1: the picture" }).getByRole("button");
const clip = (i: number) => v1().nth(i);
const inspector = () => page.getByRole("complementary", { name: "Clip inspector" });
const header = () =>
  page
    .getByRole("main")
    .locator("span.mono")
    .filter({ hasText: / fps · / });
const exportKey = () => page.getByRole("button", { name: /^Export/ });
const grid = () => page.getByRole("list", { name: "Takes" });
const card = (title: string) =>
  grid().getByRole("button", {
    name: new RegExp(`(^|\\s)${title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")} `),
  });
/** A V1 clip's name: "Shot 2 · harbour at dawn, Echo · 2.0 s". */
const named = (title: string, line = "") => new RegExp(`^(Shot \\d+ · )?${title}, ${line}`);
/** Closes a dialog a failed journey left open. */
async function closeDialogs() {
  for (const d of await page.getByRole("dialog").all())
    if (await d.isVisible()) await d.getByRole("button", { name: "Close" }).click();
}
/** The sequence's length from the transport's "0:00.00 / 0:06.13". */
const length = async () =>
  (await page.locator('span.mono[aria-live="off"]').textContent())!.split("/").pop()!.trim();

test("j01 put my three takes on a timeline from the Gallery, in the order I pick them", async () => {
  await page.goto("/gallery");
  await expect(page.getByRole("heading", { name: "Gallery", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Select several" }).click();
  for (const t of CLIPS) await card(t).click();
  const sel = page.getByRole("group", { name: "Selection" });
  await expect(sel).toContainText("3 selected");
  await sel.getByRole("button", { name: "Add to timeline" }).click();
  const added = sel.getByRole("status");
  await expect(added).toContainText(/^Added 3 clips to .+ · Open$/);
  await added.getByRole("link", { name: "Open" }).click();
  await expect(page).toHaveURL(/\/timeline\?t=/);
  await expect(v1()).toHaveCount(3);
  for (const [i, t] of CLIPS.entries())
    await expect(clip(i)).toHaveAccessibleName(named(t, "Echo · 2.0 s"));
  await expect(header()).toHaveText(/^256 × 128 · 24 fps · /);
  // the sequence's render time, its shots' in all (API 1.60; docs/mock/Timeline.dc.html)
  await expect(header()).toHaveText(/ · ⏱ \d+:\d\d$/);
  await expect(exportKey()).toHaveAccessibleName(/^Export stream copy$/);
  await shot("j01-three-takes");
});

test("j02 give the sequence a name I will recognise", async () => {
  await page.getByRole("main").locator('button[aria-haspopup="dialog"]').first().click();
  const list = page.getByRole("dialog", { name: "Sequences" });
  await expect(list).toBeVisible();
  const row = list.getByRole("button", { name: / · 3 clips · / });
  await expect(row).toHaveAttribute("aria-current", "true");
  await list.getByRole("button", { name: "Rename" }).click();
  await list.getByRole("textbox", { name: "Name" }).fill("Harbour cut");
  await list.getByRole("button", { name: "Save" }).click();
  await expect(row).toHaveAccessibleName(/^Harbour cut · 3 clips/);
  await shot("j02-named");
  await list.getByRole("button", { name: "Close" }).click();
  await expect(list).toBeHidden();
  await expect(page.getByRole("heading", { name: "Timeline: Harbour cut" })).toBeAttached();
});

test("j02b after renaming, Esc closes the Sequences dialog", async () => {
  await page.getByRole("main").locator('button[aria-haspopup="dialog"]').first().click();
  const list = page.getByRole("dialog", { name: "Sequences" });
  await list.getByRole("button", { name: "Rename" }).click();
  await list.getByRole("button", { name: "Save" }).click();
  await expect(list.getByRole("button", { name: "Rename" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(list).toBeHidden({ timeout: 3000 });
});

test("j02c clone the sequence from the Sequences dialog, then go back to it (alpha.5 item 12)", async () => {
  await closeDialogs();
  await page.getByRole("main").locator('button[aria-haspopup="dialog"]').first().click();
  const list = page.getByRole("dialog", { name: "Sequences" });
  await list.getByRole("button", { name: "Clone" }).click();
  await expect(page.getByRole("heading", { name: "Timeline: Harbour cut clone" })).toBeAttached();
  await expect(v1()).toHaveCount(3);
  await page.getByRole("main").locator('button[aria-haspopup="dialog"]').first().click();
  await list.getByRole("button", { name: /^Harbour cut · 3 clips/ }).click();
  await expect(page.getByRole("heading", { name: "Timeline: Harbour cut" })).toBeAttached();
});

test("j03 trim five frames off the end of the first clip", async () => {
  await closeDialogs();
  const before = await length();
  await clip(0).click();
  await expect(clip(0)).toHaveAttribute("aria-pressed", "true");
  await expect(
    inspector().getByRole("heading", { name: new RegExp(`${CLIPS[0]}$`) }),
  ).toBeVisible();
  // the clip's take's render time, low text (API 1.60)
  await expect(inspector()).toContainText(/ · ⏱ \d+:\d\d/);
  const out = await inspector().textContent();
  for (let i = 0; i < 5; i++) await page.keyboard.press("]");
  // 49 frames → 44 at 24 fps
  await expect(clip(0)).toHaveAccessibleName(named(CLIPS[0], "Echo · 1.8 s"));
  await expect(inspector()).not.toHaveText(out!);
  await expect.poll(length).not.toBe(before);
  await expect(exportKey()).toHaveAccessibleName(/re-encode · 1 trimmed$/); // not "1 other" (owner, Q11)
  await shot("j03-trimmed");
});

test("j04 move the last clip to the front", async () => {
  await clip(2).click();
  await page.keyboard.press("Alt+ArrowLeft");
  await page.keyboard.press("Alt+ArrowLeft");
  await expect(clip(0)).toHaveAccessibleName(named(CLIPS[2]));
  await expect(clip(1)).toHaveAccessibleName(named(CLIPS[0]));
  await expect(clip(2)).toHaveAccessibleName(named(CLIPS[1]));
  await shot("j04-reordered");
});

test("j05 split the middle clip at the playhead", async () => {
  const before = await length();
  const box = (await clip(1).boundingBox())!;
  const ruler = page.getByRole("slider", { name: "Time ruler: click to move the playhead" });
  const rb = (await ruler.boundingBox())!;
  await page.mouse.click(box.x + box.width / 2, rb.y + rb.height / 2);
  await page.getByRole("button", { name: "Split at playhead" }).click();
  await expect(v1()).toHaveCount(4);
  await expect(clip(1)).toHaveAccessibleName(named(CLIPS[0]));
  await expect(clip(2)).toHaveAccessibleName(named(CLIPS[0]));
  expect(await length()).toBe(before);
  await shot("j05-split");
});

test("j06 undo the split, redo it, then the same with the keyboard", async () => {
  const undo = page.getByRole("button", { name: "Undo", exact: true });
  const redo = page.getByRole("button", { name: "Redo", exact: true });
  await undo.click();
  await expect(v1()).toHaveCount(3);
  await redo.click();
  await expect(v1()).toHaveCount(4);
  // the main's top right padding (its top left is under the rail's wrapper, which takes the click)
  const m = (await page.getByRole("main").boundingBox())!;
  await page.mouse.click(m.x + m.width - 5, m.y + 5);
  await page.keyboard.press("ControlOrMeta+z");
  await expect(v1()).toHaveCount(3);
  await page.keyboard.press("ControlOrMeta+Shift+z");
  await expect(v1()).toHaveCount(4);
  await shot("j06-undo-redo");
});

test("j07 cut with the Blade and ripple-delete the piece", async () => {
  await page.keyboard.press("b");
  const tools = page.getByRole("radiogroup", { name: "Tool" });
  await expect(tools.getByRole("radio", { name: "Blade" })).toHaveAttribute("aria-checked", "true");
  const box = (await clip(0).boundingBox())!;
  await page.mouse.click(box.x + box.width * 0.75, box.y + box.height / 2);
  await expect(v1()).toHaveCount(5);
  await shot("j07-bladed");
  await page.keyboard.press("a");
  await expect(tools.getByRole("radio", { name: "Select" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  const before = await length();
  await clip(1).click();
  await page.keyboard.press("Delete");
  await expect(v1()).toHaveCount(4);
  await expect.poll(length).not.toBe(before);
  await expect(clip(1)).toHaveAccessibleName(named(CLIPS[0]));
  await shot("j07-piece-gone");
});

test("j08 add a still at the end and a dissolve between two clips", async () => {
  await page.keyboard.press("Escape");
  await inspector()
    .getByRole("button", { name: `Add take ${STILL}` })
    .click();
  await expect(v1()).toHaveCount(5);
  const still = clip(4);
  await expect(still).toHaveAccessibleName(new RegExp(`^Still · ${STILL},`));
  await still.click();
  const dur = inspector().getByRole("textbox", { name: "Duration in seconds" });
  await dur.fill("3");
  await dur.press("Enter");
  await expect(still).toHaveAccessibleName(/ · 3\.0 s/);
  // the two halves of the split clip: both have footage past the cut
  // the playhead sits on the split (its grab band, AudioFades frame 5, covers the clip's middle): click its right end
  await clip(2).click({ position: { x: ((await clip(2).boundingBox())?.width ?? 20) - 6, y: 8 } });
  await inspector().getByRole("combobox", { name: "Transition in" }).selectOption("12");
  await expect(clip(2)).toHaveAccessibleName(/dissolve in 12 f/);
  await expect(exportKey()).toHaveAccessibleName(/re-encode · 1 still, 1 dissolve/);
  await shot("j08-still-and-dissolve");
});

test("j09 add a voice line on a new audio track", async () => {
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "+ Audio track" }).click();
  const a2 = page.getByRole("group", { name: "A2: sound" });
  await expect(a2).toBeAttached();
  await page.getByRole("button", { name: "New line" }).click();
  const composer = page.getByRole("region", { name: "New line" });
  await expect(composer).toBeVisible();
  await composer.getByLabel("Text to speak").fill("The harbour wakes before the town.");
  // no voice yet (docs/mock/TimelineAudio.dc.html "No voice yet"): Generate line is off and says what to do
  await expect(composer.getByRole("button", { name: /^Generate line/ })).toBeDisabled();
  await expect(composer.getByRole("button", { name: /^Generate line/ })).toContainText(
    "Pick a voice first",
  );
  await shot("j09-no-voice");
  await composer.getByRole("button", { name: /^Voice: / }).click();
  const voices = page.getByRole("dialog", { name: "Voice" });
  await voices.getByRole("button", { name: /^Ada/ }).click();
  await voices.getByRole("button", { name: "Use Ada" }).click();
  await expect(voices).toBeHidden();
  await expect(composer.getByRole("button", { name: /^Voice: / })).toHaveAccessibleName(/Ada/);
  await shot("j09-new-line");
  await expect(composer.getByRole("button", { name: /^Generate line/ })).toBeEnabled();
  await composer.getByRole("button", { name: /^Generate line/ }).click();
  await expect(a2.getByRole("button", { name: /^The harbour wakes/ })).toBeVisible({
    timeout: 60_000,
  });
  await a2.getByRole("button", { name: /^The harbour wakes/ }).click();
  await inspector().getByRole("button", { name: "Mute" }).click();
  await expect(a2.getByRole("button", { name: /^The harbour wakes/ })).toHaveAccessibleName(
    /muted/,
  );
  await shot("j09-voice-line");
});

test("j10a the export plan names my clips the way the timeline does", async () => {
  await page.keyboard.press("Escape");
  const name = (await clip(2).getAttribute("aria-label"))!; // the clip with the dissolve
  const num = name.match(/^Shot \d+/)![0];
  await exportKey().click();
  const dlg = page.getByRole("dialog", { name: "Export Harbour cut" });
  await expect(dlg).toContainText("dissolves in from the shot before");
  await shot("j10a-plan-names");
  await expect(dlg).toContainText(`${num} dissolves in from the shot before`, { timeout: 3000 });
});

let exported = "";
test("j10 export the sequence", async () => {
  await closeDialogs();
  await page.keyboard.press("Escape");
  await exportKey().click();
  const dlg = page.getByRole("dialog", { name: "Export Harbour cut" });
  await expect(dlg).toBeVisible();
  await expect(dlg).toContainText("Re-encode");
  await expect(dlg).toContainText("Length · size");
  await expect(dlg).toContainText("Runs on");
  await shot("j10-export-plan");
  await dlg.getByRole("button", { name: "Export", exact: true }).click();
  await expect(dlg).toContainText(/Exported|Export failed/, { timeout: 60_000 });
  await shot("j10-exported");
  const line = dlg.getByText(/^Exported · /);
  await expect(line).toContainText(/ MB$/);
  exported = (await line.textContent())!;
  await dlg.getByRole("button", { name: "Close" }).first().click();
});

test("j10b the export says how big the file is", async () => {
  expect(exported).not.toMatch(/ · 0\.0 MB$/);
});

test("j11 come back later in a new browser and find the sequence as I left it", async ({
  browser,
}) => {
  const ctx = await browser.newContext();
  const p = await ctx.newPage();
  try {
    await signIn(p, user);
    await p.goto("/timeline");
    await expect(p.getByRole("heading", { name: "Timeline: Harbour cut" })).toBeAttached();
    const pics = p.getByRole("group", { name: "V1: the picture" }).getByRole("button");
    await expect(pics).toHaveCount(5);
    await expect(pics.nth(2)).toHaveAccessibleName(/dissolve in 12 f/);
    await expect(pics.nth(4)).toHaveAccessibleName(new RegExp(`^Still · ${STILL},.* 3\\.0 s`));
    await expect(
      p.getByRole("group", { name: "A2: sound" }).getByRole("button", { name: /^The harbour/ }),
    ).toHaveAccessibleName(/muted/);
    await expect(p.getByRole("button", { name: "Undo", exact: true })).toBeEnabled();
    await p.screenshot({ path: `${SHOTS}/j11-as-left.png`, fullPage: true });
  } finally {
    await ctx.close();
  }
});

test("j12 zoom in and out, fit, see the shortcuts and drop a marker", async () => {
  await closeDialogs();
  const zoom = page.getByRole("group", { name: "Zoom" });
  const span = zoom.locator(".tl-zv");
  const at = await span.textContent();
  await zoom.getByRole("button", { name: "Zoom in" }).click();
  await zoom.getByRole("button", { name: "Zoom in" }).click();
  await expect(span).not.toHaveText(at!);
  await zoom.getByRole("button", { name: "Zoom to fit" }).click();
  await page.keyboard.press("Shift+/");
  const keys = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(keys).toBeVisible();
  await shot("j12-shortcuts");
  await page.keyboard.press("Escape");
  await expect(keys).toBeHidden();
  await page.keyboard.press("m");
  await expect(page.getByRole("button", { name: /^Marker at / })).toBeVisible();
  await shot("j12-marker");
});

test("j13 hide the side panel for a wider timeline; it stays hidden and a clip brings it back", async () => {
  // alpha.5 item 11 (docs/mock/Timeline.dc.html "Side panel toggle frames"): open by default, remembered here
  await closeDialogs();
  const btn = page.getByRole("button", { name: "Side panel" });
  await expect(btn).toHaveAttribute("aria-expanded", "true");
  await expect(inspector()).toBeVisible();
  const narrow = (await page.locator("main#main").boundingBox())!.width;
  await btn.click();
  await expect(inspector()).toHaveCount(0);
  await expect(btn).toHaveAttribute("aria-expanded", "false");
  expect((await page.locator("main#main").boundingBox())!.width).toBeGreaterThan(narrow);
  await shot("j13-panel-closed");
  await page.reload();
  await expect(clip(0)).toBeVisible();
  await expect(inspector()).toHaveCount(0);
  await clip(0).click();
  await expect(inspector()).toBeVisible();
  await btn.click();
  await btn.click();
  await expect(inspector()).toBeVisible();
});
