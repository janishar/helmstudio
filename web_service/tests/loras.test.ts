import { describe, expect, it } from "vitest";
import nodesFixture from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import workflowsFixture from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import { ApiError, type S } from "@/lib/api";
import {
  addRows,
  checkLoraFile,
  clampStrength,
  emptyStack,
  fitReason,
  lorasNote,
  madeWithLine,
  modeOf,
  newRow,
  rowIds,
  rowIssues,
  strengthOf,
  tileLine,
  uploadLora,
  usable,
  type LoraIO,
  type UploadState,
} from "@/lib/loras";
import { buildInputs, fields, readValue, sections } from "@/lib/workflow";

type Lora = S<"Lora">;
const stack = (nodesFixture.nodes as S<"NodeSchema">[]).find(
  (n) => n.kind === "qwen-image/lora-stack",
)!;
const rows = stack.params[0];
const SHARED = "3f0a6c1e9b2d4a7c8e5f1b3d6a9c2e4f7b0d3a6c9e2f5b8d1a4c7e0f3b6d9a2c";
const MINE = "2c4e6a8b-1d3f-4a5b-9c7d-0e2f4a6b8c10";

const lora = (o: Partial<Lora>): Lora => ({
  id: MINE,
  source: "mine",
  name: "ink",
  family: "qwen-image",
  rank: 32,
  size_bytes: 144e6,
  key_style: "peft",
  licence: null,
  sha256: "a".repeat(64),
  hosts: [],
  state: "ready",
  reason: null,
  thumb_url: null,
  created_at: "2026-11-15T00:00:00Z",
  ...o,
});

describe("the rows of a LoRA stack", () => {
  it("makes a row with the schema's defaults and adds each LoRA once, up to the max", () => {
    expect(newRow(rows, SHARED)).toEqual({
      lora: { lora_id: SHARED },
      strength: 1,
      mode: "adapter",
    });
    const two = addRows(rows, [], [SHARED, MINE, SHARED]);
    expect(rowIds(rows, two)).toEqual([SHARED, MINE]);
    expect(addRows(rows, two, [MINE])).toHaveLength(2);
    const many = Array.from(
      { length: 12 },
      (_, i) => `0000000${i.toString(16)}-0000-0000-0000-000000000000`,
    );
    expect(addRows(rows, [], many)).toHaveLength(8);
  });

  it("finds the strength and mode fields from the item schema", () => {
    expect(strengthOf(rows)?.[0]).toBe("strength");
    expect(modeOf(rows)?.[1].enum).toEqual(["adapter", "bake"]);
  });

  it("clamps a strength to the schema's range and step", () => {
    const s = strengthOf(rows)![1];
    expect(clampStrength(0.83, s)).toBe(0.85);
    expect(clampStrength(9, s)).toBe(4);
    expect(clampStrength(-7.3, s)).toBe(-4);
    expect(clampStrength(Number.NaN, s)).toBe(1);
    expect(clampStrength(0.1 + 0.2, s)).toBe(0.3);
  });

  it("requires a LoRA in every row, as studio does", () => {
    expect(readValue(rows, [{ strength: 1, mode: "adapter" }])).toEqual({
      error: "item 1: a LoRA is required",
    });
    expect(readValue(rows, [newRow(rows, SHARED)])).toEqual({ value: [newRow(rows, SHARED)] });
  });

  it("puts the stack in its own inspector section, not with the references", () => {
    const w = workflowsFixture.workflows[0] as unknown as S<"Workflow">;
    const fs = fields({
      nodes: w.nodes,
      node_schemas: Object.fromEntries(
        (nodesFixture.nodes as S<"NodeSchema">[]).map((n) => [n.kind, n]),
      ),
    });
    const s = sections(fs);
    expect(s.loras.map((f) => f.key)).toEqual(["lora.rows"]);
    expect(s.loras[0].family).toBe("qwen-image");
    expect(s.refs.map((f) => f.key)).not.toContain("lora.rows");
    // an empty stack sends nothing: the template runs as before
    expect(buildInputs(fs, {}).inputs).toEqual({});
  });

  it("says what is wrong with a row: no LoRA, twice, not found, does not fit", () => {
    const lib = [
      lora({}),
      lora({ id: SHARED, source: "shared", family: "flux" }),
      lora({ id: "b".repeat(64), source: "shared", family: "unknown" }),
    ];
    const r = [
      newRow(rows, MINE),
      { strength: 1, mode: "adapter" },
      newRow(rows, MINE),
      newRow(rows, "c".repeat(64)),
      newRow(rows, SHARED),
      newRow(rows, "b".repeat(64)),
    ];
    expect(rowIssues(rows, r, lib, "qwen-image")).toEqual([
      null,
      "pick a LoRA",
      "already in row 1",
      "not found: deleted, or on no live host",
      "does not fit qwen-image (it is for flux)",
      "does not fit qwen-image (its model is unknown)",
    ]);
    // the library not known yet: only what the rows show
    expect(rowIssues(rows, r, null, "qwen-image")[3]).toBeNull();
    expect(usable(lib)).toEqual({
      [MINE]: "qwen-image",
      [SHARED]: "flux",
      ["b".repeat(64)]: "unknown",
    });
    expect(usable([lora({ state: "pending" })])).toEqual({});
  });
});

