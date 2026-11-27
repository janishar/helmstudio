// Magic, phases C and D (plan 39 §45.12): one fresh user on the test stack opens Magic from the rail on a new board
// kept in studio, makes the sample board, adds a shot at a point with its standard request and generates it on the
// fake runner (echo) until it is done with a take, opens it (run history, takes), moves it, ties a line to another
// shot, undoes (studio's undo), saves a snapshot, and finds it all again after a reload; Add's From Gallery opens the
// Gallery (frame 2a). Board: MagicBoard frames 1, 2, 3, 4, 5, 11.
// Screenshots go to E2E_SHOTS.
import { mkdirSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { newUser, signIn } from "./users";

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SHOTS ?? "test-results/magic";
mkdirSync(SHOTS, { recursive: true });

let page: Page;
const status = () => page.locator("main").getByRole("status").last();

test.beforeAll(async ({ browser }) => {
  const user = await newUser(browser, "magic");
  page = await browser.newPage({ viewport: { width: 1500, height: 1000 } });
  await signIn(page, user);
});
test.afterAll(async () => {
  await page?.close();
});

test("Magic opens from the rail on a new board; the sample board is one pick away", async () => {
  await page
    .getByRole("navigation", { name: "Sections" })
    .getByRole("link", { name: "Magic" })
    .click();
  await expect(page).toHaveURL(/\/magic$/);
  await expect(page.getByRole("heading", { name: "A new board" })).toBeVisible();
  await page.getByRole("button", { name: "Untitled board" }).click();
  await page.getByRole("menuitem", { name: "Sample board" }).click();
  for (const lane of ["Storyboard", "Shots", "Mara's lines", "Music", "Sound", "Lane 7"])
    await expect(page.getByRole("group", { name: `Lane ${lane}` })).toBeVisible();
  // kept in studio: no takes yet, so every block is a draft; the board can run
  await expect(page.getByRole("button", { name: /Video block Shot 3/ })).toContainText("Draft");
  await expect(page.getByRole("button", { name: "Run board" })).toBeEnabled();
  await page.screenshot({ path: `${SHOTS}/1-board.png` });
});

test("Add's From Gallery opens the Gallery for the point", async () => {
  const lane = page.getByRole("group", { name: "Lane Lane 7" });
  await lane.getByText("Empty · click to add").click({ position: { x: 4, y: 10 } });
  const add = page.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
  await add.getByRole("button", { name: "From Gallery" }).click();
  await expect(add).toBeHidden();
  const picker = page.getByRole("dialog", { name: "From Gallery · Lane 7 at 0:00" });
  await expect(picker).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(picker).toBeHidden();
});

test("add a shot at a point on the empty lane and generate it", async () => {
  const lane = page.getByRole("group", { name: "Lane Lane 7" });
  await lane.getByText("Empty · click to add").click({ position: { x: 4, y: 10 } });
  let dlg = page.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
  await expect(dlg.getByRole("button", { name: /Music/ })).toBeEnabled(); // phase H: Echo music serves it
  await dlg.getByRole("button", { name: "5 s" }).click();
  await dlg.getByRole("button", { name: "Next" }).click();
  // step 2: the standard request; Echo video can't take frames or a seed, each greyed with why
  dlg = page.getByRole("dialog", { name: "Video · Shot 4" });
  await dlg.getByRole("combobox", { name: "Model" }).selectOption({ label: "Echo video" });
  await expect(dlg.getByText("Echo video can't take a first frame")).toBeVisible();
  await dlg.getByRole("textbox", { name: "Prompt" }).fill("a fox in the rain");
  await expect(dlg.getByText(/≈ \d+ s/)).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/2-request.png` });
  await dlg.getByRole("button", { name: "Generate" }).click();
  const shot = lane.getByRole("button", { name: "Video block Shot 4, 5 s" });
  await expect(status()).toHaveText("Running 1 block");
  // the fake runner makes its take; the board's session events bring it to the block
  await expect(shot).toContainText("Done", { timeout: 60_000 });
  // opened: its run history and its take
  await shot.dblclick();
  const opened = page.getByRole("dialog", { name: "Video · Shot 4" });
  await expect(opened.getByRole("group", { name: "Run 1" })).toBeVisible();
  await expect(opened.getByRole("listitem", { name: /^Take 1/ })).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/3-opened.png` });
  await page.keyboard.press("Escape");
  await expect(opened).toBeHidden();
});

test("move it along the lane: it snaps and lands later", async () => {
  const shot = page.getByRole("button", { name: "Video block Shot 4, 5 s" });
  const before = await shot.evaluate((el) => (el as HTMLElement).style.left);
  const box = (await shot.boundingBox())!;
  await page.mouse.move(box.x + 30, box.y + 20);
  await page.mouse.down();
  await page.mouse.move(box.x + 80, box.y + 20, { steps: 5 });
  await page.mouse.move(box.x + 134, box.y + 20, { steps: 5 });
  await expect(page.getByRole("status").filter({ hasText: /Shot 4 · 0:/ })).toBeVisible();
  await page.mouse.up();
  await expect(status()).toHaveText("Moved Shot 4");
  expect(await shot.evaluate((el) => (el as HTMLElement).style.left)).not.toBe(before);
});

test("tie Line 2 to Shot 3, then undo back to Shot 2", async () => {
  const line = page.getByRole("button", { name: /Voice block Line 2/ });
  await expect(line.getByRole("button", { name: "Tie to Shot 2" })).toBeVisible();
  await line.click({ button: "right", position: { x: 40, y: 12 } });
  await page.getByRole("menuitem", { name: "Unlink" }).click();
  await expect(status()).toHaveText("Unlinked Line 2");
  await line.click({ button: "right", position: { x: 40, y: 12 } });
  await page.getByRole("menuitem", { name: "Tie to" }).click();
  await page.getByRole("menuitemcheckbox", { name: "Shot 3 · puddle" }).click();
  await expect(status()).toHaveText("Tied Line 2 to Shot 3 · puddle");
  await expect(line.getByRole("button", { name: "Tie to Shot 3" })).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/2-tied.png` });
  // one Undo takes the tie back, the next the unlink
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(line.getByRole("button", { name: /Tie to/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(line.getByRole("button", { name: "Tie to Shot 2" })).toBeVisible();
  // a snapshot, then the board and it are still there after a reload (kept in studio)
  await page.getByRole("button", { name: /^Snapshots/ }).click();
  await page.getByRole("button", { name: "Save snapshot" }).click();
  await expect(status()).toHaveText("Saved Snapshot 1");
  await page.reload();
  await expect(page.getByRole("button", { name: "Video block Shot 4, 5 s" })).toBeVisible();
  await expect(page.getByRole("button", { name: "The Last Tide" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Snapshots · 1" })).toBeVisible();
});
