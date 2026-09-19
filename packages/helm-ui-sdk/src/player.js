// <helm-player asset="as_01J…" fps="24" autoplay>
//
// A frame-accurate transport (04 §5): arrow keys step one frame, the frame
// counter is authoritative and the timecode is derived from it, A/B compare
// between two assets on one transport, loop and speed, and Extract frame,
// which draws the current frame to a canvas, posts it through the client and
// emits the new asset id — what turns "find the right frame" into a two-click
// anchor instead of a file hunt.
//
// Not in M6b (docs/decisions.md M6 Q13): filmstrip scrubbing from a sprite
// sheet, the waveform under the scrubber, and playing an h264 proxy when the
// source codec is not browser-decodable. All three need ffmpeg on the daemon's
// side, so they render Unsupported with a one-line reason rather than being
// absent (04 §9).
//
// Structural interface (04 §5, M6 Q12):
//
//   assets.read(id, { range })                          → a fetch Response
//   assets.upload(blob, contentType, { kind, filename }) → the new Asset
//
// The component never builds a URL. It asks the client for the bytes with a
// one-byte range, takes the URL the client resolved off the response and gives
// that to the media element, which is how a <video> loads an asset it cannot
// send a header for (M6 Q10).

import { HelmElement, define, el, kindOf, message, timecode } from "./base.js";

/** Why the ffmpeg-dependent parts are not here, in the words the panel shows. */
export const NEEDS_FFMPEG = "Filmstrip, waveform and playback of codecs this browser cannot decode need ffmpeg on the daemon, which this build does not have.";

const styles = `
  :host { display: block; }
  .stage {
    position: relative;
    background: var(--helm-ground-inset);
    display: flex; align-items: center; justify-content: center;
    min-height: 160px;
  }

  /* Full screen: the picture takes the screen and the transport sits under
   * it. Without this the media keeps its intrinsic size, so a take made at
   * 800x448 stayed 800x448 in the middle of a display's worth of ground.
   *
   * The prefixed selectors are written out rather than joined by commas: a
   * pseudo-class a browser does not know invalidates the whole selector list
   * it appears in, which would take the unprefixed rule down with it.
   */
  :host(:fullscreen) { height: 100vh; }
  :host(:fullscreen) .panel { display: flex; flex-direction: column; height: 100%; min-height: 0; }
  :host(:fullscreen) .stage { flex: 1; min-height: 0; }
  :host(:fullscreen) .stage > video, :host(:fullscreen) .stage > img {
    width: 100%; height: 100%; object-fit: contain;
  }
  :host(:-webkit-full-screen) { height: 100vh; }
  :host(:-webkit-full-screen) .panel { display: flex; flex-direction: column; height: 100%; min-height: 0; }
  :host(:-webkit-full-screen) .stage { flex: 1; min-height: 0; }
  :host(:-webkit-full-screen) .stage > video, :host(:-webkit-full-screen) .stage > img {
    width: 100%; height: 100%; object-fit: contain;
  }
  .stage .empty { color: var(--helm-log-text); }
  .stage .empty .why { color: var(--helm-log-muted); }
  video, audio, img { display: block; max-width: 100%; max-height: 60vh; }
  audio { width: 100%; }
  .strip {
    display: flex; align-items: center; gap: var(--helm-space-2);
    padding: var(--helm-space-1) var(--helm-space-3);
    border-top: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    color: var(--helm-text-muted);
    font: var(--helm-type-micro);
  }
  .scrub {
    width: 100%;
    accent-color: var(--helm-accent-base);
  }
  .transport {
    display: flex; align-items: center; gap: var(--helm-space-2);
    padding: var(--helm-space-2) var(--helm-space-3);
    border-top: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    flex-wrap: wrap;
  }
  .tc {
    font: var(--helm-type-mono);
    font-variant-numeric: tabular-nums;
    color: var(--helm-text-primary);
  }
  .frame { color: var(--helm-text-muted); }
  select {
    font: var(--helm-type-micro);
    height: var(--helm-control-sm);
    color: var(--helm-text-secondary);
    background: var(--helm-ground-page);
    border: 1px solid var(--helm-border-strong);
    border-radius: var(--helm-radius-sm);
  }
  .ab { color: var(--helm-text-muted); font: var(--helm-type-micro); }
`;

export class HelmPlayer extends HelmElement {
  static get observedAttributes() {
    return ["asset", "fps", "compare"];
  }

  // `autoplay` is read when the media mounts rather than observed: it says
  // what a newly shown asset does, not something to react to later.

  constructor() {
    super(styles);
    this.frame = 0;
    this.urls = [];
    this.showing = "a";
    this.build();
  }

  get fps() {
    const n = Number(this.getAttribute("fps"));
    return Number.isFinite(n) && n > 0 ? n : 24;
  }

