import type { Metadata } from "next";
import { Create, type CreateInitial } from "@/components/create/create";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Create" };

/** Route /: Create, on the session `?s=` names, else the newest; `?reuse=<take id>` fills the form with the
 * settings that made that take. The sessions, workflows and the open session's runs and takes are read here, so the
 * first paint is the real screen; if any read fails, Create loads them itself. */
export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ s?: string; reuse?: string }>;
}) {
  const { s, reuse } = await searchParams;
  return <Create initialSession={s} reuse={reuse} initial={await load(s)} />;
}

async function load(wanted?: string): Promise<CreateInitial | undefined> {
  const api = await serverApi();
  const [s, w] = await Promise.all([
    initial(api.GET("/v1/studio/sessions")),
    initial(api.GET("/v1/studio/workflows")),
  ]);
  if (!s || !w) return undefined;
  const sessionId = s.sessions.find((x) => x.id === wanted)?.id ?? s.sessions[0]?.id ?? null;
  const base = { sessions: s.sessions, workflows: w.workflows, sessionId };
  if (!sessionId) return { ...base, runs: [], takes: [] };
  const path = { params: { path: { session_id: sessionId } } };
  const [r, t] = await Promise.all([
    initial(api.GET("/v1/studio/sessions/{session_id}/runs", path)),
    // the same newest 200 as Create's loadTakes
    initial(
      api.GET("/v1/studio/sessions/{session_id}/takes", {
        params: { ...path.params, query: { limit: 200 } },
      }),
    ),
  ]);
  return r && t ? { ...base, runs: r.runs, takes: t.takes } : undefined;
}
