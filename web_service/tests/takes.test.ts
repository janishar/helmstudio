import { describe, expect, it, vi } from "vitest";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import type { S } from "@/lib/api";
import {
  appendPage,
  checkFile,
  fits,
  sizeSeed,
  stripItems,
  subLine,
  takeQuery,
  templateLine,
  uploadFile,
  type UploadIO,
} from "@/lib/takes";
import { buildInputs, fields, rawsFrom } from "@/lib/workflow";

const madeWith: S<"MadeWith"> = {
  workflow_id: "qwen-image",
  workflow_title: "Qwen Image",
  inputs: {},
  prompt: "a fox at dusk",
  seed: 43,
  model: { id: "qwen-image", variant: "base", precision: "int8" },
};

const take = (over: Partial<S<"Take">> = {}): S<"Take"> => ({
  id: "t1",
  kind: "image",
  source: "create",
  title: "a fox at dusk",
  url: "/files/a?exp=1&sig=x",
  thumb_url: "/files/a?exp=1&sig=x",
  content_type: "image/png",
  width: 2752,
  height: 1536,
  bytes: 10,
  created_at: "2026-11-14T10:00:00Z",
  starred: false,
  session_id: "s1",
  run_id: "r1",
  made_with: madeWith,
  parents: [],
  ...over,
});

const run = (over: Partial<S<"Run">> = {}): S<"Run"> => ({
  id: "r1",
  session_id: "s1",
  workflow_id: "qwen-image",
  inputs: {},
  status: "succeeded",
  progress: null,
  error: null,
  outputs: [],
  host: "mac",
  lane: "interactive",
  attempt: 1,
  created_at: "2026-11-14T09:59:00Z",
  started_at: null,
  finished_at: null,
  ...over,
});

describe("takeQuery", () => {
  it("sends only what is set, the search trimmed", () => {
    expect(takeQuery({}, "  ")).toEqual({});
    expect(takeQuery({ kind: "image", starred: true, source: "upload" }, " fox ", "c2")).toEqual({
      kind: "image",
      starred: true,
      source: "upload",
      q: "fox",
      cursor: "c2",
    });
    expect(takeQuery({ starred: false }, "", null)).toEqual({});
    expect(takeQuery({}, "x".repeat(300)).q).toHaveLength(200);
    expect(takeQuery({ session_id: "s1", batch_id: "b1" }, "")).toEqual({
      session_id: "s1",
      batch_id: "b1",
    });
  });
});

describe("fits", () => {
  it("ANDs the filters", () => {
    const up = take({ source: "upload", made_with: null });
    expect(fits(up, {})).toBe(true);
    expect(fits(up, { kind: "image", source: "upload" })).toBe(true);
    expect(fits(up, { source: "create" })).toBe(false);
    expect(fits(up, { starred: true })).toBe(false);
    expect(fits(up, { kind: "video" })).toBe(false);
    // another project, or a batch it is not of
    expect(fits(up, { session_id: "s2" })).toBe(false);
    expect(fits(up, { batch_id: "b1" })).toBe(false);
  });
});

describe("appendPage", () => {
  it("does not repeat a take", () => {
    const a = take({ id: "a" });
    const b = take({ id: "b" });
    expect(appendPage([a], [a, b]).map((t) => t.id)).toEqual(["a", "b"]);
  });
});

describe("labels", () => {
  it("names the source and the template", () => {
    expect(subLine(take())).toBe("Qwen Image · Create");
    expect(subLine(take({ source: "upload", made_with: null }))).toBe("Upload");
    expect(templateLine(madeWith)).toBe("Qwen Image · Base · int8");
    expect(templateLine({ ...madeWith, workflow_title: "Echo", model: null })).toBe("Echo");
  });
  it("shows size and seed when known", () => {
    expect(sizeSeed(take())).toBe("2752 × 1536 · 43");
    expect(sizeSeed(take({ made_with: { ...madeWith, seed: 0 } }))).toBe("2752 × 1536 · 0");
    expect(sizeSeed(take({ made_with: null }))).toBe("2752 × 1536");
    expect(sizeSeed(take({ width: null, height: null, made_with: null }))).toBe("");
  });
});

describe("stripItems", () => {
  const output: S<"RunOutput"> = {
    index: 0,
    key: "k",
    kind: "image",
    url: "/files/k",
    width: 1,
    height: 1,
  };
  it("shows takes and the runs without takes, newest first", () => {
    const items = stripItems(
      [
        run({ id: "done" }),
        run({ id: "busy", status: "running", created_at: "2026-11-14T10:01:00Z" }),
        run({ id: "bad", status: "failed", created_at: "2026-11-14T09:00:00Z" }),
        // finished, its take not loaded yet: shown by itself; finished with every take deleted: gone
        run({ id: "fresh", outputs: [output], created_at: "2026-11-14T08:00:00Z" }),
        run({ id: "emptied", created_at: "2026-11-14T07:00:00Z" }),
      ],
      [take({ id: "t1" })],
    );
    expect(items.map((i) => i.take?.id ?? i.run?.id)).toEqual(["busy", "t1", "bad", "fresh"]);
  });
});