describe("the picker's family fit", () => {
  it("greys what does not fit, with the reason", () => {
    expect(fitReason(lora({}), "qwen-image")).toBeNull();
    expect(fitReason(lora({ family: "ltx-2.5" }), "qwen-image")).toBe("for ltx-2.5");
    expect(fitReason(lora({ family: "unknown" }), "qwen-image")).toBe("model unknown");
    expect(fitReason(lora({ state: "pending" }), "qwen-image")).toBe("checking…");
    expect(fitReason(lora({ state: "rejected", reason: "not a safetensors file" }), null)).toBe(
      "rejected: not a safetensors file",
    );
    expect(tileLine(lora({}), "qwen-image")).toBe("rank 32 · 144 MB · mine");
    expect(tileLine(lora({ size_bytes: 1.25e9, rank: null }), "qwen-image")).toBe("1.3 GB · mine");
  });
});

describe("a take's LoRAs", () => {
  const made: S<"MadeWithLora">[] = [
    {
      node: "lora",
      id: MINE,
      source: "mine",
      name: "ink",
      strength: 0.8,
      mode: "adapter",
      sha256: "a".repeat(64),
    },
    {
      node: "lora",
      id: SHARED,
      source: "shared",
      name: "film",
      strength: 1,
      mode: "bake",
      sha256: "b".repeat(64),
    },
  ];
  it("lists them, marking an own one deleted since", () => {
    expect(madeWithLine(made[0], [])).toBe("ink · 0.80 · adapter (deleted)");
    expect(madeWithLine(made[0], null)).toBe("ink · 0.80 · adapter");
    expect(madeWithLine(made[1], [])).toBe("film · 1.00 · bake");
  });
  it("says on reuse which were deleted or are missing", () => {
    expect(lorasNote([{ id: MINE, status: "current" }], made)).toBe("");
    expect(
      lorasNote(
        [
          { id: MINE, status: "deleted" },
          { id: SHARED, status: "missing" },
        ],
        made,
      ),
    ).toBe(
      "Since this take: LoRA ink was deleted; LoRA film is on no live host now. Remove it to run.",
    );
  });
});

describe("the progress strip and an empty stack", () => {
  it("treats an empty or bypassed stack as no work", () => {
    const w = workflowsFixture.workflows[0] as unknown as S<"Workflow">;
    const node_schemas = { [stack.kind]: stack };
    expect(emptyStack({ nodes: w.nodes, node_schemas }, "lora")).toBe(true);
    const full = { ...w.nodes, lora: { ...w.nodes.lora, params: { rows: [newRow(rows, MINE)] } } };
    expect(emptyStack({ nodes: full, node_schemas }, "lora")).toBe(false);
    expect(emptyStack({ nodes: w.nodes, node_schemas }, "sample")).toBe(false);
  });
});

describe("uploading an own LoRA", () => {
  const file = (name: string, size = 10) => new File([new Uint8Array(size)], name);
  const io = (over: Partial<LoraIO> = {}): LoraIO => ({
    create: async (b) => ({
      lora: lora({ state: "pending", name: b.filename.replace(/\.safetensors$/i, "") }),
      upload_url: "/files/u/o/loras/x.safetensors?sig=1",
      content_type: "application/octet-stream",
      bytes: b.bytes,
      expires_at: "2026-11-15T00:15:00Z",
    }),
    put: async (_u, _f, _c, on) => {
      on(0.5);
      on(1);
    },
    complete: async () => lora({}),
    ...over,
  });
  const run = async (f: File, x: LoraIO) => {
    const seen: UploadState[] = [];
    const out = await uploadLora(f, (s) => seen.push(s), x);
    return {
      out,
      phases: seen.map((s) =>
        s.phase === "sending"
          ? `sending ${s.progress}`
          : s.phase === "failed"
            ? `failed: ${s.error}`
            : s.phase,
      ),
    };
  };

  it("checks the file here first", () => {
    expect(checkLoraFile({ name: "a.ckpt", size: 1 })).toBe(
      "only .safetensors files can be uploaded",
    );
    expect(checkLoraFile({ name: "a.SafeTensors", size: 2147483649 })).toBe(
      "file is larger than 2 GB",
    );
    expect(checkLoraFile({ name: "a.safetensors", size: 0 })).toBe("file is empty");
    expect(checkLoraFile({ name: "a.safetensors", size: 2147483648 })).toBeNull();
  });

  it("goes sending → checking → done", async () => {
    const r = await run(file("ink.safetensors"), io());
    expect(r.phases).toEqual(["sending 0", "sending 0.5", "sending 1", "checking", "done"]);
    expect(r.out?.state).toBe("ready");
  });

  it("fails at once on a bad file, and with the server's reason on a rejection or a full library", async () => {
    let calls = 0;
    const r1 = await run(file("ink.pt"), io({ create: async () => (calls++, Promise.reject()) }));
    expect(r1.phases).toEqual(["failed: only .safetensors files can be uploaded"]);
    expect(calls).toBe(0);
    const r2 = await run(
      file("ink.safetensors"),
      io({
        complete: () =>
          Promise.reject(new ApiError(422, "no LoRA keys found (is it a full model?)")),
      }),
    );
    expect(r2.out).toBeNull();
    expect(r2.phases.at(-1)).toBe("failed: no LoRA keys found (is it a full model?)");
    const r3 = await run(
      file("ink.safetensors"),
      io({
        create: () => Promise.reject(new ApiError(409, "You have 50 LoRAs; delete some first.")),
      }),
    );
    expect(r3.phases).toEqual(["sending 0", "failed: You have 50 LoRAs; delete some first."]);
    const r4 = await run(
      file("ink.safetensors"),
      io({ create: async (b) => ({ ...(await io().create(b)), upload_url: "javascript:x" }) }),
    );
    expect(r4.phases.at(-1)).toBe("failed: The server gave an unusable upload link.");
  });
});
