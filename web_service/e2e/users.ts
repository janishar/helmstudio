// Fresh users for the e2e specs: the test stack's admin (deploy/compose/fake.yml, the root Makefile's TEST_ADMIN)
// makes each one through Admin → Add a user, as a person would.
import { randomUUID } from "node:crypto";
import { expect, type Browser, type Page } from "@playwright/test";

export const ADMIN = {
  email: process.env.ADMIN_EMAIL ?? "admin@helm.test",
  password: process.env.HELM_TEST_ADMIN_PASSWORD ?? "helm test admin pass 1",
};
export type Creds = { email: string; password: string };

/** Signs in on /signin and waits to leave it. */
export async function signIn(page: Page, c: Creds) {
  await page.goto("/signin");
  await page.getByLabel("Email").fill(c.email);
  await page.getByLabel("Password").fill(c.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).not.toHaveURL(/\/signin/);
}

/** A new account with the user role, made by the admin in its own browser context. */
export async function newUser(browser: Browser, name: string): Promise<Creds> {
  const c = {
    email: `e2e-${name}-${randomUUID().slice(0, 8)}@example.com`,
    password: `E2e-${randomUUID()}`,
  };
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await signIn(page, ADMIN);
  await page.goto("/admin");
  const add = page.getByRole("complementary", { name: "Add a user" });
  await add.getByLabel("Name").fill(`E2E ${name}`);
  await add.getByLabel("Email").fill(c.email);
  await add.getByLabel("Temporary password").fill(c.password);
  await add.getByRole("button", { name: "Create user" }).click();
  await expect(add.getByRole("status")).toContainText(`Added ${c.email}`);
  await ctx.close();
  return c;
}
