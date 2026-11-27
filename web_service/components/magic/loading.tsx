// The board's loading state (MagicBoard frame 11), light enough for the route's first paint.
import "./magic.css";

/** The board's loading state (frame 11): the bars of lanes to come. */
export function LoadingBoard() {
  return (
    <main id="main" className="mg mg-wait flex min-w-0 flex-1 flex-col gap-3.5 px-5 pt-4 pb-5">
      <p className="m-0 text-[13px] text-t2" role="status">
        Loading the board…
      </p>
      <div className="mb">
        {[56, 104, 118, 84].map((hgt, i) => (
          <span key={i} className="sk-ln" style={{ "--h": `${hgt}px` } as React.CSSProperties} />
        ))}
      </div>
    </main>
  );
}
