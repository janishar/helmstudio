"use client";
// Edit (docs/mock/TakeActions.dc.html Edit an image take, Edit a video take, Edit an audio take, Saved as new;
// alpha.2 item 11; contracts/README.md, Plugins: takes, The Gallery's Edit): the Media tools plugin's edits of the
// selected take. Save as new runs its tool for you (POST /v1/studio/plugins/media-tools/tools/{tool}); every edit is a
// new take, the original stays. Edit shows only while the plugin is on and running.
import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { Chip } from "@/components/gallery/gallery";
import { GalleryPicker } from "@/components/gallery/picker";
import { api, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { Range } from "@/components/range";

type Take = S<"Take">;
type Input = Record<string, unknown>;

export const MEDIA_TOOLS = "media-tools";
const TOOL = { image: "edit_image", video: "edit_video", audio: "edit_audio" } as const;

/** Media tools' tools while the plugin is on and running, else null (Edit hidden). */
export function useMediaTools(): string[] | null {
  const [tools, setTools] = useState<string[] | null>(null);
  useEffect(() => {
    let stop = false;
    api
      .GET("/v1/studio/plugins/{plugin}", { params: { path: { plugin: MEDIA_TOOLS } } })
      .then(({ data }) => !stop && setTools(data?.running ? data.tools : null))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, []);
  return tools;
}

/** Whether the take's kind has its edit tool. */
export const canEdit = (tools: string[] | null, t: Take) => !!tools?.includes(TOOL[t.kind]);

/** Save as new: the tool run for the signed-in user; the new take. */
export async function saveAsNew(tool: string, input: Input): Promise<Take> {
  const done = await ok(
    api.POST("/v1/studio/plugins/{plugin}/tools/{tool}", {
      params: { path: { plugin: MEDIA_TOOLS, tool } },
      body: { input },
    }),
  );
  if (!done.take_ids[0]) throw new Error("it made no take");
  return ok(
    api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: done.take_ids[0] } } }),
  );
}

/** 0:00.90 (`d` decimals of the seconds). */
export const clock = (s: number, d = 2) =>
  `${Math.floor(s / 60)}:${(s % 60).toFixed(d).padStart(d + 3, "0")}`;
const r2 = (s: number) => Math.round(s * 100) / 100;

/** The Edit panel of a take: its kind's tabs, then Cancel and Save as new. */
export function EditPanel({
  take: t,
  saving,
  error,
  onCancel,
  onSave,
}: {
  take: Take;
  saving: boolean;
  /** why the last Save as new failed, else "" */
  error: string;
  onCancel: () => void;
  /** runs the tool (the Gallery shows Saving…, then the new take or Couldn't save) */
  onSave: (tool: string, input: Input) => void;
}) {
  const [call, setCall] = useState<[string, Input] | null>(null);
  const edit =
    t.kind === "image" ? (
      <ImageEdit take={t} onCall={setCall} />
    ) : t.kind === "video" ? (
      <VideoEdit take={t} onCall={setCall} />
    ) : (
      <AudioEdit take={t} onCall={setCall} />
    );
  return (
    <>
      {edit}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel h-10 bg-a1 px-[18px] font-semibold text-on1 disabled:opacity-50"
          disabled={!call || saving}
          onClick={() => call && onSave(...call)}
        >
          Save as new
        </button>
      </div>
      {error && (
        <div
          role="alert"
          className="rounded-xl bg-[color-mix(in_srgb,#ff6b6b_18%,var(--s2))] px-3 py-2.5 text-[13px] text-t1"
        >
          Couldn&apos;t save · {error}
        </div>
      )}
    </>
  );
}

/** The tab chips under the Edit heading: the pressed one is lit. */
function Tabs<T extends string>({
  tabs,
  tab,
  onTab,
}: {
  tabs: readonly T[];
  tab: T;
  onTab: (t: T) => void;
}) {
  return (
    <>
      <h2 className="heading">Edit</h2>
      <div className="flex flex-wrap gap-1.5" role="group" aria-label="Edit">
        {tabs.map((x) => (
          <button
            key={x}
            type="button"
            aria-pressed={x === tab}
            onClick={() => onTab(x)}
            className="chip bevel aria-pressed:bg-a1 aria-pressed:font-semibold aria-pressed:text-on1"
          >
            {x}
          </button>
        ))}
      </div>
    </>
  );
}

