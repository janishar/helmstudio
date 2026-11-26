// Magic, phase B (plan 39 §45.12): one fresh user on the test stack opens Magic from the rail and, on the sample board
// kept in the browser, adds a shot at a point, moves it, ties a line to another shot, and undoes. Board: MagicBoard
// frames 1, 2, 4, 5. Screenshots go to E2E_SHOTS.
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

test("Magic opens from the rail on the sample board", async () => {
  await page
    .getByRole("navigation", { name: "Sections" })
    .getByRole("link", { name: "Magic" })
    .click();
  await expect(page).toHaveURL(/\/magic$/);
  for (const lane of ["Storyboard", "Shots", "Mara's lines", "Music", "Sound", "Lane 7"])
    await expect(page.getByRole("group", { name: `Lane ${lane}` })).toBeVisible();
  await expect(page.getByRole("button", { name: /Video block Shot 3/ })).toContainText(
    "Out of date",
  );
  await page.screenshot({ path: `${SHOTS}/1-board.png` });
});

test("add a shot at a point on the empty lane", async () => {
  const lane = page.getByRole("group", { name: "Lane Lane 7" });
  await lane.getByText("Empty · click to add").click({ position: { x: 4, y: 10 } });
  const dlg = page.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
  await expect(dlg.getByRole("button", { name: /Music/ })).toBeDisabled();
  await dlg.getByRole("button", { name: "4 s" }).click();
  await dlg.getByRole("button", { name: "Add" }).click();
  await expect(lane.getByRole("button", { name: "Video block Shot 4, 4 s" })).toContainText(
    "Draft",
  );
  await expect(status()).toHaveText("Added Shot 4");
});

test("move it along the lane: it snaps and lands later", async () => {
  const shot = page.getByRole("button", { name: "Video block Shot 4, 4 s" });
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
  // and the board is still there after a reload (kept in this browser)
  await page.reload();
  await expect(page.getByRole("button", { name: "Video block Shot 4, 4 s" })).toBeVisible();
});
