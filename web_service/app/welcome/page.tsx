import type { CSSProperties } from "react";
import Link from "next/link";
import { MarkPaths, ThemeSelect } from "@/components/kit";
import "./landing.css";

/** Icon paths from the board, one per room or topic. */
const ICONS = {
  create: (
    <>
      <path d="M12 3v4M12 17v4M3 12h4M17 12h4" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  gallery: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <circle cx="9" cy="10" r="2" />
      <path d="m21 16-5-5L5 20" />
    </>
  ),
  pipelines: (
    <>
      <rect x="3" y="4" width="6" height="6" rx="1.5" />
      <rect x="15" y="14" width="6" height="6" rx="1.5" />
      <path d="M9 7h4a2 2 0 0 1 2 2v5" />
    </>
  ),
  timeline: (
    <>
      <path d="M3 7h18M3 17h18" />
      <rect x="5" y="4" width="6" height="6" rx="1.5" />
      <rect x="12" y="14" width="7" height="6" rx="1.5" />
    </>
  ),
  chip: (
    <>
      <rect x="6" y="6" width="12" height="12" rx="2" />
      <path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4" />
    </>
  ),
  servers: (
    <>
      <rect x="3" y="4" width="18" height="7" rx="2" />
      <rect x="3" y="13" width="18" height="7" rx="2" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </>
  ),
  lock: (
    <>
      <rect x="5" y="11" width="14" height="9" rx="2" />
      <path d="M8 11V8a4 4 0 0 1 8 0v3" />
    </>
  ),
  pack: (
    <>
      <path d="M21 8 12 3 3 8v8l9 5 9-5V8Z" />
      <path d="m3 8 9 5 9-5M12 13v8" />
    </>
  ),
};

