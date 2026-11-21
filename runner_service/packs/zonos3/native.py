"""Stage functions of the zonos3 pack (runtime native): ZONOS3-Mini speech by the pinned zonos3-server
(runner_service/vendor/zonos3, `make zonos3`) as the run's child process (core.child.serve): started on 127.0.0.1
with a free port, one POST /v2/generate per piece of the text, killed after (docs/reference-notes/voices.md 4).

speak: the text split on sentences into pieces of about PIECE_S at most (piece_chars, core.media.pieces; the server's cap is 450
frames, 30 s, a piece), each normalized by the server in the run's language (BCP 47 → ZONOS3's code) and spoken at
the run's seed with the same voice and Delivery, joined with PAUSE_S of silence. A preset voice sends its default
voice's embedding (<weights>/gguf/voices/<Language>/<Name>_*.embedding.npy); a clip voice sends its in..out part as a
WAV (ffmpeg) for the server's speaker encoder to embed. Delivery (contracts/README.md "zonos3"): emotion and energy →
mood.valence / mood.arousal = (v - 0.5) * 8, whisper → delivery.whisper = v * 1.5 (each left out at its neutral
value), speaking_rate → the speaking-rate condition 15 × v UTF-8 bytes/s, always sent (1 = 15, the checkpoint's middle bucket), guidance → speaker CFG (1: off).
Output: 16-bit PCM, 44.1 kHz mono. No torch.

dialogue (API 1.32): one server for every line, each spoken as speak speaks a text (utter) in its own voice, its style
words mapped onto its Delivery (STYLE), then its pause_s of silence, with a cue per line."""

import base64
import io
import json
import re
import subprocess
import tempfile
import urllib.error
import urllib.request
import wave
from pathlib import Path

from core import child, media
from core.graph import RunError

ZONOS3 = Path(__file__).resolve().parents[2] / "vendor/zonos3"
BINARY = ZONOS3 / "build/zonos3-server"
READY = re.compile(r"zonos3-server: listening on http://127\.0\.0\.1:(\d+)")
READY_S = (
    120  # ~1.4 s warm; the first run after a build or a boot compiles the Metal libraries (~4 s)
)
SAMPLE_RATE = 44100
MAX_CHARS = 900  # max_seconds 60 at the plan's 15 characters a second
PIECE_CHARS = 200  # at most, a request (about 13 s at 15 characters a second)
PIECE_S = 20  # a piece's estimated length at most: well under the server's 30 s cap
# characters a second at speaking rate 1: the rate condition is 15 UTF-8 bytes a second, so an alphabetic character
# is about 1/15 s and a CJK one (3 bytes) about 1/5 s
CJK = ("zh", "ja", "ko")
PAUSE_S = 0.25
REQUEST_S = 300
CLIP_S = (
    3.0,
    30.0,
)  # a clip voice's part (contracts: cloned voices; studio checked it, this is the backstop)
# BCP 47 tags (the param's enum) → ZONOS3's server codes (registries/languages.json); the rest are the same
LANGUAGES = {"en-US": "en_us", "en-GB": "en_gb", "fr": "fr_fr", "pt-BR": "pt_br", "zh": "cmn"}


def language(tag: str) -> str:
    return LANGUAGES.get(tag, tag)


def piece_chars(params: dict) -> int:
    """The longest piece for the run: PIECE_S of speech at its language's characters a second and its speaking rate
    (a slow rate fits fewer characters), at most PIECE_CHARS."""
    rate = 5 if params["language"].split("-")[0] in CJK else 15
    return max(1, min(PIECE_CHARS, int(PIECE_S * rate * params["speaking_rate"])))


def delivery(params: dict) -> dict:
    """The /v2/generate fields of the Delivery params; a control at its neutral value sends nothing, the speaking rate
    is always sent."""
    controls = {}
    for name, control in (("emotion", "mood.valence"), ("energy", "mood.arousal")):
        if dose := round((params[name] - 0.5) * 8, 6):
            controls[control] = dose
    if dose := round(params["whisper"] * 1.5, 6):
        controls["delivery.whisper"] = dose
    out: dict = {"controls": controls} if controls else {}
    # always a rate: without one the bucket is unknown and short lines ran 2-8 s; 1 is 15 bytes/s, the middle bucket
    out["condition_values"] = {"speaking_rate": round(15 * params["speaking_rate"], 6)}
    if params["guidance"] != 1:
        out["speaker_cfg_scale"] = params["guidance"]
    return out


