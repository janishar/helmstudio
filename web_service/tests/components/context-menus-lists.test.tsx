// @vitest-environment jsdom
// The lists' right-click menus (alpha.5 item 17, docs/mock/ContextMenus.dc.html frames 10-12): each item calls what
// its button, ⋯ menu or click does. Models: a model card, an add-on; Libraries: a LoRA (Models › LoRAs, Create's Add
// LoRA), a voice, an element; Hub and System: a provider, a plugin, a user, a host and a resident component, a key.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import models from "../../../contracts/fixtures/http/ModelList.valid.json";
import loraList from "../../../contracts/fixtures/http/LoraList.valid.json";
import providers from "../../../contracts/fixtures/http/ProviderList.valid.json";
import plugins from "../../../contracts/fixtures/http/PluginList.valid.json";
import hostList from "../../../contracts/fixtures/http/HostList.valid.json";
import { closeMenu } from "@/components/context-menu";
import { api, type S } from "@/lib/api";
import { reloadLibrary } from "@/lib/library";
import { reloadLoras } from "@/lib/lora-library";
import { answer } from "./helpers";

const push = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/hub",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({
  useUser: () => ({ id: "u1", roles: ["admin"] }),
  useAssistantApi: () => null,
}));

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
  push.mockReset();
});

const { ModelsScreen } = await import("@/components/models/models");
const { LoraPicker } = await import("@/components/create/loras");
const { VoicePicker } = await import("@/components/create/voices");
const { ElementsPicker } = await import("@/components/create/elements");
const { ProvidersAdmin } = await import("@/components/admin/providers");
const { PluginsScreen } = await import("@/components/plugins/plugins");
const { UsersAdmin } = await import("@/components/admin/users");
const { HostsAdmin } = await import("@/components/admin/hosts");
const { ApiKeysAdmin } = await import("@/components/admin/apikeys");

const rows = () => [...document.querySelectorAll<HTMLElement>(".cm [data-lv]")];
const labels = () => rows().map((b) => b.querySelector(".lb")!.textContent);
const item = (name: string) => {
  const b = rows().find((x) => x.querySelector(".lb")!.textContent === name);
  if (!b) throw new Error(`no menu item ${name}: ${labels().join(", ")}`);
  return b;
};
const header = () => document.querySelector(".cm .hd")?.textContent;
const rightClick = (el: Element) =>
  fireEvent.contextMenu(el, { button: 2, clientX: 50, clientY: 10 });
const pick = (name: string) => act(() => fireEvent.click(item(name)));

const list = models as unknown as S<"ModelList">;

describe("Models", () => {
  it("a model card: Open, Licence, Download to host… (greyed with its reason), Delete from host…", async () => {
    answer("GET", {
      "/v1/studio/models": () => ({ data: list }),
      "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
    });
    render(<ModelsScreen tab="models" />);
    // Qwen Image 2.1 · Base: mismatch on gpu1 (the first host it is not present on), present on mac
    const base = (await screen.findAllByRole("button", { name: "Base" }))[0];
    rightClick(base);
    expect(header()).toBe("Qwen Image 2.1 · Base");
    expect(labels()).toEqual([
      "Open",
      "Licence",
      "Show in Finder",
      "Download to host…",
      "Delete from host…",
    ]);
    expect(item("Show in Finder").title).toBe("Not on gpu1"); // ModelFolders 5c: greyed, saying why
    expect(item("Delete from host…").className).toContain("danger");
    pick("Licence");
    expect(await screen.findByRole("dialog", { name: /^Licence · / })).toBeTruthy();
    // LTX-2.5 · Distilled names no licence: Licence and Download are greyed with why
    rightClick(screen.getByRole("button", { name: "Distilled" }));
    expect(item("Licence").getAttribute("aria-disabled")).toBe("true");
    expect(item("Download to host…").getAttribute("aria-disabled")).toBe("true");
    pick("Delete from host…");
    expect(
      await screen.findByRole("dialog", { name: "Delete LTX-2.5 · Distilled from mac?" }),
    ).toBeTruthy();
  });

  it("a variant downloading to its target host offers Cancel download, the panel's call", async () => {
    const posts: string[] = [];
    const data = structuredClone(list);
    const turbo = data.models[0].variants[1];
    turbo.hosts = turbo.hosts.map(
      (h) => ({ ...h, state: "downloading", job: { id: "j9" } }) as never,
    );
    answer("GET", {
      "/v1/studio/models": () => ({ data }),
      "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
    });
    answer("POST", {
      "/v1/studio/admin/model-jobs/{job_id}/cancel": ({ params }) => {
        posts.push((params as { path: { job_id: string } }).path.job_id);
        return { data: {} };
      },
    });
    render(<ModelsScreen tab="models" />);
    rightClick(await screen.findByRole("button", { name: "Turbo" }));
    expect(labels()).toContain("Cancel download");
    pick("Cancel download");
    await waitFor(() => expect(posts).toEqual(["j9"]));
  });
});