/** A label and its mono value (the board's .row). */
function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-2.5 text-[13px]" aria-label={label}>
      <span>{label}</span>
      <span className="mono text-right text-[12px] text-t2">{value}</span>
    </div>
  );
}

function Num({
  label,
  value,
  onChange,
  max,
}: {
  label: string;
  value: number | null;
  onChange: (v: number | null) => void;
  max: number;
}) {
  return (
    <label className="flex items-center gap-2 text-[13px]">
      {label}
      <input
        type="number"
        min={label === "Pixels per side" ? 0 : 1}
        max={max}
        value={value ?? ""}
        onChange={(e) => {
          const v = e.target.valueAsNumber;
          onChange(Number.isFinite(v) ? Math.min(max, Math.max(0, Math.round(v))) : null);
        }}
        className="in mono h-8 w-24"
      />
    </label>
  );
}

// --- image

type Box = { x: number; y: number; width: number; height: number };
const RATIOS = [
  ["Free", 0],
  ["16:9", 16 / 9],
  ["9:16", 9 / 16],
  ["1:1", 1],
  ["4:3", 4 / 3],
] as const;
const IMAGE_TABS = ["Crop", "Resize", "Rotate", "Flip", "Extend", "Convert"] as const;
const FORMATS = [
  ["png", "PNG"],
  ["jpeg", "JPEG"],
  ["webp", "WebP"],
] as const;

/** The largest box of `ratio` (0: the whole picture) centred on a w × h picture. */
export function fitBox(w: number, h: number, ratio: number): Box {
  if (!ratio) return { x: 0, y: 0, width: w, height: h };
  const width = Math.min(w, Math.round(h * ratio));
  const height = Math.min(h, Math.round(width / ratio));
  return { x: Math.round((w - width) / 2), y: Math.round((h - height) / 2), width, height };
}

/** The box dragged from a to b on a w × h picture, of `ratio` (0: free), inside the picture. */
export function dragBox(
  a: [number, number],
  b: [number, number],
  w: number,
  h: number,
  ratio: number,
): Box {
  const sx = b[0] >= a[0] ? 1 : -1;
  const sy = b[1] >= a[1] ? 1 : -1;
  const roomX = sx > 0 ? w - a[0] : a[0];
  const roomY = sy > 0 ? h - a[1] : a[1];
  let bw = Math.min(Math.abs(b[0] - a[0]), roomX);
  let bh = Math.min(Math.abs(b[1] - a[1]), roomY);
  if (ratio) {
    bw = Math.min(Math.max(bw, bh * ratio), roomX, roomY * ratio);
    bh = bw / ratio;
  }
  bw = Math.max(1, Math.round(bw));
  bh = Math.max(1, Math.round(bh));
  return {
    x: Math.round(sx > 0 ? a[0] : a[0] - bw),
    y: Math.round(sy > 0 ? a[1] : a[1] - bh),
    width: bw,
    height: bh,
  };
}