def argv(gguf: Path, binary: Path = BINARY) -> list[str]:
    """The server's command line: only the files this pack downloads (never zac.gguf, the raw-reference encoders or
    the default voices folder), two slots (speaker CFG runs a twin in the second), a free port (read back from its ready line)."""
    return [str(binary), str(gguf / "backbone-ptq.gguf"), "--zac", str(gguf / "zac-keep2.gguf"),
            "--spk-encoder", str(gguf / "spk-encoder.gguf"), "--steer", str(gguf / "steer.gguf"),
            "--tts-default-voices-dir", "", "--batch", "2", "--host", "127.0.0.1", "--port", "0"]  # fmt: skip


def body(text: str, seed: int, params: dict, voice: dict) -> dict:
    """One /v2/generate request: the piece, the seed (the server takes 32 bits), language, Delivery, the voice's
    fields (speaker())."""
    return {"text": text, "stream": False, "format": "wav", "seed": seed % 2**32,
            "language": language(params["language"]), **delivery(params), **voice}  # fmt: skip


def embedding(node: str, voice: dict, gguf: Path) -> Path:
    """A preset's default-voice embedding: the voice id is its name lower-cased (voices/defaults.yaml)."""
    name = str(voice.get("item_id", "")).removeprefix("zonos3/")
    hits = [
        p
        for p in (gguf / "voices").glob("*/*.embedding.npy")
        if p.name.split("_")[0].lower() == name
    ]
    if voice.get("family") != "zonos3" or len(hits) != 1:
        raise RunError(f"{node}.voice: ZONOS3 has no default voice {voice.get('item_id')}")
    return hits[0]


def clip_part(node: str, clip: dict) -> tuple[float, float]:
    """(start, seconds) of a clip voice's in..out, within CLIP_S."""
    start, end = float(clip.get("in", 0)), float(clip["out"])
    if not 0 <= start < end:
        raise RunError(f"{node}.voice: the clip's in must be before its out")
    # within 1e-9 s of a limit is at it (studio's snapMedia: 4.1 - 1.1 is 2.9999999999999996)
    if not CLIP_S[0] - 1e-9 <= end - start <= CLIP_S[1] + 1e-9:
        raise RunError(
            f"{node}.voice: the clip lasts {end - start:.1f} s; a voice clip is {CLIP_S[0]:.0f} to "
            f"{CLIP_S[1]:.0f} s"
        )
    return start, end - start


def cut_clip(node: str, clip: dict, dest: Path) -> Path:
    """The clip's in..out from its bytes as a mono 16-bit WAV at its own rate (bit-exact flags: the same input makes
    the same bytes, so a clone repeats)."""
    start, seconds = clip_part(node, clip)
    src = dest.with_name(dest.stem + "-take")
    src.write_bytes(clip["data"])
    a = [media.FFMPEG or "ffmpeg", "-v", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-i", str(src),
         "-ss", f"{start:.6f}", "-t", f"{seconds:.6f}", "-map", "0:a:0", "-ac", "1", "-c:a", "pcm_s16le",
         "-map_metadata", "-1", "-fflags", "+bitexact", "-flags", "+bitexact", str(dest)]  # fmt: skip
    try:
        r = subprocess.run(a, capture_output=True, text=True, check=False, timeout=120)
    except subprocess.TimeoutExpired:
        raise RunError(
            f"{node}.voice: cutting the clip of take {clip.get('take_id')} took over 120 s"
        ) from None
    finally:
        src.unlink(missing_ok=True)
    if r.returncode or not dest.is_file():
        raise RunError(
            f"{node}.voice: cutting the clip of take {clip.get('take_id')} failed: {media.last_line(r.stderr)}"
        )
    return dest


