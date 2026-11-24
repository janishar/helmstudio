// Admin and settings as a real admin uses them (QA, 2026-11-24): one admin page for the file, fresh names per run, on
// the test stack's fakes. Boards: Providers, ProvidersModels, AdminAgents, AgentSettings, Users, ApiKeys, Hosts, Models,
// ModelDownload, Settings, SignIn, Landing; plans 25, 27, 28. The test stack must never reach a real provider: after
// picking the DeepSeek preset the admin points its Base URL at the fake's https address (the only test-only step).
// Global state (assistant settings, providers, key files, API keys, a downloaded variant) is read first and put back.
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { script, sent } from "./fake";
import { ADMIN, newUser, signIn, type Creds } from "./users";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/admin";
mkdirSync(SHOTS, { recursive: true });
const PROJECT = process.env.COMPOSE_PROJECT ?? "helmstudio-test";
if (PROJECT === "helmstudio") throw new Error("e2e never touches the dev stack");

const t = randomUUID().slice(0, 8);
const PROV = `ds-${t}`;
const TITLE = `DeepSeek ${t}`;
const KEYFILE = `e2e-key-${t}`;
const CUSTOM = `my-deepseek-${t}`;
const FAKE_REMOTE = "https://fake-openai:8443/v1";

let admin: Page;
let assistantBefore: unknown;
let apiKeyId = "";

const api = (p: Page) => ({
  get: async (path: string) => {
    const r = await p.request.get(`/api${path}`);
    expect(r.ok(), `GET ${path}: ${r.status()}`).toBeTruthy();
    return r.json();
  },
  put: (path: string, data: unknown) => p.request.put(`/api${path}`, { data }),
  del: (path: string) => p.request.delete(`/api${path}`),
});

async function signedIn(browser: Browser, c: Creds) {
  const p = await (await browser.newContext()).newPage();
  await signIn(p, c);
  return p;
}

test.beforeAll(async ({ browser }) => {
  admin = await signedIn(browser, ADMIN);
  assistantBefore = await api(admin).get("/v1/studio/admin/assistant");
});
test.afterAll(async () => {
  if (!admin) return;
  const a = api(admin);
  // put back what the file changed, whatever failed
  if (assistantBefore) {
    const s = assistantBefore as Record<string, unknown>;
    await a.put("/v1/studio/admin/assistant", {
      enabled: s.enabled,
      provider: s.provider ?? null,
      model: s.model ?? null,
      web_search: s.web_search ?? false,
      search_provider: s.search_provider ?? null,
      limits: s.limits,
    });
  }
  await a.del(`/v1/studio/admin/providers/${PROV}`);
  await a.del(`/v1/studio/admin/provider-keys/${KEYFILE}`);
  if (apiKeyId) await admin.request.delete(`/api/auth/admin/apikeys/${apiKeyId}`);
  await admin.context().close();
});
test.afterEach(async ({}, info) => {
  if (info.status !== info.expectedStatus)
    await admin
      ?.screenshot({ path: `${SHOTS}/${info.title.split(" ")[0]}-failed.png`, fullPage: true })
      .catch(() => undefined);
});
const shot = (p: Page, name: string) =>
  p.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });

// ---- what the admin sees
const panel = () => admin.getByRole("complementary", { name: "Add a model provider" });
const field = (label: string | RegExp) => panel().getByLabel(label, { exact: true });
const row = (name: string) => admin.getByRole("row").filter({ hasText: name });
const dock = (p: Page) => p.getByRole("navigation", { name: "Sections" });