describe("checkFile", () => {
  it("takes png, jpeg and webp up to 50 MB", () => {
    expect(checkFile({ type: "image/png", size: 1 })).toBeNull();
    expect(checkFile({ type: "image/webp", size: 52_428_800 })).toBeNull();
    expect(checkFile({ type: "image/gif", size: 1 })).toMatch(/only png, jpeg and webp/);
    expect(checkFile({ type: "image/jpeg", size: 52_428_801 })).toMatch(/larger than 50 MB/);
    expect(checkFile({ type: "image/jpeg", size: 0 })).toMatch(/empty/);
    expect(checkFile({ type: "audio/wav", size: 1 }, true)).toBeNull();
    expect(checkFile({ type: "video/mp4", size: 1 }, true)).toBe(
      "only png, jpeg and webp images or wav and mp3 audio can be uploaded",
    );
  });
});

describe("uploadFile", () => {
  const io = (over: Partial<UploadIO> = {}): UploadIO => ({
    create: vi.fn(async (b) => ({
      take_id: "u1",
      upload_url:
        "http://localhost:8300/files/u/o/uploads/u1.png?exp=1&bytes=3&ct=image%2Fpng&sig=s",
      content_type: b.content_type,
      bytes: b.bytes,
      expires_at: "2026-11-14T10:15:00Z",
    })),
    put: vi.fn(async (_u, _f, _ct, p) => p(1)),
    complete: vi.fn(async () => take({ id: "u1", source: "upload", made_with: null })),
    duration: vi.fn(async () => 20),
    ...over,
  });
  const file = (type = "image/png", body = "abc") => new File([body], "fox.png", { type });

  it("creates, PUTs to the same-origin link with the signed type, completes", async () => {
    const x = io();
    const progress = vi.fn();
    const t = await uploadFile(file(), progress, x);
    expect(t.id).toBe("u1");
    expect(x.create).toHaveBeenCalledWith({
      filename: "fox.png",
      content_type: "image/png",
      bytes: 3,
    });
    expect(x.put).toHaveBeenCalledWith(
      "/files/u/o/uploads/u1.png?exp=1&bytes=3&ct=image%2Fpng&sig=s",
      expect.any(File),
      "image/png",
      progress,
    );
    expect(progress).toHaveBeenCalledWith(1);
    expect(x.complete).toHaveBeenCalledWith("u1");
  });

  it("sends an audio file's type and length (audio pickers only)", async () => {
    const x = io();
    await uploadFile(new File(["abc"], "song.mp3", { type: "audio/mp3" }), () => {}, x, true);
    expect(x.create).toHaveBeenCalledWith({
      filename: "song.mp3",
      content_type: "audio/mpeg",
      bytes: 3,
      duration_s: 20,
    });
    await expect(
      uploadFile(new File(["abc"], "a.wav", { type: "audio/wav" }), () => {}, x),
    ).rejects.toThrow(/only png, jpeg and webp images can/);
  });

  it("refuses audio over 600 s or that the browser cannot read", async () => {
    const wav = new File(["abc"], "a.wav", { type: "audio/x-wav" });
    const long = io({ duration: vi.fn(async () => 601) });
    await expect(uploadFile(wav, () => {}, long, true)).rejects.toThrow(
      "audio is longer than 600 s",
    );
    const bad = io({ duration: vi.fn(async () => Promise.reject(new Error("x"))) });
    await expect(uploadFile(wav, () => {}, bad, true)).rejects.toThrow("not a valid wav file");
    expect(long.create).not.toHaveBeenCalled();
  });

  it("refuses a wrong type before calling the API", async () => {
    const x = io();
    await expect(uploadFile(file("image/gif"), () => {}, x)).rejects.toThrow(/only png/);
    expect(x.create).not.toHaveBeenCalled();
  });

  it("does not complete when the PUT fails", async () => {
    const x = io({ put: vi.fn(async () => Promise.reject(new Error("bad signature"))) });
    await expect(uploadFile(file(), () => {}, x)).rejects.toThrow("bad signature");
    expect(x.complete).not.toHaveBeenCalled();
  });

  it("refuses a link that is not http(s)", async () => {
    const x = io({
      create: vi.fn(async () => ({
        take_id: "u1",
        upload_url: "javascript:alert(1)",
        content_type: "image/png",
        bytes: 3,
        expires_at: "",
      })),
    });
    await expect(uploadFile(file(), () => {}, x)).rejects.toThrow(/unusable/);
    expect(x.put).not.toHaveBeenCalled();
  });
});

describe("rawsFrom", () => {
  const qwen = {
    ...qwenWorkflows.workflows[0],
    node_schemas: Object.fromEntries(qwenNodes.nodes.map((n) => [n.kind, n])),
  } as unknown as S<"Workflow">;
  const fs = fields(qwen);

  it("fills the form from a reused take, and sending it back gives the same settings", () => {
    const seed = fs.find((f) => f.param.type === "seed")!;
    const prompt = fs.find((f) => f.param.type === "text")!;
    const inputs = Object.fromEntries(fs.map((f) => [f.key, f.initial]));
    inputs[seed.key] = 43;
    inputs[prompt.key] = "a fox";
    inputs["gone.param"] = 1;
    const raws = rawsFrom(fs, inputs);
    expect(raws[seed.key]).toBe("43");
    expect(raws[prompt.key]).toBe("a fox");
    expect("gone.param" in raws).toBe(false);
    expect(buildInputs(fs, raws)).toEqual({
      inputs: { [seed.key]: 43, [prompt.key]: "a fox" },
      errors: {},
    });
  });
});
