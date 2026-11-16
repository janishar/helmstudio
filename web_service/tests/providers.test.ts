import { describe, expect, it } from "vitest";
import server from "../../contracts/fixtures/http/Provider.valid.1.json";
import remote from "../../contracts/fixtures/http/Provider.valid.2.json";
import testOk from "../../contracts/fixtures/http/ProviderTest.valid.1.json";
import testBad from "../../contracts/fixtures/http/ProviderTest.valid.2.json";
import type { S } from "@/lib/api";
import { blankForm, formOf, putOf, testLine, toggled } from "@/lib/providers";

const P = (x: unknown) => x as S<"Provider">;

describe("provider form", () => {
  it("round-trips a local server and a remote provider into a PUT body", () => {
    expect(putOf(formOf(P(server)))).toEqual({
      body: {
        kind: "server",
        title: "Ollama on the Mac",
        base_url: "http://host.docker.internal:11434/v1",
        models: ["qwen2.5:7b", "llama3.2:3b"],
        default_model: "qwen2.5:7b",
        enabled: true,
        timeout_s: 120,
        host: "mac",
        memory_gb: 6.5,
      },
    });
    const r = putOf(formOf(P(remote)));
    expect(r).toMatchObject({
      body: { kind: "remote", key_secret: "provider-fakeai", max_in_flight: 2, timeout_s: 60 },
    });
    expect("body" in r && "host" in r.body).toBe(false);
  });
  it("refuses what the API would", () => {
    const f = { ...blankForm(), name: "ollama", base_url: "http://10.0.0.5:11434/v1", models: "a" };
    expect(putOf({ ...f, name: "Ollama" })).toEqual({ error: expect.stringMatching(/^Name/) });
    expect(putOf({ ...f, base_url: "http://u:p@10.0.0.5/v1" })).toEqual({
      error: expect.stringMatching(/^Base URL/),
    });
    expect(putOf({ ...f, memory_gb: "6", host: "" })).toEqual({
      error: expect.stringMatching(/^GPU host/),
    });
    expect(putOf({ ...f, host: "mac", memory_gb: "0.1" })).toEqual({
      error: expect.stringMatching(/^Memory/),
    });
    const rf = { ...f, kind: "remote" as const, key_secret: "provider-x" };
    expect(putOf(rf)).toEqual({ error: "Base URL: a remote provider is https." });
    expect(putOf({ ...rf, base_url: "https://x.test/v1", key_secret: "sk-THE KEY" })).toEqual({
      error: expect.stringMatching(/^Key file/),
    });
  });
  it("toggles enabled, keeping the rest", () => {
    expect(toggled(P(server))).toMatchObject({ enabled: false, host: "mac", memory_gb: 6.5 });
  });
  it("says a test result in words", () => {
    expect(testLine(testOk as S<"ProviderTest">)).toBe("answered · 0.8 s");
    expect(testLine(testBad as S<"ProviderTest">)).toBe(
      "network · fakeai: network error: connection refused",
    );
  });
});
