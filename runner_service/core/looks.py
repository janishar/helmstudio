"""Starter looks: runner_service/packs/<pack>/looks/*.yaml (packs/README.md, contracts/README.md "Looks") → the
registry value <host>.<env>.looks. A broken file stops the runner like a broken pack; a LoRA file this host does not
have is published with `lora_id: null` and logged, never an error (one of another family keeps its id: studio's fit
greys the look with `LoRA <n> is for <family>`).

The runner itself needs no look logic: studio's resolver expands a look into the prompt, reference rows and LoRA rows
of the graph it sends."""

import hashlib
import io
import logging
import re
import unicodedata
from pathlib import Path

import yaml
from PIL import Image

log = logging.getLogger(__name__)

GROUPS = ("film", "camera", "colour", "lighting")
SLUG = re.compile(r"^[a-z0-9][a-z0-9-]{0,63}$")
LORA_FILE = re.compile(r"^[^/\\]{1,200}\.safetensors$")
FORMATS = {
    "PNG": ("png", "image/png"),
    "JPEG": ("jpg", "image/jpeg"),
    "WEBP": ("webp", "image/webp"),
}
KEYS = {"id", "group", "name", "text", "version", "lora", "image"}
MAX_IMAGE = 2 * 2**20
MAX_LOOKS = 200


KEPT = str.maketrans("", "", "\n\r\t")


def _control(s: str) -> bool:
    """A control character (Unicode Cc, as studio's unicode.IsControl): never in a name, in text only \\n \\r \\t."""
    return any(unicodedata.category(c) == "Cc" for c in s)


def _number(v) -> bool:
    return isinstance(v, int | float) and not isinstance(v, bool)


def _check(x, where: str, folder: Path) -> dict:
    """One YAML look, defaults filled, its image read: {id, group, name, text, version, lora, image}."""
    if not isinstance(x, dict):
        raise ValueError(f"{where}: a look is a mapping")  # noqa: TRY004
    if extra := set(x) - KEYS:
        raise ValueError(f"{where}: unknown keys {sorted(extra)}")
    look = {"text": "", "version": 1, "lora": None, "image": None} | x
    if not isinstance(look.get("id"), str) or not SLUG.match(look["id"]):
        raise ValueError(f"{where}: id must match {SLUG.pattern}")
    where = f"{where} ({look['id']})"
    if look.get("group") not in GROUPS:
        raise ValueError(f"{where}: group must be one of {', '.join(GROUPS)}")
    if not isinstance(look.get("name"), str) or not 1 <= len(look["name"]) <= 80:
        raise ValueError(f"{where}: name must be 1-80 characters")
    if not isinstance(look["text"], str) or len(look["text"]) > 2000:
        raise ValueError(f"{where}: text must be at most 2000 characters")
    if _control(look["name"]) or _control(look["text"].translate(KEPT)):
        raise ValueError(
            f"{where}: name must not contain control characters, text none but newlines and tabs"
        )
    if (
        not isinstance(look["version"], int)
        or isinstance(look["version"], bool)
        or look["version"] < 1
    ):
        raise ValueError(f"{where}: version must be an integer >= 1")
    if (lora := look["lora"]) is not None:
        if not isinstance(lora, dict) or set(lora) - {"file", "strength"}:
            raise ValueError(f"{where}: lora is {{file, strength}}")
        lora = look["lora"] = {"strength": 1} | lora
        if not isinstance(lora.get("file"), str) or not LORA_FILE.match(lora["file"]):
            raise ValueError(f"{where}: lora.file must be a .safetensors file name (no path)")
        if not _number(lora["strength"]) or not -4 <= lora["strength"] <= 4:
            raise ValueError(f"{where}: lora.strength must be a number in -4..4")
    if not look["text"] and lora is None:
        raise ValueError(f"{where}: a look needs text or a lora")
    if look["image"] is not None:
        look["image"] = _image(look["image"], where, folder)
    return look


