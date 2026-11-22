// @vitest-environment jsdom
// Models (docs/mock/Models.dc.html): a model's optional add-ons (API 1.36) listed under it, a card each with its
// size, help and hosts, and for admins its own Download (POST …/download with `addon`) and Delete (`addon` query).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/ModelList.valid.json";
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
  cleanup();
  vi.restoreAllMocks();
});

const list = fixture as unknown as S<"ModelList">;
const m = list.models.find((x) => x.addons.length)!;
const a = m.addons[0];
const job = (host: string) => ({ id: "j1", host, kind: "download", state: "queued" });

function start(data: S<"ModelList">) {
  const posts: unknown[] = [];
  answer("GET", {
    "/v1/studio/models": () => ({ data }),
    "/v1/studio/admin/model-jobs": () => ({ data: { jobs: [] } }),
  });
  answer("POST", {
    "/v1/studio/admin/models/{model}/download": (init) => {
      posts.push(init);
      return { status: 202, data: job("gpu1") };
    },
  });
  const deletes: unknown[] = [];
  vi.spyOn(api, "DELETE").mockImplementation(((_: string, init: unknown) => {
    deletes.push(init);
    return Promise.resolve({ data: { ...job("mac"), keeps: [] }, response: new Response() });
  }) as never);
  render(<ModelsScreen tab="models" />);
  return { posts, deletes };
}

const card = async () =>
  (await screen.findByText(a.title)).closest<HTMLElement>("div.border-dashed")!;

describe("Models: optional add-ons", () => {
  it("a card under its model; Download goes to the first host it can, with the add-on", async () => {
    const { posts } = start(list);
    const c = await card();
    const section = screen.getByRole("region", { name: m.title });
    expect(within(section).getByRole("heading", { name: /^Optional add-ons/ })).toBeTruthy();
    expect(c.textContent).toContain("add-on");
    expect(c.textContent).toContain(a.component);
    expect(c.textContent).toContain("16.0 GB");
    expect(within(c).getByRole("progressbar", { name: `${a.title} on mac` })).toBeTruthy();
    // nothing of it on any host yet: Delete is not offered; Download goes to gpu1 (mac is downloading)
    expect(within(c).getByRole("button", { name: "Delete" }).hasAttribute("disabled")).toBe(true);
    const down = within(c).getByRole("button", { name: "Download…" });
    expect(down.title).toBe("Download to gpu1");
    fireEvent.click(down);
    const dialog = await screen.findByRole("dialog", { name: `Download ${m.title} · ${a.title}` });
    fireEvent.click(within(dialog).getByRole("button", { name: "Download" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({
      params: { path: { model: m.id } },
      body: { host: "gpu1", addon: a.component },
    });
    expect((posts[0] as { body: object }).body).not.toHaveProperty("variant");
    expect(await within(c).findByText("Download queued on gpu1.")).toBeTruthy();
  });

  it("Delete removes only the add-on from a host that has it", async () => {
    const present = {
      ...list,
      models: list.models.map((x) =>
        x.id === m.id
          ? {
              ...x,
              addons: [
                { ...a, hosts: a.hosts.map((h) => ({ ...h, state: "present", job: null })) },
              ],
            }
          : x,
      ),
    } as S<"ModelList">;
    const { deletes } = start(present);
    const c = await card();
    expect(within(c).getByRole("button", { name: "Download…" }).hasAttribute("disabled")).toBe(
      true,
    );
    fireEvent.click(within(c).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: /^Delete .* from gpu1\?$/ });
    expect(dialog.textContent).toContain("Every variant keeps working without it.");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(deletes).toHaveLength(1));
    expect(deletes[0]).toMatchObject({
      params: { path: { model: m.id }, query: { host: "gpu1", addon: a.component } },
    });
  });
});
