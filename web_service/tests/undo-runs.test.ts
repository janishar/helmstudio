// An Auto generation's Undo (API 1.68): cancel, wait for the run to end, then delete every take it made.
import { expect, it, vi } from "vitest";

const calls: string[] = [];
const res = (status: number, data?: unknown) => ({
  data,
  error: status >= 400 ? { detail: "x" } : undefined,
  response: new Response(null, { status }),
});
let polls = 0;
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  api: {
    POST: async (p: string) => (calls.push(`POST ${p}`), res(409)), // the run ended between: go on
    GET: async () => {
      polls += 1;
      return polls < 3
        ? res(200, { status: "running", outputs: [{ take_id: "t1" }] })
        : res(200, { status: "cancelled", outputs: [{ take_id: "t1" }, { take_id: "t2" }] });
    },
    DELETE: async (_: string, o: { params: { path: { take_id: string } } }) => (
      calls.push(`DELETE ${o.params.path.take_id}`),
      res(204)
    ),
  },
}));

it("deletes the takes stored after the cancel, and a 409 cancel still deletes", async () => {
  const { undoRuns } = await import("@/components/assistant/tools");
  await undoRuns(["r1"], 0);
  expect(polls).toBe(3);
  expect(calls).toEqual(["POST /v1/studio/runs/{run_id}/cancel", "DELETE t1", "DELETE t2"]);
});
