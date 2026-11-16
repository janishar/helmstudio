import { describe, expect, it } from "vitest";
import page from "../../contracts/fixtures/http/ChatPage.valid.json";
import batchFixture from "../../contracts/fixtures/http/Batch.valid.5.json";
import serverRemote from "../../contracts/fixtures/pipelines/valid.server-remote.json";
import type { S } from "@/lib/api";
import {
  addMessage,
  callLine,
  chatClosed,
  directorState,
  rowMarks,
  type ChatView,
} from "@/lib/chat";
import { egressLines, fieldLabel, remoteLine, reservedLines } from "@/lib/egress";

const view = page as unknown as ChatView;
const batch = batchFixture as unknown as S<"Batch">;

describe("director chat", () => {
  it("adds a message once, oldest first", () => {
    const m = {
      ...view.messages[0],
      id: "00000000-0000-4000-8000-000000000001",
      at: "2026-11-16T10:19:00Z",
    };
    const v = addMessage(addMessage(view, m), m);
    expect(v.messages.map((x) => x.id)[0]).toBe(m.id);
    expect(v.messages.length).toBe(view.messages.length + 1);
  });
  it("marks the rows the director added or changed, not the refused one", () => {
    expect(rowMarks(view.messages)).toEqual({
      3: { how: "changed", wake: 3 },
      15: { how: "added", wake: 3 },
    });
  });
  it("says each call in words", () => {
    const calls = view.messages[1].tool_calls;
    expect(calls.map(callLine)).toEqual(["changed row 3", "added row 15"]);
    expect(
      callLine({ ...calls[1], result: null, error: "row: inputs.script: a value is required" }),
    ).toBe("added a row: refused · row: inputs.script: a value is required");
    expect(
      callLine({
        id: "x",
        tool: "gallery_search",
        args: {},
        result: { takes: [{}, {}], more: false } as unknown as Record<string, never>,
        error: null,
      }),
    ).toBe("searched the gallery · 2 found");
  });
  it("closes when the batch ended or the director is off", () => {
    const d = view.director;
    expect(directorState(d)).toMatch(/^Idle/);
    expect(directorState({ ...d, state: "off", wakes: 100 })).toBe("Off · used its 100 wakes");
    expect(chatClosed({ ...batch, status: "running" }, d)).toBe("");
    expect(chatClosed({ ...batch, status: "done" }, d)).toBe("The batch has ended.");
    expect(chatClosed({ ...batch, status: "running" }, { ...d, state: "off" })).toBe(
      "The director is off.",
    );
  });
});

describe("what leaves the machine", () => {
  const egress = serverRemote.egress as S<"PipelineEgress">[];
  it("names where each node's calls go and exactly what they send", () => {
    const lines = egressLines(egress, 10, (s) => (s === "judge.reasons" ? "Judge · reasons" : s));
    expect(lines[0]).toEqual({
      node: "check",
      where: "Remote · fakeai · fake-large",
      remote: true,
      sends: "its question, its routes, Judge · reasons; no thumbnails",
      load: "up to 6 calls, 53,952 tokens a row; 60 calls, 539,520 tokens for 10 rows",
    });
    expect(lines[1].where).toBe("Local server · ollama · qwen2.5:7b");
    expect(lines[1].remote).toBe(false);
  });
  it("puts the remote total beside the caps", () => {
    expect(remoteLine(egress, 10, serverRemote.pipeline.remote as S<"PipelineRemote">)).toBe(
      "Remote, worst case: 60 calls, 539,520 tokens; the batch stops at 100 calls, 200,000 tokens",
    );
    expect(
      remoteLine(
        egress.filter((e) => e.where !== "remote"),
        10,
      ),
    ).toBe("");
  });
  it("counts the director's chat once per batch", () => {
    const d: S<"PipelineEgress"> = {
      ...egress[0],
      node: "director",
      fields: ["instruction", "tool:gallery.search", "chat"],
      per_row: { calls: 12, tokens: 16000 },
      chat: { calls: 1200, tokens: 1600000 },
    };
    const [l] = egressLines([d], 2);
    expect(l.sends).toBe("its instruction, gallery search results, the chat; no thumbnails");
    expect(l.load).toBe(
      "up to 12 calls, 16,000 tokens a row; 24 calls, 32,000 tokens for 2 rows · chat: up to 1,200 calls, 1,600,000 tokens",
    );
    expect(remoteLine([d], 2)).toMatch(/^Remote, worst case: 1,224 calls, 1,632,000 tokens/);
  });
  it("labels fields and reserved memory", () => {
    expect(fieldLabel("input:url")).toBe("field url");
    expect(reservedLines(serverRemote.reserved)).toEqual(["ollama keeps 6.5 GB on mac"]);
  });
});