def speaker(node: str, voice: dict, gguf: Path, tmp: Path) -> dict:
    """The voice's /v2/generate fields: a preset's embedding file, or a clip's part for the server to embed."""
    if voice.get("source") == "preset":
        data = embedding(node, voice, gguf).read_bytes()
        return {
            "speaker_mode": "embedding",
            "speaker_embedding_base64": base64.b64encode(data).decode(),
        }
    if voice.get("source") == "clip":
        data = cut_clip(node, voice["clip"], tmp / "clip.wav").read_bytes()
        return {
            "speaker_mode": "embedding",
            "speaker_audio_base64": base64.b64encode(data).decode(),
        }
    raise RunError(f"{node}.voice: ZONOS3 cannot speak a voice of source {voice.get('source')!r}")


def generate(node: str, port: int, request: dict) -> bytes:
    """POST /v2/generate → the WAV; an HTTP error fails the run with the server's own message (paths replaced)."""
    req = urllib.request.Request(f"http://127.0.0.1:{port}/v2/generate", json.dumps(request).encode(),
                                 {"Content-Type": "application/json"})  # fmt: skip
    try:
        with urllib.request.urlopen(req, timeout=REQUEST_S) as r:
            return r.read()
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors="replace")
        try:
            msg = json.loads(raw)
            msg = msg.get("detail") or msg.get("error") or raw if isinstance(msg, dict) else raw
        except ValueError:
            msg = raw
        msg = msg if isinstance(msg, str) else json.dumps(msg)
        raise RunError(
            f"{node}: zonos3-server refused the request ({e.code}): {media.last_line(msg)}"
        ) from None
    except (
        TimeoutError,
        urllib.error.URLError,
    ) as e:  # no answer within REQUEST_S, or the connection failed
        why = e.reason if isinstance(e, urllib.error.URLError) else e
        if isinstance(why, TimeoutError) or isinstance(e, TimeoutError):
            raise RunError(f"{node}: zonos3-server sent no answer within {REQUEST_S} s") from None
        raise RunError(f"{node}: zonos3-server could not be reached ({why})") from None


def pcm(node: str, wav: bytes) -> bytes:
    """The samples of the server's WAV (16-bit PCM, 44.1 kHz mono: anything else fails)."""
    try:
        with wave.open(io.BytesIO(wav)) as w:
            facts = (w.getnchannels(), w.getsampwidth(), w.getframerate())
            frames = w.readframes(w.getnframes())
    except (wave.Error, EOFError) as e:
        raise RunError(f"{node}: zonos3-server sent no WAV ({e})") from None
    if facts != (1, 2, SAMPLE_RATE):
        raise RunError(f"{node}: zonos3-server sent {facts}, not 16-bit mono at {SAMPLE_RATE} Hz")
    return frames


def utter(ctx, port: int, text: str, seed: int, params: dict, voice: dict, step=None) -> bytes:
    """`text` split on sentences into pieces (piece_chars), each spoken at `seed` with the voice's fields and the
    params' language and Delivery, joined with PAUSE_S of silence: 16-bit PCM samples. step(i, n) after piece i."""
    out, parts = [], media.pieces(text, piece_chars(params))
    for i, part in enumerate(parts):
        ctx.check()
        samples = pcm(ctx.node, generate(ctx.node, port, body(part, seed, params, voice)))
        if not samples:
            raise RunError(f"{ctx.node}: no speech came out")
        out += [bytes(2 * round(PAUSE_S * SAMPLE_RATE))] if i else []
        out.append(samples)
        if step:
            step(i + 1, len(parts))
    return b"".join(out)


def _ready(node: str) -> None:
    if not BINARY.is_file():
        raise RunError(f"{node}: zonos3-server is not built (make zonos3 in runner_service)")


def _serve(ctx, gguf: Path):
    ctx.progress(1, 1, "starting zonos3")  # a phase without steps: 1 of 1
    return child.serve(
        ctx, argv(gguf), cwd=BINARY.parent, name="zonos3", ready=READY, ready_s=READY_S
    )


