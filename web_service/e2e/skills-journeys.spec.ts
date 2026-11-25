// Skills (alpha.2 item 36, API 1.64; boards Plugins.dc.html SKILLS and Assistant.dc.html 16): the admin finds a
// skill in the test HELM_HOME (contract_tests/fake_helm/plugins mounted as ~/.helm/plugins: fake-skill) next to the
// built-in Animator, turns Animator on; a user's chat reads it (the fake model scripted to call use_skill) and the
// @ picker lists it. The admin's assistant settings and Animator's switch are read first and put back after.
import { randomUUID } from "node:crypto";
import { mkdirSync } from "node:fs";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { ADMIN, newUser, signIn, type Creds } from "./users";
import { script, sent } from "./fake";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/skills";
mkdirSync(SHOTS, { recursive: true });
const MODEL = `skill-${randomUUID().slice(0, 8)}`;
const PROV = `srv-${MODEL}`;
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
let animatorWasOn: boolean | null = null;

async function call(p: Page, method: "get" | "put", path: string, data?: unknown) {
  const r = await p.request[method](`/api${path}`, data === undefined ? {} : { data });
  expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
  return r.json();
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(240_000);
  user = await newUser(browser, "skills");
  adminCtx = await browser.newContext();
  admin = await adminCtx.newPage();
  await signIn(admin, ADMIN);
  snapshot = await call(admin, "get", "/v1/studio/admin/assistant");
  const plugins = await call(admin, "get", "/v1/studio/admin/plugins");
  animatorWasOn =
    plugins.plugins.find((p: { name: string }) => p.name === "animator")?.enabled ?? null;
  await call(admin, "put", `/v1/studio/admin/providers/${PROV}`, { ...SERVER, models: [MODEL] });
  await call(admin, "put", "/v1/studio/admin/assistant", {
    enabled: true,
    provider: PROV,
    model: null,
    limits: (snapshot as { limits: unknown }).limits,
  });
  ctx = await browser.newContext();
  page = await ctx.newPage();
  await signIn(page, user);
});

test.afterAll(async () => {
  try {
    if (animatorWasOn === false)
      await admin.request.put("/api/v1/studio/admin/plugins/animator", {
        data: { enabled: false },
      });
    if (snapshot) {
      const s = snapshot as Record<string, unknown>;
      const back = Object.fromEntries(
        ["enabled", "provider", "model", "web_search", "search_provider", "limits"].map((k) => [
          k,
          s[k],
        ]),
      );
      const r = await admin.request.put("/api/v1/studio/admin/assistant", { data: back });
      if (!r.ok())
        await admin.request.put("/api/v1/studio/admin/assistant", {
          data: { enabled: false, limits: s.limits },
        });
    }
    await admin.request.delete(`/api/v1/studio/admin/providers/${PROV}`);
  } finally {
    await ctx?.close();
    await adminCtx?.close();
  }
});

test.afterEach(async ({}, info) => {
  if (info.status !== info.expectedStatus)
    for (const [who, p] of [
      ["admin", admin],
      ["user", page],
    ] as const)
      await p
        ?.screenshot({ path: `${SHOTS}/${info.title.split(" ")[0]}-${who}-failed.png` })
        .catch(() => undefined);
});

const row = (p: Page, text: string | RegExp) => p.getByRole("row").filter({ hasText: text });
const dock = (p = page) =>
  p.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: /^Assistant/ });
const drawer = (p = page) => p.getByRole("complementary", { name: "Assistant" });
const box = (p = page) => drawer(p).getByLabel("Message the assistant");

