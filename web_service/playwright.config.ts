// End-to-end tests (make e2e, not part of make test): the web app on the TEST stack only (helmstudio-test, Kong on
// 8390, fake runners), with fresh users the test admin makes. Never the dev stack (Kong 8300).
import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:8390";
if (/:8300\b/.test(baseURL)) throw new Error("e2e never runs on the dev stack (Kong 8300)");

export default defineConfig({
  testDir: "e2e",
  // one user per file; the files share the fake runner, so one at a time
  workers: 1,
  timeout: 90_000,
  expect: { timeout: 20_000 },
  reporter: [["list"]],
  use: { baseURL, trace: "retain-on-failure", ...devices["Desktop Chrome"] },
});
