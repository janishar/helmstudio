"""Starter looks (core/looks.py): the packs' YAML is checked at load and published on contract; the echo pack's value
is the canonical fixture; a resolved look reaches the executor as an ordinary graph."""

import io
import json
import types

import pytest
from conftest import (
    CONTRACTS,
    IMPLS,
    FakeJS,
    FakeMsg,
    FakeStore,
    command,
    consumer,
    safetensors_file,
)
from PIL import Image

from core import looks, lora, pack, registry
from messaging import schemas

FIXTURES = CONTRACTS / "fixtures"


def fake_pack(tmp_path, files: dict, family="echo", spec=None):
    """A pack folder holding only looks/<name> files (text or bytes)."""
    for name, body in files.items():
        p = tmp_path / "looks" / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(body if isinstance(body, bytes) else body.encode())
    p = types.SimpleNamespace(name="t", dir=tmp_path, family=family, spec=spec or {})
    p.looks = looks.load(p)
    return p


def library(tmp_path, files: dict[str, str]):
    """A shared LoRA index over <models>/<rel> files that fit `echo` (contract module names)."""
    models = tmp_path / "models"
    shapes = {"transformer.img_in.lora_A.weight": ("BF16", [4, 64]),
              "transformer.img_in.lora_B.weight": ("BF16", [64, 4])}  # fmt: skip
    for rel in files:
        safetensors_file(models / rel, shapes)
    (echo,) = [p for p, _ in pack.for_env("fake")]
    lib = lora.Library(models, tmp_path / "cache")
    lib.scan({"echo": pack.lora_block(echo)})
    return lib, models


def test_echo_pack_publishes_the_canonical_looks_fixture(tmp_path):
    _, _, loaded = registry.build("fake")
    lib, models = library(tmp_path, {})
    value = registry.scanned_value("looks", looks.entries(loaded, lib, models))
    want = json.loads((FIXTURES / "registry/looks.valid.json").read_text())
    strip = {"published_at", "published_by"}
    assert {k: v for k, v in value.items() if k not in strip} == {k: v for k, v in want.items() if k not in strip}


def test_qwen_pack_looks_have_text_a_thumb_and_are_on_contract(tmp_path):
    (q,) = [p for p in pack.PACKS.iterdir() if p.name == "qwen_image"]
    p = types.SimpleNamespace(name="qwen_image", dir=q, family="qwen-image", spec={})
    p.looks = looks.load(p)
    entries = looks.entries([p], *library(tmp_path, {}))
    assert schemas.registry_errors("looks", {"schema_version": 1, "host": "mac", "env": "torch",
        "published_at": "2026-11-14T10:00:00Z", "published_by": "t", "looks": entries}) == []  # fmt: skip
    groups = [e["group"] for e in entries]
    assert all(3 <= groups.count(g) <= 4 for g in looks.GROUPS)
    assert all(e["text"] and not e["loras"] for e in entries)
    # each starter look ships a 16:10 WebP thumbnail beside its YAML (well under the 2 MB cap)
    assert all(
        e["thumb"]["content_type"] == "image/webp"
        and (e["thumb"]["width"], e["thumb"]["height"]) == (640, 400)
        and e["thumb"]["bytes"] < 100_000
        for e in entries
    )


def test_a_look_lora_is_looked_up_in_the_index_by_file(tmp_path):
    lib, models = library(tmp_path, {"loras/echo-grain.safetensors": "", "Other/loras/far.safetensors": ""})
    yaml_text = """
- {id: a, group: film, name: A, lora: {file: echo-grain.safetensors}}
- {id: b, group: film, name: B, text: x, lora: {file: missing.safetensors, strength: 0.5}}
- {id: c, group: film, name: C, lora: {file: far.safetensors}}
"""
    p = fake_pack(tmp_path, {"film.yaml": yaml_text})
    a, b, c = looks.entries([p], lib, models)
    sha = lib.files[models / "loras/echo-grain.safetensors"]  # far is the same bytes: one LoRA, two names
    assert a["loras"] == [{"file": "echo-grain.safetensors", "name": "echo-grain", "strength": 1, "lora_id": sha}]
    assert b["loras"][0]["lora_id"] is None and b["loras"][0]["strength"] == 0.5
    assert c["loras"][0]["lora_id"] is None  # not in a loras/ folder of this family's models


def test_a_look_lora_of_another_family_keeps_its_id(tmp_path):
    # contracts README, Looks: lora_id = the file's SHA-256 from the index; studio's fit says "is for <family>"
    lib, models = library(tmp_path, {"loras/x.safetensors": ""})
    lib.entries[0]["family"] = "qwen-image"
    p = fake_pack(tmp_path, {"a.yaml": "- {id: a, group: film, name: A, lora: {file: x.safetensors}}"})
    assert looks.entries([p], lib, models)[0]["loras"][0]["lora_id"] == lib.entries[0]["id"]


def png(size=(3, 2), fmt="PNG") -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size).save(buf, fmt)
    return buf.getvalue()


