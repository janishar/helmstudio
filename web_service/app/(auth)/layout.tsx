import { ThemeSelect } from "@/components/kit";

/** Decorative take cards behind the sign-in card: [left, top, width, height, rotation, background, label]. */
const TAKES: [number, number, number, number, number, string, string][] = [
  [
    6,
    15,
    230,
    150,
    -8,
    "radial-gradient(120px 90px at 30% 30%, color-mix(in srgb, var(--a1) 85%, transparent), transparent 70%), linear-gradient(150deg, var(--a2), var(--s4))",
    "image",
  ],
  [
    17,
    43,
    170,
    220,
    6,
    "radial-gradient(110px 140px at 70% 25%, color-mix(in srgb, var(--a2) 80%, transparent), transparent 70%), linear-gradient(200deg, var(--s5), var(--a1))",
    "▶ clip",
  ],
  [
    5,
    75,
    150,
    110,
    -4,
    "linear-gradient(120deg, color-mix(in srgb, var(--a1) 70%, var(--s3)), var(--s2))",
    "take",
  ],
  [
    76,
    13,
    180,
    230,
    7,
    "radial-gradient(120px 120px at 30% 70%, color-mix(in srgb, var(--a1) 75%, transparent), transparent 70%), linear-gradient(160deg, var(--s4), var(--a2))",
    "image",
  ],
  [
    71,
    54,
    240,
    150,
    -6,
    "radial-gradient(140px 90px at 75% 35%, color-mix(in srgb, var(--a2) 75%, transparent), transparent 70%), linear-gradient(140deg, var(--a1), var(--s5))",
    "▶ clip",
  ],
  [
    85,
    79,
    130,
    130,
    10,
    "linear-gradient(210deg, color-mix(in srgb, var(--a2) 65%, var(--s3)), var(--s2))",
    "take",
  ],
];

/** The page before an account is known: floating takes, the theme select and the card. */
export default function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <main className="relative flex min-h-dvh items-center justify-center overflow-hidden p-6">
      <div className="absolute top-5 right-6 z-[2] flex items-center gap-2 rounded-full bg-s1 py-1.5 pr-1.5 pl-3.5">
        <label htmlFor="theme" className="label">
          Theme
        </label>
        <ThemeSelect
          id="theme"
          className="h-8 rounded-full border-0 bg-s3 px-2.5 text-[13px] text-t1"
        />
      </div>
      <div aria-hidden className="hidden md:block">
        {TAKES.map(([l, t, w, h, r, bg, tag], i) => (
          <div
            key={i}
            className="absolute flex items-end p-3"
            style={{
              left: `${l}%`,
              top: `${t}%`,
              width: w,
              height: h,
              transform: `rotate(${r}deg)`,
              background: bg,
              borderRadius: "28px 28px 28px 12px",
              boxShadow:
                "inset 0 1px 0 rgba(255,255,255,.25), inset 0 -6px 12px color-mix(in srgb, var(--shd) 30%, transparent), 0 34px 56px -20px color-mix(in srgb, var(--shd) 90%, transparent)",
            }}
          >
            <span
              className="mono rounded-full px-2 py-0.5 text-[10px] text-white"
              style={{ background: "color-mix(in srgb, var(--shd) 45%, transparent)" }}
            >
              {tag}
            </span>
          </div>
        ))}
      </div>
      {children}
    </main>
  );
}
