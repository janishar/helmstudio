// @vitest-environment jsdom
// The Gallery's Edit (components/gallery/edit.tsx; docs/mock/TakeActions.dc.html Edit an image take, Edit a video
// take, Edit an audio take, Saved as new; alpha.2 item 11): shown only while Media tools runs; each kind's panel
// builds its tool's input; Save as new runs it (POST /v1/studio/plugins/media-tools/tools/{tool}), selects the new
// take next to the original with the toast, whose Undo deletes it; a failure says Couldn't save in plain words.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { S } from "@/lib/api";
import { dragBox, fitBox } from "@/components/gallery/edit";
import { answer, audio, take, video } from "./helpers";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/gallery",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const { Gallery } = await import("@/components/gallery/gallery");

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const TOOLS = ["edit_image", "edit_video", "frame_image", "edit_audio", "join_audio", "frames"];
const page = (...takes: S<"Take">[]): S<"TakePage"> => ({ takes, next_cursor: null });
const made = take({ title: "Fox at the window · cropped", source: "tool" });

/** Media tools as GET /plugins/media-tools answers (`running` null: off, 404) and the new take's GET. */
function serve(running: boolean | null = true) {
  return answer("GET", {
    "/v1/studio/plugins/{plugin}": () =>
      running === null
        ? { status: 404, error: { detail: "not found" } }
        : { data: { name: "media-tools", title: "Media tools", running, tools: TOOLS } },
    "/v1/studio/takes/{take_id}": (init) => {
      const id = (init.params as { path: { take_id: string } }).path.take_id;
      return id === made.id ? { data: made } : { status: 404, error: { detail: "not found" } };
    },
  });
}

/** POST …/tools/{tool}: what it got, answering `reply`. */
function tools(reply: { status?: number; data?: unknown; error?: unknown } = {}) {
  const got: { tool: string; input: unknown }[] = [];
  answer("POST", {
    "/v1/studio/plugins/{plugin}/tools/{tool}": (init) => {
      got.push({
        tool: (init.params as { path: { tool: string } }).path.tool,
        input: (init.body as { input: unknown }).input,
      });
      return reply.status ? reply : { data: { take_ids: [made.id], line: "Cropped · Fox" } };
    },
  });
  return got;
}

const aside = () => within(screen.getByRole("complementary", { name: "Selected take" }));
const edit = async () => fireEvent.click(await aside().findByRole("button", { name: "Edit" }));
const press = (name: string) => fireEvent.click(aside().getByRole("button", { name }));
const save = () => aside().getByRole("button", { name: "Save as new" });