test("a01 add DeepSeek from the preset, with my own model name and less thinking", async () => {
  await admin.goto("/admin");
  await admin.getByRole("tab", { name: "Model providers" }).click();
  await expect(admin).toHaveURL(/tab=providers/);
  await expect(panel().getByRole("heading", { name: /Add a model provider/ })).toBeVisible();
  await field("Preset").selectOption({ label: "DeepSeek" });
  // the preset fills the form
  await expect(panel().getByRole("group", { name: "Kind" }).getByRole("button", { name: "Remote" }))
    .toHaveAttribute("aria-pressed", "true"); // prettier-ignore
  await expect(field("Name")).toHaveValue("deepseek");
  await expect(field("Title users see")).toHaveValue("DeepSeek");
  await expect(field("Base URL")).toHaveValue("https://api.deepseek.com");
  await expect(field("Key file")).toHaveValue("deepseek");
  await expect(panel().getByRole("checkbox", { name: "Use deepseek-flash" })).toBeChecked();
  await expect(panel().getByRole("checkbox", { name: "Use deepseek-v4-pro" })).toBeChecked();
  await expect(panel().getByRole("radio", { name: "Default for deepseek-flash" })).toBeChecked();
  await expect(panel().getByText("Missing", { exact: true })).toBeVisible();
  await expect(panel().getByText("Add the key to load the full list.")).toBeVisible();
  await expect(panel().getByLabel("API key")).toBeVisible();
  await shot(admin, "a01-preset-filled");

  // test stack: never the real DeepSeek (see the file's head)
  await field("Name").fill(PROV);
  await field("Title users see").fill(TITLE);
  await field("Base URL").fill(FAKE_REMOTE);
  await field("Key file").fill(KEYFILE);
  await expect(
    panel().getByText("Refresh asks only the preset's or the saved URL. Save first."),
  ).toBeVisible();
  // my own model, by name
  await panel().getByLabel("Model id").fill(CUSTOM);
  await panel().getByLabel("Model id").press("Enter");
  const custom = panel()
    .getByRole("group", { name: "Models" })
    .locator("div")
    .filter({ hasText: CUSTOM })
    .first();
  await expect(panel().getByRole("checkbox", { name: `Use ${CUSTOM}` })).toBeChecked();
  await expect(custom.getByText("custom", { exact: true })).toBeVisible();
  // the flash model thinks less
  const think = panel().getByRole("radiogroup", { name: "Thinking for deepseek-flash" });
  await expect(think.getByRole("radio")).toHaveText(["Off", "Low", "High"]);
  await expect(think.getByRole("radio", { name: "High" })).toHaveAttribute("aria-checked", "true");
  await think.getByRole("radio", { name: "Low" }).click();
  await expect(think.getByRole("radio", { name: "Low" })).toHaveAttribute("aria-checked", "true");
  await shot(admin, "a01-before-save");
  await panel().getByRole("button", { name: "Save provider" }).click();

  const r = row(PROV);
  await expect(r).toHaveCount(1);
  await expect(r).toContainText(TITLE);
  await expect(r).toContainText("remote");
  await expect(r).toContainText(FAKE_REMOTE);
  await expect(r).toContainText("deepseek-flash (default)");
  await expect(r).toContainText("deepseek-v4-pro");
  await expect(r).toContainText(CUSTOM);
  await expect(r).toContainText(`key file ${KEYFILE}`);
  await expect(r).toContainText("Enabled");
  // the form is clear for the next one
  await expect(field("Name")).toHaveValue("");
  await shot(admin, "a01-saved");
});

