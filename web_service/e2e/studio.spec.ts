// The studio end to end on the test stack's fake runner (echo pack): sign in, an Echo run from Create, Gallery's
// Use in… on an image take, and a Timeline clip action (Retake a section, then Extend). One fresh user, in order.
import { expect, test, type Page } from "@playwright/test";
import { newUser, signIn, type Creds } from "./users";

test.describe.configure({ mode: "serial" });

let user: Creds;
let page: Page;
test.beforeAll(async ({ browser }) => {
  user = await newUser(browser, "studio");
  page = await browser.newPage();
});
test.afterAll(() => page?.close());

const takes = () => page.getByRole("group", { name: "Takes" });

test("sign in: a wrong password is refused, the right one opens Create", async () => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/signin/);
  await page.getByLabel("Email").fill(user.email);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("alert").filter({ hasText: /./ })).toBeVisible();
  await expect(page).toHaveURL(/\/signin/);
  await signIn(page, user);
  await expect(page.getByRole("heading", { name: "What will you make?" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Workflow: Echo. Change workflow" })).toBeVisible();
});

test("Create: an Echo run shows its take", async () => {
  await page.getByRole("combobox", { name: "Prompt" }).fill("a red fox in snow");
  await page.getByRole("button", { name: /^Generate/ }).click();
  await expect(
    page.getByRole("group", { name: "Run progress" }).getByRole("img", { name: "done" }),
  ).toBeVisible();
  await expect(takes().getByRole("button", { name: "Take, a red fox in snow" })).toBeVisible();
  await expect(page.getByRole("img", { name: "a red fox in snow" })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Session: a red fox in snow\./ })).toBeVisible();
});

test("Gallery: Use in… on an image take opens Create with it as the first frame", async () => {
  await page.goto("/gallery");
  const panel = page.getByRole("complementary", { name: "Selected take" });
  await expect(panel.getByRole("paragraph")).toHaveText("a red fox in snow");
  await expect(panel.getByRole("heading", { name: /Use in…/ })).toBeVisible();
  const offer = panel.getByRole("link", { name: "Echo image to video" });
  await expect(offer).toHaveAttribute(
    "href",
    /^\/\?s=[0-9a-f-]{36}&use=[0-9a-f-]{36}&w=echo-image-to-video$/,
  );
  await offer.click();
  await expect(
    page.getByRole("button", { name: /^Workflow: Echo image to video · 1 frame\./ }),
  ).toBeVisible();
  await expect(page.getByRole("tab", { name: "Video" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: "First frame: a red fox in snow" })).toBeVisible();
  const frames = page.getByRole("list", { name: "Frames, in order" });
  await expect(frames.getByRole("listitem")).toHaveCount(1);
  await expect(frames.getByText("frame 0 · 0.0 s")).toBeVisible();
});

test("Timeline: Retake a section replaces the clip, Extend inserts after it", async () => {
  // a video take of the Echo video workflow, onto a new sequence
  await page.goto("/");
  await page.getByRole("tab", { name: "Video" }).click();
  await page.getByRole("button", { name: /^Workflow:/ }).click();
  await page.getByRole("menuitemradio", { name: "Echo video", exact: true }).click();
  await page.getByRole("combobox", { name: "Prompt" }).fill("a fox runs");
  await page.getByRole("button", { name: /^Generate/ }).click();
  await expect(takes().getByRole("button", { name: /^Take, a fox runs, / })).toBeVisible();
  await takes().getByRole("button", { name: "Add to timeline" }).click();
  await expect(takes().getByRole("status")).toContainText("Added 1 clip to");

  await page.goto("/timeline");
  const v1 = page.getByRole("group", { name: "V1: the picture" });
  const inspector = page.getByRole("complementary", { name: "Clip inspector" });
  await v1.getByRole("button", { name: /^a fox runs, / }).click();
  for (const a of ["Retake a section", "Audio to video", "Extend"])
    await expect(inspector.getByRole("button", { name: a, exact: true })).toBeVisible();

  await inspector.getByRole("button", { name: "Retake a section" }).click();
  await expect(inspector.getByText(/^Regenerates frames \d+ – \d+ · /)).toBeVisible();
  await expect(inspector.getByRole("button", { name: "Replace" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await inspector.getByRole("combobox", { name: "Prompt" }).fill("the fox leaps");
  await inspector.getByRole("button", { name: "Generate" }).click();
  await expect(v1.getByRole("button", { name: /^Retaking a fox runs, / })).toBeVisible();
  // the shot's take, titled by its prompt, fills the same slot
  await expect(v1.getByRole("button", { name: /^the fox leaps, / })).toBeVisible();
  await expect(v1.getByRole("button")).toHaveCount(1);
  await expect(inspector.getByText(/From take the fox leaps · Echo retake/)).toBeVisible();

  // the retake's take is the clip now: Extend after it lands in a new slot after
  await inspector.getByRole("button", { name: "Extend", exact: true }).click();
  await expect(inspector.getByRole("button", { name: "Insert after" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await inspector.getByRole("combobox", { name: "Prompt" }).fill("the fox keeps running");
  await inspector.getByRole("button", { name: "Generate" }).click();
  await expect(v1.getByRole("button")).toHaveCount(2);
  await expect(v1.getByRole("button").nth(1)).toHaveAccessibleName(/^the fox keeps running, /);
  await expect(v1.getByRole("button").nth(0)).toHaveAccessibleName(/^the fox leaps, /);
});
