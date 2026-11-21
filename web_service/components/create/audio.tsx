"use client";
// Speech on the stage (docs/mock/CreateAudio.dc.html): the take's waveform with a playhead, play / pause, the time,
// a scrubber, Loop and the sound switch; and the shared "play one clip at a time" player the voice picker's play
// buttons use.
import { useCallback, useEffect, useRef, useState } from "react";
import { Pause, Play, Volume2, VolumeX } from "lucide-react";
import { clock } from "@/lib/runs";
import { ErrorNote } from "@/components/kit";

/** How many bars the stage's waveform draws. */
const BARS = 100;

/** Each bar's height (0–1): the loudest sample of its slice of the clip, against the loudest of all. */
export function peaksOf(samples: Float32Array, n = BARS): number[] {
  const per = Math.max(1, Math.floor(samples.length / n));
  const out = Array.from({ length: n }, (_, i) => {
    let m = 0;
    for (let j = i * per; j < Math.min(samples.length, (i + 1) * per); j++)
      m = Math.max(m, Math.abs(samples[j]));
    return m;
  });
  const top = Math.max(...out);
  return top > 0 ? out.map((x) => x / top) : out;
}

/** The clip's bars, once it is fetched and decoded; null until then (or where the browser cannot decode); `failed`
 * when the clip could not be fetched or decoded. */
function usePeaks(url?: string) {
  const [peaks, setPeaks] = useState<{ url: string; bars: number[] | null } | null>(null);
  useEffect(() => {
    if (!url || typeof AudioContext === "undefined") return;
    let stop = false;
    const ctx = new AudioContext();
    fetch(url)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.arrayBuffer();
      })
      .then((b) => ctx.decodeAudioData(b))
      .then((a) => !stop && setPeaks({ url, bars: peaksOf(a.getChannelData(0)) }))
      .catch(() => !stop && setPeaks({ url, bars: null }))
      .finally(() => void ctx.close().catch(() => undefined));
    return () => {
      stop = true;
    };
  }, [url]);
  const mine = peaks && peaks.url === url ? peaks : null;
  return { bars: mine?.bars ?? null, failed: !!mine && !mine.bars };
}

/** The stage's player of an audio take: the waveform (played bars in the accent, the playhead), then play / pause,
 * "0:03 / 0:07", the scrubber, Loop and the sound switch. */
export function WavePlayer({ url, duration }: { url?: string; duration?: number }) {
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState(0);
  const [len, setLen] = useState(duration ?? 0);
  const [loop, setLoop] = useState(false);
  const [muted, setMuted] = useState(false);
  const { bars, failed: noPeaks } = usePeaks(url);
  const [broken, setBroken] = useState(false);
  const at = len > 0 ? Math.min(1, pos / len) : 0;
  return (
    <div className="flex w-full max-w-[860px] flex-col items-center gap-3.5">
      <audio
        ref={ref}
        src={url}
        preload="metadata"
        loop={loop}
        muted={muted}
        onLoadedMetadata={(e) =>
          Number.isFinite(e.currentTarget.duration) && setLen(e.currentTarget.duration)
        }
        onTimeUpdate={(e) => setPos(e.currentTarget.currentTime)}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => setPlaying(false)}
        onError={() => setBroken(true)}
      />
      <div
        role="group"
        aria-label="Waveform of the take"
        className="relative flex h-[168px] w-full items-center gap-[2px] rounded-xl bg-s1 px-[2px]"
      >
        {Array.from({ length: BARS }, (_, i) => (
          <span
            key={i}
            aria-hidden
            className="min-w-[2px] flex-1 rounded-[2px]"
            style={{
              height: `${bars ? Math.max(3, Math.round(bars[i] * 94)) : 3}%`,
              background: pos > 0 && (i + 0.5) / BARS <= at ? "var(--a2)" : "var(--dash)",
            }}
          />
        ))}
        <span
          aria-hidden
          className="absolute top-1.5 bottom-1.5 -ml-px w-0.5 rounded-[1px] bg-t1"
          style={{ left: `${at * 100}%` }}
        />
      </div>
      <div className="flex w-full flex-wrap items-center justify-center gap-2.5">
        <button
          type="button"
          aria-label={playing ? "Pause" : "Play"}
          onClick={() => {
            const a = ref.current;
            if (a) void (a.paused ? a.play()?.catch(() => undefined) : a.pause());
          }}
          className="grid size-11 place-items-center rounded-full bg-t1 text-ground"
        >
          {playing ? (
            <Pause size={14} aria-hidden className="fill-current" />
          ) : (
            <Play size={14} aria-hidden className="fill-current" />
          )}
        </button>
        <span className="mono min-w-[92px] text-[13px] text-t1">
          {clock(pos)} / {clock(len)}
        </span>
        <label htmlFor="scrub" className="sr-only">
          Scrub the take
        </label>
        <input
          id="scrub"
          type="range"
          min={0}
          max={len || 0}
          step={0.01}
          value={Math.min(pos, len)}
          onChange={(e) => {
            const t = Number(e.target.value);
            setPos(t);
            if (ref.current) ref.current.currentTime = t;
          }}
          className="flex-[1_1_240px] accent-a2"
        />
        <button
          type="button"
          aria-pressed={loop}
          onClick={() => setLoop((l) => !l)}
          className="chip bevel rounded-[10px] aria-pressed:bg-t1 aria-pressed:font-semibold aria-pressed:text-panel"
        >
          Loop
        </button>
        <button
          type="button"
          aria-label={muted ? "Sound off" : "Volume"}
          aria-pressed={muted}
          title={muted ? "Sound off" : "Sound on"}
          onClick={() => setMuted((m) => !m)}
          className="chip bevel grid w-9 place-items-center rounded-[10px] p-0"
        >
          {muted ? <VolumeX size={16} aria-hidden /> : <Volume2 size={16} aria-hidden />}
        </button>
      </div>
      {broken ? (
        <ErrorNote>
          This take&apos;s sound could not be played. Reload the page to try again.
        </ErrorNote>
      ) : (
        noPeaks && <ErrorNote>The waveform could not be drawn; the take may still play.</ErrorNote>
      )}
    </div>
  );
}

/** One clip at a time: `toggle(id, url)` plays it (stopping any other) or stops it; `playing` is its id. */
export function usePlayer() {
  const ref = useRef<HTMLAudioElement | null>(null);
  const [playing, setPlaying] = useState<string | null>(null);
  useEffect(() => () => ref.current?.pause(), []);
  const toggle = useCallback(
    (id: string, url: string) => {
      ref.current?.pause();
      if (playing === id) return setPlaying(null);
      const a = new Audio(url);
      a.onended = () => setPlaying((p) => (p === id ? null : p));
      ref.current = a;
      setPlaying(id);
      void a.play()?.catch(() => setPlaying((p) => (p === id ? null : p)));
    },
    [playing],
  );
  return { playing, toggle };
}