test("a02 a wrong key gets a plain error, then the right key is saved and the models load", async () => {
  await row(PROV).getByRole("button", { name: "Edit" }).click();
  await expect(panel().getByRole("heading", { name: `Edit ${PROV}` })).toBeVisible();
  await expect(field("Name")).toHaveAttribute("readonly", "");
  await expect(panel().getByText("Missing", { exact: true })).toBeVisible();
  const key = panel().getByLabel("API key", { exact: true });
  const saveKey = panel().getByRole("button", { name: "Save key" });
  await expect(saveKey).toBeDisabled();

  await key.fill(`refused-${t}`);
  await saveKey.click();
  await expect(panel().getByRole("alert")).toHaveText("the provider refused this key; not saved");
  await expect(panel().getByText("Missing", { exact: true })).toBeVisible();
  await expect(key).toHaveValue("");
  await shot(admin, "a02-refused");

  await key.fill(`down-${t}`);
  await saveKey.click();
  const amber = panel().getByRole("alert").filter({ hasText: "could not check" });
  await expect(amber).toContainText("could not check: HTTP 503");
  await expect(amber.getByRole("button", { name: "Save anyway" })).toBeEnabled();
  await shot(admin, "a02-unreachable");

  await key.fill(`fake-ok-${t}`);
  await saveKey.click();
  await expect(panel().getByText("Found", { exact: true })).toBeVisible();
  await expect(panel().getByRole("button", { name: "Replace key" })).toBeVisible();
  await expect(panel().getByRole("button", { name: "Remove key" })).toBeVisible();
  await expect(panel().getByLabel("API key", { exact: true })).toHaveCount(0);
  expect(await admin.content()).not.toContain(`fake-ok-${t}`); // never shown again
  // the models come from the provider by themselves
  await expect(panel().getByText(/^\d+ found$/)).toBeVisible({ timeout: 30_000 });
  await expect(panel().getByRole("checkbox", { name: "Use fake-large" })).toBeVisible();
  await shot(admin, "a02-key-found");
  await panel().getByRole("button", { name: "Cancel" }).click();
  await expect(panel().getByRole("heading", { name: "Add a model provider" })).toBeVisible();
});

test("a03 test the provider from the list, switch it off and on", async () => {
  const r = row(PROV);
  await r.getByRole("button", { name: "Test" }).click();
  await expect(r).toContainText(/answered · [\d.]+ s|\d+ models listed · [\d.]+ s/, {
    timeout: 30_000,
  });
  await shot(admin, "a03-tested");
  await r.getByRole("button", { name: "Disable" }).click();
  await expect(r).toContainText("Disabled");
  await r.getByRole("button", { name: "Enable" }).click();
  await expect(r.getByRole("button", { name: "Disable" })).toBeVisible();
});