/** The outlined "helmstudio" wordmark (Bricolage Grotesque 700) from docs/brand/logo/a-as-drawn/lockup-horizontal.svg. */
const WORD =
  "M4.8 0V-28.7L4.7 -70.1H19.2V-55.8Q19.2 -53.8 19 -50.8Q18.8 -47.8 18.4 -44.45Q18 -41.1 17.55 -37.85Q17.1 -34.6 16.8 -32H18.1Q19.2 -39.5 21.4 -44.35Q23.6 -49.2 27.35 -51.55Q31.1 -53.9 36.4 -53.9Q40.4 -53.9 43.3 -52.6Q46.2 -51.3 48.2 -48.95Q50.2 -46.6 51.4 -43.4Q52.6 -40.2 53.15 -36.45Q53.7 -32.7 53.7 -28.5V0H39.2V-27.1Q39.2 -29.7 38.95 -32.25Q38.7 -34.8 37.85 -36.85Q37 -38.9 35.4 -40.15Q33.8 -41.4 31.1 -41.4Q27.5 -41.4 24.85 -38.9Q22.2 -36.4 20.7 -31.5Q19.2 -26.6 19.2 -19.5V0ZM85.1 1.4Q78.2 1.4 73.2 -0.6Q68.2 -2.6 64.95 -6.2Q61.7 -9.8 60.15 -14.65Q58.6 -19.5 58.6 -25.3Q58.6 -31.2 60.2 -36.35Q61.8 -41.5 64.95 -45.45Q68.1 -49.4 72.9 -51.65Q77.7 -53.9 84.1 -53.9Q90.5 -53.9 95.15 -51.75Q99.8 -49.6 102.8 -45.6Q105.8 -41.6 106.9 -36.15Q108 -30.7 107.2 -24.1L67.7 -23.6V-31.6L97.1 -32.1L94.1 -27.6Q94.6 -32.5 93.55 -35.8Q92.5 -39.1 90.15 -40.8Q87.8 -42.5 84.2 -42.5Q80.3 -42.5 77.65 -40.45Q75 -38.4 73.75 -34.7Q72.5 -31 72.5 -25.8Q72.5 -17.4 75.8 -13.4Q79.1 -9.4 85.2 -9.4Q88 -9.4 89.9 -10.15Q91.8 -10.9 92.95 -12.2Q94.1 -13.5 94.65 -15.2Q95.2 -16.9 95.2 -18.8L108.3 -17.4Q108.1 -13.8 106.8 -10.45Q105.5 -7.1 102.8 -4.45Q100.1 -1.8 95.75 -0.2Q91.4 1.4 85.1 1.4ZM113.6 0V-70.1H127.9V0ZM136.1 0V-29.6L136 -52.5H148.1L147.9 -32.4H149.1Q150 -40.1 152.15 -44.8Q154.3 -49.5 157.7 -51.7Q161.1 -53.9 165.8 -53.9Q169.2 -53.9 171.8 -52.7Q174.4 -51.5 176.25 -48.95Q178.1 -46.4 179.05 -42.3Q180 -38.2 180 -32.4H181.1Q182.1 -40.1 184.4 -44.8Q186.7 -49.5 190.3 -51.7Q193.9 -53.9 198.6 -53.9Q202.4 -53.9 205.25 -52.55Q208.1 -51.2 210 -48.8Q211.9 -46.4 213.05 -43.2Q214.2 -40 214.7 -36.25Q215.2 -32.5 215.2 -28.5V0H200.8V-27Q200.8 -28.8 200.65 -31.2Q200.5 -33.6 199.8 -35.9Q199.1 -38.2 197.6 -39.75Q196.1 -41.3 193.5 -41.3Q190.3 -41.3 187.9 -38.8Q185.5 -36.3 184.1 -31.6Q182.7 -26.9 182.7 -20.1V0H168.6V-27Q168.6 -28.7 168.4 -31.1Q168.2 -33.5 167.55 -35.85Q166.9 -38.2 165.4 -39.75Q163.9 -41.3 161.1 -41.3Q158.1 -41.3 155.7 -38.95Q153.3 -36.6 151.9 -31.9Q150.5 -27.2 150.5 -20.1V0ZM244.1 1.4Q238.9 1.4 234.5 0.35Q230.1 -0.7 226.9 -2.85Q223.7 -5 221.9 -8.45Q220.1 -11.9 220.2 -16.7L232.3 -18.9Q232.5 -15.7 234 -13.5Q235.5 -11.3 238.2 -10.2Q240.9 -9.1 244.7 -9.1Q248.8 -9.1 251.3 -10.4Q253.8 -11.7 253.8 -14.4Q253.8 -16.2 252.65 -17.25Q251.5 -18.3 249 -19.05Q246.5 -19.8 242.5 -20.7Q238 -21.8 234.05 -22.95Q230.1 -24.1 227.1 -25.85Q224.1 -27.6 222.4 -30.35Q220.7 -33.1 220.7 -37.2Q220.7 -42.4 223.45 -46.1Q226.2 -49.8 231.25 -51.85Q236.3 -53.9 243.1 -53.9Q249.2 -53.9 254.3 -52.05Q259.4 -50.2 262.45 -46.3Q265.5 -42.4 265.4 -36.2L253.2 -34.1Q253.3 -37 251.95 -39.1Q250.6 -41.2 248.2 -42.25Q245.8 -43.3 242.5 -43.3Q238.6 -43.3 236.4 -41.85Q234.2 -40.4 234.2 -38.1Q234.2 -36.2 235.6 -35.05Q237 -33.9 239.65 -33.1Q242.3 -32.3 246.2 -31.5Q249.8 -30.8 253.55 -29.75Q257.3 -28.7 260.4 -26.9Q263.5 -25.1 265.4 -22.2Q267.3 -19.3 267.3 -14.8Q267.3 -9.9 264.6 -6.2Q261.9 -2.5 256.75 -0.55Q251.6 1.4 244.1 1.4ZM291.5 1.2Q286.2 1.2 282.8 -0.4Q279.4 -2 277.5 -4.7Q275.6 -7.4 274.9 -10.75Q274.2 -14.1 274.2 -17.7V-41.1H267.7L267.8 -51.1H270.1Q274.8 -51.6 276.85 -53.9Q278.9 -56.2 279.4 -61L279.9 -64.3H288.3V-52.5H301V-40.8H288.3V-18.7Q288.3 -14.7 290.2 -13.15Q292.1 -11.6 294.9 -11.6Q296.5 -11.6 298.15 -12.05Q299.8 -12.5 301.2 -13.3V-0.3Q298.2 0.6 295.8 0.9Q293.4 1.2 291.5 1.2ZM323.2 1.4Q319.6 1.4 316.8 0.4Q314 -0.6 311.9 -2.65Q309.8 -4.7 308.4 -7.75Q307 -10.8 306.3 -14.9Q305.6 -19 305.6 -24.2V-52.5H320.1V-26Q320.1 -23.7 320.25 -21.1Q320.4 -18.5 321.15 -16.25Q321.9 -14 323.55 -12.55Q325.2 -11.1 328.2 -11.1Q330.8 -11.1 332.9 -12.4Q335 -13.7 336.5 -16.35Q338 -19 338.75 -22.85Q339.5 -26.7 339.6 -31.7V-52.5H354V-23.5V0H341.8L342 -19.5H340.9Q340 -12.1 337.8 -7.5Q335.6 -2.9 332 -0.75Q328.4 1.4 323.2 1.4ZM380.3 1.4Q374.4 1.4 369.7 -1.75Q365 -4.9 362.3 -11.15Q359.6 -17.4 359.6 -26.6Q359.6 -35.3 362.05 -41.4Q364.5 -47.5 369 -50.65Q373.5 -53.8 379.8 -53.8Q384.6 -53.8 388.2 -51.85Q391.8 -49.9 394.05 -45.85Q396.3 -41.8 397.4 -35.6H398.7Q398.2 -39.2 397.65 -42.5Q397.1 -45.8 396.85 -48.8Q396.6 -51.8 396.6 -54.2V-70.1H411.1L411 -21.6L411.1 0H398.6L398.8 -16.2H397.6Q396.5 -10.3 394.2 -6.4Q391.9 -2.5 388.45 -0.55Q385 1.4 380.3 1.4ZM385.1 -10.3Q387.8 -10.3 389.95 -11.5Q392.1 -12.7 393.6 -14.85Q395.1 -17 395.9 -19.75Q396.7 -22.5 396.7 -25.6V-26.7Q396.7 -29.1 396.15 -31.35Q395.6 -33.6 394.6 -35.55Q393.6 -37.5 392.15 -38.9Q390.7 -40.3 388.9 -41.1Q387.1 -41.9 385 -41.9Q381.6 -41.9 379.2 -39.8Q376.8 -37.7 375.6 -34.1Q374.4 -30.5 374.4 -26Q374.4 -21.5 375.65 -17.95Q376.9 -14.4 379.35 -12.35Q381.8 -10.3 385.1 -10.3ZM419 0V-52.5H433.4V0ZM426.1 -58.1Q421.9 -58.1 419.7 -59.9Q417.5 -61.7 417.5 -65Q417.5 -68.5 419.7 -70.25Q421.9 -72 426.1 -72Q430.4 -72 432.6 -70.2Q434.8 -68.4 434.8 -65Q434.8 -61.7 432.6 -59.9Q430.4 -58.1 426.1 -58.1ZM465.1 1.4Q457.5 1.4 451.6 -1.7Q445.7 -4.8 442.35 -10.9Q439 -17 439 -26.3Q439 -35.7 442.45 -41.8Q445.9 -47.9 451.8 -50.9Q457.7 -53.9 465.2 -53.9Q472.9 -53.9 478.75 -50.85Q484.6 -47.8 487.95 -41.65Q491.3 -35.5 491.3 -26.2Q491.3 -16.7 487.85 -10.6Q484.4 -4.5 478.45 -1.55Q472.5 1.4 465.1 1.4ZM465.5 -9.4Q468 -9.4 470.05 -10.35Q472.1 -11.3 473.6 -13.3Q475.1 -15.3 475.9 -18.3Q476.7 -21.3 476.7 -25.4Q476.7 -29.7 475.8 -32.95Q474.9 -36.2 473.35 -38.35Q471.8 -40.5 469.6 -41.55Q467.4 -42.6 464.8 -42.6Q462.3 -42.6 460.25 -41.6Q458.2 -40.6 456.7 -38.6Q455.2 -36.6 454.4 -33.55Q453.6 -30.5 453.6 -26.4Q453.6 -22.2 454.45 -18.95Q455.3 -15.7 456.9 -13.6Q458.5 -11.5 460.65 -10.45Q462.8 -9.4 465.5 -9.4Z";

