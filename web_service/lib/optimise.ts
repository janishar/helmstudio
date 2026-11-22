// Optimised uploads (contracts/README.md "Optimised uploads", plan 20-h3-options-and-uploads 24.5,
// docs/mock/UploadOptimise.dc.html): before an image or a wav uploads, the browser makes it smaller. Images: scaled to
// the registry's cap only when over it, then WebP 0.9 (JPEG where WebP can't be made; alpha kept, PNG then); the
// original when nothing was scaled and the new file is not 10 % smaller. A wav: AAC-LC in an m4a via WebCodecs (the
// MP4 box is written here: one audio track). Anything the browser can't do uploads as it is.
import type { S } from "@/lib/api";

export type Limits = S<"UploadLimits">;
/** What uploads, its type and the tile's mono line ("12.4 → 1.9 MB · WebP", "as is"). */
export type Optimised = { blob: Blob; type: string; line: string };
/** The optimising step's report: a fraction (audio) and a line (an image's size once decoded). */
export type Step = { f?: number; line?: string };

const mb = (n: number) => {
  const m = n / 1_048_576;
  return m < 0.1 ? m.toFixed(2) : m.toFixed(1);
};
/** "12.4 → 1.9 MB". */
export const sizes = (from: number, to: number) => `${mb(from)} → ${mb(to)} MB`;

/** An image's size with the long side capped at `max` (pure). */
export function imageSize(width: number, height: number, max: number) {
  const k = Math.min(1, max / Math.max(width, height));
  return {
    width: k < 1 ? Math.max(1, Math.round(width * k)) : width,
    height: k < 1 ? Math.max(1, Math.round(height * k)) : height,
    scaled: k < 1,
  };
}

/** The type an image is encoded to (pure): WebP, else PNG to keep alpha, else JPEG. */
export const imageType = (alpha: boolean, webp: boolean) =>
  webp ? "image/webp" : alpha ? "image/png" : "image/jpeg";

/** Whether the original uploads instead (pure): nothing scaled and the new file not at least 10 % smaller. */
export const keepOriginal = (scaled: boolean, original: number, made: number) =>
  !scaled && made > original * 0.9;

const NAMES: Record<string, string> = {
  "image/webp": "WebP",
  "image/jpeg": "JPEG",
  "image/png": "PNG",
};
/** "12.4 → 1.9 MB · WebP", ", transparent" when the alpha was kept. */
export const imageLine = (from: number, to: number, type: string, alpha: boolean) =>
  `${sizes(from, to)} · ${NAMES[type] ?? type}${alpha ? ", transparent" : ""}`;

/** The AAC rate for a source rate (pure): 44.1 and 48 kHz as they are, any other resampled to 48 kHz. */
export const aacRate = (rate: number) => (rate === 44_100 || rate === 48_000 ? rate : 48_000);
/** The AAC bitrate: 256 kb/s stereo, 192 mono. */
export const aacBitrate = (channels: number) => (channels === 1 ? 192_000 : 256_000);

/** A wav's rate and channels from its fmt chunk, or null when it is not a RIFF/WAVE. */
export function wavFormat(data: ArrayBuffer): { rate: number; channels: number } | null {
  const v = new DataView(data);
  const tag = (at: number) => String.fromCharCode(...new Uint8Array(data, at, 4));
  if (data.byteLength < 12 || tag(0) !== "RIFF" || tag(8) !== "WAVE") return null;
  for (
    let at = 12;
    at + 8 <= data.byteLength;
    at += 8 + v.getUint32(at + 4, true) + (v.getUint32(at + 4, true) & 1)
  )
    if (tag(at) === "fmt " && at + 16 <= data.byteLength)
      return { channels: v.getUint16(at + 10, true), rate: v.getUint32(at + 12, true) };
  return null;
}

/** The optimised file for an upload of `type` (an upload type). `onStep` reports the optimising step; a stop signal
 * aborts it (an AbortError); a failure to decode or encode gives the original. */
export async function optimise(
  file: Blob,
  type: string,
  limits: Limits,
  onStep: (s: Step) => void,
  signal?: AbortSignal,
): Promise<Optimised> {
  const asIs = (line = "as is"): Optimised => ({ blob: file, type, line });
  try {
    if (type.startsWith("image/"))
      return (await image(file, type, limits, onStep, signal)) ?? asIs();
    if (type === "audio/wav")
      return (await wav(file, onStep, signal)) ?? asIs("as is · this browser can't make m4a");
  } catch (e) {
    if (signal?.aborted) throw e;
  }
  return asIs(); // mp3 and m4a: already small
}