function ImageEdit({
  take: t,
  onCall,
}: {
  take: Take;
  onCall: (c: [string, Input] | null) => void;
}) {
  const w = t.width ?? 1;
  const h = t.height ?? 1;
  const [tab, setTab] = useState<(typeof IMAGE_TABS)[number]>("Crop");
  const [ratio, setRatio] = useState(0);
  const [crop, setCrop] = useState<Box>(fitBox(w, h, 0));
  const [size, setSize] = useState<{ width?: number; height?: number }>({});
  const [rotate, setRotate] = useState<90 | 180 | 270 | null>(null);
  const [flip, setFlip] = useState<"horizontal" | "vertical" | null>(null);
  const [pad, setPad] = useState<number | null>(null);
  const [color, setColor] = useState("#000000");
  const [clear, setClear] = useState(false);
  const [format, setFormat] = useState<string | null>(null);
  const pic = useRef<HTMLDivElement>(null);
  const from = useRef<[number, number] | null>(null);
  const cropped = crop.width !== w || crop.height !== h;

  const input: Input = { take_id: t.id };
  if (cropped) input.crop = crop;
  if (size.width || size.height) input.resize = size;
  if (rotate) input.rotate = rotate;
  if (flip) input.flip = flip;
  if (pad)
    input.extend = {
      top: pad,
      right: pad,
      bottom: pad,
      left: pad,
      color: clear ? "transparent" : color,
    };
  if (format) input.format = format;
  const key = JSON.stringify(Object.keys(input).length > 1 ? ["edit_image", input] : null);
  useEffect(() => {
    onCall(JSON.parse(key));
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps

  const at = (e: React.PointerEvent): [number, number] => {
    const r = pic.current!.getBoundingClientRect();
    return [
      Math.min(w, Math.max(0, ((e.clientX - r.left) / (r.width || 1)) * w)),
      Math.min(h, Math.max(0, ((e.clientY - r.top) / (r.height || 1)) * h)),
    ];
  };
  const cw = cropped ? crop.width : w;
  const ch = cropped ? crop.height : h;
  const label = RATIOS.find(([, v]) => v === ratio)?.[0] ?? "Free";
  const row: Record<(typeof IMAGE_TABS)[number], string> = {
    Crop: cropped
      ? `${label} · ${crop.width} × ${crop.height} at ${crop.x}, ${crop.y}`
      : "The whole picture",
    Resize: size.width
      ? `${size.width} × ${Math.round((size.width * ch) / cw)}`
      : size.height
        ? `${Math.round((size.height * cw) / ch)} × ${size.height}`
        : `${cw} × ${ch} (as it is)`,
    Rotate: rotate ? `${rotate}° clockwise` : "None",
    Flip: flip === "horizontal" ? "Across" : flip === "vertical" ? "Down" : "None",
    Extend: pad ? `${pad} px per side · ${clear ? "transparent" : color}` : "None",
    Convert: format
      ? FORMATS.find(([f]) => f === format)![1]
      : `As it is (${t.content_type.split("/")[1]?.toUpperCase()})`,
  };
  return (
    <>
      <div className="relative aspect-video shrink-0 overflow-hidden rounded-xl">
        <div
          ref={pic}
          className="absolute inset-0 m-auto touch-none bg-cover bg-center"
          style={{
            aspectRatio: `${w} / ${h}`,
            height: w / h < 16 / 9 ? "100%" : undefined,
            width: w / h >= 16 / 9 ? "100%" : undefined,
            backgroundImage: `url("${safeUrl(t.url)}")`,
          }}
          role="img"
          aria-label={t.title}
          onPointerDown={(e) => {
            if (tab !== "Crop") return;
            from.current = at(e);
            e.currentTarget.setPointerCapture?.(e.pointerId);
          }}
          onPointerMove={(e) => {
            if (from.current) setCrop(dragBox(from.current, at(e), w, h, ratio));
          }}
          onPointerUp={() => (from.current = null)}
        >
          {cropped && (
            <div
              aria-label="Crop box"
              className="pointer-events-none absolute rounded-[4px] border-2 border-a1 shadow-[0_0_0_999px_color-mix(in_srgb,var(--g)_55%,transparent)]"
              style={{
                left: `${(crop.x / w) * 100}%`,
                top: `${(crop.y / h) * 100}%`,
                width: `${(crop.width / w) * 100}%`,
                height: `${(crop.height / h) * 100}%`,
              }}
            />
          )}
        </div>
      </div>
      <Tabs tabs={IMAGE_TABS} tab={tab} onTab={setTab} />
      <Row label={tab} value={row[tab]} />
      <div className="flex flex-wrap items-center gap-1.5">
        {tab === "Crop" &&
          RATIOS.map(([name, v]) => (
            <Chip
              key={name}
              pressed={ratio === v}
              onClick={() => {
                setRatio(v);
                setCrop(fitBox(w, h, v));
              }}
            >
              {name}
            </Chip>
          ))}
        {tab === "Resize" && (
          <>
            <Num
              label="Width"
              max={8192}
              value={size.width ?? null}
              onChange={(v) => setSize(v ? { width: v } : {})}
            />
            <Num
              label="Height"
              max={8192}
              value={size.height ?? null}
              onChange={(v) => setSize(v ? { height: v } : {})}
            />
          </>
        )}
        {tab === "Rotate" &&
          ([90, 180, 270] as const).map((d) => (
            <Chip key={d} pressed={rotate === d} onClick={() => setRotate(rotate === d ? null : d)}>
              {d}°
            </Chip>
          ))}
        {tab === "Flip" &&
          (
            [
              ["horizontal", "Across"],
              ["vertical", "Down"],
            ] as const
          ).map(([f, name]) => (
            <Chip key={f} pressed={flip === f} onClick={() => setFlip(flip === f ? null : f)}>
              {name}
            </Chip>
          ))}
        {tab === "Extend" && (
          <>
            <Num label="Pixels per side" max={4096} value={pad} onChange={setPad} />
            <input
              type="color"
              aria-label="Colour"
              value={color}
              disabled={clear}
              onChange={(e) => setColor(e.target.value)}
              className="h-8 w-10 rounded-lg"
            />
            <Chip pressed={clear} onClick={() => setClear(!clear)}>
              Transparent
            </Chip>
          </>
        )}
        {tab === "Convert" &&
          FORMATS.map(([f, name]) => (
            <Chip key={f} pressed={format === f} onClick={() => setFormat(format === f ? null : f)}>
              {name}
            </Chip>
          ))}
      </div>
    </>
  );
}

// --- the trim bar (video over its frames, audio over its waveform)

/** A range of a `len` s take: drag an end of the box to trim; elsewhere moves the playhead. */
export function TrimBar({
  len,
  span,
  onSpan,
  at,
  onSeek,
  style,
  height,
}: {
  len: number;
  span: [number, number];
  onSpan: (s: [number, number]) => void;
  at?: number;
  onSeek?: (t: number) => void;
  style: React.CSSProperties;
  height: number;
}) {
  const bar = useRef<HTMLDivElement>(null);
  const end = useRef<0 | 1 | null>(null);
  const time = (e: React.PointerEvent) => {
    const r = bar.current!.getBoundingClientRect();
    return r2(Math.min(len, Math.max(0, ((e.clientX - r.left) / (r.width || 1)) * len)));
  };
  const move = (e: React.PointerEvent) => {
    const t = time(e);
    if (end.current === 0) onSpan([Math.min(t, span[1] - 0.1), span[1]]);
    else if (end.current === 1) onSpan([span[0], Math.max(t, span[0] + 0.1)]);
  };
  const pct = (t: number) => `${(t / (len || 1)) * 100}%`;
  return (
    <div
      ref={bar}
      aria-label="Trim"
      className="relative shrink-0 touch-none overflow-hidden rounded-lg bg-s2"
      style={{ height, ...style }}
      onPointerDown={(e) => {
        const t = time(e);
        const near = len * 0.04;
        end.current = Math.abs(t - span[0]) <= near ? 0 : Math.abs(t - span[1]) <= near ? 1 : null;
        if (end.current === null) onSeek?.(t);
        else e.currentTarget.setPointerCapture?.(e.pointerId);
      }}
      onPointerMove={move}
      onPointerUp={() => (end.current = null)}
    >
      <div
        className="pointer-events-none absolute inset-y-0 rounded-lg border-2 border-a1 bg-[color-mix(in_srgb,var(--a1)_14%,transparent)]"
        style={{ left: pct(span[0]), right: `calc(100% - ${pct(span[1])})` }}
      />
      {at !== undefined && (
        <div
          className="pointer-events-none absolute inset-y-0 w-0.5 bg-t1"
          style={{ left: pct(at) }}
        />
      )}
    </div>
  );
}

// --- video

const VIDEO_TABS = ["Trim", "Speed", "Mute", "New sound", "Frame as image"] as const;
const SPEEDS = [0.25, 0.5, 1, 1.5, 2, 4];

function VideoEdit({
  take: t,
  onCall,
}: {
  take: Take;
  onCall: (c: [string, Input] | null) => void;
}) {
  const len = t.video?.duration_s ?? 0;
  const [tab, setTab] = useState<(typeof VIDEO_TABS)[number]>("Trim");
  const [span, setSpan] = useState<[number, number]>([0, len]);
  const [at, setAt] = useState(0);
  const [speed, setSpeed] = useState(1);
  const [mute, setMute] = useState(false);
  const [sound, setSound] = useState<Take | null>(null);
  const [picking, setPicking] = useState(false);
  const player = useRef<HTMLVideoElement>(null);
  const trimmed = span[0] > 0 || span[1] < len;

  const input: Input = { take_id: t.id };
  if (trimmed) input.trim = { start: span[0], end: span[1] };
  if (speed !== 1) input.speed = speed;
  if (mute) input.mute = true;
  if (sound) input.audio_take_id = sound.id;
  const call: [string, Input] | null =
    tab === "Frame as image"
      ? ["frame_image", { take_id: t.id, at }]
      : Object.keys(input).length > 1
        ? ["edit_video", input]
        : null;
  const key = JSON.stringify(call);
  useEffect(() => {
    onCall(JSON.parse(key));
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps

  const seek = (s: number) => {
    setAt(s);
    if (player.current) player.current.currentTime = s;
  };
  const part = r2((span[1] - span[0]) / speed);
  const row: Record<(typeof VIDEO_TABS)[number], [string, string]> = {
    Trim: ["Trim", `${clock(span[0])} – ${clock(span[1])} · ${r2(span[1] - span[0]).toFixed(2)} s`],
    Speed: ["Speed", `${speed}× · ${part.toFixed(2)} s`],
    Mute: ["Sound", mute ? "Off" : "As it is"],
    "New sound": ["New sound", sound ? sound.title : "None"],
    "Frame as image": ["Frame", `at ${clock(at)}`],
  };
  return (
    <>
      <div className="flex aspect-video shrink-0 items-center justify-center overflow-hidden rounded-xl">
        <video
          ref={player}
          src={safeUrl(t.url) ?? undefined}
          poster={safeUrl(t.thumb_url) ?? undefined}
          muted
          playsInline
          aria-label={t.title}
          className="max-h-full max-w-full rounded-[10px] object-contain"
          onClick={(e) =>
            void (e.currentTarget.paused ? e.currentTarget.play() : e.currentTarget.pause())
          }
          onTimeUpdate={(e) => setAt(r2(e.currentTarget.currentTime))}
        />
      </div>
      <TrimBar
        len={len}
        span={span}
        onSpan={setSpan}
        at={at}
        onSeek={seek}
        height={36}
        style={{
          backgroundImage: `url("${safeUrl(t.thumb_url)}")`,
          backgroundSize: "auto 100%",
          backgroundRepeat: "repeat-x",
        }}
      />
      <Row label={row[tab][0]} value={row[tab][1]} />
      <Tabs tabs={VIDEO_TABS} tab={tab} onTab={setTab} />
      <div className="flex flex-wrap items-center gap-1.5">
        {tab === "Speed" &&
          SPEEDS.map((s) => (
            <Chip key={s} pressed={speed === s} onClick={() => setSpeed(s)}>
              {s}×
            </Chip>
          ))}
        {tab === "Mute" && (
          <Chip
            pressed={mute}
            onClick={() => {
              setMute(!mute);
              setSound(null);
            }}
          >
            Sound off
          </Chip>
        )}
        {tab === "New sound" &&
          (sound ? (
            <span className="chip bevel flex items-center gap-1.5">
              {sound.title}
              <button type="button" aria-label="No new sound" onClick={() => setSound(null)}>
                <X size={12} aria-hidden />
              </button>
            </span>
          ) : (
            <button type="button" className="chip bevel" onClick={() => setPicking(true)}>
              Pick an audio take…
            </button>
          ))}
        {tab === "Frame as image" && (
          <span className="text-[12px] text-t2">Saves the frame under the playhead.</span>
        )}
      </div>
      <GalleryPicker
        kind="audio"
        open={picking}
        onClose={() => setPicking(false)}
        max={1}
        title="New sound"
        onPick={([s]) => {
          setPicking(false);
          if (s) {
            setSound(s);
            setMute(false);
          }
        }}
      />
    </>
  );
}

// --- audio

const AUDIO_TABS = ["Trim", "Volume", "Join"] as const;
const WAVE: React.CSSProperties = {
  background:
    "repeating-linear-gradient(90deg, var(--ln) 0 2px, transparent 2px 6px) center / 100% 48px no-repeat",
};

function AudioEdit({
  take: t,
  onCall,
}: {
  take: Take;
  onCall: (c: [string, Input] | null) => void;
}) {
  const len = t.audio?.duration_s ?? 0;
  const [tab, setTab] = useState<(typeof AUDIO_TABS)[number]>("Trim");
  const [span, setSpan] = useState<[number, number]>([0, len]);
  const [db, setDb] = useState(0);
  const [join, setJoin] = useState<Take[]>([t]);
  const [picking, setPicking] = useState(false);
  const drag = useRef<number | null>(null);
  const trimmed = span[0] > 0 || span[1] < len;

  const input: Input = { take_id: t.id };
  if (trimmed) input.trim = { start: span[0], end: span[1] };
  if (db) input.volume_db = db;
  const call: [string, Input] | null =
    tab === "Join"
      ? join.length >= 2
        ? ["join_audio", { take_ids: join.map((x) => x.id) }]
        : null
      : Object.keys(input).length > 1
        ? ["edit_audio", input]
        : null;
  const key = JSON.stringify(call);
  useEffect(() => {
    onCall(JSON.parse(key));
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps

  const dB = db < 0 ? `−${-db} dB` : db > 0 ? `+${db} dB` : "0 dB";
  return (
    <>
      <TrimBar
        len={len}
        span={span}
        onSpan={setSpan}
        height={96}
        style={{ ...WAVE, border: "1px dashed var(--dash)", borderRadius: 12 }}
      />
      {tab === "Join" ? (
        <Row label="Join" value={`${join.length} sounds in order`} />
      ) : (
        <Row label="Trim · volume" value={`${clock(span[0], 1)} – ${clock(span[1], 1)} · ${dB}`} />
      )}
      <Tabs tabs={AUDIO_TABS} tab={tab} onTab={setTab} />
      {tab === "Volume" && (
        <label className="flex items-center gap-2 text-[13px]">
          Volume
          <Range
            min={-30}
            max={20}
            step={1}
            value={db}
            onChange={(e) => setDb(e.target.valueAsNumber)}
            className="grow"
          />
          <span className="mono w-14 text-right text-[12px] text-t2">{dB}</span>
        </label>
      )}
      {tab === "Join" && (
        <>
          <ol className="m-0 flex list-none flex-col gap-1 p-0" aria-label="Join in order">
            {join.map((x, i) => (
              <li
                key={x.id}
                draggable
                onDragStart={() => (drag.current = i)}
                onDragOver={(e) => e.preventDefault()}
                onDrop={() => {
                  const from = drag.current;
                  drag.current = null;
                  if (from === null || from === i) return;
                  const next = [...join];
                  next.splice(i, 0, ...next.splice(from, 1));
                  setJoin(next);
                }}
                className="flex cursor-grab items-center gap-2 rounded-lg bg-s2 px-2.5 py-1.5 text-[13px]"
              >
                <span className="mono text-[12px] text-t2">{i + 1}</span>
                <span className="grow truncate">{x.title}</span>
                {x.audio && (
                  <span className="mono text-[12px] text-t2">
                    {x.audio.duration_s.toFixed(1)} s
                  </span>
                )}
                <button
                  type="button"
                  aria-label={`Leave out ${x.title}`}
                  onClick={() => setJoin(join.filter((y) => y.id !== x.id))}
                >
                  <X size={12} aria-hidden />
                </button>
              </li>
            ))}
          </ol>
          {join.length < 8 && (
            <button
              type="button"
              className="chip bevel self-start"
              onClick={() => setPicking(true)}
            >
              Add audio takes…
            </button>
          )}
          <span className="text-[12px] text-t2">
            Pick 2–8 audio takes, drag to order; they play one after another.
          </span>
        </>
      )}
      <GalleryPicker
        kind="audio"
        open={picking}
        onClose={() => setPicking(false)}
        max={8 - join.length}
        title="Join audio"
        onPick={(more) => {
          setPicking(false);
          setJoin([...join, ...more.filter((m) => !join.some((x) => x.id === m.id))].slice(0, 8));
        }}
      />
    </>
  );
}

/** The Gallery's toast (board Saved as new): Saving…, then the new take with Undo. */
export function SavedToast({
  state,
  onUndo,
}: {
  state: { saving: true } | { saving: false; title: string };
  onUndo: () => void;
}) {
  return (
    <div
      role="status"
      className="fixed bottom-5 left-1/2 z-50 flex w-[min(92vw,420px)] -translate-x-1/2 items-center gap-2.5 rounded-xl bg-s3 px-3 py-2.5 text-[13px] shadow-lg"
    >
      {state.saving ? (
        <>
          <span className="inline-block size-2 rounded-full bg-[#f2c14e]" aria-hidden />
          <span className="grow">Saving…</span>
        </>
      ) : (
        <>
          <span className="grow">Saved as a new take · {state.title}</span>
          <button type="button" className="chip bevel h-7 text-[12px]" onClick={onUndo}>
            Undo
          </button>
        </>
      )}
    </div>
  );
}
