"""Starter voices: runner_service/packs/<pack>/voices/*.yaml (packs/README.md, contracts/README.md "Voices: starter
voices") → the registry value <host>.<env>.voices. A broken file stops the runner like a broken look file; a preview
clip next to the YAML is PUT once to its content-addressed key. The runner reads no library at run time: studio sends
a run's voice resolved (defs voice)."""

import hashlib
import io
import logging
import re
import wave

import yaml

from core.looks import SLUG, _control

log = logging.getLogger(__name__)

SPEAKER = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
LANGUAGE = re.compile(r"^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$")
KEYS = {"id", "name", "speaker", "language", "style", "version", "preview"}
MAX_PREVIEW, MAX_PREVIEW_S, MAX_VOICES = 2 * 2**20, 30, 200


def _check(x, where: str, folder) -> dict:
    """One YAML voice, defaults filled, its preview read: {id, name, speaker, language, style?, version, preview}."""
    if not isinstance(x, dict):
        raise ValueError(f"{where}: a voice is a mapping")  # noqa: TRY004
    if extra := set(x) - KEYS:
        raise ValueError(f"{where}: unknown keys {sorted(extra)}")
    v = {"version": 1, "preview": None} | x
    if not isinstance(v.get("id"), str) or not SLUG.match(v["id"]):
        raise ValueError(f"{where}: id must match {SLUG.pattern}")
    where = f"{where} ({v['id']})"
    if not isinstance(v.get("name"), str) or not 1 <= len(v["name"]) <= 80 or _control(v["name"]):
        raise ValueError(f"{where}: name must be 1-80 characters, no control characters")
    if not isinstance(v.get("speaker"), str) or not SPEAKER.match(v["speaker"]):
        raise ValueError(f"{where}: speaker must match {SPEAKER.pattern}")
    if not isinstance(v.get("language"), str) or not LANGUAGE.match(v["language"]):
        raise ValueError(f"{where}: language must be a BCP 47 tag ({LANGUAGE.pattern})")
    if "style" in v and (not isinstance(v["style"], str) or not 1 <= len(v["style"]) <= 500 or _control(v["style"])):
        raise ValueError(f"{where}: style must be 1-500 characters, no control characters")
    if not isinstance(v["version"], int) or isinstance(v["version"], bool) or v["version"] < 1:
        raise ValueError(f"{where}: version must be an integer >= 1")
    if v["preview"] is not None:
        v["preview"] = _preview(v["preview"], where, folder)
    return v


def _preview(name, where: str, folder) -> dict:
    """The voice's preview clip next to the YAML: {data, sha256, duration_s, sample_rate}."""
    if not isinstance(name, str) or "/" in name or "\\" in name or not (folder / name).is_file():
        raise ValueError(f"{where}: preview {name!r} is not a file next to the YAML")
    data = (folder / name).read_bytes()
    if len(data) > MAX_PREVIEW:
        raise ValueError(f"{where}: preview {name} is larger than 2 MB")
    try:
        with wave.open(io.BytesIO(data)) as w:  # PCM only: wave refuses other encodings
            channels, rate, frames = w.getnchannels(), w.getframerate(), w.getnframes()
    except Exception as e:
        raise ValueError(f"{where}: preview {name} is not a PCM WAV ({type(e).__name__})") from e
    if channels not in (1, 2) or not 8000 <= rate <= 96000 or not 0 < frames / rate <= MAX_PREVIEW_S:
        raise ValueError(f"{where}: preview {name} must be mono or stereo, 8-96 kHz, up to {MAX_PREVIEW_S} s")
    return {"data": data, "sha256": hashlib.sha256(data).hexdigest(), "duration_s": round(frames / rate, 3),
            "sample_rate": rate}  # fmt: skip


def load(pack) -> list[dict]:
    """The pack's voices, by file name then file order, checked (an id is unique in the family and is no look id
    of it). ValueError naming the file and the voice."""
    out, seen = [], {look["id"] for look in getattr(pack, "looks", [])}
    for path in sorted(pack.dir.glob("voices/*.yaml")):
        items = yaml.safe_load(path.read_text()) or []
        if not isinstance(items, list):
            raise ValueError(f"{path}: a voices file is a list of voices")  # noqa: TRY004
        for i, x in enumerate(items, 1):
            v = _check(x, f"{path} voice {i}", path.parent)
            if v["id"] in seen:
                raise ValueError(f"{path} voice {i}: id {v['id']} is used twice in {pack.family} (voices and looks)")
            seen.add(v["id"])
            out.append(v)
    if len(out) > MAX_VOICES:
        raise ValueError(f"pack {pack.name}: {len(out)} voices, at most {MAX_VOICES}")
    return out


def preview_key(family: str, v: dict) -> str:
    return f"packs/{family}/voices/{v['id']}-{v['preview']['sha256'][:16]}.wav"


def entries(loaded) -> list[dict]:
    """The registry value's `voices`: every pack's voices (Pack.voices), by pack, then file, then file order."""
    out = []
    for p in loaded:
        for v in p.voices:
            preview = None
            if pv := v["preview"]:
                preview = {"key": preview_key(p.family, v), "bytes": len(pv["data"]),
                           "duration_s": pv["duration_s"], "sample_rate": pv["sample_rate"]}  # fmt: skip
            out.append({"id": f"{p.family}/{v['id']}", "family": p.family, "name": v["name"],
                        "speaker": v["speaker"], "language": v["language"]}
                       | ({"style": v["style"]} if "style" in v else {})
                       | {"version": v["version"], "preview": preview})  # fmt: skip
    return out


async def put_previews(loaded, store) -> None:
    """PUT each preview clip to its content-addressed key unless the key exists (never overwritten)."""
    for p in loaded:
        for v in p.voices:
            if v["preview"] and await store.get(key := preview_key(p.family, v), 0) is None:
                await store.put(key, v["preview"]["data"], "audio/wav")
                log.info("voice preview %s stored", key)
