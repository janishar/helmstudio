"""Stage functions of the zonos3 pack (runtime native): ZONOS3-Mini speech by the pinned zonos3-server
(runner_service/vendor/zonos3, `make zonos3`) as the run's child process (core.child.serve): started on 127.0.0.1
with a free port, one POST /v2/generate per piece of the text, killed after (docs/reference-notes/voices.md 4).

speak: the text split on sentences into pieces of about PIECE_S at most (piece_chars, core.media.pieces; the server's cap is 450
frames, 30 s, a piece), each normalized by the server in the run's language (BCP 47 → ZONOS3's code) and spoken at
the run's seed with the same voice and Delivery, joined with PAUSE_S of silence. A preset voice sends its default
voice's embedding (<weights>/gguf/voices/<Language>/<Name>_*.embedding.npy); a clip voice sends its in..out part as a
WAV (ffmpeg) for the server's speaker encoder to embed. Delivery (contracts/README.md "zonos3"): emotion and energy →
mood.valence / mood.arousal = (v - 0.5) * 8, whisper → delivery.whisper = v * 1.5 (each left out at its neutral
value), speaking_rate → the speaking-rate condition 15 × v UTF-8 bytes/s (1: none), guidance → speaker CFG (1: off).
Output: 16-bit PCM, 44.1 kHz mono. No torch."""

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
    """The /v2/generate fields of the Delivery params; a param at its neutral value sends nothing."""
    controls = {}
    for name, control in (("emotion", "mood.valence"), ("energy", "mood.arousal")):
        if dose := round((params[name] - 0.5) * 8, 6):
            controls[control] = dose
    if dose := round(params["whisper"] * 1.5, 6):
        controls["delivery.whisper"] = dose
    out: dict = {"controls": controls} if controls else {}
    if params["speaking_rate"] != 1:
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


def speak(ctx, params: dict, inputs: dict) -> dict:
    text = inputs["text"].strip()
    if not text:
        raise RunError(f"{ctx.node}: nothing to say")
    if len(text) > MAX_CHARS:
        raise RunError(
            f"{ctx.node}: at most {MAX_CHARS} characters (about 60 s of speech), not {len(text)}"
        )
    if not BINARY.is_file():
        raise RunError(f"{ctx.node}: zonos3-server is not built (make zonos3 in runner_service)")
    gguf = inputs["model"].folder().resolve() / "gguf"
    parts = media.pieces(text, piece_chars(params))
    out = []
    with tempfile.TemporaryDirectory(prefix="zonos3-") as tmp:
        voice = speaker(ctx.node, params["voice"], gguf, Path(tmp).resolve())
        ctx.progress(1, 1, "starting zonos3")  # a phase without steps: 1 of 1
        with child.serve(
            ctx, argv(gguf), cwd=BINARY.parent, name="zonos3", ready=READY, ready_s=READY_S
        ) as port:
            for i, part in enumerate(parts):
                ctx.check()
                samples = pcm(
                    ctx.node, generate(ctx.node, port, body(part, inputs["seed"], params, voice))
                )
                if not samples:
                    raise RunError(f"{ctx.node}: no speech came out")
                out += [bytes(2 * round(PAUSE_S * SAMPLE_RATE))] if i else []
                out.append(samples)
                ctx.progress(i + 1, len(parts), "speaking")
    data = b"".join(out)
    return {
        "audio": {
            "wav": data,
            "sample_rate": SAMPLE_RATE,
            "duration_s": len(data) / 2 / SAMPLE_RATE,
        }
    }