def _image(name, where: str, folder: Path) -> dict:
    """The look's thumbnail next to the YAML: {data, ext, content_type, width, height, sha256}."""
    if not isinstance(name, str) or "/" in name or "\\" in name or not (folder / name).is_file():
        raise ValueError(f"{where}: image {name!r} is not a file next to the YAML")
    data = (folder / name).read_bytes()
    if len(data) > MAX_IMAGE:
        raise ValueError(f"{where}: image {name} is larger than 2 MB")
    try:
        with Image.open(io.BytesIO(data)) as im:
            fmt, (width, height) = im.format, im.size
            im.verify()
    except Exception as e:
        raise ValueError(f"{where}: image {name} does not decode ({type(e).__name__})") from e
    if fmt not in FORMATS:
        raise ValueError(f"{where}: image {name} must be PNG, JPEG or WebP (it is {fmt})")
    ext, content_type = FORMATS[fmt]
    return {"data": data, "ext": ext, "content_type": content_type, "width": width,
            "height": height, "sha256": hashlib.sha256(data).hexdigest()}  # fmt: skip


def load(pack) -> list[dict]:
    """The pack's looks, by file name then file order, checked. ValueError naming the file and the look."""
    out, seen = [], set()
    for path in sorted(pack.dir.glob("looks/*.yaml")):
        items = yaml.safe_load(path.read_text()) or []
        if not isinstance(items, list):
            raise ValueError(f"{path}: a looks file is a list of looks")  # noqa: TRY004
        for i, x in enumerate(items, 1):
            look = _check(x, f"{path} look {i}", path.parent)
            if look["id"] in seen:
                raise ValueError(f"{path} look {i}: id {look['id']} is used twice in {pack.family}")
            seen.add(look["id"])
            out.append(look)
    if len(out) > MAX_LOOKS:
        raise ValueError(f"pack {pack.name}: {len(out)} looks, at most {MAX_LOOKS}")
    return out


def thumb_key(family: str, look: dict) -> str:
    img = look["image"]
    return f"packs/{family}/looks/{look['id']}-{img['sha256'][:16]}.{img['ext']}"


def lora_id(pack, file: str, library, models: Path) -> str | None:
    """The SHA-256 of `file` in the host's shared LoRA index, looked up in <models>/loras/ and the loras/ folder of
    each of the family's model folders, whatever family it indexes to (another one is logged); else None (logged)."""
    folders = [models / "loras"] + [models / v["dir"] / "loras" for v in pack.spec.get("variants", {}).values()]  # fmt: skip
    families = {e["id"]: e["family"] for e in library.entries}
    for p in (f / file for f in folders):
        if (sha := library.files.get(p)) is None:
            continue
        if families.get(sha) != pack.family:
            log.warning("look LoRA %s is for %s, not %s: studio greys the look", p, families.get(sha), pack.family)  # fmt: skip
        return sha
    log.warning("look LoRA %s is not on this host: published unavailable", file)
    return None


def entries(loaded, library, models: Path) -> list[dict]:
    """The registry value's `looks`: every pack's looks (Pack.looks), by pack, then file, then file order."""
    out = []
    for p in loaded:
        for look in p.looks:
            loras = []
            if lora := look["lora"]:
                loras.append({"file": lora["file"], "name": lora["file"].removesuffix(".safetensors")[:80],
                              "strength": lora["strength"], "lora_id": lora_id(p, lora["file"], library, models)})  # fmt: skip
            thumb = None
            if img := look["image"]:
                thumb = {"key": thumb_key(p.family, look), "content_type": img["content_type"],
                         "width": img["width"], "height": img["height"], "bytes": len(img["data"])}  # fmt: skip
            out.append({"id": f"{p.family}/{look['id']}", "family": p.family, "group": look["group"],
                        "name": look["name"], "text": look["text"], "version": look["version"],
                        "loras": loras, "thumb": thumb})  # fmt: skip
    return out


async def put_thumbs(loaded, store) -> None:
    """PUT each look image to its content-addressed key unless the key exists (never overwritten)."""
    for p in loaded:
        for look in p.looks:
            if (img := look["image"]) and await store.get(
                key := thumb_key(p.family, look), 0
            ) is None:
                await store.put(key, img["data"], img["content_type"])
                log.info("look thumbnail %s stored", key)
