// The in-app Assistant as people use it, most frequent first (QA, 2026-11-24): one fresh user, the test stack's fake
// model scripted per turn (e2e/fake.ts), the admin's assistant settings read first and put back after. Screenshots
// of each journey's key moment go to E2E_SHOTS (default test-results/assistant).
import { randomUUID } from "node:crypto";
import { mkdirSync } from "node:fs";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { ADMIN, newUser, signIn, type Creds } from "./users";
import { script, sent } from "./fake";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/assistant";
mkdirSync(SHOTS, { recursive: true });
const hex = randomUUID().slice(0, 8);
const MODEL = `asst-${hex}`;
const PROV = `srv-${MODEL}`;
const THINK_MODEL = `qwen3:${hex}`;
const THINK_PROV = `thk-${hex}`;
const LIMITS = {
  model_calls_per_day: 2000,
  tokens_per_day: 20000000,
  look_at_per_hour: 200,
  messages_per_minute: 30,
};
const SERVER = {
  kind: "server",
  title: "Fake server",
  base_url: "http://fake-openai:8000/v1",
  host: "fake",
  memory_gb: 0.5,
  timeout_s: 30,
};

let user: Creds;
let adminCtx: BrowserContext;
let admin: Page;
let ctx: BrowserContext;
let page: Page;
let snapshot: Record<string, unknown> | null = null;

const api = (p: Page) => ({
  get: async (path: string) => {
    const r = await p.request.get(`/api${path}`);
    expect(r.ok(), `GET ${path}: ${r.status()}`).toBeTruthy();
    return r.json();
  },
  put: async (path: string, data: unknown) => {
    const r = await p.request.put(`/api${path}`, { data });
    expect(r.ok(), `PUT ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return r.json();
  },
  post: async (path: string, data: unknown) => {
    const r = await p.request.post(`/api${path}`, { data });
    expect(r.ok(), `POST ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return r.json();
  },
});
const settings = (body: Record<string, unknown>) =>
  api(admin).put("/v1/studio/admin/assistant", body);

test.beforeAll(async ({ browser }) => {
  test.setTimeout(240_000);
  user = await newUser(browser, "assistant");
  adminCtx = await browser.newContext();
  admin = await adminCtx.newPage();
  await signIn(admin, ADMIN);
  await echoPresent();
  snapshot = await api(admin).get("/v1/studio/admin/assistant");
  await api(admin).put(`/v1/studio/admin/providers/${PROV}`, { ...SERVER, models: [MODEL] });
  await settings({ enabled: true, provider: PROV, model: null, limits: LIMITS });
  ctx = await browser.newContext();
  page = await ctx.newPage();
  await signIn(page, user);
});

test.afterAll(async () => {
  try {
    if (snapshot) {
      const s = snapshot as {
        enabled: boolean;
        provider: string | null;
        model: string | null;
        web_search: boolean;
        search_provider: string | null;
        limits: typeof LIMITS;
      };
      const back = {
        enabled: s.enabled,
        provider: s.provider,
        model: s.model,
        web_search: s.web_search,
        search_provider: s.search_provider,
        limits: s.limits,
      };
      const r = await admin.request.put("/api/v1/studio/admin/assistant", { data: back });
      if (!r.ok())
        await admin.request.put("/api/v1/studio/admin/assistant", {
          data: { enabled: false, limits: s.limits },
        });
    }
    for (const p of [PROV, THINK_PROV])
      await admin.request.delete(`/api/v1/studio/admin/providers/${p}`);
  } finally {
    await ctx?.close();
    await adminCtx?.close();
  }
});

test.afterEach(async ({}, info) => {
  if (info.status !== info.expectedStatus)
    await page
      ?.screenshot({ path: `${SHOTS}/${slug(info.title)}-failed.png`, fullPage: true })
      .catch(() => undefined);
});

const slug = (t: string) => t.split(" ")[0].toLowerCase();
const shot = (name: string, p = page) =>
  p.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });

// ---- what the user sees and does
const dock = (p = page) =>
  p.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: /^Assistant/ });
const drawer = (p = page) => p.getByRole("complementary", { name: "Assistant" });
const box = (p = page) => drawer(p).getByLabel("Message the assistant");
const lines = (p = page) =>
  drawer(p).getByRole("list", { name: "What it did" }).getByRole("listitem");
const line = (text: string | RegExp, p = page) => lines(p).filter({ hasText: text }).last();
const working = (p = page) => drawer(p).getByText("Working", { exact: true });
const thinkingBubble = (p = page) =>
  drawer(p)
    .getByRole("status")
    .filter({ hasText: /^Thinking… \d+ s$/ });
const prompt = (p = page) => p.getByRole("combobox", { name: "Prompt" });

async function open(p = page) {
  await expect(dock(p)).toBeVisible(); // the drawer comes back open with it after a reload
  if ((await dock(p).getAttribute("aria-expanded")) !== "true") await dock(p).click();
  await expect(drawer(p)).toBeVisible();
}
/** Types a message and presses Enter, as a person does. */
async function say(text: string, p = page) {
  await box(p).fill(text);
  await box(p).press("Enter");
  await expect(box(p)).toHaveValue("");
}
/** The turn ended: the reply is on screen and the Working chip went. */
async function replied(text: string | RegExp, p = page) {
  await expect(drawer(p).getByText(text).last()).toBeVisible({ timeout: 40_000 });
  await expect(working(p)).toBeHidden();
}
/** New chat asks the old chat's summary with one call without tools: wait for it so it eats no script. */
async function newChat(p = page) {
  const n = sent(MODEL).length;
  // Chats replaces the header's New chat button: New chat is the list's first row (board frame 7)
  await drawer(p).getByRole("button", { name: "Chats" }).click();
  await drawer(p)
    .getByRole("menu", { name: "Chats" })
    .getByRole("menuitem", { name: "New chat" })
    .click();
  await expect(drawer(p).getByText("Hi. What should we make?")).toBeVisible();
  await expect
    .poll(
      () =>
        sent(MODEL)
          .slice(n)
          .some((r) => !r.body.tools),
      { timeout: 20_000 },
    )
    .toBe(true);
}
const told = (from: number, model = MODEL) =>
  sent(model)
    .slice(from)
    .map((r) => JSON.stringify(r.body.messages));

