// Every test file: Create's left form (lib/workflow.ts leftForm) is module state, so one test's form never opens the next.
import { afterEach, beforeEach } from "vitest";
import { leftForm } from "@/lib/workflow";

afterEach(() => {
  leftForm.current = null;
});

// Create's settings drawer is closed by default (alpha.5 item 10); the Create tests written before it read the
// Inspector, so they start with it open. create-popups.test.tsx clears this to test the default.
beforeEach(() => {
  try {
    globalThis.localStorage?.setItem("create-settings-drawer", "1");
  } catch {
    /* node environment */
  }
});
