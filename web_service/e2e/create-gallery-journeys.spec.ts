// Create and Gallery as people use them (QA, 2026-11-24): one fresh user on the test stack's fake runner (echo pack),
// no assistant, journeys in order (each builds on the takes of the ones before). Boards: Main, Gallery, TakeActions,
// ImageToVideo, UploadOptimise, Looks. Screenshots of each journey's key moment go to E2E_SHOTS.
import { mkdirSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { newUser, signIn, type Creds } from "./users";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/create-gallery";
mkdirSync(SHOTS, { recursive: true });

let user: Creds;
let page: Page;
test.beforeAll(async ({ browser }) => {
  user = await newUser(browser, "create-gallery");
  page = await browser.newPage();
  await signIn(page, user);
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
const takes = () => page.getByRole("group", { name: "Takes" });
const prompt = () => page.getByRole("combobox", { name: "Prompt" });
const sessionButton = () => page.getByRole("button", { name: /^Session: / });
const sessions = () => page.getByRole("dialog", { name: "Sessions" });
const grid = () => page.getByRole("list", { name: "Takes" });
const panel = () => page.getByRole("complementary", { name: "Selected take" });
const nav = (name: string) =>
  page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name });

/** Types a prompt, presses Generate and waits for the take to show in the strip. */
async function generate(text: string, take: string | RegExp = `Take, ${text}`) {
  await prompt().fill(text);
  await page.getByRole("button", { name: /^Generate/ }).click();
  await expect(takes().getByRole("button", { name: take })).toBeVisible();
}

/** The gallery card of a take, by its title. */
const card = (title: string | RegExp) =>
  grid().getByRole("button", {
    // a starred card's name starts with "starred", a video's with its length
    name:
      typeof title === "string"
        ? new RegExp(`(^|\\s)${title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")} `)
        : title,
  });

async function openGallery() {
  await nav("Gallery").click();
  await expect(page.getByRole("heading", { name: "Gallery", level: 1 })).toBeVisible();
}

test("j01 my first picture: type a prompt, Generate, the take shows with its picture", async () => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "What will you make?" })).toBeVisible();
  await expect(sessionButton()).toHaveAccessibleName("Session: new. Change session");
  await generate("a red fox in snow");
  const pic = page.getByRole("img", { name: "a red fox in snow" });
  await expect(pic).toBeVisible();
  // the picture really loaded, not a broken image
  await expect.poll(() => pic.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0);
  // the stage's badge has its render time (API 1.60; docs/mock/Main.dc.html "A finished take")
  await expect(page.getByTitle("Render time (start to file)")).toHaveText(/· ⏱ \d+:\d\d/);
  // the session is named from the first prompt
  await expect(sessionButton()).toHaveAccessibleName(/^Session: a red fox in snow\./);
  await shot("j01-first-picture");
});