/** Create greys Generate for the echo family while none of its (tiny) variants is on the fake host, and the contract
 * suite deletes them: download tiny as the admin first, as the Models screen would. */
async function echoPresent() {
  const tiny = async () => {
    const l = await api(admin).get("/v1/studio/models");
    const m = l.models.find((x: { id: string }) => x.id === "echo");
    return m.variants
      .find((v: { id: string }) => v.id === "tiny")
      .hosts.find((h: { host: string }) => h.host === "fake")?.state;
  };
  if ((await tiny()) === "present") return;
  const r = await admin.request.post("/api/v1/studio/admin/models/echo/download", {
    data: { host: "fake", variant: "tiny", accept_licence: true },
  });
  expect([200, 202, 409]).toContain(r.status());
  await expect.poll(tiny, { timeout: 120_000, intervals: [1000] }).toBe("present");
}

/** n Echo image takes made through the API (a session, then a run each), oldest first. */
async function echoTakes(titles: string[]) {
  const a = api(page);
  const s = await a.post("/v1/studio/sessions", { title: "e2e assistant clips" });
  const ids: string[] = [];
  for (const t of titles) {
    const run = await a.post(`/v1/studio/sessions/${s.id}/runs`, {
      workflow_id: "echo",
      inputs: { "prompt.text": t, "echo.steps": 2, "echo.delay_ms": 50 },
    });
    let r = run;
    await expect
      .poll(
        async () => {
          r = await a.get(`/v1/studio/runs/${run.id}`);
          return r.status;
        },
        { timeout: 30_000 },
      )
      .toBe("succeeded");
    ids.push(r.outputs[0].take_id);
  }
  return ids;
}

// ---------------------------------------------------------------- journeys

