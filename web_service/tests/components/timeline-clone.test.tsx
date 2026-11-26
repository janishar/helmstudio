// @vitest-environment jsdom
// alpha.5 items 12 and 14: the Sequences dialog's Clone (Rename · Clone · Delete) makes "<name> clone" and opens it;
// a sequence's name follows its session's `session` events while nobody chose it (a typed name stays).
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { Sequences } from "@/components/timeline/dialogs";
import type { S } from "@/lib/api";
import { retitled } from "@/lib/timeline";
import { answer } from "./helpers";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const tl = {
  id: "t1",
  name: "The Last Tide",
  title_source: "user",
  named_by: null,
} as S<"Timeline">;

it("Clone sits between Rename and Delete, posts from_timeline_id and opens the clone", async () => {
  answer("GET", { "/v1/studio/timelines": () => ({ data: { timelines: [] } }) });
  const post = answer("POST", {
    "/v1/studio/timelines": () => ({
      status: 201,
      data: { ...tl, id: "t2", name: "The Last Tide clone" },
    }),
  });
  const onOpen = vi.fn();
  const onClose = vi.fn();
  render(
    <Sequences
      open
      onClose={onClose}
      current={tl}
      onOpen={onOpen}
      onNew={vi.fn()}
      onRenamed={vi.fn()}
      onDeleted={vi.fn()}
    />,
  );
  const names = screen
    .getAllByRole("button")
    .map((b) => b.textContent)
    .filter((t) => ["Rename", "Clone", "Delete"].includes(t ?? ""));
  expect(names).toEqual(["Rename", "Clone", "Delete"]);
  fireEvent.click(screen.getByRole("button", { name: "Clone" }));
  await waitFor(() => expect(onOpen).toHaveBeenCalledWith("t2"));
  expect(post.mock.calls[0][1]).toEqual({ body: { from_timeline_id: "t1" } });
  expect(onClose).toHaveBeenCalled();
});

it("a clone that fails says why and opens nothing", async () => {
  answer("GET", { "/v1/studio/timelines": () => ({ data: { timelines: [] } }) });
  answer("POST", {});
  const onOpen = vi.fn();
  render(
    <Sequences
      open
      onClose={vi.fn()}
      current={tl}
      onOpen={onOpen}
      onNew={vi.fn()}
      onRenamed={vi.fn()}
      onDeleted={vi.fn()}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Clone" }));
  await screen.findByText("Not found.");
  expect(onOpen).not.toHaveBeenCalled();
});

it("retitled: prompt then model replace a default name; a typed or model name stays", () => {
  const untitled = { name: "Untitled sequence", title_source: "default", named_by: null } as const;
  const p = retitled(untitled, { title: "Shot a", title_source: "prompt", named_by: null });
  expect(p).toEqual({ name: "Shot a", title_source: "prompt", named_by: null });
  const m = retitled(p!, { title: "Red fox at dusk", title_source: "model", named_by: "Fake" });
  expect(m).toEqual({ name: "Red fox at dusk", title_source: "model", named_by: "Fake" });
  expect(retitled(m!, { title: "Shot a", title_source: "prompt", named_by: null })).toBeUndefined();
  const mine = { name: "Mine", title_source: "user", named_by: null } as const;
  expect(
    retitled(mine, { title: "Red fox", title_source: "model", named_by: "Fake" }),
  ).toBeUndefined();
});
