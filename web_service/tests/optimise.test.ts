// The upload optimiser's decisions (lib/optimise.ts). jsdom has no canvas encoder or WebCodecs: the image path runs on
// fakes of createImageBitmap and OffscreenCanvas; the m4a box is checked by reading it back.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  aacBitrate,
  aacRate,
  imageLine,
  imageSize,
  imageType,
  keepOriginal,
  muxM4a,
  optimise,
  wavFormat,
} from "@/lib/optimise";

const LIMITS = { image_max_side: 8192 };

describe("the decisions", () => {
  it("scales only a long side over the cap", () => {
    expect(imageSize(6000, 4000, 8192)).toEqual({ width: 6000, height: 4000, scaled: false });
    expect(imageSize(12000, 3000, 8192)).toEqual({ width: 8192, height: 2048, scaled: true });
    expect(imageSize(3000, 16384, 4096)).toEqual({ width: 750, height: 4096, scaled: true });
  });
  it("encodes WebP, else PNG for alpha, else JPEG", () => {
    expect(imageType(true, true)).toBe("image/webp");
    expect(imageType(true, false)).toBe("image/png");
    expect(imageType(false, false)).toBe("image/jpeg");
  });
  it("keeps the original unless scaled or at least 10 % smaller", () => {
    expect(keepOriginal(false, 1000, 901)).toBe(true);
    expect(keepOriginal(false, 1000, 900)).toBe(false);
    expect(keepOriginal(true, 1000, 2000)).toBe(false);
  });
  it("says the sizes and the type, in decimal MB (the Gallery's rule)", () => {
    expect(imageLine(12_400_000, 1_900_000, "image/webp", false)).toBe("12.4 → 1.9 MB · WebP");
    expect(imageLine(3_200_000, 900_000, "image/webp", true)).toBe(
      "3.2 → 0.9 MB · WebP, transparent",
    );
  });
  it("encodes AAC at 44.1 or 48 kHz, resamples any other rate, 256 kb/s stereo, 192 mono", () => {
    expect([
      aacRate(44_100),
      aacRate(48_000),
      aacRate(16_000),
      aacRate(24_000),
      aacRate(96_000),
    ]).toEqual([44_100, 48_000, 48_000, 48_000, 48_000]);
    expect([aacBitrate(2), aacBitrate(1)]).toEqual([256_000, 192_000]);
  });
  it("reads a wav's rate and channels past other chunks", () => {
    const b = new Uint8Array(56);
    const v = new DataView(b.buffer);
    b.set(
      [..."RIFF"].map((c) => c.charCodeAt(0)),
      0,
    );
    b.set(
      [..."WAVE"].map((c) => c.charCodeAt(0)),
      8,
    );
    b.set(
      [..."LIST"].map((c) => c.charCodeAt(0)),
      12,
    );
    v.setUint32(16, 3, true); // an odd chunk: padded to 4
    b.set(
      [..."fmt "].map((c) => c.charCodeAt(0)),
      24,
    );
    v.setUint32(28, 16, true);
    v.setUint16(34, 1, true);
    v.setUint32(36, 24_000, true);
    expect(wavFormat(b.buffer)).toEqual({ rate: 24_000, channels: 1 });
    expect(wavFormat(new TextEncoder().encode("ID3\x03").buffer)).toBeNull();
  });
});

describe("muxM4a", () => {
  it("writes ftyp M4A, a moov whose one chunk points at the frames in mdat", async () => {
    const frames = [new Uint8Array([1, 2, 3]), new Uint8Array([4, 5]), new Uint8Array([6])];
    const blob = muxM4a(frames, 48_000, 2);
    expect(blob.type).toBe("audio/mp4");
    const b = new Uint8Array(await blob.arrayBuffer());
    const v = new DataView(b.buffer);
    const at = (s: string) => {
      for (let i = 0; i + 4 <= b.length; i++)
        if (String.fromCharCode(...b.subarray(i, i + 4)) === s) return i - 4; // the box starts at its size
      return -1;
    };
    const tag = (i: number) => String.fromCharCode(...b.subarray(i + 4, i + 8));
    // top level: ftyp, moov, mdat, sizes adding up to the file
    let i = 0;
    const top: string[] = [];
    while (i < b.length) {
      top.push(tag(i));
      i += v.getUint32(i);
    }
    expect(top).toEqual(["ftyp", "moov", "mdat"]);
    expect(i).toBe(b.length);
    expect(String.fromCharCode(...b.subarray(8, 12))).toBe("M4A ");
    const stsz = at("stsz");
    expect([
      v.getUint32(stsz + 16),
      v.getUint32(stsz + 20),
      v.getUint32(stsz + 24),
      v.getUint32(stsz + 28),
    ]).toEqual([3, 3, 2, 1]);
    const offset = v.getUint32(at("stco") + 16);
    expect([...b.subarray(offset, offset + 6)]).toEqual([1, 2, 3, 4, 5, 6]);
    const mdhd = at("mdhd");
    expect([v.getUint32(mdhd + 20), v.getUint32(mdhd + 24)]).toEqual([48_000, 3 * 1024]);
    // AudioSpecificConfig: AAC LC (2), 48 kHz (index 3), 2 channels
    const dsi = b.indexOf(0x05, at("esds") + 12);
    expect([...b.subarray(dsi, dsi + 4)]).toEqual([0x05, 2, 0x11, 0x90]);
  });
});