test("J1 I open the assistant, ask what is on this screen, read the answer and close it", async () => {
  await page.goto("/");
  await dock().click();
  await expect(drawer()).toBeVisible();
  await expect(box()).toBeFocused();
  await expect(drawer().getByText("Hi. What should we make?")).toBeVisible();
  await expect(drawer().getByText("Runs on this machine")).toBeVisible();
  await expect(drawer().getByRole("button", { name: "Send" })).toBeDisabled();
  // Shift+Enter is a new line, not a send
  await box().fill("a");
  await box().press("Shift+Enter");
  await box().pressSequentially("b");
  await expect(box()).toHaveValue("a\nb");
  await box().fill("");

  const n = sent(MODEL).length;
  script(
    MODEL,
    { content: "Looking.", tool_calls: [{ tool: "search_takes", args: { limit: 3 } }] },
    { content: "You are on Create with the Echo workflow. You have no takes yet." },
  );
  await say("What is on this screen?");
  await expect(drawer().getByText("What is on this screen?")).toBeVisible();
  await replied("You are on Create with the Echo workflow. You have no takes yet.");
  const calls = told(n);
  expect(calls).toHaveLength(2);
  expect(calls[0]).toContain("create"); // the screen went with the message
  await shot("j01-ask-about-the-screen");

  await drawer()
    .getByRole("button", { name: /^Close \(/ })
    .click();
  await expect(drawer()).toBeHidden();
  await expect(dock()).toHaveAttribute("aria-expanded", "false");
  await dock().click();
  await expect(drawer().getByText("You are on Create with the Echo workflow.")).toBeVisible();
  // a short answer has no More (alpha.2 item 24)
  await expect(drawer().getByRole("button", { name: "More" })).toHaveCount(0);
});

test("J2 make a portrait of an old fisherman, then make it warmer, then undo and redo that", async () => {
  await open();
  script(
    MODEL,
    {
      content: "Writing it.",
      tool_calls: [
        { tool: "change", args: { key: "prompt.text", value: "portrait of an old fisherman" } },
      ],
    },
    { content: "Done. Press Generate when ready." },
  );
  await say("Make a portrait of an old fisherman");
  await replied("Done. Press Generate when ready.");
  await expect(line("Wrote prompt")).toBeVisible();
  await expect(prompt()).toHaveValue("portrait of an old fisherman");

  const warm = "portrait of an old fisherman, warm golden hour light";
  script(
    MODEL,
    {
      content: "Warmer.",
      tool_calls: [{ tool: "change", args: { key: "prompt.text", value: warm } }],
    },
    { content: "Made it golden hour." },
  );
  await say("make it warmer, golden hour");
  await replied("Made it golden hour.");
  await expect(prompt()).toHaveValue(warm);
  await shot("j02-warmer-prompt");

  const last = line("Wrote prompt");
  await last.getByRole("button", { name: /^Undo: / }).click();
  await expect(prompt()).toHaveValue("portrait of an old fisherman");
  await last.getByRole("button", { name: /^Redo: / }).click();
  await expect(prompt()).toHaveValue(warm);

  // a setting the form does not have: a plain reason, no Undo
  script(
    MODEL,
    { content: "Trying.", tool_calls: [{ tool: "change", args: { key: "nope", value: 1 } }] },
    { content: "That setting is not on this form." },
  );
  await say("set nope to 1");
  await replied("That setting is not on this form.");
  await expect(line("nope: no such setting")).toBeVisible();
  await expect(line("nope: no such setting").getByRole("button")).toHaveCount(0);
});

test("J3 use this image and create video through ltx 10 sec", async () => {
  test.setTimeout(150_000);
  // the user made a still and picked it
  await page.goto("/");
  await prompt().fill("a red fox in snow");
  await page.getByRole("button", { name: /^Generate/ }).click();
  await expect(
    page.getByRole("group", { name: "Run progress" }).getByRole("img", { name: "done" }).last(),
  ).toBeVisible();
  const takes = page.getByRole("group", { name: "Takes" });
  await takes.getByRole("button", { name: "Take, a red fox in snow" }).click();
  const found = await api(page).get("/v1/studio/takes?q=red%20fox%20snow");
  const take = found.takes.find((t: { title: string }) => t.title === "a red fox in snow").id;

  // the test stack has no LTX: the model says so after the switch fails, then offers Echo image to video
  await open();
  let n = sent(MODEL).length;
  script(
    MODEL,
    {
      content: "Switching to LTX.",
      tool_calls: [{ tool: "switchTo", args: { workflow: "LTX 2.3" } }],
    },
    { content: "LTX is not installed here, so I can't make it with LTX." },
  );
  await say("use this image and create video through ltx 10 sec");
  await replied("LTX is not installed here, so I can't make it with LTX.");
  // owner, 2026-11-25 (Q6): say why, with a link to Models
  const ltx = line("LTX 2.3 is not installed. Get it in Models.");
  await expect(ltx).toHaveText("LTX 2.3 is not installed. Get it in Models.");
  await expect(ltx.getByRole("link", { name: "Models" })).toHaveAttribute("href", "/models");
  expect(told(n)[1]).toContain("LTX 2.3 is not installed. Get it in Models."); // told the same words
  await shot("j03-ltx-not-here");

  // the same request on Echo image to video: First frame, 10 s (over the fake's 5 s), then the Generate card
  n = sent(MODEL).length;
  script(
    MODEL,
    {
      content: "Putting it in as the first frame.",
      tool_calls: [
        { tool: "useIn", args: { take_id: take, workflow: "echo-image-to-video" } },
        { tool: "change", args: { key: "duration.frames", value: 241 } },
      ],
    },
    {
      content: "This model makes at most 5 s, so I set 5 s.",
      tool_calls: [
        { tool: "change", args: { key: "duration.frames", value: 121 } },
        { tool: "generate", args: {} },
      ],
    },
    { content: "Your clip is on its way." },
  );
  await say("use this image and create video through echo image to video 10 sec");
  const card = drawer().getByRole("region", { name: /^Confirm: Generate/ });
  await expect(card).toBeVisible({ timeout: 40_000 });
  expect(told(n)[0]).toContain(take); // the picked take was on the screen it read
  await expect(line(/^Put a red fox in snow in .*Echo image to video/)).toBeVisible();
  await expect(page.getByRole("button", { name: /^Workflow: Echo image to video/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "First frame: a red fox in snow" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Video" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("121 frames · 5.0 s").first()).toBeVisible(); // the form took 5 s
  await expect(line("Set Length: 5.0 s")).toBeVisible(); // in seconds (owner, 2026-11-25, Q1)
  await expect(card.getByText("uses the GPU")).toBeVisible();
  await expect(drawer().getByText("Waiting", { exact: true })).toBeVisible();
  await shot("j03-generate-card");
  // A1 (fixed): each action sees the screen as the earlier actions of the same reply left it
  await expect(line("duration.frames: no such setting")).toHaveCount(0); // asked after useIn switched
  await expect(card).toHaveAccessibleName(/^Confirm: Generate a 5 s clip\?/); // the form says 5.0 s

  await card.getByRole("button", { name: "GENERATE" }).click();
  await expect(line(/^You clicked GENERATE · 1 /)).toBeVisible();
  await replied("Your clip is on its way.");
  await expect(
    page.getByRole("group", { name: "Run progress" }).getByRole("img", { name: "done" }).last(),
  ).toBeVisible({ timeout: 40_000 });
  await shot("j03-clip-made");

  // 10 s on a model that makes at most 5 s: refused with the form's reason
  const n2 = sent(MODEL).length;
  script(
    MODEL,
    {
      content: "Ten seconds.",
      tool_calls: [{ tool: "change", args: { key: "duration.frames", value: 241 } }],
    },
    { content: "This model makes at most 5 s." },
  );
  await say("make it 10 seconds");
  await replied("This model makes at most 5 s.");
  await expect(line(/^Length: /)).toHaveText("Length: the longest is 5.0 s"); // seconds, not the frame grid
  expect(told(n2).join()).toContain("(121 frames at 24 fps)"); // the model still gets the frames
  await shot("j03-ten-seconds-refused");
});

test("J4 take me to the gallery (and what happens to the rest of that reply)", async () => {
  await open();
  script(
    MODEL,
    {
      content: "Opening Gallery.",
      tool_calls: [
        { tool: "navigate", args: { to: "/gallery" } },
        { tool: "change", args: { key: "prompt.text", value: "a fox" } },
      ],
    },
    { content: "Here are your takes." },
  );
  await say("Take me to the gallery");
  await expect(page).toHaveURL(/\/gallery/);
  await replied("Here are your takes.");
  await expect(drawer()).toBeVisible(); // stays open across the screen change
  await expect(line(/^Opened Gallery/)).toBeVisible();
  await expect(line("Did not run · went to another screen")).toBeVisible();
  await shot("j04-gallery");

  script(
    MODEL,
    { content: "Opening Admin.", tool_calls: [{ tool: "navigate", args: { to: "/admin" } }] },
    { content: "Admin is only for admins." },
  );
  await say("open the admin page");
  await replied("Admin is only for admins.");
  await expect(line("Can't go there · Admin is for admins only")).toBeVisible();
  await expect(page).toHaveURL(/\/gallery/);
});

test("J5 a model that thinks: the Thinking count keeps going while I close and reopen", async () => {
  test.setTimeout(150_000);
  await api(admin).put(`/v1/studio/admin/providers/${THINK_PROV}`, {
    ...SERVER,
    preset: "ollama",
    models: [THINK_MODEL],
    thinking: { [THINK_MODEL]: "on" },
  });
  await settings({ enabled: true, provider: THINK_PROV, model: THINK_MODEL, limits: LIMITS });
  try {
    await page.goto("/");
    await open();
    script(THINK_MODEL, { delay_s: 9, content: "Here is a warm portrait prompt." });
    await say("give me a warm portrait prompt");
    await expect(thinkingBubble()).toBeVisible();
    await expect(thinkingBubble()).toHaveText(/^Thinking… [3-9] s$/, { timeout: 8_000 });
    await expect(drawer().getByRole("heading", { name: "Assistant" })).toBeVisible();
    await shot("j05-thinking");
    const before = Number((await thinkingBubble().textContent())!.match(/(\d+) s/)![1]);
    await page.keyboard.press("ControlOrMeta+j");
    await expect(drawer()).toBeHidden();
    await page.waitForTimeout(2000);
    await page.keyboard.press("ControlOrMeta+j");
    const after = Number((await thinkingBubble().textContent())!.match(/(\d+) s/)![1]);
    expect(after).toBeGreaterThanOrEqual(before + 2); // kept counting, not back to 0
    await replied("Here is a warm portrait prompt.");
    await expect(thinkingBubble()).toHaveCount(0);
  } finally {
    await settings({ enabled: true, provider: PROV, model: null, limits: LIMITS });
  }

  // a model that does not think: the Working chip, no bubble
  await page.reload();
  await open();
  script(MODEL, { delay_s: 5, content: "Done without thinking." });
  await say("another one");
  await expect(working()).toBeVisible();
  await page.waitForTimeout(1500);
  await expect(thinkingBubble()).toHaveCount(0);
  await replied("Done without thinking.");
});

test("J6 I come back later in a new window, find my chat, and start a new one", async ({
  browser,
}) => {
  const later = await browser.newContext();
  const p = await later.newPage();
  try {
    await signIn(p, user);
    await p.goto("/");
    await open(p);
    await expect(drawer(p).getByText("Done without thinking.")).toBeVisible();
    await expect(drawer(p).getByText("Read-only", { exact: true })).toBeHidden();
    await expect(box(p)).toBeEnabled();
    await shot("j06-chat-is-back", p);

    await newChat(p);
    // the older chat is in Chats, below the new one; picked, its whole thread comes back (board frame 7)
    await drawer(p).getByRole("button", { name: "Chats" }).click();
    const list = drawer(p).getByRole("menu", { name: "Chats" });
    const rows = list.getByRole("menuitemradio");
    await expect(rows.first()).toHaveAttribute("aria-checked", "true");
    await expect(rows.first()).toContainText("Open");
    await shot("j06-chats-list", p);
    await rows.nth(1).click();
    await expect(list).toBeHidden();
    await expect(drawer(p).getByText("Done without thinking.")).toBeVisible();
    await expect(box(p)).toBeEnabled();
    // Escape closes the list, not the drawer; then back to the new chat (still the newest, for J7)
    await drawer(p).getByRole("button", { name: "Chats" }).click();
    await p.keyboard.press("Escape");
    await expect(list).toBeHidden();
    await expect(drawer(p)).toBeVisible();
    await drawer(p).getByRole("button", { name: "Chats" }).click();
    await rows.first().click();
    await expect(drawer(p).getByText("Hi. What should we make?")).toBeVisible();
    await drawer(p).getByRole("button", { name: "Memory" }).click();
    await drawer(p).getByRole("tab", { name: "Chats" }).click();
    await expect(
      drawer(p).getByRole("list", { name: "Chats" }).getByRole("listitem").first(),
    ).toContainText("chat · ");
    await shot("j06-memory-chats", p);
  } finally {
    await later.close();
  }
});

test("J7 I tell it a preference and a new chat remembers it", async () => {
  await page.reload(); // the newest chat (the one J6 started)
  await open();
  script(
    MODEL,
    {
      content: "Noted.",
      tool_calls: [
        { tool: "remember", args: { kind: "preference", text: "Likes warm, golden-hour light" } },
      ],
    },
    { content: "I'll keep that in mind." },
  );
  await say("I always want warm, golden-hour light");
  await replied("I'll keep that in mind.");
  await expect(line("Remembered: Likes warm, golden-hour light")).toBeVisible();
  await expect(
    line("Remembered: Likes warm, golden-hour light").getByRole("button", { name: /^Undo: / }),
  ).toBeVisible();

  await drawer().getByRole("button", { name: "Memory" }).click();
  await drawer().getByRole("tab", { name: "Likes" }).click();
  const likes = drawer().getByRole("list", { name: "Likes" });
  await expect(likes).toContainText("Likes warm, golden-hour light");
  await shot("j07-memory-likes");
  await drawer().getByRole("button", { name: "Close Memory" }).click();

  await newChat();
  const n = sent(MODEL).length;
  script(MODEL, { content: "Sure, warm light it is." });
  await say("make a portrait");
  await replied("Sure, warm light it is.");
  const sys = told(n)[0];
  expect(sys).toContain("What the user asked you to remember");
  expect(sys).toContain("Likes warm, golden-hour light");

  // Memory: delete it by hand; Clear all asks first
  await drawer().getByRole("button", { name: "Memory" }).click();
  await drawer().getByRole("tab", { name: "Likes" }).click();
  // Edit and Delete in the item's ⋯ menu (alpha.2 item 5)
  await likes
    .getByRole("listitem")
    .filter({ hasText: "Likes warm, golden-hour light" })
    .getByRole("button", { name: "More for this item" })
    .click();
  await drawer().getByRole("menuitem", { name: "Delete" }).click();
  await expect(drawer().getByText("Nothing here yet.")).toBeVisible();
  await drawer().getByRole("button", { name: "Clear all" }).click();
  const clear = drawer().getByRole("region", { name: "Confirm: Clear all memory?" });
  await expect(clear).toBeVisible();
  await clear.getByRole("button", { name: "Not now" }).click();
  await drawer().getByRole("button", { name: "Close Memory" }).click();
});

test("J8 take me to the timeline and put my last 3 takes in order", async () => {
  test.setTimeout(150_000);
  const titles = ["shot one harbour", "shot two boat", "shot three gulls"];
  const ids = await echoTakes(titles);
  await open();
  script(
    MODEL,
    { content: "Opening Timeline.", tool_calls: [{ tool: "navigate", args: { to: "/timeline" } }] },
    {
      content: "Adding them.",
      tool_calls: [
        {
          tool: "edit",
          args: {
            ops: [
              {
                op: "add",
                take_ids: ids,
                ids: ids.map(() => randomUUID().replace(/-/g, "").slice(0, 12)),
              },
            ],
          },
        },
      ],
    },
    {
      content: "Making a sequence first.",
      tool_calls: [{ tool: "newSequence", args: { title: "Harbour" } }],
    },
    {
      content: "Adding them.",
      tool_calls: [
        {
          tool: "edit",
          args: {
            ops: [
              {
                op: "add",
                take_ids: ids,
                ids: ids.map(() => randomUUID().replace(/-/g, "").slice(0, 12)),
              },
            ],
          },
        },
      ],
    },
    { content: "Added your last 3 takes in order." },
  );
  await say("Take me to the timeline and put the last 3 takes in order");
  await expect(page).toHaveURL(/\/timeline/);
  await replied("Added your last 3 takes in order.");
  await expect(line(/^Opened Timeline/)).toBeVisible();
  await expect(line("No sequence is open: make one with New sequence")).toBeVisible();
  await expect(line(/^New sequence · Harbour/)).toBeVisible();
  const added = line("Added 3 clips");
  await expect(added).toBeVisible();
  const v1 = page.getByRole("group", { name: "V1: the picture" }).getByRole("button");
  await expect(v1).toHaveCount(3);
  for (const [i, t] of titles.entries())
    await expect(v1.nth(i)).toHaveAccessibleName(new RegExp(`^Still · ${t},`));
  await shot("j08-timeline-three-clips");

  await added.getByRole("button", { name: /^Undo: / }).click();
  await expect(v1).toHaveCount(0);
  await added.getByRole("button", { name: /^Redo: / }).click();
  await expect(v1).toHaveCount(3);
});

test("J9 I say Not now to a card, leave while one waits, and say yes to a delete", async () => {
  await page.goto("/");
  await open();
  let n = sent(MODEL).length;
  script(
    MODEL,
    { content: "Ready to go.", tool_calls: [{ tool: "generate", args: {} }] },
    { content: "OK, not now." },
  );
  await say("generate it");
  const card = drawer().getByRole("region", { name: /^Confirm: Generate/ });
  await expect(card).toBeVisible({ timeout: 30_000 });
  await card.getByRole("button", { name: "Not now" }).click();
  await replied("OK, not now.");
  await expect(line("Not now · Generate")).toBeVisible();
  expect(told(n)[1]).toContain("cancelled");

  // leaving the screen while the card waits cancels it
  script(
    MODEL,
    { content: "Ready to go.", tool_calls: [{ tool: "generate", args: {} }] },
    { content: "You left, so I did not generate." },
  );
  await say("generate it now");
  await expect(card).toBeVisible({ timeout: 30_000 });
  await page
    .getByRole("navigation", { name: "Sections" })
    .getByRole("link", { name: "Gallery" })
    .click();
  await expect(page).toHaveURL(/\/gallery/);
  await replied("You left, so I did not generate.");
  await expect(line("Cancelled: you left the screen")).toBeVisible();

  // delete a take from the Gallery: a danger card, Yes deletes it
  const found = await api(page).get("/v1/studio/takes?q=gulls");
  const gull = found.takes[0].id;
  n = sent(MODEL).length;
  script(
    MODEL,
    { content: "Deleting it.", tool_calls: [{ tool: "delete", args: { take_ids: [gull] } }] },
    { content: "Deleted." },
  );
  await say("delete the gulls take");
  const del = drawer().getByRole("region", { name: "Confirm: Delete shot three gulls?" });
  await expect(del).toBeVisible({ timeout: 30_000 });
  await shot("j09-delete-card");
  await del.getByRole("button", { name: "DELETE" }).click();
  await replied("Deleted.");
  await expect(line("Deleted shot three gulls")).toBeVisible();
  const r = await page.request.get(`/api/v1/studio/takes/${gull}`);
  expect(r.status()).toBe(404);
});

test("J9b I reload the page later: nothing from earlier in the chat runs again", async () => {
  // this chat has run a navigate, timeline edits, generate and delete cards in this tab; it shows in full runs
  // (the replay is long enough), not always in a run of J8-J9b alone
  const posted: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && /\/assistant\/threads\/[^/]+\/messages$/.test(r.url()))
      posted.push(r.postData() ?? "");
  });
  for (const to of ["/", "/gallery", "/"]) {
    await page.goto(to);
    await open();
    await expect(drawer().getByText("Deleted.").last()).toBeVisible();
    await page.waitForTimeout(3000);
  }
  await shot("j09b-after-reload");
  await expect.soft(page).not.toHaveURL(/\/timeline/); // not moved by an old navigate
  await expect.soft(drawer().getByText("Waiting", { exact: true })).toBeHidden(); // no old card
  expect.soft(posted.filter((x) => x.includes('"kind":"results"'))).toEqual([]);
  await expect(drawer().getByRole("alert")).toHaveCount(0); // no "nothing waits on <id>"
});

test("J10 the provider refuses or is down: the error is in plain words and I can go on", async () => {
  test.setTimeout(150_000);
  await open();
  const notes = drawer().getByRole("note");
  script(MODEL, { status: 401 });
  await say("hello?");
  await expect(notes.last()).toHaveText(/refused its key\. Ask your admin to check it\./, {
    timeout: 30_000,
  });
  await expect(working()).toBeHidden();
  script(MODEL, { content: "Back." });
  await say("hello again");
  await replied("Back.");

  script(MODEL, { status: 503 }, { status: 503 }, { status: 503 }, { status: 503 });
  await say("are you there?");
  await expect(notes.last()).toHaveText(/is busy\. Try again in a minute\./, {
    timeout: 60_000,
  });
  await shot("j10-provider-down");

  // a tool that is not offered, twice
  script(
    MODEL,
    { content: "Hm.", tool_calls: [{ tool: "launch_rockets", args: {} }] },
    { content: "Hm.", tool_calls: [{ tool: "launch_rockets", args: {} }] },
  );
  await say("do the thing");
  // the plain line; the checker's words only behind Details (board frame 7)
  await expect(notes.last()).toContainText("The model gave a broken answer twice.", {
    timeout: 30_000,
  });
  await expect(notes.last().getByRole("button", { name: "Retry" })).toBeVisible();
  const details = notes.last().getByRole("button", { name: "Details" });
  await expect(details).toHaveAttribute("aria-expanded", "false");
  await details.click();
  await expect(notes.last()).toContainText(/The answer was invalid twice: /);
  await shot("j10-broken-answer-details");
  for (const t of await notes.allTextContents())
    expect(t, "no code or JSON in a notice").not.toMatch(/[{}[\]]|Traceback|Error:/);
});

test("J11 a long request stops at the per-message cap, and a daily cap locks the box", async () => {
  test.setTimeout(180_000);
  await open();
  const n = sent(MODEL).length;
  script(
    MODEL,
    ...Array.from({ length: 40 }, () => ({
      content: "",
      tool_calls: [{ tool: "search_takes", args: { limit: 1 } }],
    })),
  );
  await say("look through all my takes one by one");
  await expect(drawer().getByRole("note").last()).toHaveText(
    "Stopped at 40 model calls for one message.",
    { timeout: 120_000 },
  );
  expect(sent(MODEL).length - n).toBe(40);
  await shot("j11-message-cap");

  await settings({
    enabled: true,
    provider: PROV,
    model: null,
    limits: { ...LIMITS, model_calls_per_day: 1 },
  });
  try {
    await say("one more");
    const bar = drawer().getByRole("alert").filter({ hasText: "Daily limit reached" });
    await expect(bar).toBeVisible({ timeout: 30_000 });
    await expect(bar).toContainText("You used today's 1 model call."); // singular (owner, 2026-11-25)
    await expect(
      drawer().getByText("Your message is kept. The screen still works as usual."),
    ).toBeVisible();
    await expect(box()).toBeDisabled();
    await expect(box()).toHaveAttribute("placeholder", /^Back /);
    await expect(drawer().getByRole("button", { name: "Send" })).toBeDisabled();
    await shot("j11-daily-limit");
  } finally {
    await settings({ enabled: true, provider: PROV, model: null, limits: LIMITS });
  }
});

test("J11b after the admin raises the daily cap, I can write again", async () => {
  // real use: the user asks the admin, the admin raises it, the user reloads and expects to go on
  await page.reload();
  await open();
  await expect(box()).toBeEnabled(); // A2 (fixed)
  await shot("j11b-open-after-raise");
  // and a new chat works too
  await newChat();
  await expect(box()).toBeEnabled();
  script(MODEL, { content: "Here again." });
  await say("hello after the cap");
  await replied("Here again.");
});

test("J12 the admin turns the assistant off and on: the dock follows", async () => {
  await admin.goto("/admin?tab=agents");
  await expect(dock(admin)).toBeVisible();
  const form = admin.getByRole("form", { name: "Assistant settings" });
  const edit = admin.locator('[aria-controls="assistant-settings"]');
  try {
    await edit.click();
    await form.getByRole("switch", { name: "Assistant on" }).click();
    await expect(form.getByRole("switch", { name: "Assistant on" })).toHaveAttribute(
      "aria-checked",
      "false",
    );
    await form.getByRole("button", { name: "Save" }).click();
    await expect(dock(admin)).toBeHidden(); // in that page, no reload
    await shot("j12-admin-off", admin);
    await page.reload();
    await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();
    await expect(dock()).toBeHidden();

    await edit.click();
    await form.getByRole("switch", { name: "Assistant on" }).click();
    await form.getByLabel("Provider").selectOption(PROV);
    await form.getByRole("button", { name: "Save" }).click();
    await expect(dock(admin)).toBeVisible();
    await page.reload();
    await expect(dock()).toBeVisible();
  } finally {
    await settings({ enabled: true, provider: PROV, model: null, limits: LIMITS });
  }
});

test("J13 two tabs: the other tab is read-only while it works, then free again", async () => {
  const b = await ctx.newPage();
  try {
    await page.goto("/");
    await open();
    await b.goto("/");
    await open(b);
    script(MODEL, { delay_s: 8, content: "Here you go." });
    await say("a slow one");
    await expect(drawer(b).getByText("Read-only", { exact: true })).toBeVisible();
    await expect(
      drawer(b).getByText("This chat is working in another tab. Its actions run there."),
    ).toBeVisible();
    await expect(drawer(b).getByRole("button", { name: "Continue here" })).toBeVisible();
    await expect(box(b)).toBeHidden();
    await shot("j13-read-only-tab", b);
    await replied("Here you go.");
    await expect(drawer(b).getByText("Here you go.")).toBeVisible();
    await expect(box(b)).toBeVisible();
  } finally {
    await b.close();
  }
});

test("J14 keyboard only: Cmd/Ctrl J, Enter, Tab to the header buttons, Escape", async () => {
  await page.goto("/gallery");
  await expect(dock()).toBeVisible();
  if (await drawer().isVisible()) await page.keyboard.press("ControlOrMeta+j");
  await expect(drawer()).toBeHidden();
  await page.keyboard.press("ControlOrMeta+j");
  await expect(drawer()).toBeVisible();
  await expect(box()).toBeFocused();
  const names = new Set<string>();
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Shift+Tab");
    names.add(await page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? ""));
  }
  expect([...names]).toEqual(expect.arrayContaining(["Memory", "Chats"]));
  expect([...names].some((x) => x.startsWith("Close ("))).toBe(true);
  await shot("j14-keyboard");
  // Escape closes it while the focus is in it (owner, 2026-11-25, Q3)
  await box().focus();
  await page.keyboard.press("Escape");
  await expect(drawer()).toBeHidden();
  await expect(dock()).toBeVisible();
});

