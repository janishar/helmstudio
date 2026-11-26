// @vitest-environment jsdom
// Model folders on the Models page (docs/mock/ModelFolders.dc.html, plan 40, API 1.78): the Folders strip and the
// chips' disk tags (frame 1), T9 not connected (3), Edit folders (2: reorder, Downloads, Remove's confirm, Add, Save
// as PUT …/folders), the folder row's menu (5a), Download's Folder pick (4), Delete's which copy (6), the model card's
// Copies (5c) and the Mac app's Show in Finder.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/ModelList.valid.2.json";
import { closeMenu } from "@/components/context-menu";
import { ModelsScreen } from "@/components/models/models";
import { api, type S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("@/components/shell", () => ({ useUser: () => ({ roles: ["admin"] }) }));
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
  };
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
  delete (globalThis as { helmDesktop?: unknown }).helmDesktop;
});

const MAC = "3f2a9c1e7b04";
const T9 = "a81d0e5c22f9";
const GB = 1e9;

/** mac: Mac (downloads) and T9, T9 connected unless `t9Off`; Turbo on mac in both folders, loaded from Mac. */
function data(t9Off = false): S<"ModelList"> {
  const d = structuredClone(fixture) as unknown as S<"ModelList">;
  const mac = d.hosts.find((h) => h.host === "mac")!;
  mac.folders = [
    { ...mac.folders![0], name: "Mac", free_bytes: 43 * GB, total_bytes: 994 * GB },
    {
      ...mac.folders![1],
      name: "T9",
      connected: !t9Off,
      writable: !t9Off,
      free_bytes: t9Off ? null : 205 * GB,
      total_bytes: t9Off ? null : 1.8e12,
    },
  ];
  const turbo = d.models[0].variants[1];
  turbo.hosts = turbo.hosts.map((h) =>
    h.host === "mac"
      ? {
          ...h,
          state: "present",
          bytes: 40.9 * GB,
          job: null,
          folder: MAC,
          copies: [
            { folder: MAC, state: "present", bytes: 40.9 * GB, freed_bytes: 18.6 * GB },
            t9Off
              ? { folder: T9, state: "unplugged", bytes: null, freed_bytes: null }
              : { folder: T9, state: "present", bytes: 40.9 * GB, freed_bytes: 40.9 * GB },
          ],
        }
      : h,
  );
  // LTX Distilled only on T9: unplugged when T9 is
  const ltx = d.models[1].variants[0];
  ltx.hosts = [
    t9Off
      ? {
          host: "mac",
          state: "unplugged",
          bytes: null,
          job: null,
          folder: null,
          copies: [{ folder: T9, state: "unplugged", bytes: null, freed_bytes: null }],
        }
      : {
          host: "mac",
          state: "present",
          bytes: 20 * GB,
          job: null,
          folder: T9,
          copies: [{ folder: T9, state: "present", bytes: 20 * GB, freed_bytes: 20 * GB }],
        },
  ];
  return d;
}

function start(d: S<"ModelList">) {
  const puts: { params?: unknown; body?: unknown }[] = [];
  const posts: { params?: unknown; body?: unknown }[] = [];
  const deletes: unknown[] = [];
  answer("GET", {
    "/v1/studio/models": () => ({ data: d }),
    "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
  });
  answer("PUT", {
    "/v1/studio/admin/hosts/{host}/folders": (init) => {
      puts.push(init);
      return { data: {} };
    },
  });
  answer("POST", {
    "/v1/studio/admin/models/{model}/download": (init) => {
      posts.push(init);
      return { status: 202, data: { id: "j1", host: "mac", kind: "download", state: "queued" } };
    },
  });
  vi.spyOn(api, "DELETE").mockImplementation(((_: string, init: unknown) => {
    deletes.push(init);
    return Promise.resolve({
      data: { id: "j2", host: "mac", kind: "delete", state: "queued", keeps: [] },
      response: new Response(),
    });
  }) as never);
  render(<ModelsScreen tab="models" />);
  return { puts, posts, deletes };
}

const strip = () => screen.findByRole("group", { name: "Folders on mac" });
const rows = () => [...document.querySelectorAll<HTMLElement>(".cm [data-lv]")];
const labels = () => rows().map((b) => b.querySelector(".lb")!.textContent);
const item = (name: string) => rows().find((x) => x.querySelector(".lb")!.textContent === name)!;
const card = (title: string) =>
  screen.getAllByRole("button", { name: title })[0].closest<HTMLElement>("div.cursor-pointer")!;