test("S1 the admin finds skills, built in and in ~/.helm/plugins, and turns Animator on", async () => {
  await admin.goto("/plugins");
  await admin.getByRole("button", { name: "Rescan", exact: true }).first().click();
  await expect(admin.getByRole("status")).toBeVisible(); // what the Rescan found (or "No new plugins.")

  // yours: the test HELM_HOME's fake-skill, Off until turned on
  const fake = row(admin, /fake-skill/);
  await expect(fake).toContainText("Skill");
  await expect(fake).toContainText("yours");
  await expect(fake).toContainText("Off");

  const anim = row(admin, /^Animator/);
  await expect(anim).toContainText("0.2.0 · built in · plugins/animator");
  await expect(anim).toContainText("Skill");
  if ((await anim.getByRole("button", { name: "Enable" }).count()) > 0)
    await anim.getByRole("button", { name: "Enable" }).click();
  await expect(anim).toContainText("On");
  await expect(anim.getByRole("button", { name: "Disable" })).toBeVisible();

  // its aside, as the board's "A skill · the aside"
  await anim.getByRole("button", { name: "Animator" }).click();
  const aside = admin.getByRole("complementary", { name: "Plugin: Animator" });
  await expect(aside.getByRole("switch", { name: "Enabled" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await expect(aside).toContainText("Use when making a cartoon or animated shot");
  await expect(aside).toContainText("0.2.0 · format 1");
  await expect(aside).toContainText("Built in · plugins/animator");
  await expect(aside).toContainText("SKILL.md · camera-moves.md");
  await expect(aside).toContainText("Recipes");
  await expect(aside).toContainText("MAY DO");

  // where skills come from, in Add plugin
  await admin.getByRole("button", { name: "Add plugin" }).click();
  const menu = admin.getByRole("menu", { name: "Add plugin" });
  await expect(menu.getByRole("menuitem", { name: /^Built in/ })).toBeVisible();
  await expect(
    menu.getByRole("menuitem", { name: /^Yours: drop a folder or a \.zip/ }),
  ).toBeVisible();
  await expect(menu.getByRole("menuitem", { name: "Upload a .zip here · later" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  await admin.keyboard.press("Escape");
  await admin.screenshot({ path: `${SHOTS}/s1-animator-on.png`, fullPage: true });
});

test("S2 the Assistant reads Animator for a shot plan; tapping its line shows the guide", async () => {
  await page.goto("/");
  await expect(dock()).toBeVisible();
  if ((await dock().getAttribute("aria-expanded")) !== "true") await dock().click();
  await expect(drawer()).toBeVisible();

  const n = sent(MODEL).length;
  script(
    MODEL,
    {
      content: "Reading the guide.",
      tool_calls: [{ tool: "use_skill", args: { skill: "animator" } }],
    },
    { content: "4 shots, 16 s on LTX-2.5." },
  );
  await box().fill("Plan 4 shots: a fox chases a leaf");
  await box().press("Enter");
  await expect(drawer().getByText("4 shots, 16 s on LTX-2.5.")).toBeVisible({ timeout: 40_000 });
  // the model was offered use_skill and told Animator is there
  const first = sent(MODEL).slice(n)[0];
  expect(JSON.stringify(first.body.tools)).toContain("use_skill");
  expect(JSON.stringify(first.body.messages)).toContain("animator");

  const skl = drawer().getByRole("button", { name: "Using skill: Animator · tap to see" });
  await expect(skl).toBeVisible();
  await skl.click();
  const open = drawer().getByRole("button", { name: "Using skill: Animator · tap to hide" });
  await expect(open).toHaveAttribute("aria-expanded", "true");
  await expect(open).toContainText("built in · 0.2.0");
  await expect(
    drawer().getByText(/A guide for making a short animated \(cartoon\) film/),
  ).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/s2-using-skill-open.png` });
});

test("S3 typing @anim in the composer lists @animator; picking it inserts it", async () => {
  await box().fill("@anim");
  const pick = drawer().getByRole("listbox", { name: "Pick a skill, take or element" });
  await expect(pick.getByRole("option", { name: /@animator/ })).toBeVisible();
  await expect(pick.getByRole("option", { name: /@animator/ })).toContainText("skill");
  await page.screenshot({ path: `${SHOTS}/s3-at-picker.png` });
  await box().press("Enter");
  await expect(box()).toHaveValue("@animator ");
  await expect(pick).toBeHidden();
  await box().fill("");
});