describe("Gallery: Edit", () => {
  it("is hidden while Media tools is off or not running", async () => {
    serve(null);
    render(<Gallery initial={page(take())} />);
    await aside().findByRole("button", { name: "Rename" });
    await new Promise((r) => setTimeout(r, 0));
    expect(aside().queryByRole("button", { name: "Edit" })).toBeNull();
    cleanup();
    serve(false);
    render(<Gallery initial={page(take())} />);
    await aside().findByRole("button", { name: "Rename" });
    await new Promise((r) => setTimeout(r, 0));
    expect(aside().queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("an image: crop by ratio, rotate, convert; Save as new selects the new take with the toast, Undo deletes it", async () => {
    serve();
    const got = tools();
    const t = take({ title: "Fox at the window" });
    render(<Gallery initial={page(t, take())} />);
    await edit();
    expect(aside().getByRole("heading", { name: "Edit" })).toBeTruthy();
    for (const name of ["Crop", "Resize", "Rotate", "Flip", "Extend", "Convert"])
      expect(aside().getByRole("button", { name })).toBeTruthy();
    expect(aside().getByLabelText("Crop").textContent).toContain("The whole picture");
    expect((save() as HTMLButtonElement).disabled).toBe(true); // nothing to do yet
    press("16:9");
    expect(aside().getByLabelText("Crop").textContent).toContain("16:9 · 512 × 288 at 0, 112");
    expect(aside().getByLabelText("Crop box")).toBeTruthy();
    press("Rotate");
    press("90°");
    expect(aside().getByLabelText("Rotate").textContent).toContain("90° clockwise");
    press("Convert");
    press("WebP");
    fireEvent.click(save());
    await screen.findByText(`Saved as a new take · ${made.title}`);
    expect(got).toEqual([
      {
        tool: "edit_image",
        input: {
          take_id: t.id,
          crop: { x: 0, y: 112, width: 512, height: 288 },
          rotate: 90,
          format: "webp",
        },
      },
    ]);
    // the new take is selected, next to the original
    await aside().findByRole("button", { name: "Rename" });
    const del = answer("DELETE", { "/v1/studio/takes/{take_id}": () => ({ status: 204 }) });
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/v1/studio/takes/{take_id}", {
        params: { path: { take_id: made.id } },
      }),
    );
    await waitFor(() => expect(screen.queryByText(/Saved as a new take/)).toBeNull());
  });

  it("says Saving… while it works, then Couldn't save in plain words", async () => {
    serve();
    let fail: (v: unknown) => void = () => undefined;
    vi.spyOn((await import("@/lib/api")).api, "POST").mockImplementation(
      () => new Promise((r) => (fail = r)) as never,
    );
    render(<Gallery initial={page(take())} />);
    await edit();
    press("Flip");
    press("Across");
    fireEvent.click(save());
    expect(await screen.findByText("Saving…")).toBeTruthy();
    expect((save() as HTMLButtonElement).disabled).toBe(true);
    fail({
      data: undefined,
      error: { detail: "it took too long; try a shorter part" },
      response: new Response(null, { status: 502 }),
    });
    expect((await aside().findByRole("alert")).textContent).toBe(
      "Couldn't save · it took too long; try a shorter part",
    );
    expect(screen.queryByText("Saving…")).toBeNull();
    fireEvent.click(aside().getByRole("button", { name: "Cancel" }));
    expect(aside().queryByRole("button", { name: "Save as new" })).toBeNull();
  });

  it("a video (a Timeline export too): trim bar, speed, frame as image", async () => {
    serve();
    const got = tools();
    const v = video(4, 24, { source: "export", title: "Shot 3" });
    render(<Gallery initial={page(v)} />);
    await edit();
    expect(aside().getAllByLabelText("Trim")).toHaveLength(2); // the bar and its row
    for (const name of ["Speed", "Mute", "New sound", "Frame as image"])
      expect(aside().getByRole("button", { name })).toBeTruthy();
    expect(aside().getAllByLabelText("Trim")[1].textContent).toContain(
      "0:00.00 – 0:04.00 · 4.00 s",
    );
    press("Speed");
    press("2×");
    expect(aside().getByLabelText("Speed").textContent).toContain("2× · 2.00 s");
    press("Mute");
    press("Sound off");
    fireEvent.click(save());
    await waitFor(() => expect(got).toHaveLength(1));
    expect(got[0]).toEqual({ tool: "edit_video", input: { take_id: v.id, speed: 2, mute: true } });
  });

  it("a video's Frame as image saves the frame under the playhead", async () => {
    serve();
    const got = tools();
    const v = video(4);
    render(<Gallery initial={page(v)} />);
    await edit();
    press("Frame as image");
    expect(aside().getByLabelText("Frame").textContent).toContain("at 0:00.00");
    fireEvent.click(save());
    await waitFor(() =>
      expect(got).toEqual([{ tool: "frame_image", input: { take_id: v.id, at: 0 } }]),
    );
  });

  it("an audio take: volume; Join needs 2–8 takes", async () => {
    serve();
    const got = tools();
    const a = audio(16, { title: "Rain" });
    render(<Gallery initial={page(a)} />);
    await edit();
    expect(aside().getByLabelText("Trim · volume").textContent).toContain("0:00.0 – 0:16.0 · 0 dB");
    press("Volume");
    fireEvent.change(aside().getByRole("slider"), { target: { value: "-6" } });
    expect(aside().getByLabelText("Trim · volume").textContent).toContain("−6 dB");
    press("Join");
    expect(aside().getByRole("list", { name: "Join in order" }).textContent).toContain("Rain");
    expect((save() as HTMLButtonElement).disabled).toBe(true); // one take is no join
    expect(aside().getByRole("button", { name: "Add audio takes…" })).toBeTruthy();
    press("Volume");
    fireEvent.click(save());
    await waitFor(() =>
      expect(got).toEqual([{ tool: "edit_audio", input: { take_id: a.id, volume_db: -6 } }]),
    );
  });
});

describe("crop boxes", () => {
  it("fits the largest box of a ratio, centred", () => {
    expect(fitBox(1920, 1080, 0)).toEqual({ x: 0, y: 0, width: 1920, height: 1080 });
    expect(fitBox(1920, 1080, 1)).toEqual({ x: 420, y: 0, width: 1080, height: 1080 });
    expect(fitBox(1000, 1000, 16 / 9)).toEqual({ x: 0, y: 219, width: 1000, height: 563 });
  });

  it("a drag keeps the ratio and stays inside the picture", () => {
    expect(dragBox([100, 100], [300, 150], 1000, 1000, 0)).toEqual({
      x: 100,
      y: 100,
      width: 200,
      height: 50,
    });
    expect(dragBox([300, 150], [100, 100], 1000, 1000, 0)).toEqual({
      x: 100,
      y: 100,
      width: 200,
      height: 50,
    });
    expect(dragBox([100, 100], [300, 150], 1000, 1000, 1)).toEqual({
      x: 100,
      y: 100,
      width: 200,
      height: 200,
    });
    expect(dragBox([900, 900], [2000, 2000], 1000, 1000, 1)).toEqual({
      x: 900,
      y: 900,
      width: 100,
      height: 100,
    });
  });
});