  build() {
    this.media = null;
    this.stage = el("div", { class: "stage", part: "stage" });

    this.scrub = el("input", {
      type: "range", class: "scrub", min: "0", max: "0", step: "1", value: "0",
      "aria-label": "Position, in frames",
      oninput: () => this.seekFrame(Number(this.scrub.value)),
    });
    this.strip = el("div", { class: "strip", part: "filmstrip", role: "status" },
      el("span", { text: "Filmstrip and waveform" }),
      el("span", { class: "spacer" }),
      el("span", { part: "unsupported", text: "Unsupported — " + NEEDS_FFMPEG }));

    this.playBtn = el("button", { part: "play", text: "Play", onclick: () => this.toggle() });
    this.backBtn = el("button", { text: "◀ Frame", "aria-label": "Back one frame", onclick: () => this.step(-1) });
    this.fwdBtn = el("button", { text: "Frame ▶", "aria-label": "Forward one frame", onclick: () => this.step(1) });
    this.tc = el("span", { class: "tc", part: "timecode", text: "00:00:00:00" });
    this.frameText = el("span", { class: "tc frame", part: "frame", text: "f 0" });
    this.loopBtn = el("button", { text: "Loop", "aria-pressed": "false", onclick: () => this.toggleLoop() });
    this.speed = el("select", { "aria-label": "Speed", onchange: () => { if (this.media) this.media.playbackRate = Number(this.speed.value); } },
      ...["0.25", "0.5", "1", "1.5", "2"].map((v) => el("option", { value: v, text: v + "×", selected: v === "1" })));
    this.abBtn = el("button", { text: "A / B", hidden: true, onclick: () => this.swap() });
    this.abText = el("span", { class: "ab", hidden: true, text: "A" });
    this.extractBtn = el("button", { part: "extract", text: "Extract frame", onclick: () => this.extract() });

    this.transport = el("div", { class: "transport", part: "transport" },
      this.playBtn, this.backBtn, this.fwdBtn, this.tc, this.frameText,
      el("span", { class: "spacer" }),
      this.abBtn, this.abText, this.loopBtn, this.speed, this.extractBtn);

    this.mountPanel(el("div", { class: "panel" }, this.stage, this.scrub, this.strip, this.transport));

    this.addEventListener("keydown", (e) => {
      if (e.key === "ArrowLeft") { e.preventDefault(); this.step(e.shiftKey ? -this.fps : -1); }
      if (e.key === "ArrowRight") { e.preventDefault(); this.step(e.shiftKey ? this.fps : 1); }
      if (e.key === " ") { e.preventDefault(); this.toggle(); }
    });
    this.tabIndex = 0;
  }

  connectedCallback() {
    this.reload();
  }

  attributeChangedCallback(name, before, after) {
    if (before !== after && this.isConnected) this.reload();
  }

  disconnectedCallback() {
    this.stopClock();
    for (const u of this.urls.splice(0)) URL.revokeObjectURL(u);
    super.disconnectedCallback();
  }

  async reload() {
    if (!this.needClient(this.region)) return;
    const id = this.getAttribute("asset");
    const compare = this.getAttribute("compare");
    this.abBtn.hidden = !compare;
    this.abText.hidden = !compare;
    if (!id) {
      this.stage.replaceChildren(el("div", { class: "empty", part: "empty" },
        el("p", { text: "No asset" }),
        el("p", { class: "why", text: "Set the asset attribute to something to play." })));
      return;
    }
    const generation = (this.generation = (this.generation || 0) + 1);
    try {
      this.sources = { a: await this.sourceFor(id), b: compare ? await this.sourceFor(compare) : null };
      if (generation !== this.generation || !this.live) return;
      this.mount(this.sources[this.showing] || this.sources.a);
    } catch (err) {
      if (generation !== this.generation || !this.live) return;
      this.fail(this.region, err, kindOf(err) === "NotFound"
        ? "The bytes for this take aren't where helmstudio left them — they may have been moved or deleted in Finder."
        : "That asset could not be read.");
    }
  }

  /**
   * sourceFor asks the client for one byte of the asset and keeps the URL the
   * client resolved. The body is cancelled at once: the point of the request
   * is the address, which the media element then fetches for itself with the
   * ranges it wants.
   */
  async sourceFor(id) {
    const res = await this.client.assets.read(id, { range: "bytes=0-0" });
    if (res.body && typeof res.body.cancel === "function") {
      try {
        await res.body.cancel();
      } catch {
        // A cancelled body that was already consumed is not a problem.
      }
    }
    return { id, url: res.url, type: (res.headers && res.headers.get && res.headers.get("Content-Type")) || "" };
  }