async def test_an_image_becomes_a_content_addressed_thumb_put_once(tmp_path):
    data = png()
    p = fake_pack(
        tmp_path,
        {"l.yaml": "- {id: win, group: lighting, name: W, text: t, image: w.png}", "w.png": data},
    )
    (e,) = looks.entries([p], *library(tmp_path, {}))
    import hashlib

    key = f"packs/echo/looks/win-{hashlib.sha256(data).hexdigest()[:16]}.png"
    assert e["thumb"] == {
        "key": key,
        "content_type": "image/png",
        "width": 3,
        "height": 2,
        "bytes": len(data),
    }
    store = FakeStore()
    await looks.put_thumbs([p], store)
    assert store.objects[key] == (data, "image/png")
    store.objects[key] = (b"kept", "image/png")
    await looks.put_thumbs([p], store)
    assert store.objects[key][0] == b"kept"  # never overwritten


@pytest.mark.parametrize(
    ("files", "error"),
    [
        ({"a.yaml": "- {id: Bad, group: film, name: A, text: t}"}, "id must match"),
        ({"a.yaml": "- {id: a, group: character, name: A, text: t}"}, "group must be one of"),
        ({"a.yaml": "- {id: a, group: film, name: '', text: t}"}, "name must be 1-80"),
        ({"a.yaml": "- {id: a, group: film, name: A}"}, "needs text or a lora"),
        ({"a.yaml": "- {id: a, group: film, name: A, text: t, version: 0}"}, "version must be"),
        ({"a.yaml": "- {id: a, group: film, name: A, text: t, extra: 1}"}, "unknown keys"),
        ({"a.yaml": "- {id: a, group: film, name: A, lora: {file: ../x.safetensors}}"}, "no path"),
        (
            {"a.yaml": "- {id: a, group: film, name: A, lora: {file: x.safetensors, strength: 5}}"},
            "0..2",
        ),
        (
            {
                "a.yaml": "- {id: a, group: film, name: A, text: t}",
                "b.yaml": "- {id: a, group: camera, name: B, text: t}",
            },
            "used twice",
        ),
        (
            {"a.yaml": "- {id: a, group: film, name: A, text: t, image: gone.png}"},
            "not a file next to",
        ),
        (
            {
                "a.yaml": "- {id: a, group: film, name: A, text: t, image: x.png}",
                "x.png": b"not an image",
            },
            "does not decode",
        ),
        (
            {
                "a.yaml": "- {id: a, group: film, name: A, text: t, image: x.gif}",
                "x.gif": png(fmt="GIF"),
            },
            "PNG, JPEG or WebP",
        ),
        (
            {
                "a.yaml": "- {id: a, group: film, name: A, text: t, image: x.png}",
                "x.png": png() + bytes(2 * 2**20),
            },
            "larger than 2 MB",
        ),
        ({"a.yaml": "id: a"}, "a list of looks"),
        ({"a.yaml": '- {id: a, group: film, name: A, text: "a\\0b"}'}, "control characters"),
        ({"a.yaml": '- {id: a, group: film, name: "A\\tB", text: t}'}, "control characters"),
        (
            {"a.yaml": "\n".join(f"- {{id: a{i}, group: film, name: A, text: t}}" for i in range(201))},
            "at most 200",
        ),
    ],
)
def test_a_broken_looks_file_stops_the_pack(tmp_path, files, error):
    with pytest.raises(ValueError, match=error):
        fake_pack(tmp_path, files)


async def test_a_command_resolved_with_looks_runs_unchanged_on_the_echo_pack():
    """The resolver's output (contracts fixtures: a prompt with look lines, a user LoRA row and a look's LoRA row at
    its saved strength) placed in the echo + LoRA graph runs as an ordinary command: no look logic here."""
    text = json.loads((FIXTURES / "mentions/resolve.looks-text.json").read_text())["expect"]["inputs"]["prompt.text"]
    rows = json.loads((FIXTURES / "mentions/resolve.looks-images-loras.json").read_text())["expect"]["inputs"]["lora.rows"]  # fmt: skip
    graph = json.loads((FIXTURES / "mentions/fit.json").read_text())["workflows"]["echo-stack"]["nodes"]
    graph["prompt"]["params"]["text"] = text
    shared = {"source": "shared", "name": "ink-wash", "sha256": "3f0a" + "0" * 60, "bytes": 1024}
    # studio fills every default before it sends (graph.problems: param_missing), so the look row gets mode adapter
    look_row = {"mode": "adapter"} | rows[2] | {"lora": rows[2]["lora"] | shared | {"name": "noir-lut"}}
    graph["lora"]["params"]["rows"] = [rows[0] | {"lora": rows[0]["lora"] | shared}, look_row]
    graph["echo"]["params"] = {"steps": 1, "delay_ms": 0, "fail": False, "refs": []}
    graph["output"]["params"] = {}
    cmd = command()
    cmd["data"]["workflow"]["nodes"] = graph
    assert schemas.message_errors(cmd) == []
    seen = []
    echo = IMPLS["echo/echo"]
    impls = IMPLS | {"echo/echo": pack.Node(lambda ctx, p, i: seen.append(i["text"]) or echo.fn(ctx, p, i), code="t")}
    js = FakeJS()
    await consumer(js, impls=impls).handle(FakeMsg(cmd, js))
    assert js.events()[-1]["data"]["status"] == "succeeded"
    assert seen == [f"{text} [LoRA ink-wash x0.8, noir-lut x0.6]"]