def speak(ctx, params: dict, inputs: dict) -> dict:
    text = inputs["text"].strip()
    if not text:
        raise RunError(f"{ctx.node}: nothing to say")
    if len(text) > MAX_CHARS:
        raise RunError(
            f"{ctx.node}: at most {MAX_CHARS} characters (about 60 s of speech), not {len(text)}"
        )
    _ready(ctx.node)
    gguf = inputs["model"].folder().resolve() / "gguf"
    with tempfile.TemporaryDirectory(prefix="zonos3-") as tmp:
        voice = speaker(ctx.node, params["voice"], gguf, Path(tmp).resolve())
        with _serve(ctx, gguf) as port:
            data = utter(ctx, port, text, inputs["seed"], params, voice,
                         lambda i, n: ctx.progress(i, n, "speaking"))  # fmt: skip
    return {
        "audio": {
            "wav": data,
            "sample_rate": SAMPLE_RATE,
            "duration_s": len(data) / 2 / SAMPLE_RATE,
        }
    }


# --- dialogue (API 1.32, contracts/README.md "Dialogue and Timeline audio")

# A line's (style) words → that line's Delivery (the values of the workflow's presets: Warm, Tense, Whisper); a word
# not here is ignored, a later word overrides an earlier one on the same slider (GOTCHAS.md)
STYLE = {
    word: delivery
    for words, delivery in (
        ("whisper whispers whispering whispered hushed", {"whisper": 0.9, "energy": 0.25, "speaking_rate": 0.9}),
        ("quiet quietly soft softly calm calmly gentle gently", {"energy": 0.3}),
        ("warm warmly kind kindly tender tenderly", {"emotion": 0.7, "energy": 0.45}),
        ("happy cheerful cheerfully glad bright", {"emotion": 0.8}),
        ("sad sadly gloomy tired wearily", {"emotion": 0.25, "energy": 0.35}),
        ("tense tensely urgent urgently nervous nervously afraid scared",
         {"emotion": 0.3, "energy": 0.75, "speaking_rate": 1.1}),
        ("angry angrily furious shouting shouts loud loudly", {"emotion": 0.2, "energy": 0.9}),
        ("excited excitedly eager eagerly", {"emotion": 0.7, "energy": 0.8}),
        ("slow slowly", {"speaking_rate": 0.85}),
        ("fast quick quickly hurried hurriedly rushed", {"speaking_rate": 1.15}),
    )
    for word in words.split()
}  # fmt: skip


def styled(params: dict, style: str) -> dict:
    """The run's params with a line's style words mapped onto its Delivery (STYLE)."""
    out = dict(params)
    for word in re.findall(r"[a-z]+", style.lower()):
        out |= STYLE.get(word, {})
    return out


def check_dialogue(params: dict, wired: set, models: dict | None = None) -> str | None:
    return media.lines_problem(params.get("lines") or [])


def dialogue(ctx, params: dict, inputs: dict) -> dict:
    """One server for the whole dialogue: each line as speak says it, in its voice (a preset's embedding or a clip
    the server embeds; each voice's fields made once), at the one seed, with the run's Delivery and the line's style
    mapped onto it (styled), then its pause_s of silence; one take with a cue per line (core.media.stitch)."""
    lines = params["lines"]
    if not lines:
        raise RunError(f"{ctx.node}: nothing to say")
    _ready(ctx.node)
    gguf, spoken, voices = inputs["model"].folder().resolve() / "gguf", [], {}
    with tempfile.TemporaryDirectory(prefix="zonos3-") as tmp:
        # every voice first (a bad one fails before the server starts); one id is one resolved voice in a run
        for i, row in enumerate(lines, 1):
            if not row["text"].strip():
                raise RunError(f"{ctx.node}.lines[{i}]: nothing to say")
            if (vid := row["voice"]["item_id"]) not in voices:
                (folder := Path(tmp).resolve() / str(i)).mkdir()
                voices[vid] = speaker(f"{ctx.node}.lines[{i}]", row["voice"], gguf, folder)
        with _serve(ctx, gguf) as port:
            for i, row in enumerate(lines, 1):
                line, voice = styled(params, row.get("style", "")), voices[row["voice"]["item_id"]]
                spoken.append(utter(ctx, port, row["text"].strip(), inputs["seed"], line, voice))
                ctx.progress(i, len(lines), "speaking")
    wav, cues = media.stitch(lines, spoken, SAMPLE_RATE)
    return {"audio": {"wav": wav, "sample_rate": SAMPLE_RATE, "duration_s": len(wav) / 2 / SAMPLE_RATE,
                      "cues": cues}}  # fmt: skip