  mount(source) {
    this.stopClock();
    const audio = source.type.startsWith("audio/");
    const image = source.type.startsWith("image/");
    const tag = image ? "img" : audio ? "audio" : "video";
    const media = el(tag, image
      ? { src: source.url, alt: "" }
      : { src: source.url, controls: false, preload: "metadata", playsinline: true });
    this.media = image ? null : media;
    this.stage.replaceChildren(media);
    this.transport.hidden = image;
    this.scrub.hidden = image;
    this.strip.hidden = image;
    if (image) return;

    media.playbackRate = Number(this.speed.value);
    media.loop = this.loopBtn.getAttribute("aria-pressed") === "true";
    // A host that says autoplay means "start when it is shown". The browser
    // may still refuse — no user activation, or sound without a gesture — and
    // a refusal is not an error here: the transport is right there.
    if (this.hasAttribute("autoplay")) media.play().catch(() => {});
    media.addEventListener("loadedmetadata", () => this.onMeta());
    media.addEventListener("play", () => { this.playBtn.textContent = "Pause"; this.startClock(); });
    media.addEventListener("pause", () => { this.playBtn.textContent = "Play"; this.stopClock(); this.readFrame(); });
    media.addEventListener("ended", () => { this.playBtn.textContent = "Play"; this.stopClock(); });
    media.addEventListener("seeked", () => this.readFrame());
    media.addEventListener("error", () => {
      // A codec this browser cannot decode is exactly what the h264 proxy
      // would have answered, so it says that rather than failing silently.
      this.stage.replaceChildren(el("div", { class: "empty", part: "empty", role: "status" },
        el("p", { text: "This browser cannot play these bytes" }),
        el("p", { class: "why", text: NEEDS_FFMPEG })));
    });
  }

  onMeta() {
    const duration = this.media.duration;
    const frames = Number.isFinite(duration) ? Math.max(0, Math.round(duration * this.fps) - 1) : 0;
    this.scrub.max = String(frames);
    this.readFrame();
  }

  /**
   * startClock follows the frames the compositor actually presented, which is
   * what makes the counter authoritative rather than a division of currentTime.
   */
  startClock() {
    const media = this.media;
    if (!media) return;
    if (typeof media.requestVideoFrameCallback === "function") {
      const tick = (_now, meta) => {
        if (this.media !== media || media.paused) return;
        this.setFrame(Math.round((meta.mediaTime || media.currentTime) * this.fps));
        this.vfc = media.requestVideoFrameCallback(tick);
      };
      this.vfc = media.requestVideoFrameCallback(tick);
      return;
    }
    // Audio, and any browser without the callback: the clock is all there is,
    // so the counter is derived rather than observed. It says the same thing
    // for a file whose frames land on the grid, and drifts by less than one
    // frame for one that does not.
    const tick = () => {
      if (this.media !== media || media.paused) return;
      this.readFrame();
      this.raf = requestAnimationFrame(tick);
    };
    this.raf = requestAnimationFrame(tick);
  }

  stopClock() {
    if (this.vfc && this.media && typeof this.media.cancelVideoFrameCallback === "function") {
      this.media.cancelVideoFrameCallback(this.vfc);
    }
    if (this.raf) cancelAnimationFrame(this.raf);
    this.vfc = this.raf = null;
  }

  readFrame() {
    if (this.media) this.setFrame(Math.round(this.media.currentTime * this.fps));
  }

  setFrame(frame) {
    this.frame = Math.max(0, frame);
    this.tc.textContent = timecode(this.frame, this.fps);
    this.frameText.textContent = "f " + this.frame;
    this.scrub.value = String(this.frame);
  }

  /** seekFrame puts the playhead in the middle of a frame, never on its edge. */
  seekFrame(frame) {
    if (!this.media) return;
    this.frame = Math.max(0, frame);
    this.media.currentTime = (this.frame + 0.5) / this.fps;
    this.setFrame(this.frame);
  }

  step(by) {
    if (!this.media) return;
    if (!this.media.paused) this.media.pause();
    this.seekFrame(this.frame + by);
  }

  toggle() {
    if (!this.media) return;
    if (this.media.paused) this.media.play().catch(() => {});
    else this.media.pause();
  }

  toggleLoop() {
    const on = this.loopBtn.getAttribute("aria-pressed") !== "true";
    this.loopBtn.setAttribute("aria-pressed", String(on));
    if (this.media) this.media.loop = on;
  }

  /** swap shows the other asset at the same frame, which is what compare means. */
  swap() {
    if (!this.sources || !this.sources.b) return;
    const frame = this.frame;
    this.showing = this.showing === "a" ? "b" : "a";
    this.abText.textContent = this.showing.toUpperCase();
    this.mount(this.sources[this.showing]);
    const media = this.media;
    if (media) media.addEventListener("loadedmetadata", () => this.seekFrame(frame), { once: true });
  }

  async extract() {
    const media = this.media;
    if (!media || !media.videoWidth) return;
    this.extractBtn.disabled = true;
    try {
      const canvas = el("canvas", { width: media.videoWidth, height: media.videoHeight });
      canvas.getContext("2d").drawImage(media, 0, 0);
      const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/png"));
      const asset = await this.client.assets.upload(blob, "image/png", {
        kind: "image",
        filename: `frame-${String(this.frame).padStart(6, "0")}.png`,
        width: canvas.width,
        height: canvas.height,
      });
      this.dispatchEvent(new CustomEvent("frame-extracted", {
        detail: { asset: asset.id, frame: this.frame, item: asset },
        bubbles: true,
        composed: true,
      }));
      this.extractBtn.textContent = "Extracted";
    } catch (err) {
      this.extractBtn.textContent = message(err, "Extract failed");
    } finally {
      this.extractBtn.disabled = false;
      setTimeout(() => { this.extractBtn.textContent = "Extract frame"; }, 1500);
    }
  }
}

define("helm-player", HelmPlayer);