test("a04 make it the assistant, ask it something, then turn it off", async () => {
  await admin.getByRole("tab", { name: "AI agents" }).click();
  await expect(admin).toHaveURL(/tab=agents/);
  const arow = admin.getByRole("row").filter({ hasText: "the in-app assistant" });
  await arow.getByRole("button", { name: "Edit" }).click();
  const form = admin.getByRole("form", { name: "Assistant settings" });
  await expect(form).toBeVisible();
  const on = form.getByRole("switch", { name: "Assistant on" });
  if ((await on.getAttribute("aria-checked")) !== "true") await on.click();
  await form.getByLabel("Provider").selectOption({ label: `${TITLE} (remote)` });
  await expect(form.getByLabel("Model", { exact: true })).toContainText(
    "deepseek-flash · Thinking Low (default)",
  );
  await expect(form).toContainText(
    `Sends your messages, the screen summary and tool results to ${TITLE}; never images.`,
  );
  await shot(admin, "a04-assistant-form");
  await form.getByRole("button", { name: "Save" }).click();
  await expect(arow).toContainText(`${TITLE} · deepseek-flash`);
  await expect(arow).toContainText(`your messages to ${TITLE}`);
  await expect(arow).toContainText("On");
  // the dock follows at once
  await expect(dock(admin).getByRole("button", { name: "Assistant" })).toBeVisible();
  await shot(admin, "a04-assistant-on");

  await dock(admin).getByRole("button", { name: "Assistant" }).click();
  const drawer = admin.getByRole("complementary", { name: "Assistant" });
  const calls = sent("deepseek-flash").length;
  script("deepseek-flash", { content: `Hello from ${TITLE}.` });
  await drawer.getByLabel("Message the assistant").fill("hello");
  await drawer.getByLabel("Message the assistant").press("Enter");
  await expect(drawer.getByText(`Hello from ${TITLE}.`)).toBeVisible({ timeout: 30_000 });
  // it went to the new provider with the thinking the admin picked (Low)
  const last = sent("deepseek-flash").slice(calls).at(-1)?.body as
    Record<string, unknown> | undefined;
  expect(last?.reasoning_effort).toBe("low");
  await shot(admin, "a04-assistant-answered");
  await drawer.getByRole("button", { name: /^Close \(/ }).click();

  await arow.getByRole("button", { name: "Edit" }).click();
  await form.getByRole("switch", { name: "Assistant on" }).click();
  await form.getByRole("button", { name: "Save" }).click();
  await expect(arow).toContainText("no Assistant in the dock");
  await expect(dock(admin).getByRole("button", { name: "Assistant" })).toHaveCount(0);
});

test("a05 delete a provider, after changing my mind once", async () => {
  await admin.getByRole("tab", { name: "Model providers" }).click();
  const r = row(PROV);
  await r.getByRole("button", { name: "Delete" }).click();
  const dlg = admin.getByRole("dialog", { name: "Delete this provider?" });
  await expect(dlg).toContainText(`“${TITLE}” is deleted`);
  await shot(admin, "a05-delete-asked");
  await dlg.getByRole("button", { name: "Keep it" }).click();
  await expect(r).toHaveCount(1);
  await r.getByRole("button", { name: "Delete" }).click();
  await dlg.getByRole("button", { name: "Delete" }).click();
  await expect(r).toHaveCount(0);
});

// ---- users
let person: Creds & { name: string };
let personPage: Page;

test("a06 add a user who signs in and sees only their own work", async ({ browser }) => {
  // someone else already made a take
  const other = await newUser(browser, "admin-other");
  const op = await signedIn(browser, other);
  const s = await (
    await op.request.post("/api/v1/studio/sessions", { data: { title: "Not yours" } })
  ).json();
  const run = await (
    await op.request.post(`/api/v1/studio/sessions/${s.id}/runs`, {
      data: {
        workflow_id: "echo",
        inputs: { "prompt.text": `secret fox ${t}`, "echo.steps": 2, "echo.delay_ms": 50 },
      },
    })
  ).json();
  await expect
    .poll(
      async () => (await (await op.request.get(`/api/v1/studio/runs/${run.id}`)).json()).status,
      { timeout: 60_000 },
    )
    .toBe("succeeded");
  await op.context().close();

  await admin.goto("/admin");
  const add = admin.getByRole("complementary", { name: "Add a user" });
  person = { name: `Rita ${t}`, email: `rita-${t}@example.com`, password: "" };
  await add.getByLabel("Name").fill(person.name);
  await add.getByLabel("Email").fill(person.email);
  await add.getByRole("button", { name: "Generate" }).click();
  person.password = await add.getByLabel("Temporary password").inputValue();
  expect(person.password.length).toBeGreaterThanOrEqual(10);
  await expect(add.getByRole("checkbox", { name: /^User/ })).toBeChecked();
  await add.getByRole("button", { name: "Create user" }).click();
  await expect(add.getByRole("status")).toHaveText(
    `Added ${person.email}. Share the email and password with them.`,
  );
  const r = row(person.email);
  await expect(r).toContainText(person.name);
  await expect(r).toContainText("Active");
  await expect(r).toContainText("user");
  await shot(admin, "a06-user-added");

  personPage = await signedIn(browser, person);
  await expect(dock(personPage).getByRole("link", { name: "Admin" })).toHaveCount(0);
  await personPage.goto("/gallery");
  await expect(personPage.getByRole("heading", { name: "Gallery", level: 1 })).toBeVisible();
  await expect(personPage.getByText(`secret fox ${t}`)).toHaveCount(0);
  await shot(personPage, "a06-user-gallery");
  await personPage.goto("/admin");
  await expect(personPage.getByRole("heading", { name: "Not found" })).toBeVisible();
});

test("a07 reset a user's password, then disable and enable the account", async ({ browser }) => {
  const r = row(person.email);
  await r.getByRole("button", { name: "Reset password" }).click();
  const dlg = admin.getByRole("dialog", { name: `New password for ${person.name}` });
  const fresh = `Fresh-pass-${t}`;
  await dlg.getByLabel("Temporary password").fill(fresh);
  await dlg.getByRole("button", { name: "Set password" }).click();
  await expect(dlg).toBeHidden();
  person.password = fresh;
  await shot(admin, "a07-password-reset");

  await r.getByRole("button", { name: "Disable" }).click();
  await expect(r).toContainText("Disabled");
  const p = await (await browser.newContext()).newPage();
  await p.goto("/signin");
  await p.getByLabel("Email").fill(person.email);
  await p.getByLabel("Password").fill(fresh);
  await p.getByRole("button", { name: "Sign in" }).click();
  const err = p.getByRole("alert").filter({ hasText: /\w/ });
  await expect(err).toBeVisible();
  await expect(p.locator("#form-error")).toHaveText("This account is off. Ask your admin.");
  await shot(p, "a07-disabled-signin");

  await r.getByRole("button", { name: "Enable" }).click();
  await expect(r).toContainText("Active");
  await signIn(p, person);
  await p.context().close();
});

test("a08 an API key made on the server shows up, and revoking it shuts it out", async () => {
  const out = execFileSync("docker", [
    "exec",
    `${PROJECT}-auth-1`,
    "/app/auth",
    "apikey",
    `e2e-${t}`,
  ]).toString();
  const key = out.match(/helm_[A-Za-z0-9_-]{43}/)?.[0];
  expect(key, "the command prints the key").toBeTruthy();
  const token = (await admin.context().cookies()).find((c) => c.value.startsWith("eyJ"))?.value;
  const me = () =>
    admin.request.get("/auth/me", {
      headers: { "X-API-Key": key!, Authorization: `Bearer ${token}` },
    });
  expect((await me()).status()).toBe(200);

  await admin.goto("/admin?tab=keys");
  const r = row(`e2e-${t}`);
  await expect(r).toContainText(key!.slice(0, 12) + "…");
  await expect(r).toContainText("Active");
  expect(await admin.content()).not.toContain(key!);
  await shot(admin, "a08-key-listed");
  apiKeyId =
    (await (await admin.request.get("/api/auth/admin/apikeys")).json()).api_keys?.find(
      (k: { client: string }) => k.client === `e2e-${t}`,
    )?.id ?? "";

  await r.getByRole("button", { name: "Revoke" }).click();
  const dlg = admin.getByRole("dialog", { name: "Revoke this key?" });
  await expect(dlg).toContainText(`e2e-${t} key (${key!.slice(0, 12)}…)`);
  await dlg.getByRole("button", { name: "Revoke" }).click();
  await expect(r).toContainText(/Revoked/);
  await expect(r.getByRole("button", { name: "Revoke" })).toHaveCount(0);
  await shot(admin, "a08-key-revoked");
  await expect
    .poll(async () => (await me()).status(), { timeout: 75_000, intervals: [5_000] })
    .toBe(401);
});

test("a09 check the hosts", async () => {
  await admin.goto("/admin?tab=hosts");
  const card = admin.getByRole("region", { name: /^fake / });
  await expect(card).toBeVisible();
  await expect(card.getByRole("meter", { name: "Memory used" })).toBeVisible();
  await expect(card).toContainText(/Heartbeat \d+ s ago|Heartbeat/);
  const side = admin.getByRole("complementary", { name: "Host settings" });
  await expect(side.getByRole("heading", { name: /fake/ })).toBeVisible();
  await expect(side.getByRole("radio", { name: "Keep warm" })).toBeVisible();
  await expect(side.getByText("Memory reserve, GB")).toBeVisible();
  await shot(admin, "a09-hosts");
});

test("a10 download a model to a host, then delete it again", async () => {
  test.setTimeout(240_000);
  await admin.goto("/models");
  const echo = admin.getByRole("region", { name: "Echo (fake)" });
  await echo.getByRole("button", { name: "Plus" }).click();
  const side = admin.getByRole("complementary", { name: "Selected model" });
  await expect(side.getByRole("heading", { name: "Echo (fake) · Plus" })).toBeVisible();
  const before = await side.textContent();
  expect(before, "Plus starts missing on the fake host").toMatch(/fake\s*missing/);
  await side.getByRole("button", { name: "Download to host" }).click();
  const dlg = admin.getByRole("dialog", { name: "Download Echo (fake) · Plus" });
  await expect(dlg).toBeVisible();
  await shot(admin, "a10-download-dialog");
  for (const box of await dlg.getByRole("checkbox").all()) await box.check(); // a licence to accept, if any
  await dlg.getByRole("button", { name: /^Download/ }).click();
  await expect(side.getByRole("status")).toContainText("Download queued on fake.");
  await expect(side).toContainText(/fake\s*present/, { timeout: 180_000 });
  await shot(admin, "a10-downloaded");
  await side.getByRole("button", { name: "Delete from host" }).click();
  const del = admin.getByRole("dialog", { name: "Delete Echo (fake) · Plus from fake?" });
  await del.getByRole("button", { name: /^Delete/ }).click();
  await expect(side).toContainText(/fake\s*missing/, { timeout: 120_000 });
});

test("a11 a user changes their password, signs out and back in", async () => {
  const p = personPage;
  // a07 reset the password and disabled the account, which signed this tab out: sign in with the new one
  await signIn(p, person);
  await p.goto("/settings");
  const acct = p.getByRole("region", { name: "Account" });
  await expect(acct).toContainText(person.name);
  await expect(acct).toContainText(person.email);
  await expect(acct).toContainText("user");
  const pw = p.getByRole("region", { name: "Password" });
  await pw.getByLabel("Current password").fill("not my password 1");
  await pw.getByLabel("New password").fill("short");
  await pw.getByLabel("Confirm").fill("short");
  await pw.getByRole("button", { name: "Change password" }).click();
  await expect(pw).toContainText("At least 10 characters");
  const next = `Brand-new-${t}-pass`;
  await pw.getByLabel("New password").fill(next);
  await pw.getByLabel("Confirm").fill(next);
  await pw.getByRole("button", { name: "Change password" }).click();
  await expect(pw).toContainText("Current password is incorrect.");
  await shot(p, "a11-wrong-current");
  await pw.getByLabel("Current password").fill(person.password);
  await pw.getByLabel("New password").fill(next);
  await pw.getByLabel("Confirm").fill(next);
  await pw.getByRole("button", { name: "Change password" }).click();
  await expect(pw.getByRole("status")).toContainText("Password changed.");
  await shot(p, "a11-changed");

  await p
    .getByRole("region", { name: "Sign out" })
    .getByRole("button", { name: "Sign out" })
    .click();
  await expect(p).toHaveURL(/\/signin|\/welcome|\/$/);
  await p.goto("/signin");
  await p.getByLabel("Email").fill(person.email);
  await p.getByLabel("Password").fill(person.password);
  await p.getByRole("button", { name: "Sign in" }).click();
  const err = p.getByRole("alert").filter({ hasText: /\w/ });
  await expect(err).toBeVisible();
  await expect(p.locator("#form-error")).toHaveText("Wrong email or password.");
  await shot(p, "a11-old-password");
  person.password = next;
  await signIn(p, person);
  await expect(p).not.toHaveURL(/signin/);
  await p.context().close();
});

test("a12 a visitor lands, goes to sign in, and gets plain errors", async ({ browser }) => {
  const p = await (await browser.newContext()).newPage();
  await p.goto("/");
  await expect(p.getByRole("link", { name: /Sign in/ }).first()).toBeVisible();
  await shot(p, "a12-landing");
  await p
    .getByRole("link", { name: /Sign in/ })
    .first()
    .click();
  await expect(p).toHaveURL(/\/signin/);
  await p.getByRole("button", { name: "Sign in" }).click();
  await expect(p.getByLabel("Email")).toBeFocused(); // the browser's own required check, or ours
  await p.getByLabel("Email").fill(ADMIN.email);
  await p.getByLabel("Password").fill("not the password 1");
  await p.getByRole("button", { name: "Sign in" }).click();
  const err = p.getByRole("alert").filter({ hasText: /\w/ });
  await expect(err).toBeVisible();
  await expect(p.locator("#form-error")).toHaveText("Wrong email or password.");
  await expect(p.getByLabel("Email")).toHaveValue(ADMIN.email);
  await shot(p, "a12-wrong-password");
  await p.context().close();
});