describe("Model folders: the strip and the chips (frames 1, 3)", () => {
  it("All hosts: a strip for mac (two folders), none for gpu1; disk tags and also on T9", async () => {
    start(data());
    const s = await strip();
    expect(s.textContent).toContain("Folders · mac");
    expect(s.textContent).toMatch(/1Mac~\/GenAI\/models43 GB freeDownloads/);
    expect(s.textContent).toMatch(/2T9\/Volumes\/ALI T9\/models205 GB free/);
    expect(screen.queryByRole("group", { name: "Folders on gpu1" })).toBeNull();
    const turbo = card("Turbo");
    expect(turbo.textContent).toContain("macMacalso on T9");
    expect(card("Distilled").textContent).toContain("macT9");
  });

  it("T9 unplugged: Not connected on the strip; Turbo keeps Mac; LTX shows mac · T9 not connected", async () => {
    start(data(true));
    const s = await strip();
    expect(s.textContent).toContain("Not connected");
    expect(card("Turbo").textContent).toContain("macMacT9 not connected");
    expect(card("Distilled").textContent).toContain("mac · T9 not connected");
  });

  it("the side panel: present · Mac and Copies on mac (used, spare)", async () => {
    start(data());
    fireEvent.click((await screen.findAllByRole("button", { name: "Turbo" }))[0]);
    const side = screen.getByRole("complementary", { name: "Selected model" });
    await waitFor(() => expect(side.textContent).toContain("present · Mac"));
    expect(within(side).getByRole("heading", { name: /^Copies on mac/ })).toBeTruthy();
    expect(side.textContent).toMatch(/1 · Macused~\/GenAI\/models\/Qwen-Image-2.1-Turbo/);
    expect(side.textContent).toMatch(/2 · T9spare\/Volumes\/ALI T9\/models\/Qwen-Image-2.1-Turbo/);
  });
});

