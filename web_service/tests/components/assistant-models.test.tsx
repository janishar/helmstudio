// @vitest-environment jsdom
// The Models screen's actions for the assistant (components/models/assistant.ts; plan 18 §22.5, §22.6, decision 3;
// board frame 6): an admin's openRow is a line; Download is a card that posts nothing; its Yes opens the row's own
// Download, where the licence tick and Download stay the admin's clicks, and resolves with the job (or cancelled);
// Delete is a card; a user who is not an admin gets no action at all.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/ModelList.valid.json";
import { ModelsScreen } from "@/components/models/models";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { asked, type Asked, type Outcome } from "@/lib/assistant";
import { api, type S } from "@/lib/api";
import { answer } from "./helpers";

const who = vi.hoisted(() => ({ roles: ["admin"] }));
vi.mock("@/components/shell", () => ({ useUser: () => who }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  who.roles = ["admin"];
});

const list = fixture as unknown as S<"ModelList">;
const JOB = "00000000-0000-4000-8000-000000000001";

function start() {
  const posts: { path: string; body: unknown }[] = [];
  const deletes: unknown[] = [];
  answer("GET", {
    "/v1/studio/models": () => ({ data: list }),
    "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
  });
  answer("POST", {
    "/v1/studio/admin/models/{model}/download": (init) => {
      posts.push({ path: "download", body: init.body });
      return { status: 202, data: { id: JOB, host: "gpu1", kind: "download", state: "queued" } };
    },
    "/v1/studio/admin/model-jobs/{job_id}/cancel": (init) => {
      posts.push({ path: "cancel", body: init.params });
      return { data: { id: JOB, state: "cancelled" } };
    },
  });
  vi.spyOn(api, "DELETE").mockImplementation(((_: string, init: unknown) => {
    deletes.push(init);
    return Promise.resolve({
      data: { id: JOB, host: "mac", kind: "delete", state: "queued", keeps: [] },
      response: new Response(),
    });
  }) as never);
  const reg = new Registry();
  reg.setPath("/models");
  render(
    <ToolsContext value={reg}>
      <ModelsScreen tab="models" />
    </ToolsContext>,
  );
  let n = 0;
  const run = (name: string, args: Record<string, unknown> = {}) =>
    reg
      .run(
        { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
        () => undefined,
      )
      .then((r) => r.out);
  const call = async (name: string, args: Record<string, unknown> = {}) =>
    (await act(() => run(name, args))) as Outcome | Asked;
  return { reg, call, run, posts, deletes };
}
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};
const card = (o: Outcome | Asked) => {
  if (!asked(o)) throw new Error(`no card: ${o.text}`);
  return o;
};

describe("the Models screen's actions", () => {
  it("openRow, then Download is a card; its Yes opens the row's Download and the tick is the admin's", async () => {
    const { run, posts, reg } = start();
    await screen.findByRole("region", { name: "Qwen Image 2.1" });
    expect(
      out(await run("openRow", { model: "qwen-image-2.1", variant: "Turbo", host: "gpu1" })),
    ).toMatchObject({ text: "Opened Models · Qwen Image 2.1 · Turbo · gpu1" });
    expect(reg.current()?.spec.sees).toEqual(["Models", "Qwen Image 2.1 · Turbo", "gpu1"]);
    const a = card(await run("download"));
    expect(a.card).toMatchObject({
      kind: "download",
      title: "Download Qwen Image 2.1 · Turbo?",
      tags: ["uses disk and network"],
      go: "DOWNLOAD",
    });
    expect(a.card.rows).toContainEqual(["Licence", "Qwen Research License · not accepted yet"]);
    expect(a.card.note).toMatch(/tick I accept there first/);
    expect(posts).toEqual([]);
    const yes = a.yes();
    const dialog = await screen.findByRole("dialog", { name: "Download Qwen Image 2.1 · Turbo" });
    const go = within(dialog).getByRole("button", { name: "Download" });
    expect(go.hasAttribute("disabled")).toBe(true); // the licence tick is not the assistant's
    fireEvent.click(within(dialog).getByRole("checkbox", { name: "I accept this licence" }));
    fireEvent.click(go);
    expect(await yes).toMatchObject({
      status: "done",
      text: "Download queued on gpu1 · Qwen Image 2.1 · Turbo",
      result: { job_id: JOB },
    });
    expect(posts[0].body).toMatchObject({ host: "gpu1", variant: "turbo", accept_licence: true });
  });

  it("Download's dialog closed without Download is cancelled; a present row is refused", async () => {
    const { run, posts } = start();
    await screen.findByRole("region", { name: "Qwen Image 2.1" });
    await run("openRow", { model: "qwen-image-2.1", variant: "turbo", host: "gpu1" });
    const yes = card(await run("download")).yes();
    const dialog = await screen.findByRole("dialog", { name: "Download Qwen Image 2.1 · Turbo" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(await yes).toMatchObject({ status: "cancelled" });
    expect(posts).toEqual([]);
    await run("openRow", { model: "qwen-image-2.1", variant: "base", host: "mac" });
    expect(out(await run("download"))).toMatchObject({
      status: "failed",
      text: "Download · already on mac",
    });
  });

  it("Delete is a card that deletes nothing until Yes; cancel a job is a line", async () => {
    const { call, deletes, posts } = start();
    await screen.findByRole("region", { name: "Qwen Image 2.1" });
    const d = card(await call("delete", { model: "qwen-image-2.1", variant: "base", host: "mac" }));
    expect(d.card).toMatchObject({
      kind: "delete",
      danger: true,
      title: "Delete Qwen Image 2.1 · Base from mac?",
    });
    expect(deletes).toEqual([]);
    expect(await act(() => d.yes())).toMatchObject({ text: "Delete queued on mac" });
    expect(deletes[0]).toMatchObject({
      params: { path: { model: "qwen-image-2.1" }, query: { host: "mac", variant: "base" } },
    });
    expect(
      out(await call("delete", { model: "ltx-2.5", variant: "distilled", host: "gpu1" })),
    ).toMatchObject({
      status: "failed",
    });
    expect(out(await call("cancelJob", { job_id: JOB }))).toMatchObject({
      text: "Cancelled the job",
    });
    await waitFor(() => expect(posts.some((p) => p.path === "cancel")).toBe(true));
  });

  it("a user who is not an admin gets the summary and no action: it says to ask an admin", async () => {
    who.roles = [];
    const { reg, call } = start();
    await screen.findByRole("region", { name: "Qwen Image 2.1" });
    const s = reg.summary();
    expect(s.actions).not.toContain("download");
    expect(s.actions).not.toContain("openRow");
    expect((s.fields as unknown as { admin: boolean }).admin).toBe(false);
    expect(out(await call("download"))).toMatchObject({ status: "failed" });
  });
});