describe("Libraries", () => {
  it("Models › LoRAs: yours Rename and Delete (its confirm)", async () => {
    answer("GET", {
      "/v1/studio/loras": () => ({ data: loraList }),
      "/v1/studio/models": () => ({ data: list }),
      "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
    });
    await reloadLoras();
    render(<ModelsScreen tab="loras" />);
    const tile = (await screen.findAllByText("my-sketch-style"))[0].closest("li")!;
    rightClick(tile);
    expect(labels()).toEqual(["Rename", "Delete"]);
    pick("Delete");
    expect(await screen.findByRole("dialog", { name: "Delete this LoRA?" })).toBeTruthy();
  });

  it("Create's Add LoRA: Add puts it on the stack; a LoRA that does not fit is greyed", async () => {
    answer("GET", { "/v1/studio/loras": () => ({ data: loraList }) });
    await reloadLoras();
    const onPick = vi.fn();
    const onClose = vi.fn();
    render(
      <LoraPicker open onClose={onClose} family="qwen-image" max={4} taken={[]} onPick={onPick} />,
    );
    const dialog = await screen.findByRole("dialog", { name: "Add LoRA" });
    rightClick((await within(dialog).findByText("new-upload")).closest("li")!);
    expect(item("Add").getAttribute("aria-disabled")).toBe("true");
    rightClick(within(dialog).getByText("my-sketch-style").closest("li")!);
    expect(labels()).toEqual(["Add", "Rename", "Delete"]);
    pick("Add");
    expect(onPick).toHaveBeenCalledWith(["2c4e6a8b-1d3f-4a5b-9c7d-0e2f4a6b8c10"]);
    expect(onClose).toHaveBeenCalled();
  });

  const lib = (over: Partial<S<"LibraryItem">>) =>
    ({
      id: "e1",
      kind: "element",
      group: "character",
      name: "Fox",
      handle: "fox",
      text: "",
      images: [],
      thumb_url: null,
      version: 1,
      created_at: "2026-11-21T10:00:00Z",
      updated_at: "2026-11-21T10:00:00Z",
      source: "mine",
      family: null,
      loras: [],
      needs: [],
      ...over,
    }) as S<"LibraryItem">;

  it("a voice: Play, Use; yours also Rename and Delete (its inline confirm)", async () => {
    const narrator = lib({
      id: "v1",
      kind: "voice",
      group: "designed",
      name: "Narrator",
      handle: null,
      thumb_url: "/files/u/x/n.wav",
      voice: { source: "designed" } as never,
    });
    answer("GET", { "/v1/studio/library": () => ({ data: { items: [narrator] } }) });
    const onPick = vi.fn();
    const wf = { id: "", title: "", family: "", nodes: [], node_schemas: {} } as never;
    render(
      <VoicePicker
        open
        onClose={() => undefined}
        value=""
        onPick={onPick}
        ctx={{ workflow: wf, familyTitle: (f) => f }}
      />,
    );
    const dialog = await screen.findByRole("dialog", { name: "Voice" });
    fireEvent.click(await within(dialog).findByRole("tab", { name: /^Mine/ }));
    const card = (await within(dialog).findByText("Narrator")).closest("li")!;
    rightClick(card);
    expect(header()).toBe("Narrator");
    expect(labels()).toEqual(["Play", "Use", "Rename", "Delete"]);
    pick("Delete");
    expect(within(dialog).getByText("Delete Narrator? Takes made with it keep it.")).toBeTruthy();
    rightClick(card);
    pick("Use");
    expect(onPick).toHaveBeenCalledWith("v1");
  });

  it("an element: Put @fox in the prompt, Edit, Delete (its inline confirm)", async () => {
    answer("GET", {
      "/v1/studio/library": ({ params }) => {
        const k = (params as { query: { kind: string } }).query.kind;
        return { data: { items: k === "element" ? [lib({})] : [] } };
      },
    });
    await reloadLibrary();
    const onInsert = vi.fn();
    render(<ElementsPicker open onClose={() => undefined} onInsert={onInsert} inPrompt={[]} />);
    const card = (await screen.findByText("@fox")).closest("li")!;
    rightClick(card);
    expect(header()).toBe("@fox · Fox");
    expect(labels()).toEqual(["Put @fox in the prompt", "Edit", "Delete"]);
    pick("Delete");
    expect(screen.getByText("Delete @fox? Takes made with it keep it.")).toBeTruthy();
    rightClick(card);
    pick("Put @fox in the prompt");
    expect(onInsert).toHaveBeenCalledWith("fox");
  });
});

