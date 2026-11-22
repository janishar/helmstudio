"use client";
// The Judge panel's "Test on takes" (docs/mock/PipelineGraph.dc.html; owner's Q7, API 1.28): pick 1 to 8 takes, the
// saved judge checks them once (POST /pipelines/{id}/judge-tests, a run outside any batch), and each take's verdict
// and reason show under the buttons. The editor saves first, so the test is of the judge as it is on screen.
import { useEffect, useRef, useState } from "react";
import { GalleryPicker } from "@/components/gallery/picker";
import { api, message, ok, type S } from "@/lib/api";

type Take = S<"Take">;
type Test = S<"JudgeTest">;

const POLL_MS = 1500;
const OPEN = new Set(["queued", "running"]);

/** The one-line summary of a finished test ("pass · keeps take 3", "fail · 1 of 4 passed · first failed: …"). */
export function testSummary(t: Test): string {
  if (t.status === "queued") return "queued…";
  if (t.status === "running") return "judging…";
  if (t.status !== "succeeded") return t.error ?? t.status;
  const kept = t.takes.flatMap((x, i) => (x.picked ? [`take ${i + 1}`] : []));
  const passed = t.takes.filter((x) => x.passes).length;
  return t.verdict === "pass"
    ? `pass · keeps ${kept.join(", ")}`
    : `fail · ${passed} of ${t.takes.length} passed${t.first_failed ? ` · first failed: ${t.first_failed}` : ""}`;
}

export function JudgeTest({
  pipelineId,
  node,
  save,
  children,
}: {
  pipelineId: string;
  node: string;
  /** saves the editor's document first (the test reads the saved judge) */
  save: () => Promise<void>;
  /** the buttons before "Test on takes" (Open memory) */
  children?: React.ReactNode;
}) {
  const [picking, setPicking] = useState(false);
  const [takes, setTakes] = useState<Take[]>([]);
  const [test, setTest] = useState<Test | null>(null);
  const [error, setError] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  const follow = (id: string) => {
    timer.current = setTimeout(async () => {
      try {
        const t = await ok(
          api.GET("/v1/studio/judge-tests/{test_id}", { params: { path: { test_id: id } } }),
        );
        setTest(t);
        if (OPEN.has(t.status)) follow(id);
      } catch (e) {
        setError(message(e));
      }
    }, POLL_MS);
  };

  const start = async (picked: Take[]) => {
    setPicking(false);
    if (!picked.length) return;
    clearTimeout(timer.current);
    setTakes(picked);
    setTest(null);
    setError("");
    try {
      await save();
      const t = await ok(
        api.POST("/v1/studio/pipelines/{pipeline_id}/judge-tests", {
          params: { path: { pipeline_id: pipelineId } },
          body: { node, take_ids: picked.map((x) => x.id) },
        }),
      );
      setTest(t);
      if (OPEN.has(t.status)) follow(t.id);
    } catch (e) {
      setError(message(e));
    }
  };

  const byId = new Map(takes.map((t) => [t.id, t]));
  return (
    <>
      <div className="flex flex-wrap gap-2">
        {children}
        <button type="button" className="chip bevel" onClick={() => setPicking(true)}>
          Test on takes
        </button>
      </div>
      {error && <p className="m-0 text-[12px] text-bad">{error}</p>}
      {test && (
        <section aria-label="Test on takes" className="flex flex-col gap-1.5" aria-live="polite">
          <p
            className={`m-0 text-[12px] ${test.status === "succeeded" ? "text-t1" : test.status === "failed" ? "text-bad" : "text-t2"}`}
          >
            {testSummary(test)}
          </p>
          {test.takes.map((v, i) => {
            const t = byId.get(v.take_id);
            const thumb = t?.video?.poster_url ?? t?.thumb_url;
            const failed = v.checks
              .filter((c) => !c.pass && c.level === "must")
              .map((c) => c.criterion);
            return (
              <div key={v.take_id} className="set flex items-start gap-2 py-1.5">
                {thumb && (
                  <img
                    loading="lazy"
                    decoding="async"
                    src={thumb}
                    alt=""
                    className="size-10 shrink-0 rounded-md object-cover"
                  />
                )}
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-[13px]">
                    take {i + 1}{" "}
                    <b className={v.picked ? "text-a1" : v.passes ? "text-t1" : "text-bad"}>
                      {v.picked ? "kept" : v.passes ? "passes" : "fails"}
                    </b>
                    <span className="text-[11px] text-t2"> · rank {v.rank}</span>
                  </span>
                  {failed.length > 0 && (
                    <span className="text-[11px] text-bad">fails: {failed.join(", ")}</span>
                  )}
                  <span className="text-[12px] text-t2">{v.reason}</span>
                </div>
              </div>
            );
          })}
        </section>
      )}
      <GalleryPicker
        open={picking}
        onClose={() => setPicking(false)}
        max={8}
        kind={null}
        title="Test on takes"
        help="The judge as saved checks them in the order you pick, once; nothing is kept in memory."
        onPick={(picked) => void start(picked)}
      />
    </>
  );
}