/** Logo A's horizontal lockup, 24 px tall; colours from the theme (landing.css `.bm`, parchment's light rule). */
function Lockup() {
  return (
    <svg
      className="bm"
      role="img"
      aria-label="helmstudio"
      width="132"
      height="24"
      viewBox="0 0 285.8 52"
    >
      <g transform="scale(2.1666666666666665)">
        <MarkPaths arch="var(--bA)" stem="var(--bB)" />
      </g>
      <path transform="translate(69.60 46.80) scale(0.44)" d={WORD} style={{ fill: "var(--t1)" }} />
    </svg>
  );
}

/** A gradient icon tile: `c` is its colour (c1–c6), `small` the 34 px size. */
function Ic({ name, c, small }: { name: keyof typeof ICONS; c: string; small?: boolean }) {
  return (
    <svg
      aria-hidden
      className={`ic ${c}${small ? " s" : ""}`}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {ICONS[name]}
    </svg>
  );
}

const col = (gap: number): CSSProperties => ({ display: "flex", flexDirection: "column", gap });
const wrapRow = (gap: number): CSSProperties => ({ display: "flex", flexWrap: "wrap", gap });
const grow = <div style={{ flexGrow: 1 }} />;
/** A take tile with its picture (public/showcase); the tN gradient shows until it loads. Below the fold it waits. */
const pic = (src: string, t: string, style?: CSSProperties, eager = false) => (
  <img
    className={`tk ${t}`}
    src={`/showcase/${src}.jpg`}
    alt=""
    loading={eager ? "eager" : "lazy"}
    decoding="async"
    style={style}
  />
);
const strip = (src: string, t: string, flex: number) =>
  pic(src, t, { flex, aspectRatio: "auto", height: 46 });

