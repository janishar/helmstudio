// The sequence document, as the editor reasons about it (docs/decisions.md
// M8 Q7, Q8, Q10).
//
// The daemon owns the document and its rules. This module does not replace
// them: every write goes to the daemon, and what comes back — snapped, laid out
// and checked — is what the editor draws next. What this module is for is the
// time between a drag starting and that answer arriving, when the editor has to
// show where a clip would go, snapped the way the daemon will snap it, without
// asking. So the arithmetic here mirrors internal/timeline exactly — the same
// exact ratios for NTSC rates, and the same rounding — and a test compares the
// two.
//
// Everything here is a pure function over plain objects. Nothing touches the
// DOM, a client or a clock, which is what lets the editor's decisions be tested
// without a browser playing anything.

/** The rates a target may declare, as the exact ratios they are (M8 Q7). */
export const RATES = new Map([
  [23.976, [24000, 1001]],
  [24, [24, 1]],
  [25, [25, 1]],
  [29.97, [30000, 1001]],
  [30, [30, 1]],
  [48, [48, 1]],
  [50, [50, 1]],
  [59.94, [60000, 1001]],
  [60, [60, 1]],
]);

/** ratio is the exact rate behind a target's fps, or null for one it is not. */
export function ratio(fps) {
  for (const [decimal, r] of RATES) {
    if (Math.abs(decimal - fps) < 1e-6) return r;
  }
  return null;
}

/**
 * round rounds half away from zero, as Go's math.Round does. JavaScript's
 * Math.round rounds half towards positive infinity, and the two disagree on a
 * negative half — which a drag to the left produces.
 */
export function round(x) {
  return Math.sign(x) * Math.round(Math.abs(x));
}

/** frames is the whole frame a time falls on, rounded to the nearest. */
export function frames(seconds, fps) {
  const [num, den] = ratio(fps) || [Math.round(fps) || 24, 1];
  return round((seconds * num) / den);
}

/** evenFrames is the nearest even frame count, which a centred dissolve needs. */
export function evenFrames(seconds, fps) {
  const [num, den] = ratio(fps) || [Math.round(fps) || 24, 1];
  return round((seconds * num) / den / 2) * 2;
}

/** seconds is where a whole frame falls, exactly. */
export function seconds(frameCount, fps) {
  const [num, den] = ratio(fps) || [Math.round(fps) || 24, 1];
  return (frameCount * den) / num;
}

/** snap puts a time on the target's frame grid. */
export function snap(t, fps) {
  return seconds(frames(t, fps), fps);
}

/** snapSample puts a sound clip's in or out on the target's sample grid. */
export function snapSample(t, sampleRate) {
  return round(t * sampleRate) / sampleRate;
}

/** kindOf is what a clip is: an image holds, a sound sits on an audio track. */
export function kindOf(track, clip) {
  if (clip.hold !== undefined && clip.hold !== null) return "image";
  return track.kind === "audio" ? "audio" : "video";
}

/** length is how long a clip lasts in the sequence. */
export function length(clip) {
  if (clip.hold !== undefined && clip.hold !== null) return clip.hold;
  return (clip.out ?? 0) - (clip.in ?? 0);
}

export function start(clip) {
  return clip.at ?? 0;
}

export function end(clip) {
  return start(clip) + length(clip);
}

/** duration is V1's end, or the last sound to stop when there is no V1. */
export function duration(doc) {
  const tracks = doc.tracks || [];
  const fps = doc.target.fps;
  const video = tracks.find((t) => t.kind === "video");
  const within = video ? [video] : tracks;
  let last = 0;
  for (const t of within) for (const c of t.clips) last = Math.max(last, end(c));
  return snap(last, fps);
}

/** gain turns decibels into the multiplier a gain node or a volume takes. */
export function gain(db) {
  return Math.pow(10, (db ?? 0) / 20);
}

const clone = (tracks) => tracks.map((t) => ({ ...t, clips: t.clips.map((c) => ({ ...c })) }));

/**
 * lay puts V1's clips end to end from 0, as the daemon does for a clip sent
 * without `at`. The editor runs it after every local change to V1, so what it
 * draws before the answer arrives is where the daemon will put things.
 */
