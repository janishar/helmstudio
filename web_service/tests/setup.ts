// Every test file: Create's left form (lib/workflow.ts leftForm) is module state, so one test's form never opens the next.
import { afterEach } from "vitest";
import { leftForm } from "@/lib/workflow";

afterEach(() => {
  leftForm.current = null;
});
