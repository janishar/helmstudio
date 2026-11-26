// Every test file: Create's left form (lib/workflow.ts leftForm) is module state, so one test's form never opens the next.
import { afterEach, beforeEach } from "vitest";
import { leftForm } from "@/lib/workflow";

afterEach(() => {
  leftForm.current = null;
});

// Create's settings drawer is open by default (alpha.5 item 11); each test starts with it open, whatever the last one left.
beforeEach(() => {
  try {
    globalThis.localStorage?.setItem("create-settings-drawer", "1");
  } catch {
    /* node environment */
  }
});