export function lay(tracks, fps) {
  const out = clone(tracks);
  const video = out.find((t) => t.kind === "video");
  if (!video) return out;
  let at = 0;
  video.clips.forEach((c, i) => {
    c.at = at;
    at = snap(at + length(c), fps);
    // A clip that has become the first has nothing to dissolve from.
    if (i === 0) delete c.transition_in;
  });
  return out;
}

/**
 * request is the document as a write sends it. The daemon sets `name` and a
 * clip's `studio_id` and ignores them in a request, so they are not sent; and
 * V1's clips go without `at`, because the video track is contiguous and the
 * daemon lays it — sending positions would turn a trim into a refusal for a
 * gap the editor never meant to leave.
 */
export function request(tracks) {
  return tracks.map((t) => {
    const out = { kind: t.kind, clips: [] };
    if (t.gain_db !== undefined && t.gain_db !== null) out.gain_db = t.gain_db;
    for (const c of t.clips) {
      const clip = { asset_id: c.asset_id };
      for (const key of ["in", "out", "hold", "gain_db", "audio", "transition_in"]) {
        if (c[key] !== undefined && c[key] !== null) clip[key] = c[key];
      }
      if (t.kind !== "video" && c.at !== undefined && c.at !== null) clip.at = c.at;
      out.clips.push(clip);
    }
    return out;
  });
}

// ------------------------------------------------------------------- edits
//
// Each takes tracks and returns new ones. None of them decides whether an edit
// is allowed: an out past the end of a source, a dissolve without handles, two
// sounds overlapping — the daemon refuses those with a reason, and the editor
// shows it. They only clamp what is certainly nonsense, such as a clip shorter
// than one frame, so a stray drag does not become a round trip.

/** move reorders V1, or moves a sound clip to a new start. */
export function move(tracks, fps, trackIndex, clipIndex, to) {
  const out = clone(tracks);
  const track = out[trackIndex];
  if (!track || !track.clips[clipIndex]) return out;
  if (track.kind === "video") {
    const [clip] = track.clips.splice(clipIndex, 1);
    track.clips.splice(Math.max(0, Math.min(to, track.clips.length)), 0, clip);
    return lay(out, fps);
  }
  const clip = track.clips[clipIndex];
  clip.at = Math.max(0, snap(to, fps));
  track.clips.sort((a, b) => start(a) - start(b));
  return out;
}

/**
 * trim moves one edge of a clip by delta seconds. A sound's left edge keeps its
 * right edge where it was, as an editor's does; V1 ripples, because it has no
 * gaps to absorb a change.
 */
export function trim(tracks, target, trackIndex, clipIndex, edge, delta, sourceLength) {
  const out = clone(tracks);
  const track = out[trackIndex];
  const clip = track && track.clips[clipIndex];
  if (!clip) return out;
  const fps = target.fps;
  const oneFrame = seconds(1, fps);
  const snapIn = (t) => (track.kind === "audio" ? snapSample(t, target.sample_rate) : snap(t, fps));

  if (kindOf(track, clip) === "image") {
    clip.hold = Math.max(oneFrame, snap(clip.hold + (edge === "in" ? -delta : delta), fps));
    return track.kind === "video" ? lay(out, fps) : out;
  }
  if (edge === "in") {
    const limit = (clip.out ?? 0) - oneFrame;
    const next = Math.max(0, Math.min(limit, snapIn((clip.in ?? 0) + delta)));
    const moved = next - (clip.in ?? 0);
    clip.in = next;
    if (track.kind !== "video") clip.at = Math.max(0, snap(start(clip) + moved, fps));
  } else {
    let next = Math.max((clip.in ?? 0) + oneFrame, snapIn((clip.out ?? 0) + delta));
    if (Number.isFinite(sourceLength) && sourceLength > 0) next = Math.min(next, snapIn(sourceLength));
    clip.out = next;
  }
  return track.kind === "video" ? lay(out, fps) : out;
}

export function remove(tracks, fps, trackIndex, clipIndex) {
  const out = clone(tracks);
  const track = out[trackIndex];
  if (!track) return out;
  track.clips.splice(clipIndex, 1);
  return track.kind === "video" ? lay(out, fps) : out;
}