test("J15 on a phone: a bottom sheet that shrinks to one line while it acts", async () => {
  const p = await ctx.newPage();
  try {
    await p.setViewportSize({ width: 375, height: 812 });
    await p.goto("/");
    await open(p);
    await expect(drawer(p).getByRole("button", { name: "Shrink" })).toBeVisible();
    await expect(drawer(p).getByText("Sees", { exact: true })).toBeHidden();
    await shot("j15-phone-sheet", p);
    script(
      MODEL,
      {
        content: "Writing it.",
        tool_calls: [
          { tool: "change", args: { key: "prompt.text", value: "a lighthouse at dusk" } },
        ],
      },
      { content: "Wrote it." },
    );
    await say("write a lighthouse at dusk", p);
    await expect(drawer(p).getByRole("button", { name: "Open the chat" })).toBeVisible({
      timeout: 30_000,
    });
    await shot("j15-phone-shrunk", p);
    await drawer(p).getByRole("button", { name: "Open the chat" }).click();
    await replied("Wrote it.", p);
    await expect(line("Wrote prompt", p)).toBeVisible();
    await expect(prompt(p)).toHaveValue("a lighthouse at dusk");
  } finally {
    await p.close();
  }
});

test("J16 I write while it works: it reads me at the next step, or takes it as the next message, and Stop ends it", async () => {
  test.setTimeout(150_000);
  await page.goto("/");
  await open();
  // c · read at the next step: the next model call has my words, its answer follows them
  let n = sent(MODEL).length;
  script(
    MODEL,
    {
      delay_s: 6,
      content: "Writing it.",
      tool_calls: [
        { tool: "change", args: { key: "prompt.text", value: "a rainy London street" } },
      ],
    },
    {
      content: "Added a red bus.",
      tool_calls: [
        {
          tool: "change",
          args: { key: "prompt.text", value: "a rainy London street, a red bus behind her" },
        },
      ],
    },
    { content: "Done: a red bus behind her." },
  );
  await say("make a rainy London still");
  // a · the box stays open while it works
  await expect(box()).toHaveAttribute("placeholder", "Steer the answer…");
  await expect(drawer().getByText("steers this answer")).toBeVisible();
  await expect(drawer().getByRole("button", { name: "Stop" })).toBeVisible();
  await shot("j16-steer-working");
  // b · sent: at the bottom, dimmed, at the next step (the first model call runs by now)
  await page.waitForTimeout(1500);
  await say("add a red bus behind her");
  const waiting = drawer().getByLabel("Waiting for the next step");
  await expect(waiting).toHaveCount(1);
  await expect(waiting).toContainText("at the next step");
  await shot("j16-steer-waiting");
  await replied("Done: a red bus behind her.");
  await expect(drawer().getByText("at the next step")).toHaveCount(0);
  // read after the first step's line, before the answer that follows it
  const read = drawer().locator(".msg.me").filter({ hasText: "add a red bus behind her" });
  const after = await read.evaluate((el) => {
    const all = [...el.parentElement!.querySelectorAll(".msg")];
    const i = all.indexOf(el);
    return [all[i - 1]?.textContent ?? "", all[i + 1]?.textContent ?? ""];
  });
  expect(after[0]).toContain("Writing it.");
  expect(after[1]).toContain("Added a red bus.");
  await shot("j16-steer-read");
  await expect(prompt()).toHaveValue("a rainy London street, a red bus behind her");
  expect(told(n)[1]).toContain("User: add a red bus behind her");
  await expect(box()).toHaveAttribute("placeholder", "Ask, or say what to do");
  await expect(drawer().getByRole("button", { name: "Stop" })).toHaveCount(0);

  // d · it finished first: my message starts the next answer
  n = sent(MODEL).length;
  script(MODEL, { delay_s: 5, content: "Here is the plan." }, { content: "Made it night." });
  await say("plan a still");
  await page.waitForTimeout(1500);
  await say("make it night");
  await expect(drawer().getByText("sent as the next message")).toBeVisible({ timeout: 30_000 });
  await shot("j16-steer-next");
  await replied("Made it night.");
  expect(told(n)).toHaveLength(2);
  expect(told(n)[1]).toContain("User: make it night");

  // Stop cancels the model call in flight (item 27, board frame 15): Stopping… at once, then idle with nothing of
  // the answer kept, no Stopped line, no model call after
  n = sent(MODEL).length;
  script(MODEL, {
    delay_s: 4,
    content: "Starting.",
    tool_calls: [{ tool: "change", args: { key: "prompt.text", value: "never" } }],
  });
  await say("write a long prompt");
  await page.waitForTimeout(1500); // the model call runs
  await drawer().getByRole("button", { name: "Stop" }).click();
  await expect(drawer().getByRole("button", { name: "Stopping…" })).toBeDisabled();
  await expect(drawer().getByRole("textbox", { name: "Message the assistant" })).toBeFocused();
  await expect(drawer().getByText("Stopping…").first()).toBeHidden({ timeout: 20_000 });
  await expect(working()).toBeHidden();
  await page.waitForTimeout(4000); // past the call's delay: its answer never comes
  await expect(drawer().getByText("Starting.")).toHaveCount(0);
  await expect(drawer().getByText(/Stopped/)).toHaveCount(0);
  await expect(prompt()).not.toHaveValue("never");
  expect(sent(MODEL).length - n).toBe(1);
});

