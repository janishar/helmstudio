// The board's loading state (MagicBoard frame 11), light enough for the route's first paint.
import "./magic.css";

/** The board's loading state (frame 11): the bars of lanes to come; `failed` (ErrorStates a3): the board didn't load,
 * its note and Retry over the bars. */
export function LoadingBoard({ failed }: { failed?: React.ReactNode }) {
  return (
    <main id="main" className="mg mg-wait flex min-w-0 flex-1 flex-col gap-3.5 px-5 pt-4 pb-5">
      {!failed && (
        <p className="m-0 text-[13px] text-t2" role="status">
          Loading the board…
        </p>
      )}
      <div className="mb relative">
        {[56, 104, 118, 84].map((hgt, i) => (
          <span key={i} className="sk-ln" style={{ "--h": `${hgt}px` } as React.CSSProperties} />
        ))}
        {failed && (
          <div
            role="alert"
            className="absolute top-1/2 left-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center gap-3 rounded-xl bg-s2 py-2 pr-2 pl-3.5 text-[13px] text-bad shadow-[0_24px_40px_-12px_color-mix(in_srgb,var(--shd)_80%,transparent)]"
          >
            {failed}
          </div>
        )}
      </div>
    </main>
  );
}