describe("Hub and System", () => {
  it("a provider: Test, Edit, Disable, Delete… (its card)", async () => {
    const tests: unknown[] = [];
    answer("GET", {
      "/v1/studio/admin/providers": () => ({ data: providers }),
      "/v1/studio/admin/provider-presets": () => ({ data: { presets: [], remote_enabled: false } }),
      "/v1/studio/admin/assistant": () => ({ status: 404, error: { detail: "no" } }),
    });
    answer("POST", {
      "/v1/studio/admin/providers/{name}/test": (init) => {
        tests.push(init);
        return {
          data: { ok: true, models: [], answer: null, error: null, failure: null, seconds: 0.1 },
        };
      },
    });
    render(<ProvidersAdmin />);
    const row = (await screen.findByText(providers.providers[0].title!)).closest("tr")!;
    rightClick(row);
    expect(labels()).toEqual(["Test", "Edit", "Disable", "Delete…"]);
    expect(item("Disable").className).toContain("danger");
    pick("Test");
    await waitFor(() => expect(tests).toHaveLength(1));
    rightClick(row);
    pick("Delete…");
    expect(await screen.findByRole("dialog", { name: "Delete this provider?" })).toBeTruthy();
  });

  it("a plugin: Open in Providers or Add as provider, Open Assistant, Remove…; a faded row Remove only", async () => {
    answer("GET", { "/v1/studio/admin/plugins": () => ({ data: plugins }) });
    render(<PluginsScreen />);
    const row = (name: string) => screen.getAllByText(name)[0].closest("tr")!;
    await screen.findAllByText("Your Claude subscription");
    rightClick(row("Your Claude subscription"));
    expect(labels()).toEqual(["Open in Providers", "Open Assistant", "Remove…"]);
    pick("Open in Providers");
    expect(push).toHaveBeenCalledWith(expect.stringContaining("providers"));
    rightClick(row("Media tools"));
    expect(item("Add as provider").getAttribute("aria-disabled")).toBe("true");
    rightClick(row("Storyboard artist"));
    expect(labels()).toEqual(["Remove…"]);
    pick("Remove…");
    expect(await screen.findByRole("dialog", { name: "Remove Storyboard artist?" })).toBeTruthy();
  });

  it("a user: Roles…, Reset password…, Disable; the root admin's row has none", async () => {
    const user = (id: string, name: string, roles: string[]) =>
      ({
        id,
        name,
        email: `${name}@studio.test`,
        roles,
        disabled: false,
        created_at: "2026-11-01T00:00:00Z",
      }) as S<"User">;
    const puts: unknown[] = [];
    vi.spyOn(api, "PUT").mockImplementation(((_: string, init: { body: unknown }) => {
      puts.push(init.body);
      return Promise.resolve({
        data: { ...user("2", "asha", ["user"]), disabled: true },
        response: new Response(),
      });
    }) as never);
    render(
      <UsersAdmin initialUsers={[user("1", "root", ["admin"]), user("2", "asha", ["user"])]} />,
    );
    rightClick(screen.getByText("root").closest("tr")!);
    expect(document.querySelector(".cm")).toBeNull();
    rightClick(screen.getByText("asha").closest("tr")!);
    expect(header()).toBe("asha · asha@studio.test");
    expect(labels()).toEqual(["Roles…", "Reset password…", "Disable"]);
    pick("Disable");
    await waitFor(() => expect(puts).toEqual([{ disabled: true }]));
  });

  it("a host: Unload all…, Evict all…; a resident component: Pin, Unload… with their cards", async () => {
    answer("GET", {
      "/v1/studio/hosts": () => ({ data: hostList }),
      "/v1/studio/queue": () => ({ data: { runs: [] } }),
      "/v1/studio/models": () => ({ data: list }),
      "/auth/admin/users": () => ({ data: { users: [] } }),
    });
    render(<HostsAdmin />);
    const section = (await screen.findAllByRole("region")).find((r) =>
      r.getAttribute("aria-label")!.startsWith("mac"),
    )!;
    rightClick(section);
    expect(labels()).toEqual(["Unload all…", "Evict all…"]);
    pick("Evict all…");
    expect(await screen.findByRole("dialog", { name: "Evict all on mac?" })).toBeTruthy();
    const unload = within(section).getAllByRole("button", { name: /^Unload (?!all)/ })[0];
    rightClick(unload.parentElement!);
    expect(labels()).toEqual(["Pin", "Unload…"]);
  });

  it("an API key: Copy prefix, Revoke…; a revoked key has none", async () => {
    const key = (id: string, client: string, revoked_at: string | null) => ({
      id,
      client,
      prefix: `hs_${id}`,
      created_at: "2026-11-01T00:00:00Z",
      last_used_at: null,
      revoked_at,
    });
    answer("GET", {
      "/auth/admin/apikeys": () => ({
        data: {
          api_keys: [key("4f2a", "ci-runner", null), key("91c0", "old", "2026-11-02T00:00:00Z")],
        },
      }),
    });
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(<ApiKeysAdmin />);
    rightClick((await screen.findByText("old")).closest("tr")!);
    expect(document.querySelector(".cm")).toBeNull();
    rightClick(screen.getByText("ci-runner").closest("tr")!);
    expect(header()).toBe("ci-runner · hs_4f2a…");
    expect(labels()).toEqual(["Copy prefix", "Revoke…"]);
    pick("Copy prefix");
    expect(writeText).toHaveBeenCalledWith("hs_4f2a");
    rightClick(screen.getByText("ci-runner").closest("tr")!);
    pick("Revoke…");
    expect(await screen.findByRole("dialog", { name: "Revoke this key?" })).toBeTruthy();
  });
});