async function image(
  file: Blob,
  type: string,
  limits: Limits,
  onStep: (s: Step) => void,
  signal?: AbortSignal,
): Promise<Optimised | null> {
  if (typeof OffscreenCanvas === "undefined" || typeof createImageBitmap === "undefined")
    return null;
  const bmp = await createImageBitmap(file, { imageOrientation: "from-image" });
  try {
    onStep({ line: `${bmp.width} × ${bmp.height}` });
    const p = imageSize(bmp.width, bmp.height, limits.image_max_side);
    const canvas = new OffscreenCanvas(p.width, p.height);
    const g = canvas.getContext("2d", { willReadFrequently: true });
    if (!g) return null;
    g.imageSmoothingQuality = "high";
    g.drawImage(bmp, 0, 0, p.width, p.height);
    const alpha = type !== "image/jpeg" && transparent(g, p.width, p.height);
    signal?.throwIfAborted();
    // without a WebP encoder (Safari) the canvas gives a PNG
    let blob = await canvas.convertToBlob({ type: "image/webp", quality: 0.9 });
    const want = imageType(alpha, blob.type === "image/webp");
    if (blob.type !== want) blob = await canvas.convertToBlob({ type: want, quality: 0.9 });
    signal?.throwIfAborted();
    if (keepOriginal(p.scaled, file.size, blob.size)) return null;
    return { blob, type: blob.type, line: imageLine(file.size, blob.size, blob.type, alpha) };
  } finally {
    bmp.close();
  }
}

/** Any pixel not opaque, read in bands so a large canvas is never copied whole. */
function transparent(g: OffscreenCanvasRenderingContext2D, width: number, height: number) {
  for (let y = 0; y < height; y += 256) {
    const d = g.getImageData(0, y, width, Math.min(256, height - y)).data;
    for (let i = 3; i < d.length; i += 4) if (d[i] < 255) return true;
  }
  return false;
}

async function wav(
  file: Blob,
  onStep: (s: Step) => void,
  signal?: AbortSignal,
): Promise<Optimised | null> {
  if (typeof AudioEncoder === "undefined" || typeof OfflineAudioContext === "undefined")
    return null;
  const data = await file.arrayBuffer();
  const fmt = wavFormat(data);
  // ponytail: more than two channels upload as they are; a downmix if surround wavs ever matter
  if (!fmt || fmt.channels < 1 || fmt.channels > 2) return null;
  const ch = fmt.channels;
  const rate = aacRate(fmt.rate);
  const config = {
    codec: "mp4a.40.2",
    sampleRate: rate,
    numberOfChannels: ch,
    bitrate: aacBitrate(ch),
  };
  if (!(await AudioEncoder.isConfigSupported(config)).supported) return null;
  // decodeAudioData resamples to the context's rate
  const buf = await new OfflineAudioContext(ch, 1, rate).decodeAudioData(data);
  const line = `${sizes(file.size, (buf.duration * config.bitrate) / 8)} · m4a`;
  onStep({ f: 0, line });
  const frames: Uint8Array[] = [];
  let failed: unknown = null;
  const enc = new AudioEncoder({
    output: (c) => {
      const b = new Uint8Array(c.byteLength);
      c.copyTo(b);
      frames.push(b);
      const f = (c.timestamp + (c.duration ?? 0)) / 1e6 / buf.duration;
      onStep({ f: Math.max(0, Math.min(1, f)), line });
    },
    error: (e) => {
      failed = e;
    },
  });
  try {
    enc.configure(config);
    for (let at = 0; at < buf.length && !failed; at += rate) {
      signal?.throwIfAborted();
      const n = Math.min(rate, buf.length - at);
      const planar = new Float32Array(n * ch);
      for (let c = 0; c < ch; c++) planar.set(buf.getChannelData(c).subarray(at, at + n), c * n);
      const a = new AudioData({
        format: "f32-planar",
        sampleRate: rate,
        numberOfFrames: n,
        numberOfChannels: ch,
        timestamp: Math.round((at * 1e6) / rate),
        data: planar,
      });
      enc.encode(a);
      a.close();
      while (enc.encodeQueueSize > 4) await new Promise((r) => setTimeout(r, 5));
    }
    signal?.throwIfAborted();
    await enc.flush();
  } finally {
    if (enc.state !== "closed") enc.close();
  }
  if (failed || !frames.length) return null;
  const blob = muxM4a(frames, rate, ch);
  return { blob, type: "audio/mp4", line: `${sizes(file.size, blob.size)} · m4a` };
}

