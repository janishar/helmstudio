"""omni/listen on the mlx runner (plan 42 step 2): Qwen3-Omni-30B-A3B (GGUF Q8_0 + its Q8_0 projector) in llama.cpp's
llama-server, a server child (core.child.start) that the residency manager holds as the component `model`: it
stays loaded between runs (keep_warm) and is stopped when the component goes (Server.evicted). The ASR component is
Qwen3-ASR-1.7B through mlx-audio, in this process.

A video take goes to the server whole (llama-server samples its frames with ffmpeg, VIDEO_FPS a second) with its
sound as a second part (16 kHz mono WAV); an audio take as the WAV alone. One greedy answer (temperature 0, seed 0),
no schema; the answer record (defs `answer`) has no `seen` (nothing is stored). The transcript is the stage's
reported `transcript` output (resolved.texts `listen.transcript`)."""

import base64
import json
import logging
import os
import re
import shutil
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

from core import ask as asks
from core import child, media
from core.graph import RunError
from core.media import MediaError

log = logging.getLogger("omni")
READY = re.compile(r"listening on http://127\.0\.0\.1:(\d+)")
READY_S = 600  # about 60 s from the T9 measured; a cold disk can be slower
ANSWER_S = 300
CTX, VIDEO_FPS, RATE = 32768, 2, 16000
SYSTEM = "Answer the user's question about the take in plain words. The audio is the take's own sound."
MISSING_ASR = "omni: mlx-audio is not installed (uv sync --project envs/mlx --extra llm)"


def binary(env=os.environ) -> Path:
    """llama-server: OMNI_LLAMA_SERVER, else on PATH, else Homebrew's."""
    if b := env.get("OMNI_LLAMA_SERVER"):
        return Path(b).expanduser()
    found = shutil.which("llama-server", path=env.get("PATH"))
    return Path(found) if found else Path("/opt/homebrew/bin/llama-server")


def argv(exe: Path, gguf: Path, mmproj: Path) -> list[str]:
    """One slot of CTX tokens, every layer on Metal, a free port read back from the ready line, no web UI."""
    return [str(exe), "-m", str(gguf), "--mmproj", str(mmproj), "--host", "127.0.0.1", "--port", "0",
            "-c", str(CTX), "-np", "1", "-ngl", "999", "--video-fps", str(VIDEO_FPS), "--no-webui"]  # fmt: skip


class Server:
    """A resident llama-server: its child and port; evicted() stops it (core.residency)."""

    def __init__(self, proc: child._Child, port: int) -> None:
        self.proc, self.port = proc, port

    def alive(self) -> bool:
        return self.proc.proc.poll() is None

    def evicted(self) -> None:
        self.proc.stop()

    def chat(self, node: str, content: list[dict], max_tokens: int) -> tuple[str, int, int]:
        body = {"messages": [{"role": "system", "content": SYSTEM}, {"role": "user", "content": content}],
                "temperature": 0, "seed": asks.SEED, "max_tokens": max_tokens}  # fmt: skip
        req = urllib.request.Request(f"http://127.0.0.1:{self.port}/v1/chat/completions", json.dumps(body).encode(),
                                     {"Content-Type": "application/json"})  # fmt: skip
        # ponytail: a cancel waits for the answer (seconds warm); stream it if answers get long
        try:
            with urllib.request.urlopen(req, timeout=ANSWER_S) as r:
                out = json.load(r)
        except urllib.error.HTTPError as e:
            raise RunError(
                f"{node}: llama-server refused the request ({e.code}): {e.read()[:300].decode(errors='replace')}"
            ) from e
        except (OSError, ValueError) as e:
            why = self.proc.why() if not self.alive() else str(e)
            raise RunError(f"{node}: llama-server sent no answer: {why}") from e
        usage = out.get("usage") or {}
        return (
            (out["choices"][0]["message"].get("content") or "").strip(),
            usage.get("prompt_tokens", 0),
            usage.get("completion_tokens", 0),
        )


def load_component(component: str, model, ctx):
    """core.loaders_mlx hook: `model` starts the server (its weights load in it), `asr` mlx-audio's Qwen3-ASR."""
    if component == "asr":
        try:
            from mlx_audio.stt import load
        except ImportError as e:
            raise RunError(MISSING_ASR) from e
        return load(str(model.files("asr")[0].parent))
    files = model.files("model")
    gguf = next((p for p in files if not p.name.startswith("mmproj")), None)
    mmproj = next((p for p in files if p.name.startswith("mmproj")), None)
    exe = binary()
    if not exe.is_file():
        raise RunError(f"{ctx.node}: llama-server is not installed (brew install llama.cpp)")
    if gguf is None or mmproj is None:
        raise RunError(f"{ctx.node}: the Q8_0 GGUF or its mmproj is missing from the models folder")
    proc, port = child.start(ctx, argv(exe, gguf, mmproj), cwd=gguf.parent, name="llama", ready=READY, ready_s=READY_S)
    return Server(proc, port)


def b64(data: bytes) -> str:
    return base64.b64encode(data).decode()


def listen(ctx, params: dict, inputs: dict) -> dict:
    src, model = params["source"], inputs["model"]
    question = params["question"].strip()
    if not question:
        raise RunError(f"{ctx.node}.question: ask something")
    server = ctx.component(model, "model")
    if not server.alive():  # it stopped since its last run (killed, crashed): start it again
        ctx.residency.release({model.key("model")})
        server = ctx.component(model, "model")
    with tempfile.TemporaryDirectory(prefix="listen-") as tmp:
        path = Path(tmp) / "take"
        path.write_bytes(src["data"])
        try:
            info = media.probe(path)
            wav = media.wav(media.pcm(src["data"], RATE, 1), RATE, 1) if info["audio"] else None
        except MediaError as e:
            raise RunError(f"{ctx.node}.source: not a readable video or audio take") from e
        content = []
        if info["video"] is not None:
            content.append({"type": "video_url", "video_url": {"url": "data:video/mp4;base64," + b64(src["data"])}})
        if wav:
            content.append({"type": "input_audio", "input_audio": {"data": b64(wav), "format": "wav"}})
        content.append({"type": "text", "text": question if wav else f"(The take has no sound.)\n\n{question}"})
        ctx.progress(1, 1, "listening")
        started = time.monotonic()
        text, tokens_in, tokens_out = server.chat(ctx.node, content, params["max_tokens"])
        seconds = time.monotonic() - started
        transcript = ""
        if params["transcript"] and wav:
            ctx.check()
            ctx.progress(1, 1, "transcribing")
            (Path(tmp) / "sound.wav").write_bytes(wav)
            transcript = (ctx.component(model, "asr").generate(str(Path(tmp) / "sound.wav")).text or "").strip()
    answer = asks.Answer(text[: asks.MAX_TEXT])
    answer.record = {"tries": [{"text": text[: asks.MAX_TEXT], "error": None}], "seen": [], "tokens_in": tokens_in,
                     "tokens_out": tokens_out, "seconds": round(seconds, 2)}  # fmt: skip
    return {"answer": answer, "transcript": transcript}
