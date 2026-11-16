import { describe, expect, it } from "vitest";
import type { S } from "@/lib/api";
import { keyCounts, keyOrder, lastUsed } from "@/lib/apikeys";

const key = (id: string, revoked: string | null): S<"ApiKey"> => ({
  id,
  client_id: id,
  client: id,
  prefix: "helm_abcdefg",
  scopes: [],
  created_at: "2026-11-01T00:00:00Z",
  revoked_at: revoked,
  last_used_at: null,
});

describe("API keys", () => {
  const keys = [key("a", "2026-11-02T00:00:00Z"), key("b", null), key("c", null)];
  it("counts active and revoked keys and lists the active first", () => {
    expect(keyCounts(keys)).toEqual({ active: 2, revoked: 1 });
    expect(keyOrder(keys).map((k) => k.id)).toEqual(["b", "c", "a"]);
  });
  it("words the last use", () => {
    const now = Date.parse("2026-11-16T10:00:00Z");
    const when = (iso: string) => `at ${iso}`;
    expect(lastUsed(null, now, when)).toBe("never");
    expect(lastUsed("2026-11-16T09:59:30Z", now, when)).toBe("now");
    expect(lastUsed("2026-11-15T09:00:00Z", now, when)).toBe("at 2026-11-15T09:00:00Z");
  });
});
