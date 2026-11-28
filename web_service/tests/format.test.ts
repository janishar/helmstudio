import { describe, expect, it } from "vitest";
import { clock, plural } from "@/lib/format";

describe("format", () => {
  it("clock rounds to the second, everywhere", () => {
    expect(clock(119.6)).toBe("2:00");
    expect(clock(119.4)).toBe("1:59");
    expect(clock(-3)).toBe("0:00");
  });
  it("plural", () => {
    expect(plural(1, "take")).toBe("1 take");
    expect(plural(2, "take")).toBe("2 takes");
  });
});