test("J17 answers that read well, older steps folded, full screen, the minimized bar, memory rows (alpha.2)", async () => {
  test.setTimeout(180_000);
  await page.goto("/");
  await open();
  if (!(await drawer().getByText("Hi. What should we make?").isVisible())) await newChat();
  const bar = (p = page) => p.getByRole("button", { name: /^Open the Assistant/ });

  // a · formatting, Copy, one-line steps (board frame 10a, 10c)
  script(
    MODEL,
    {
      content: "Setting it up.",
      tool_calls: [
        { tool: "change", args: { key: "prompt.text", value: "a red fox in the snow at dusk" } },
        {
          tool: "change",
          args: { key: "look:the_window_light_of_an_early_winter_morning", value: 1 },
        },
        {
          tool: "remember",
          args: {
            kind: "project",
            text: "Widower film: five shots, Elias and Biscuit. Voice for Elias designed (warm, slow, a little hoarse) and saved. Shot 1 animated, 5 s; shot 2 waits on the window light; the narrator line was rewritten gentler twice.",
          },
        },
      ],
    },
    {
      content:
        "**Name the move first.** LTX follows the first camera words.\n\n- One subject: the fox\n- 5 s or less\n- *slow* beats *very slow*\n\n1. Open Video\n2. Set Camera to `Dolly in`\n3. Start the prompt like this:\n\n```prompt\nSlow dolly in. A red fox trots across the snow at dusk, birches behind.\n```",
    },
  );
  await say("How do I get a slow push-in on the fox shot?");
  await replied("LTX follows the first camera words.");
  await expect(drawer().locator(".cb pre")).toContainText("Slow dolly in.");
  await expect(drawer().getByText("Dolly in", { exact: true })).toHaveJSProperty("tagName", "CODE");
  await expect(drawer().getByRole("button", { name: "Copy the answer" })).toBeVisible();
  await expect(drawer().getByRole("button", { name: "Copy the prompt" })).toBeVisible();
  await shot("d1-formatting");

  // b · a plan as a checklist; the earlier turn's steps fold into one line (frames 10f, 10h)
  script(MODEL, {
    content:
      "Plan:\n- [x] Keyframe for each shot\n- [x] Voice for @elias\n- [ ] Animate shots 2 and 3\n- [ ] Place them on the Timeline",
  });
  await say("Make the whole park scene");
  await replied("Animate shots 2 and 3");
  await expect(drawer().getByRole("list", { name: "Plan" }).getByRole("listitem")).toHaveCount(4);
  const sum = drawer().getByRole("button", { name: /^3 steps · 1 memory$/ });
  await expect(sum).toHaveAttribute("aria-expanded", "false");
  await shot("d1-plan-and-folded");
  await sum.click();
  await expect(line(/^Remembered: Widower film/)).toBeVisible();
  await shot("d1-folded-open");

  // c · full screen and back (frame 9)
  await drawer()
    .getByRole("button", { name: /^Full screen/ })
    .click();
  await expect(drawer()).toHaveClass(/full/);
  await shot("d1-full-screen");
  await box().focus();
  await page.keyboard.press("Escape");
  await expect(drawer()).not.toHaveClass(/full/);
  await expect(drawer()).toBeVisible();

  // d · minimized while it works, then a new answer; it never covers Generate
  script(MODEL, { delay_s: 4, content: "Done: the window shot is set." });
  await say("set up the window shot");
  await drawer().getByRole("button", { name: "Minimize" }).click();
  await expect(drawer()).toBeHidden();
  await expect(page.getByRole("button", { name: "Open the Assistant · Working" })).toBeVisible();
  await shot("d1-minimized-working");
  await expect(page.getByRole("button", { name: "Open the Assistant · new answer" })).toBeVisible({
    timeout: 30_000,
  });
  const b = (await bar().boundingBox())!;
  expect(b.height).toBe(44);
  for (const g of await page.locator("button.key").all()) {
    const k = await g.boundingBox();
    if (k)
      expect(
        k.x < b.x + b.width && b.x < k.x + k.width && k.y < b.y + b.height && b.y < k.y + k.height,
        "the bar covers Generate",
      ).toBe(false);
  }
  await shot("d1-minimized-new-answer");
  await page.getByRole("button", { name: "Open the Assistant · new answer" }).click();
  await expect(drawer().getByText("Done: the window shot is set.")).toBeVisible();

  // e · Memory rows: full width, ⋯ menu with Edit and Delete (frame 14a)
  await drawer().getByRole("button", { name: "Memory", exact: true }).click();
  await drawer().getByRole("tab", { name: "Projects" }).click();
  const row = drawer()
    .getByRole("list", { name: "Projects" })
    .getByRole("listitem")
    .filter({ hasText: "Widower film" });
  await row.getByRole("button", { name: "More for this item" }).click();
  await expect(drawer().getByRole("menuitem", { name: "Edit" })).toBeVisible();
  await shot("d1-memory-menu");
  await drawer().getByRole("menuitem", { name: "Delete" }).click();
  await expect(row).toHaveCount(0);
  await drawer().getByRole("button", { name: "Close Memory" }).click();

  // f · on the Timeline a timecode is a chip that moves the playhead (frame 10e)
  await page.goto("/timeline");
  await open();
  script(MODEL, { content: "Three shots: 0:00 the harbour, 0:02 the boat, 0:04 the gulls." });
  await say("What is on this timeline?");
  await replied("the gulls.");
  const tc = drawer().getByRole("button", { name: "Jump the Timeline to 0:02" });
  await expect(tc).toBeVisible();
  await tc.click();
  await shot("d1-timecodes");

  // g · on a phone: 44 px taps in the sheet, and Minimize is a 44 px bar
  const p = await ctx.newPage();
  try {
    await p.setViewportSize({ width: 375, height: 812 });
    await p.goto("/");
    await open(p);
    const min = drawer(p).getByRole("button", { name: "Minimize" });
    expect((await min.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await shot("d1-phone-sheet", p);
    await min.click();
    expect((await bar(p).boundingBox())!.height).toBe(44);
    await shot("d1-phone-minimized", p);
    await bar(p).click();
    await expect(drawer(p)).toBeVisible();
  } finally {
    await p.close();
  }
});

test("J18 Auto mode: the switch and a generation that runs by itself with Undo (alpha.2 item 12)", async () => {
  await page.goto("/");
  await open();
  await newChat();
  const sw = drawer().getByRole("switch", { name: "Auto" });
  await expect(sw).toHaveAttribute("aria-checked", "false"); // a · off by default, per chat
  await sw.hover();
  await expect(drawer().getByRole("tooltip")).toContainText("big runs still ask");
  await shot("j18-auto-off");
  await sw.click();
  await expect(sw).toHaveAttribute("aria-checked", "true");

  // b · a workflow never run has no time estimate: its first generation still asks; Yes runs it
  const make = (done: string) =>
    script(
      MODEL,
      {
        content: "Making it.",
        tool_calls: [
          { tool: "change", args: { key: "prompt.text", value: "a lighthouse at dusk" } },
          { tool: "generate", args: {} },
        ],
      },
      { content: done },
    );
  make("Made the first.");
  await say("make a lighthouse at dusk");
  const card = drawer().getByRole("region", { name: /^Confirm: Generate/ });
  await expect(
    drawer().getByText("Auto stops here: no time estimate for this run yet"),
  ).toBeVisible();
  await card.getByRole("button", { name: "GENERATE" }).click();
  await replied("Made the first.");
  await expect(
    page.getByRole("group", { name: "Run progress" }).getByRole("img", { name: "done" }).last(),
  ).toBeVisible({ timeout: 40_000 });

  // c · now it has a time: the next generation on your machines runs without its card, says Auto, has Undo
  make("Made it.");
  await say("another one");
  await replied("Made it.");
  await expect(line(/^Auto · Generated 1 take/)).toBeVisible();
  await expect(drawer().getByRole("region", { name: /^Confirm/ })).toHaveCount(0);
  await expect(
    line(/^Auto · Generated 1 take/).getByRole("button", { name: /^Undo: Generated 1 take/ }),
  ).toBeVisible();
  await shot("j18-auto-generated");

  await sw.click(); // off again: the next generation asks
  await expect(sw).toHaveAttribute("aria-checked", "false");
});
