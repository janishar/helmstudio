import type { CSSProperties } from "react";
import Link from "next/link";
import { ThemeSelect } from "@/components/kit";
import "./landing.css";

/** Icon paths from the board, one per room or topic. */
const ICONS = {
  logo: (
    <>
      <circle cx="12" cy="12" r="6" />
      <circle cx="12" cy="12" r="1.5" />
      <path d="M12 2v4M12 18v4M2 12h4M18 12h4M5 5l2.8 2.8M16.2 16.2 19 19M19 5l-2.8 2.8M7.8 16.2 5 19" />
    </>
  ),
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
const strip = (t: string, flex: number) => (
  <div className={`tk ${t}`} style={{ flex, aspectRatio: "auto", height: 46 }} />
);

/** Route /welcome, shown at / to a signed-out visitor (proxy.ts): the landing page (docs/mock/Landing.dc.html). */
export default function Landing() {
  return (
    <div className="lp">
      <header className="wrap">
        <nav aria-label="Site" className="top">
          <a href="#top" style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <Ic name="logo" c="c5" small />
            <span className="d" style={{ fontSize: 19 }}>
              helmstudio
            </span>
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
                A fox crossing a snowfield at dusk, <span style={{ color: "var(--a1)" }}>@fox</span>
                , long lens
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 10 }}>
                <div className="tk t1" />
                <div
                  className="tk t2"
                  style={{ outline: "2px solid var(--a1)", outlineOffset: 2 }}
                />
                <div className="tk t3" />
                <div className="tk t4" />
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
                <div className="tk t3" />
                <div className="tk t1" />
                <div className="tk t4" />
                <div className="tk t2" />
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
                {strip("t1", 3)}
                {strip("t4", 2)}
                {strip("t3", 4)}
                {strip("t2", 2)}
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
              <div className="tk t2" style={{ flex: "0 0 96px" }} />
              <p style={{ fontSize: 13, lineHeight: "20px", color: "var(--t1b)" }}>
                Kept take 2. Fox is in frame, no extra limbs, horizon level.
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
        <span style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <Ic name="logo" c="c5" small />
          <span className="d" style={{ fontSize: 16, color: "var(--t1)" }}>
            helmstudio
          </span>
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