describe("Model folders: Edit folders (frame 2) and the row menu (5a)", () => {
  it("reorder by drag, keep Downloads on Mac, add a path, Save sends the list", async () => {
    const { puts } = start(data());
    fireEvent.click(within(await strip()).getByRole("button", { name: "Edit…" }));
    const dlg = await screen.findByRole("dialog", { name: "Folders · mac" });
    const list = within(dlg).getByRole("list");
    const [mac, t9] = within(list).getAllByRole("listitem");
    expect(within(mac).getByRole("radio")).toHaveProperty("checked", true);
    fireEvent.dragStart(t9);
    fireEvent.dragOver(mac);
    fireEvent.drop(mac);
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((r) => r.getAttribute("aria-label")),
    ).toEqual(["T9", "Mac"]);
    // Downloads followed Mac to row 2
    expect(
      within(within(list).getByRole("listitem", { name: "Mac" })).getByRole("radio"),
    ).toHaveProperty("checked", true);
    const path = within(dlg).getByRole("textbox", { name: "Folder path" });
    fireEvent.change(path, { target: { value: "models" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Add" }));
    expect(dlg.textContent).toContain("paths must be absolute or start with ~/");
    fireEvent.change(path, { target: { value: "/Volumes/Studio/models" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Add" }));
    const added = within(list).getByRole("listitem", { name: "Studio" });
    expect(added.textContent).toContain("Checked on Save");
    // no Browse in a browser
    expect(within(dlg).queryByRole("button", { name: "Browse…" })).toBeNull();
    fireEvent.click(within(dlg).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0]).toMatchObject({
      params: { path: { host: "mac" } },
      body: {
        folders: [
          { path: "/Volumes/ALI T9/models" },
          { path: "~/GenAI/models" },
          { path: "/Volumes/Studio/models" },
        ],
        downloads: 1,
      },
    });
  });

  it("Alt+↑ moves a row; Remove asks first and says the files stay; the last can't go", async () => {
    start(data());
    fireEvent.click(within(await strip()).getByRole("button", { name: "Edit…" }));
    const dlg = await screen.findByRole("dialog", { name: "Folders · mac" });
    const list = within(dlg).getByRole("list");
    fireEvent.keyDown(within(list).getByRole("listitem", { name: "T9" }), {
      key: "ArrowUp",
      altKey: true,
    });
    expect(within(list).getAllByRole("listitem")[0].getAttribute("aria-label")).toBe("T9");
    fireEvent.click(within(dlg).getByRole("button", { name: "Remove T9" }));
    const confirm = within(dlg).getByRole("alertdialog", { name: "Remove T9?" });
    expect(confirm.textContent).toContain(
      "The files stay on disk. Models only there show as missing on mac.",
    );
    fireEvent.click(within(confirm).getByRole("button", { name: "Remove" }));
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(dlg).getByRole("button", { name: "Remove Mac" })).toHaveProperty(
      "disabled",
      true,
    );
  });

  it("a row's right-click: Move up greyed on the first, Use for downloads greyed where they go", async () => {
    start(data());
    fireEvent.click(within(await strip()).getByRole("button", { name: "Edit…" }));
    const dlg = await screen.findByRole("dialog", { name: "Folders · mac" });
    fireEvent.contextMenu(within(dlg).getByRole("listitem", { name: "Mac" }), { button: 2 });
    expect(document.querySelector(".cm .hd")!.textContent).toBe("Mac · ~/GenAI/models");
    expect(labels()).toEqual(["Move up", "Move down", "Use for downloads", "Remove…"]);
    expect(item("Move up").title).toBe("Already first");
    expect(item("Use for downloads").title).toBe("Downloads go here");
    act(() => fireEvent.click(item("Move down")));
    const order = within(dlg)
      .getAllByRole("listitem")
      .map((r) => r.getAttribute("aria-label"));
    expect(order).toEqual(["T9", "Mac"]);
  });

  it("the strip chip's Use for downloads saves at once", async () => {
    const { puts } = start(data());
    const s = await strip();
    fireEvent.contextMenu(within(s).getByTitle("/Volumes/ALI T9/models"), { button: 2 });
    act(() => fireEvent.click(item("Use for downloads")));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toEqual({
      folders: [{ path: "~/GenAI/models" }, { path: "/Volumes/ALI T9/models" }],
      downloads: 1,
    });
  });

  it("the Mac app: Browse… adds the chosen folder; Open in Finder reveals it", async () => {
    const reveal = vi.fn(() => Promise.resolve(true));
    const chooseFolder = vi.fn(() => Promise.resolve("/Volumes/Studio/models"));
    (globalThis as { helmDesktop?: unknown }).helmDesktop = {
      localHost: "mac",
      reveal,
      chooseFolder,
    };
    start(data());
    fireEvent.click(within(await strip()).getByRole("button", { name: "Edit…" }));
    const dlg = await screen.findByRole("dialog", { name: "Folders · mac" });
    fireEvent.click(within(dlg).getByRole("button", { name: "Browse…" }));
    expect(await within(dlg).findByRole("listitem", { name: "Studio" })).toBeTruthy();
    fireEvent.contextMenu(within(dlg).getByRole("listitem", { name: "T9" }), { button: 2 });
    act(() => fireEvent.click(item("Open in Finder")));
    expect(reveal).toHaveBeenCalledWith("/Volumes/ALI T9/models");
  });
});

describe("Model folders: Download's Folder (frame 4) and Delete's copy (frame 6)", () => {
  it("Download: Mac picked first (Downloads); T9 picked goes as folder", async () => {
    const d = data();
    const base = d.models[0].variants[0];
    base.hosts = base.hosts.map((h) =>
      h.host === "mac" ? { ...h, state: "missing", bytes: null, folder: null, copies: [] } : h,
    );
    const r = start(d);
    fireEvent.click((await screen.findAllByRole("button", { name: "Base" }))[0]);
    const panel = screen.getByRole("complementary", { name: "Selected model" });
    fireEvent.change(within(panel).getByLabelText("Target host"), { target: { value: "mac" } });
    fireEvent.click(within(panel).getByRole("button", { name: "Download to host" }));
    const dlg = await screen.findByRole("dialog", { name: /^Download Qwen Image 2.1 · Base/ });
    const pick = within(dlg).getByRole("radiogroup", { name: "Folder" });
    const radios = within(pick).getAllByRole("radio");
    expect(radios[0]).toHaveProperty("checked", true);
    expect(pick.textContent).toMatch(/Mac~\/GenAI\/modelsDownloads43 GB free/);
    expect(dlg.textContent).toContain("~/GenAI/models/Qwen-Image-2.1");
    fireEvent.click(radios[1]);
    expect(dlg.textContent).toContain("/Volumes/ALI T9/models/Qwen-Image-2.1");
    fireEvent.click(within(dlg).getByRole("button", { name: "Download" }));
    await waitFor(() => expect(r.posts).toHaveLength(1));
    expect(r.posts[0].body).toMatchObject({ host: "mac", variant: "base", folder: T9 });
  });

  it("Download: an unplugged folder is greyed and cannot be picked", async () => {
    const d = data(true);
    const base = d.models[0].variants[0];
    base.hosts = base.hosts.map((h) =>
      h.host === "mac" ? { ...h, state: "missing", bytes: null, folder: null, copies: [] } : h,
    );
    start(d);
    fireEvent.click((await screen.findAllByRole("button", { name: "Base" }))[0]);
    const panel = screen.getByRole("complementary", { name: "Selected model" });
    fireEvent.change(within(panel).getByLabelText("Target host"), { target: { value: "mac" } });
    fireEvent.click(within(panel).getByRole("button", { name: "Download to host" }));
    const dlg = await screen.findByRole("dialog", { name: /^Download Qwen Image 2.1 · Base/ });
    const radios = within(within(dlg).getByRole("radiogroup", { name: "Folder" })).getAllByRole(
      "radio",
    );
    expect(radios[1]).toHaveProperty("disabled", true);
    expect(dlg.textContent).toContain("Not connected");
  });

  it("Delete: which copy, the used one picked; Both sends both folders", async () => {
    const { deletes } = start(data());
    fireEvent.click((await screen.findAllByRole("button", { name: "Turbo" }))[0]);
    const panel = screen.getByRole("complementary", { name: "Selected model" });
    fireEvent.change(within(panel).getByLabelText("Target host"), { target: { value: "mac" } });
    fireEvent.click(within(panel).getByRole("button", { name: "Delete from host" }));
    const dlg = await screen.findByRole("dialog", {
      name: "Delete Qwen Image 2.1 · Turbo from mac?",
    });
    const pick = within(dlg).getByRole("radiogroup", { name: "Copy" });
    expect(pick.textContent).toMatch(/Mac~\/GenAI\/modelsused18.6 GB back · 62 GB free/);
    expect(pick.textContent).toMatch(/T9\/Volumes\/ALI T9\/models40.9 GB back · 246 GB free/);
    expect(pick.textContent).toContain("Both59.5 GB back");
    expect(dlg.textContent).toContain("After this, mac loads Turbo from T9, the other copy.");
    expect(within(dlg).getByRole("button", { name: "Delete Mac copy" })).toBeTruthy();
    fireEvent.click(within(pick).getAllByRole("radio")[2]);
    expect(dlg.textContent).toContain("After this, Turbo is not on mac: it shows missing there.");
    fireEvent.click(within(dlg).getByRole("button", { name: "Delete both" }));
    await waitFor(() => expect(deletes).toHaveLength(1));
    expect(deletes[0]).toMatchObject({
      params: { query: { host: "mac", variant: "turbo", folder: [MAC, T9] } },
    });
  });

  it("Delete: the other copy unplugged greys it and Both", async () => {
    start(data(true));
    fireEvent.click((await screen.findAllByRole("button", { name: "Turbo" }))[0]);
    const panel = screen.getByRole("complementary", { name: "Selected model" });
    fireEvent.change(within(panel).getByLabelText("Target host"), { target: { value: "mac" } });
    fireEvent.click(within(panel).getByRole("button", { name: "Delete from host" }));
    const dlg = await screen.findByRole("dialog", {
      name: "Delete Qwen Image 2.1 · Turbo from mac?",
    });
    const radios = within(within(dlg).getByRole("radiogroup", { name: "Copy" })).getAllByRole(
      "radio",
    );
    expect(radios.map((r) => (r as HTMLInputElement).disabled)).toEqual([false, true, true]);
    expect(dlg.textContent).toContain("T9 not connected");
    expect(dlg.textContent).toContain(
      "After this, Turbo is only on T9: it shows T9 not connected until T9 is back.",
    );
  });

  it("the model card menu: Copies on mac (used, spare); Show in Finder in the Mac app", async () => {
    const reveal = vi.fn(() => Promise.resolve(true));
    (globalThis as { helmDesktop?: unknown }).helmDesktop = { localHost: "mac", reveal };
    start(data());
    fireEvent.click((await screen.findAllByRole("button", { name: "Turbo" }))[0]);
    const panel = screen.getByRole("complementary", { name: "Selected model" });
    fireEvent.change(within(panel).getByLabelText("Target host"), { target: { value: "mac" } });
    fireEvent.contextMenu(card("Turbo"), { button: 2 });
    expect(labels()).toEqual([
      "Open",
      "Licence",
      "Show in Finder",
      "Mac",
      "T9",
      "Download to host…",
      "Delete from host…",
    ]);
    expect([...document.querySelectorAll(".cm .hd")].map((h) => h.textContent)).toContain(
      "Copies on mac",
    );
    expect(item("Mac").querySelector("kbd")!.textContent).toBe("used");
    expect(item("T9").querySelector("kbd")!.textContent).toBe("spare");
    act(() => fireEvent.click(item("Show in Finder")));
    expect(reveal).toHaveBeenCalledWith("~/GenAI/models/Qwen-Image-2.1-Turbo");
  });
});
