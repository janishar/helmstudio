// Magic's own marks (MagicBoard): the joins' bowtie set the owner picked (Dissolve a bowtie, Cut a line, Fade a half
// bowtie), Wipe and Morph, and a block kind's lucide glyph for the Add dialog's orbs and the menus.
import {
  AudioLines,
  Clapperboard,
  Image as ImageIcon,
  MessagesSquare,
  Mic,
  Music,
  Pencil,
  type LucideIcon,
} from "lucide-react";
import type { Kind, TransitionKind } from "@/lib/magic";

type P = { className?: string; size?: number };
const Svg = ({ className, size = 14, children }: P & { children: React.ReactNode }) => (
  <svg
    aria-hidden
    className={className}
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.2"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    {children}
  </svg>
);

export const CutIcon = (p: P) => (
  <Svg {...p}>
    <path d="M12 3v18" />
  </Svg>
);
export const DissolveIcon = (p: P) => (
  <Svg {...p}>
    <path d="M3 5 21 19V5L3 19Z" />
  </Svg>
);
export const FadeIcon = (p: P) => (
  <Svg {...p}>
    <path d="M3 19 21 5v14z" />
  </Svg>
);
export const WipeIcon = (p: P) => (
  <Svg {...p}>
    <rect x="3" y="5" width="18" height="14" rx="2" />
    <path d="M14 5 10 19" />
  </Svg>
);
export const MorphIcon = (p: P) => (
  <Svg {...p}>
    <path d="M11 2.8a1 1 0 0 1 2 0l1 5.6a2 2 0 0 0 1.6 1.6l5.6 1a1 1 0 0 1 0 2l-5.6 1a2 2 0 0 0-1.6 1.6l-1 5.6a1 1 0 0 1-2 0l-1-5.6a2 2 0 0 0-1.6-1.6l-5.6-1a1 1 0 0 1 0-2l5.6-1a2 2 0 0 0 1.6-1.6z" />
  </Svg>
);

export const JOIN_ICON: Record<TransitionKind, (p: P) => React.ReactNode> = {
  cut: CutIcon,
  dissolve: DissolveIcon,
  fade: FadeIcon,
  wipe: WipeIcon,
  morph: MorphIcon,
};

/** A kind's glyph (Dialogue is a Voice block with turns, its own glyph in Add). */
export const KIND_ICON: Record<Kind | "dialogue", LucideIcon> = {
  video: Clapperboard,
  image: ImageIcon,
  voice: Mic,
  dialogue: MessagesSquare,
  music: Music,
  sound: AudioLines,
  frame: Pencil,
};

/** A kind's name as the board writes it on a block's badge. */
export const KIND_NAME: Record<Kind, string> = {
  video: "Video",
  image: "Image",
  voice: "Voice",
  music: "Music",
  sound: "Sound",
  frame: "Board",
};

const BARS = [
  5, 8.6, 6.6, 9.2, 9.6, 2.8, 2.2, 12, 5.2, 4.8, 14, 7.6, 12, 7.8, 9.8, 3.8, 9.6, 12.4, 8.4, 11, 10,
  2.8, 11.2, 9.2, 5.6, 2.4, 12.4, 7.8, 10.6, 12.6, 10.6, 13, 6.8, 11.6, 7.4, 13.2, 12.6, 3.2, 3.8,
  4.6, 13.6,
];

/** A waveform's bars every 3 px across `width`, one speech-like shape for every sound (not the real audio, as the
 * Timeline's tiles). */
export function Wave({
  color,
  width,
  height = 16,
}: {
  color: string;
  width: number;
  height?: number;
}) {
  const n = Math.max(4, Math.floor(width / 3));
  return (
    <svg
      className="wvf"
      aria-hidden
      viewBox={`0 0 ${n * 3} 16`}
      preserveAspectRatio="none"
      style={{ height }}
    >
      <path
        d={Array.from({ length: n }, (_, i) => {
          const h = BARS[i % BARS.length];
          return `M${i * 3 + 1} ${8 - h / 2}V${8 + h / 2}`;
        }).join("")}
        stroke={color}
        strokeWidth="1.6"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** The tie mark's glyph (a link). */
export const LinkGlyph = () => (
  <svg
    width="10"
    height="10"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden
  >
    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
  </svg>
);