// ---------------------------------------------------------------- the m4a box (ISO 14496-12 / -14, one AAC track)

type Bytes = number[] | Uint8Array;
const u32 = (n: number) => [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255];
const u16 = (n: number) => [(n >>> 8) & 255, n & 255];
const ascii = (s: string) => Array.from(s, (c) => c.charCodeAt(0));
function box(type: string, ...parts: Bytes[]): Uint8Array {
  const size = 8 + parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(size);
  out.set([...u32(size), ...ascii(type)]);
  let at = 8;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}
const full = (type: string, flags: number, ...parts: Bytes[]) => box(type, u32(flags), ...parts); // version 0
const MATRIX = [0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000].flatMap(u32);
const zeros = (n: number) => new Array<number>(n).fill(0);
const descriptor = (tag: number, ...parts: number[][]) => {
  const body = parts.flat();
  return [tag, body.length, ...body]; // every one here is under 128 bytes
};

/** An m4a of raw AAC-LC frames (1024 samples each, as WebCodecs gives them): ftyp, moov (one chunk, faststart),
 * mdat. */
export function muxM4a(frames: Uint8Array[], rate: number, channels: number): Blob {
  const n = frames.length;
  const bytes = frames.reduce((s, f) => s + f.length, 0);
  const duration = n * 1024;
  const freq = [
    96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000,
  ].indexOf(rate);
  const asc = [(2 << 3) | (freq >> 1), ((freq & 1) << 7) | (channels << 3)]; // AudioSpecificConfig: AAC LC
  const bitrate = Math.round((bytes * 8 * rate) / duration);
  const esds = full(
    "esds",
    0,
    descriptor(
      3,
      [...u16(1), 0],
      descriptor(4, [0x40, 0x15, 0, 0, 0, ...u32(bitrate), ...u32(bitrate)], descriptor(5, asc)),
      descriptor(6, [2]),
    ),
  );
  const mp4a = box(
    "mp4a",
    zeros(6),
    u16(1),
    zeros(8),
    u16(channels),
    u16(16),
    zeros(4),
    u32(rate * 65536),
    esds,
  );
  const ftyp = box("ftyp", ascii("M4A "), u32(512), ascii("M4A isommp42"));
  const moov = (offset: number) =>
    box(
      "moov",
      full(
        "mvhd",
        0,
        zeros(8),
        u32(rate),
        u32(duration),
        u32(0x10000),
        u16(0x100),
        zeros(10),
        MATRIX,
        zeros(24),
        u32(2),
      ),
      box(
        "trak",
        full(
          "tkhd",
          3,
          zeros(8),
          u32(1),
          zeros(4),
          u32(duration),
          zeros(8),
          zeros(4),
          u16(0x100),
          zeros(2),
          MATRIX,
          zeros(8),
        ),
        box(
          "mdia",
          full("mdhd", 0, zeros(8), u32(rate), u32(duration), u16(0x55c4), zeros(2)),
          full("hdlr", 0, zeros(4), ascii("soun"), zeros(12), ascii("SoundHandler\0")),
          box(
            "minf",
            full("smhd", 0, zeros(4)),
            box("dinf", full("dref", 0, u32(1), full("url ", 1))),
            box(
              "stbl",
              full("stsd", 0, u32(1), mp4a),
              full("stts", 0, u32(1), u32(n), u32(1024)),
              full("stsc", 0, u32(1), u32(1), u32(n), u32(1)),
              full(
                "stsz",
                0,
                u32(0),
                u32(n),
                frames.flatMap((f) => u32(f.length)),
              ),
              full("stco", 0, u32(1), u32(offset)),
            ),
          ),
        ),
      ),
    );
  const head = moov(0).length; // the offset's own width never changes the size
  const m = moov(ftyp.length + head + 8);
  return new Blob(
    [
      ftyp,
      m,
      new Uint8Array(u32(8 + bytes)),
      new Uint8Array(ascii("mdat")),
      ...frames,
    ] as BlobPart[],
    {
      type: "audio/mp4",
    },
  );
}