/** Route /welcome, shown at / to a signed-out visitor (proxy.ts): the landing page (docs/mock/Landing.dc.html). */
export default function Landing() {
  return (
    <div className="lp">
      <header className="wrap">
        <nav aria-label="Site" className="top">
          <a
            href="#top"
            aria-label="helmstudio, top of page"
            style={{ display: "flex", alignItems: "center" }}
          >
            <Lockup />
          </a>
          {grow}
          <a className="l" href="#rooms">
            Studio
          </a>
          <a className="l" href="#pipelines">
            Pipelines
          </a>
          <a className="l" href="#machines">
            Your machines
          </a>
          <a className="l" href="#models">
            Models
          </a>
          <Link className="b1 sm" href="/signin">
            Sign in
          </Link>
        </nav>
      </header>

      <main>
        <section
          id="top"
          className="wrap"
          style={{
            display: "flex",
            flexWrap: "wrap",
            alignItems: "center",
            gap: 52,
            paddingTop: 88,
            paddingBottom: 104,
          }}
        >
          <div style={{ flex: "1 1 500px", ...col(26) }}>
            <span className="pill" style={{ alignSelf: "flex-start" }}>
              <i />
              Self-hosted
            </span>
            <h1
              className="d"
              style={{ fontSize: 64, lineHeight: "66px", letterSpacing: "-.025em" }}
            >
              Wire the models.
              <br />
              <span style={{ color: "var(--a1)" }}>Direct</span> the film.
            </h1>
            <p className="p" style={{ fontSize: 18, lineHeight: "28px" }}>
              Image and video generation for your team, on your own GPUs. Build a workflow once,
              then shoot takes from it.
            </p>
            <div style={wrapRow(14)}>
              <Link className="b1" href="/signin">
                Sign in
              </Link>
              <a className="b2" href="#selfhost">
                Host it yourself
              </a>
            </div>
          </div>

          <div
            aria-label="A workflow graph that becomes a studio"
            role="img"
            className="hero-art"
            style={{ flex: "0 1 560px", position: "relative", height: 580, minWidth: 320 }}
          >
            <div
              style={{
                position: "absolute",
                left: 0,
                top: 0,
                width: 410,
                height: 250,
                borderRadius: "30px 30px 30px 12px",
                background: "var(--p0)",
                transform: "rotate(-4deg)",
                boxShadow:
                  "inset 0 1px 0 rgba(255,255,255,.08), 0 40px 60px -24px color-mix(in srgb, var(--shd) 90%, transparent)",
              }}
            >
              <svg
                aria-hidden
                width="410"
                height="250"
                viewBox="0 0 410 250"
                fill="none"
                stroke="var(--a1)"
                strokeWidth="2"
                strokeLinecap="round"
                style={{ position: "absolute", inset: 0, opacity: 0.8 }}
              >
                <path d="M124 74h28M256 74h28M204 152v-50M336 102v50" />
                <path d="M256 180c40 0 4-92 28-98" stroke="var(--a2)" />
              </svg>
              {(
                [
                  ["Prompt", "Text", 20, 46],
                  ["Encode", "Conditioning", 152, 46],
                  ["Sample", "Latent", 284, 46],
                  ["Model", "qwen-image", 152, 152],
                  ["Decode", "Image", 284, 152],
                ] as const
              ).map(([name, out, left, top]) => (
                <div key={name} className="nd" style={{ left, top }}>
                  {name}
                  <small>{out}</small>
                </div>
              ))}
            </div>
            <div
              style={{
                position: "absolute",
                right: 0,
                top: 312,
                width: 440,
                padding: 20,
                borderRadius: "34px 34px 34px 14px",
                background: "var(--s1)",
                backgroundImage:
                  "linear-gradient(180deg, rgba(255,255,255,.06), rgba(255,255,255,0) 46%)",
                transform: "rotate(2.5deg)",
                boxShadow:
                  "inset 0 1px 0 rgba(255,255,255,.1), 0 48px 70px -26px color-mix(in srgb, var(--shd) 95%, transparent)",
                ...col(14),
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <Ic name="create" c="c1" small />
                <span style={{ fontWeight: 600 }}>Create</span>
                <span className="m" style={{ fontSize: 11, color: "var(--t2)" }}>
                  Qwen Image
                </span>
              </div>
              <div
                style={{
                  padding: "12px 14px",
                  borderRadius: "16px 16px 16px 6px",
                  background: "var(--in)",
                  fontSize: 13,
                  lineHeight: "20px",
                  color: "var(--t1b)",
                }}
              >
                <span style={{ color: "var(--a1)" }}>@mara</span> in the rain, neon city at night,
                long lens
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 10 }}>
                {pic("mara-street", "t1", undefined, true)}
                {pic(
                  "mara-looks-back",
                  "t2",
                  { outline: "2px solid var(--a1)", outlineOffset: 2 },
                  true,
                )}
                {pic("mara-umbrella", "t3", undefined, true)}
                {pic("mara-train", "t4", undefined, true)}
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <span className="pill" style={{ height: 30, fontSize: 12 }}>
                  16:9
                </span>
                <span className="pill" style={{ height: 30, fontSize: 12 }}>
                  4 takes
                </span>
                {grow}
                <span className="b1 sm" style={{ height: 36 }}>
                  Generate
                </span>
              </div>
            </div>
            <span
              className="pill"
              style={{
                position: "absolute",
                right: 8,
                top: 262,
                background: "var(--s3)",
                transform: "rotate(3deg)",
                boxShadow: "0 16px 24px -10px color-mix(in srgb, var(--shd) 85%, transparent)",
              }}
            >
              <i />
              Same workflow, two views
            </span>
          </div>
        </section>

        <section id="rooms" className="wrap" style={{ ...col(40), paddingBottom: 112 }}>
          <div style={col(14)}>
            <span className="eb">
              <Ic name="create" c="c1" small />
              The studio
            </span>
            <h2 className="d h2">Four rooms. One gallery between them.</h2>
          </div>
          <div
            className="grid12"
            style={{ display: "grid", gridTemplateColumns: "repeat(12, 1fr)", gap: 24 }}
          >
            <article className="card" style={{ gridColumn: "span 7", minWidth: 0 }}>
              <Ic name="create" c="c1" />
              <h3>Create</h3>
              <p>
                Pick a model, write a prompt, shoot takes. Call your characters and places by name
                with @, and set a look once.
              </p>
              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: "repeat(4, 1fr)",
                  gap: 12,
                  paddingTop: 6,
                }}
              >
                {pic("outpost-monolith", "t3")}
                {pic("sneaker-dunes", "t1")}
                {pic("mara-motorbike", "t4")}
                {pic("fox-dusk-43", "t2")}
              </div>
            </article>
            <article
              className="card"
              style={{ gridColumn: "span 5", minWidth: 0, borderRadius: "32px 32px 12px 32px" }}
            >
              <Ic name="gallery" c="c3" />
              <h3>Gallery</h3>
              <p>
                Every take lands here with how it was made. Send any of them to Create, a pipeline
                or the timeline.
              </p>
              <div style={{ ...wrapRow(8), paddingTop: 6 }}>
                <span className="pill">Use in…</span>
                <span className="pill">Add to timeline</span>
                <span className="pill">Save as look</span>
              </div>
            </article>
            <article className="card" style={{ gridColumn: "span 5", minWidth: 0 }}>
              <Ic name="pipelines" c="c2" />
              <h3>Pipelines</h3>
              <p>
                Chain workflows and run them over a list while you are away. Agent nodes pick,
                rewrite and retry.
              </p>
            </article>
            <article
              className="card"
              style={{ gridColumn: "span 7", minWidth: 0, borderRadius: "12px 32px 32px 32px" }}
            >
              <Ic name="timeline" c="c4" />
              <h3>Timeline</h3>
              <p>
                Direct shot by shot with a shared cast and look, cut the takes together, and export.
              </p>
              <div aria-hidden style={{ display: "flex", gap: 8, paddingTop: 6 }}>
                {strip("mara-bus", "t1", 3)}
                {strip("mara-street", "t4", 2)}
                {strip("mara-motorbike", "t3", 4)}
                {strip("mara-puddle", "t2", 2)}
              </div>
            </article>
          </div>
        </section>

        <section
          id="pipelines"
          className="wrap"
          style={{
            display: "flex",
            flexWrap: "wrap",
            alignItems: "center",
            gap: 56,
            paddingBottom: 112,
          }}
        >
          <div style={{ flex: "1 1 380px", ...col(20) }}>
            <span className="eb">
              <Ic name="pipelines" c="c2" small />
              Pipelines
            </span>
            <h2 className="d h2">Batches that check their own work.</h2>
            <p className="p">
              A judge checks each take against your checklist, keeps the best and sends the rest
              back.
            </p>
            <div style={{ ...col(12), paddingTop: 4 }}>
              <div className="ck">Suggests first. Switch to auto when you trust it.</div>
              <div className="ck">Learns from the takes you keep and reject.</div>
              <div className="ck">Every retry is capped and logged.</div>
            </div>
          </div>
          <div className="card" style={{ flex: "1 1 560px", minWidth: 0, gap: 22, padding: 32 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <span className="m" style={{ fontSize: 12, color: "var(--t2)" }}>
                still-to-clip
              </span>
              {grow}
              <span className="pill" style={{ height: 28, fontSize: 12 }}>
                <i />
                row 7 of 10
              </span>
            </div>
            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                alignItems: "center",
                gap: 10,
                rowGap: 14,
              }}
            >
              {(
                [
                  ["Inputs", "10 rows"],
                  ["Step", "4 stills"],
                  ["Judge", "keep 1"],
                  ["Step", "video"],
                  ["Output", "collection"],
                ] as const
              ).map(([name, note], i) => (
                <div key={i} style={{ display: "contents" }}>
                  {i > 0 && <span className="ar" />}
                  <div
                    className="sc"
                    style={
                      name === "Judge"
                        ? { outline: "2px solid var(--a1)", outlineOffset: 3 }
                        : undefined
                    }
                  >
                    {name}
                    <small>{note}</small>
                  </div>
                </div>
              ))}
            </div>
            <div style={wrapRow(10)}>
              <span
                className="pill"
                style={{ background: "color-mix(in srgb, var(--a2) 22%, var(--s2))" }}
              >
                fail → Write → retry, 2 at most
              </span>
              <span className="pill">Memory · your past picks</span>
            </div>
            <div
              style={{
                display: "flex",
                gap: 14,
                alignItems: "center",
                padding: "14px 16px",
                borderRadius: "18px 18px 18px 8px",
                background: "var(--in)",
              }}
            >
              {pic("sneaker-splash", "t2", { flex: "0 0 96px", minWidth: 0 })}
              <p style={{ fontSize: 13, lineHeight: "20px", color: "var(--t1b)" }}>
                Kept take 2. Shoe is sharp, no logo, horizon level.
              </p>
            </div>
          </div>
        </section>

        <section id="machines" className="wrap" style={{ ...col(40), paddingBottom: 112 }}>
          <div style={col(14)}>
            <span className="eb">
              <Ic name="chip" c="c5" small />
              Your machines
            </span>
            <h2 className="d h2">Runs where your GPUs are.</h2>
          </div>
          <div
            className="grid12"
            style={{ display: "grid", gridTemplateColumns: "repeat(12, 1fr)", gap: 24 }}
          >
            <article className="card" style={{ gridColumn: "span 4", minWidth: 0 }}>
              <Ic name="chip" c="c5" />
              <h3>Apple Silicon</h3>
              <p>MLX or PyTorch MPS, per model. Weights load straight into GPU memory.</p>
            </article>
            <article
              className="card"
              style={{ gridColumn: "span 5", minWidth: 0, borderRadius: "32px 12px 32px 32px" }}
            >
              <Ic name="servers" c="c2" />
              <h3>NVIDIA, one server or many</h3>
              <p>
                Add CUDA servers. Each job goes to one with the model and the room. In v1, CUDA runs
                Qwen Image; LTX video runs on Apple Silicon.
              </p>
            </article>
            <article
              className="card"
              style={{ gridColumn: "span 3", minWidth: 0, borderRadius: "32px 32px 12px 32px" }}
            >
              <Ic name="lock" c="c6" />
              <h3>Private per person</h3>
              <p>
                Your gallery, pipelines and memory are yours alone. Accounts come from the admin.
              </p>
            </article>
          </div>
        </section>

        <section
          id="models"
          className="wrap"
          style={{
            display: "flex",
            flexWrap: "wrap",
            alignItems: "center",
            gap: 56,
            paddingBottom: 112,
          }}
        >
          <pre className="code" style={{ flex: "1 1 520px", minWidth: 0 }}>
            <u># packs/qwen_image/model.yaml</u>
            {"\n"}
            <b>id</b>: qwen-image-2.1{"\n"}
            <b>family</b>: qwen-image{"\n"}
            <b>runtimes</b>:{"\n"}
            {"  torch: {env: torch, devices: [mps, cuda]}\n"}
            <b>variants</b>:{"\n"}
            {"  base:  {dir: Qwen-Image-2.1}\n"}
            {"  turbo: {dir: Qwen-Image-2.1-Turbo, shares: [text_encoder, vae]}\n"}
            <b>components</b>:{"\n"}
            {"  transformer: {precision: [bf16, int8]}\n"}
            <b>lora</b>: {"{component: transformer}"}
          </pre>
          <div style={{ flex: "1 1 380px", ...col(20) }}>
            <span className="eb">
              <Ic name="pack" c="c5" small />
              Models
            </span>
            <h2 className="d h2">A model is one pack.</h2>
            <p className="p">
              A YAML file and a small module per runtime. Its nodes and studio controls follow.
              Stack LoRAs and change their strength without reloading.
            </p>
            <div style={wrapRow(10)}>
              <span className="pill">Qwen Image 2.1</span>
              <span className="pill">LTX-2.5</span>
            </div>
          </div>
        </section>

        <section id="selfhost" className="wrap" style={{ paddingBottom: 96 }}>
          <div
            className="card"
            style={{
              flexDirection: "row",
              flexWrap: "wrap",
              alignItems: "center",
              gap: 48,
              padding: 56,
              borderRadius: "44px 44px 44px 16px",
              background: "var(--s2)",
              backgroundImage:
                "radial-gradient(700px 380px at 100% 0%, color-mix(in srgb, var(--a1) 16%, transparent), transparent 65%), linear-gradient(180deg, rgba(255,255,255,.05), rgba(255,255,255,0) 46%)",
            }}
          >
            <div style={{ flex: "1 1 380px", ...col(20) }}>
              <h2
                className="d"
                style={{
                  fontSize: 52,
                  lineHeight: "54px",
                  letterSpacing: "-.015em",
                  textWrap: "balance",
                }}
              >
                Two commands, then sign in.
              </h2>
              <p className="p">
                Got an account from your admin? Sign in. Hosting for a team? Set the admin account
                in .env, then start the stack.
              </p>
              <div style={wrapRow(14)}>
                <Link className="b1" href="/signin">
                  Sign in
                </Link>
              </div>
            </div>
            <pre className="code" style={{ flex: "1 1 380px", minWidth: 0 }}>
              <u># services, in containers</u>
              {"\n"}
              <b>$</b> docker compose up -d{"\n"}
              <u># GPU runners, native on each Mac</u>
              {"\n"}
              <b>$</b> make runners
            </pre>
          </div>
        </section>
      </main>

      <footer
        className="wrap"
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: 20,
          paddingBottom: 48,
          fontSize: 13,
          color: "var(--t2)",
        }}
      >
        <span style={{ display: "flex", alignItems: "center" }}>
          <Lockup />
        </span>
        <span>Self-hosted image and video studio</span>
        {grow}
        <label htmlFor="theme">Theme</label>
        <ThemeSelect
          id="theme"
          className="h-9 rounded-[10px] border border-t2 bg-s3 px-2.5 text-[13px] text-t1"
        />
      </footer>
    </div>
  );
}