/** setClip changes gain, sound or a dissolve on one clip; null removes a key. */
export function setClip(tracks, trackIndex, clipIndex, changes) {
  const out = clone(tracks);
  const clip = out[trackIndex] && out[trackIndex].clips[clipIndex];
  if (!clip) return out;
  for (const [key, value] of Object.entries(changes)) {
    if (value === null || value === undefined) delete clip[key];
    else clip[key] = value;
  }
  return out;
}

export function setTrackGain(tracks, trackIndex, db) {
  const out = clone(tracks);
  if (!out[trackIndex]) return out;
  if (db === null || db === undefined) delete out[trackIndex].gain_db;
  else out[trackIndex].gain_db = db;
  return out;
}

/** addAudioTrack adds an empty sound track, up to the eight a sequence may hold. */
export function addAudioTrack(tracks) {
  const out = clone(tracks);
  if (out.filter((t) => t.kind === "audio").length >= 8) return out;
  out.push({ kind: "audio", clips: [] });
  return out;
}

// ------------------------------------------------------------------ playing

/**
 * dissolveAt is the dissolve a time falls inside, if any: centred on the cut
 * into V1 clip `index`, taking half its length from each side (M8 Q8).
 */
function dissolveAt(video, t) {
  for (let i = 1; i < video.clips.length; i++) {
    const c = video.clips[i];
    const d = c.transition_in && c.transition_in.duration;
    if (!d) continue;
    const cut = start(c);
    if (t >= cut - d / 2 && t < cut + d / 2) return { index: i, from: cut - d / 2, duration: d };
  }
  return null;
}

/** local is where in its source a clip is at sequence time t. */
function local(clip, t) {
  if (clip.hold !== undefined && clip.hold !== null) return 0;
  return (clip.in ?? 0) + (t - start(clip));
}

/**
 * frameAt is what plays at time t: the picture, the one mixing into it inside a
 * dissolve, and every sound, each with where in its source it is and how loud.
 *
 * It is the whole of the preview's decision. The preview engine only makes
 * media elements agree with it, which is why this can be tested exhaustively
 * and the engine barely at all.
 */
export function frameAt(doc, t) {
  const tracks = doc.tracks || [];
  const video = tracks.find((tr) => tr.kind === "video");
  const out = { picture: [], sound: [] };

  if (video) {
    const vi = tracks.indexOf(video);
    const d = dissolveAt(video, t);
    if (d) {
      const mix = Math.max(0, Math.min(1, (t - d.from) / d.duration));
      const outgoing = video.clips[d.index - 1];
      const incoming = video.clips[d.index];
      out.picture.push({ track: vi, index: d.index - 1, clip: outgoing, local: local(outgoing, t), opacity: 1 - mix });
      out.picture.push({ track: vi, index: d.index, clip: incoming, local: local(incoming, t), opacity: mix });
    } else {
      const index = video.clips.findIndex((c) => t >= start(c) && t < end(c));
      if (index >= 0) {
        const c = video.clips[index];
        out.picture.push({ track: vi, index, clip: c, local: local(c, t), opacity: 1 });
      }
    }
    // A video clip's own sound plays under it, at its own gain, unless muted,
    // and the two sides of a dissolve crossfade with their pictures.
    for (const p of out.picture) {
      if (kindOf(video, p.clip) !== "video" || p.clip.audio === false) continue;
      out.sound.push({ track: vi, index: p.index, clip: p.clip, local: p.local, gain: gain(p.clip.gain_db) * p.opacity });
    }
  }

  tracks.forEach((tr, ti) => {
    if (tr.kind !== "audio") return;
    tr.clips.forEach((c, ci) => {
      if (t < start(c) || t >= end(c)) return;
      out.sound.push({ track: ti, index: ci, clip: c, local: local(c, t), gain: gain(c.gain_db) * gain(tr.gain_db) });
    });
  });
  return out;
}

/** cuts is every time something starts or stops, for snapping a drag to. */
export function cuts(doc) {
  const out = new Set([0]);
  for (const tr of doc.tracks || []) {
    for (const c of tr.clips) {
      out.add(start(c));
      out.add(end(c));
    }
  }
  return [...out].sort((a, b) => a - b);
}
