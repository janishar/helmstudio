// The Export key's second line (planLine): trimmed clips read "trimmed", any other reason "other" (owner, Q11).
import { expect, it } from "vitest";
import { planLine } from "@/components/timeline/dialogs";
import type { S } from "@/lib/api";

const plan = (codes: string[]) =>
  ({
    blocked: [],
    stream_copy: false,
    reasons: codes.map((code) => ({ code, clip: null, message: "" })),
  }) as unknown as S<"ExportPlan">;

it("names trims as trimmed and keeps the rest as other", () => {
  expect(planLine(plan(["trimmed", "trimmed", "trimmed"]))).toBe("re-encode · 3 trimmed");
  expect(planLine(plan(["image", "trimmed", "codec", "size"]))).toBe(
    "re-encode · 1 still, 1 trimmed, 2 other",
  );
});