describe("optimise", () => {
  afterEach(() => vi.unstubAllGlobals());
  const png = (size: number) => new Blob([new Uint8Array(size)], { type: "image/png" });

  /** createImageBitmap and OffscreenCanvas faking a w × h decode; `webp` false: the canvas gives a PNG instead
   * (Safari); `sizes`: the encoded size by type; `alpha`: a transparent pixel. */
  function fakeCanvas(
    w: number,
    h: number,
    o: { webp?: boolean; alpha?: boolean; sizes?: Record<string, number> },
  ) {
    const made: { width: number; height: number }[] = [];
    vi.stubGlobal("createImageBitmap", async () => ({ width: w, height: h, close: () => {} }));
    vi.stubGlobal(
      "OffscreenCanvas",
      class {
        constructor(
          public width: number,
          public height: number,
        ) {
          made.push({ width, height });
        }
        getContext() {
          return {
            drawImage: () => {},
            getImageData: (_x: number, _y: number, cw: number, ch: number) => ({
              data: new Uint8ClampedArray(cw * ch * 4).fill(255).fill(o.alpha ? 0 : 255, 3, 4),
            }),
          };
        }
        async convertToBlob({ type }: { type: string }) {
          const t = type === "image/webp" && o.webp === false ? "image/png" : type;
          return new Blob([new Uint8Array(o.sizes?.[t] ?? 10)], { type: t });
        }
      },
    );
    return made;
  }

  it("makes WebP, scaled to the cap, with its line", async () => {
    const made = fakeCanvas(12000, 6000, { sizes: { "image/webp": 2_000_000 } });
    const steps: unknown[] = [];
    const out = await optimise(png(13_000_000), "image/png", LIMITS, (s) => steps.push(s));
    expect(made).toEqual([{ width: 8192, height: 4096 }]);
    expect(out.type).toBe("image/webp");
    expect(out.line).toBe("13.0 → 2.0 MB · WebP");
    expect(steps).toEqual([{ line: "12000 × 6000" }]);
  });

  it("keeps alpha: PNG where WebP can't be made; JPEG for an opaque one", async () => {
    fakeCanvas(100, 100, { webp: false, alpha: true, sizes: { "image/png": 100 } });
    const t = await optimise(png(1000), "image/png", LIMITS, () => {});
    expect([t.type, t.line]).toEqual(["image/png", "0.00 → 0.00 MB · PNG, transparent"]);
    fakeCanvas(100, 100, { webp: false, sizes: { "image/jpeg": 100 } });
    expect((await optimise(png(1000), "image/png", LIMITS, () => {})).type).toBe("image/jpeg");
  });

  it("uploads the original when not 10 % smaller, or when the browser can't", async () => {
    fakeCanvas(100, 100, { sizes: { "image/webp": 950 } });
    const f = png(1000);
    expect(await optimise(f, "image/png", LIMITS, () => {})).toEqual({
      blob: f,
      type: "image/png",
      line: "as is",
    });
    vi.unstubAllGlobals(); // jsdom: no OffscreenCanvas, no AudioEncoder
    expect((await optimise(f, "image/png", LIMITS, () => {})).blob).toBe(f);
    const wav = new Blob(["RIFF"], { type: "audio/wav" });
    expect(await optimise(wav, "audio/wav", LIMITS, () => {})).toEqual({
      blob: wav,
      type: "audio/wav",
      line: "as is · this browser can't make m4a",
    });
    const mp3 = new Blob(["ID3"]);
    expect((await optimise(mp3, "audio/mpeg", LIMITS, () => {})).blob).toBe(mp3);
  });

  it("stops on the signal instead of falling back", async () => {
    fakeCanvas(100, 100, {});
    const stop = new AbortController();
    stop.abort();
    await expect(optimise(png(1000), "image/png", LIMITS, () => {}, stop.signal)).rejects.toThrow();
  });
});
