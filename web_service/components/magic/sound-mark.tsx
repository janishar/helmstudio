// A music, sound or voice block's mark (plan 39 phase H; the design system's card looks): music an album cover (its
// gradient, a vinyl disc), sound a sky orb, voice an amber orb. By the shown take's class (MagicTake.sound), else the
// block's kind, so a sound placed on the Music lane still shows as a sound.
import type { Block, TakeInfo } from "@/lib/magic";

export type SoundLook = "music" | "sound" | "voice";

/** The look of block b with its shown take: the take's class, else the block's kind (null: not audio). */
export function soundLook(b: Pick<Block, "kind">, take?: TakeInfo): SoundLook | null {
  if (take?.kind === "audio" && take.sound) return take.sound;
  return b.kind === "music" || b.kind === "sound" || b.kind === "voice" ? b.kind : null;
}

export function SoundMark({
  look,
  art,
  large,
}: {
  look: SoundLook;
  art?: string;
  large?: boolean;
}) {
  if (look === "music")
    return (
      <span
        className={`mcv ${large ? "lg" : ""}`}
        aria-hidden
        style={{ background: art ?? "var(--s5)" }}
      >
        <span className="disc" />
      </span>
    );
  const size = large ? 28 : 12;
  return (
    <span className={`mo ${look === "voice" ? "k8v" : "k8s"} ${large ? "lg" : ""}`} aria-hidden>
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {look === "voice" ? (
          <>
            <path d="M12 19v3" />
            <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
            <rect x="9" y="2" width="6" height="13" rx="3" />
          </>
        ) : (
          <path d="M2 10v3M6 6v11M10 3v18M14 8v7M18 5v13M22 10v3" />
        )}
      </svg>
    </span>
  );
}