test("j02 a second try in the same session: both takes stay, I can go back to the first", async () => {
  await generate("a fox at dusk");
  await expect(takes().getByRole("button", { name: /^Take, / })).toHaveCount(2);
  await takes().getByRole("button", { name: "Take, a red fox in snow" }).click();
  await expect(takes().getByRole("button", { name: "Take, a red fox in snow" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.getByRole("img", { name: "a red fox in snow" })).toBeVisible();
  await shot("j02-two-takes");
});

test("j03 turn my picture into a clip: Use in… Echo image to video, Generate, a video take", async () => {
  await takes().getByRole("button", { name: "Use in…" }).click();
  await page
    .getByRole("menu", { name: "Use in" })
    .getByRole("menuitem", { name: "Echo image to video" })
    .click();
  await expect(page.getByRole("button", { name: /^Workflow: Echo image to video/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Video" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: "First frame: a red fox in snow" })).toBeVisible();
  await generate("the fox runs", /^Take, the fox runs, /);
  await shot("j03-clip-made");
});

test("j04 find yesterday's picture in the Gallery and see how it was made", async () => {
  await openGallery();
  await page.getByRole("searchbox", { name: "Search prompts" }).fill("red fox");
  await expect(card("a red fox in snow")).toBeVisible();
  await expect(card("a fox at dusk")).toHaveCount(0);
  await card("a red fox in snow").click();
  await expect(panel().getByRole("heading", { name: "Made with" })).toBeVisible();
  await expect(panel().getByRole("paragraph")).toHaveText("a red fox in snow");
  await expect(panel()).toContainText("Template");
  await expect(panel()).toContainText("Echo");
  await expect(panel()).toContainText("512 × 512");
  // its render time, low text (API 1.60; docs/mock/Gallery.dc.html)
  await expect(panel()).toContainText(/Render time\s*⏱ \d+:\d\d/);
  await shot("j04-gallery-made-with");
});

test("j04b double-click a take: its full preview opens, as the expand button does", async () => {
  await card("a red fox in snow").dblclick();
  const full = page.getByRole("dialog", { name: "a red fox in snow" });
  await expect(full).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(full).toHaveCount(0);
  // a single click still only selects
  await card("a red fox in snow").click();
  await expect(card("a red fox in snow")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("j05 reuse its settings: Create opens with the same workflow and prompt", async () => {
  await panel().getByRole("link", { name: "Reuse settings" }).click();
  await expect(page.getByRole("button", { name: /^Workflow: Echo\./ })).toBeVisible();
  await expect(prompt()).toHaveValue("a red fox in snow");
  // the stage shows the reused take, so form and picture match (owner, 2026-11-25, Q9)
  await expect(takes().getByRole("button", { name: /^Take, a red fox in snow/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await shot("j05-reused");
});

test("j06 upload my own photo and use it as the first frame of a clip", async () => {
  // the bytes of a real picture: the fox take's file, uploaded under my own file name
  await openGallery();
  await card("a red fox in snow").click();
  const src = await panel().getByRole("img", { name: "a red fox in snow" }).getAttribute("src");
  const res = await page.request.get(src!);
  expect(res.ok()).toBeTruthy();
  const body = await res.body();
  const type = res.headers()["content-type"] ?? "image/png";
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await (await chooser).setFiles({ name: "my-photo.png", mimeType: type, buffer: body });
  await expect(card(/(^|\s)my-photo/)).toBeVisible();
  await expect(card(/(^|\s)my-photo/)).toContainText("Upload");
  await card(/(^|\s)my-photo/).click();
  await expect(panel().getByRole("heading", { name: "Uploaded" })).toBeVisible();
  await shot("j06-uploaded");
  await panel().getByRole("link", { name: "Echo image to video" }).click();
  await expect(page.getByRole("button", { name: /^Workflow: Echo image to video/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /^First frame: my-photo/ })).toBeVisible();
  await shot("j06-upload-first-frame");
});

test("j07 keep the good one: star it, the Starred filter shows it, rename it", async () => {
  await openGallery();
  await card("a red fox in snow").click();
  await panel().getByRole("button", { name: "Star" }).click();
  await expect(panel().getByRole("button", { name: "Unstar" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(card("a red fox in snow").getByRole("img", { name: "starred" })).toBeVisible();
  const filters = page.getByRole("group", { name: "Filters" });
  await filters.getByRole("button", { name: "Starred" }).click();
  await expect(grid().getByRole("button")).toHaveCount(1);
  await panel().getByRole("button", { name: "Rename" }).click();
  const dlg = page.getByRole("dialog", { name: "Rename take" });
  await dlg.getByLabel("Title").fill("Fox, the keeper");
  await dlg.getByRole("button", { name: "Save" }).click();
  await expect(dlg).toBeHidden();
  await expect(card("Fox, the keeper")).toBeVisible();
  await shot("j07-starred-renamed");
  await filters.getByRole("button", { name: "All" }).click();
});

test("j08 leave Create half-written and come back: my prompt is still there", async () => {
  await nav("Create").click();
  await expect(prompt()).toBeVisible();
  await prompt().fill("a draft I have not run yet");
  await openGallery();
  await nav("Create").click();
  await expect(prompt()).toHaveValue("a draft I have not run yet");
  await shot("j08-form-kept");
});

test("j08b reload Create with a half-written prompt: it is still there", async () => {
  await page.reload();
  // kept in this browser per session (owner, 2026-11-25, Q8)
  await expect(prompt()).toHaveValue("a draft I have not run yet");
  await shot("j08b-after-reload");
});

test("j09 tidy my sessions: rename, start a new one, go back, delete one", async () => {
  await page.goto("/");
  await sessionButton().click();
  const current = (await sessionButton().getAttribute("aria-label"))!
    .replace(/^Session: /, "")
    .replace(/\. Change session$/, "");
  await sessions()
    .getByRole("button", { name: `Rename ${current}` })
    .click();
  await sessions().getByLabel("Session title").fill("Fox shoot");
  await sessions().getByLabel("Session title").press("Enter");
  await expect(sessionButton()).toHaveAccessibleName("Session: Fox shoot. Change session");
  await expect(sessions().getByRole("button", { name: "Fox shoot", exact: true })).toBeVisible();
  await shot("j09-renamed");
  // the menu stays open after a rename: a click on the title closes it, another opens it
  await sessions().getByRole("button", { name: "New session" }).click();
  // a New session is "Untitled" until its first run names it (Main board); the test stack has no naming model
  await expect(sessionButton()).toHaveAccessibleName("Session: Untitled. Change session");
  await expect(takes()).toHaveCount(0);
  await generate("a lone pine");
  // naming is off by default (plan 21 §25.5): its first prompt's first words name it (owner, 2026-11-25, Q7)
  await expect(sessionButton()).toHaveAccessibleName("Session: A lone pine. Change session");
  const pine = "A lone pine";

  await sessionButton().click();
  await sessions().getByRole("button", { name: "Fox shoot", exact: true }).click();
  await expect(sessionButton()).toHaveAccessibleName("Session: Fox shoot. Change session");
  await expect(takes().getByRole("button", { name: /^Take, the fox runs, / })).toBeVisible();
  // its half-written prompt comes back on a switch in the same tab, not only on a reload (review, 2026-11-25)
  await expect(prompt()).toHaveValue("a draft I have not run yet");

  await sessionButton().click();
  // Rename and Delete show on the row under the pointer
  await sessions().getByRole("button", { name: pine, exact: true }).hover();
  await sessions()
    .getByRole("button", { name: `Delete ${pine}` })
    .click();
  const confirm = page.getByRole("dialog", { name: "Delete this session?" });
  await expect(confirm).toContainText("Its takes stay in the gallery.");
  await shot("j09-delete-session-card");
  await confirm.getByRole("button", { name: "Delete" }).click();
  await sessionButton().click();
  await expect(sessions().getByRole("button", { name: pine, exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");
  // its take stays in the gallery
  await openGallery();
  await expect(card("a lone pine")).toBeVisible();
  await shot("j09-sessions-tidied");
});

test("j10 throw away a bad take: Delete in the Gallery, it is gone there and in Create", async () => {
  await card("a fox at dusk").click();
  await panel().getByRole("button", { name: "Delete" }).click();
  const confirm = page.getByRole("dialog", { name: "Delete this take?" });
  await confirm.getByRole("button", { name: "Delete" }).click();
  await expect(card("a fox at dusk")).toHaveCount(0);
  await shot("j10-take-deleted");
  await nav("Create").click();
  await expect(sessionButton()).toHaveAccessibleName("Session: Fox shoot. Change session");
  await expect(takes().getByRole("button", { name: /^Take, the fox runs, / })).toBeVisible();
  await expect(takes().getByRole("button", { name: "Take, a fox at dusk" })).toHaveCount(0);
});

test("j11 clean up several at once: select two, star them, delete them", async () => {
  await openGallery();
  await page.getByRole("button", { name: "Select several" }).click();
  await card("a lone pine").click();
  await card(/(^|\s)my-photo/).click();
  const sel = page.getByRole("group", { name: "Selection" });
  await expect(sel).toContainText("2 selected");
  await sel.getByRole("button", { name: "Star" }).click();
  await expect(card("a lone pine").getByRole("img", { name: "starred" })).toBeVisible();
  await expect(card(/(^|\s)my-photo/).getByRole("img", { name: "starred" })).toBeVisible();
  await sel.getByRole("button", { name: "Delete" }).click();
  const confirm = page.getByRole("dialog", { name: "Delete 2 takes?" });
  await shot("j11-delete-two-card");
  await confirm.getByRole("button", { name: "Delete" }).click();
  await expect(card("a lone pine")).toHaveCount(0);
  await expect(card(/(^|\s)my-photo/)).toHaveCount(0);
  await page.getByRole("button", { name: "Done" }).click();
  await shot("j11-cleaned");
});

test("j12 save a look from a take and pick it in Create", async () => {
  await card("Fox, the keeper").click();
  await panel().getByRole("button", { name: "Save as look" }).click();
  const dlg = page.getByRole("dialog", { name: "Save as look" });
  await dlg.getByLabel("Name").fill("Golden fox");
  await dlg.getByRole("group", { name: "Group" }).getByRole("button", { name: "Lighting" }).click();
  await dlg.getByRole("button", { name: "Save" }).click();
  await expect(dlg.getByRole("status")).toContainText("Saved Golden fox in Lighting");
  await dlg.getByRole("button", { name: "Close" }).last().click();

  await nav("Create").click();
  await page.getByRole("tab", { name: "Image" }).click();
  await page.getByRole("button", { name: /^Workflow:/ }).click();
  await page.getByRole("menuitemradio", { name: "Echo", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Workflow: Echo\./ })).toBeVisible();
  await page.getByRole("button", { name: "Lighting: Auto" }).click();
  const looks = page.getByRole("dialog", { name: "Looks" });
  await looks.getByRole("button", { name: /^Golden fox/ }).click();
  await shot("j12-look-picker");
  await looks.getByRole("button", { name: "Use 1" }).click();
  await expect(page.getByRole("button", { name: "Lighting: Golden fox" })).toBeVisible();
  await shot("j12-look-chosen");
});

test("j13 see what my clip was made from and jump to that picture", async () => {
  await openGallery();
  await card("the fox runs").click();
  await expect(panel().getByRole("heading", { name: "Lineage" })).toBeVisible();
  const parent = panel().getByRole("button", { name: "Reference 1: Fox, the keeper" });
  await expect(parent).toBeVisible();
  await expect
    .poll(() => parent.locator("img").evaluate((i: HTMLImageElement) => i.naturalWidth))
    .toBeGreaterThan(0);
  await shot("j13-lineage");
  await parent.click();
  await expect(panel().getByRole("paragraph")).toHaveText("a red fox in snow");
  await expect(card("Fox, the keeper")).toHaveAttribute("aria-pressed", "true");
});

test("j14 settings in popups: a chip's popup edits its setting, Esc closes it, All settings opens the drawer", async () => {
  // alpha.5 item 10 (docs/mock/TextToVideo.dc.html frames 1-6): the drawer is closed by default, remembered
  await page.goto("/");
  await page.evaluate(() => localStorage.removeItem("create-settings-drawer"));
  await page.reload();
  const echoVideo = async () => {
    await page.getByRole("tab", { name: "Video" }).click();
    await page.getByRole("button", { name: /^Workflow:/ }).click();
    await page.getByRole("menuitemradio", { name: "Echo video", exact: true }).click();
    await expect(page.getByRole("button", { name: /^Workflow: Echo video\./ })).toBeVisible();
  };
  await echoVideo();
  const composer = page.getByRole("form", { name: "Composer" });
  const sliders = composer.getByRole("button", { name: "All settings" });
  await expect(sliders).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("complementary", { name: "Inspector" })).toHaveCount(0);
  // the size chip: its popup, a change shows on the chip at once
  const size = composer.locator("[aria-controls=canvas-pop]");
  await size.click();
  const canvas = page.getByRole("dialog", { name: "Canvas" });
  await expect(canvas).toBeVisible();
  const before = await size.textContent();
  await canvas.getByRole("button", { name: "1:1" }).click();
  await expect(size).not.toHaveText(before!);
  await expect(size).toHaveText(/^(\d+) × \1$/);
  await shot("j14-size-popup");
  await page.keyboard.press("Escape");
  await expect(canvas).toBeHidden();
  await expect(size).toBeFocused();
  // All settings opens the drawer; a reload keeps it open; the sliders button closes it
  await size.click();
  await canvas.getByRole("button", { name: "All settings" }).click();
  await expect(page.getByRole("complementary", { name: "Inspector" })).toBeVisible();
  await expect(sliders).toHaveAttribute("aria-expanded", "true");
  // the reload keeps it open, the empty screen too (it follows the drawer: owner, 2026-11-26); the sliders button
  // closes it, and then the empty screen has no aside at all
  await page.reload();
  await echoVideo();
  expect(await page.evaluate(() => localStorage.getItem("create-settings-drawer"))).toBe("1");
  await expect(page.getByRole("complementary", { name: "Inspector" })).toBeVisible();
  await sliders.click();
  await expect(page.getByRole("complementary")).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("create-settings-drawer"))).toBeNull();
  // a phone-width window: the size and length chips still open their popups, fully on screen (frame 7b)
  const wide = page.viewportSize()!;
  await page.setViewportSize({ width: 375, height: 760 });
  try {
    for (const chip of [size, composer.locator("[aria-controls^=len-pop-]").first()]) {
      await chip.click();
      const pop = page.locator(`#${await chip.getAttribute("aria-controls")}`);
      await expect(pop).toBeVisible();
      const box = (await pop.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(375);
      await expect(page.getByRole("complementary")).toHaveCount(0);
      await page.keyboard.press("Escape");
      await expect(pop).toBeHidden();
    }
    await shot("j14-phone");
  } finally {
    await page.setViewportSize(wide);
  }
});
