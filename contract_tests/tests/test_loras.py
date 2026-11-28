"""LoRAs (API 1.9) on the fake runner: own LoRAs uploaded by the signed PUT (synthetic safetensors built here from
contracts/fixtures/loras: random A/B values, the fixture's alphas and metadata) and checked at complete (rank, size,
key style, family, licence, sha256); the upload checks that reject a file (pickle, not safetensors, an 8 MB header, a
full model, a stray key, a bad dtype, offsets, broken pairs, rank); the 2 GB, 50-LoRA and 20 GB caps; rename and
delete; the list (shared ones when a live host publishes any, own ones first, filters).

With a LoRA stack on the fake runner (a node of env fake with a rows param holding a Lora field, Text in and out:
echo/lora-stack), runs of a user workflow that puts it between prompt and echo: made_with.loras, the run's 422s
(strength, duplicate, unknown, pending, family, row cap), a deleted LoRA (reuse says `deleted`, a new run 422, a queued
run fails at dispatch), a batch grid over strengths, and isolation (B: 404 on A's LoRA; B's run, batch, workflow and
pipeline naming it are refused without its name; B's list has only shared and B's own). Those tests are skipped when
the fake runner has no stack.

Not covered: `size differs from the declared bytes` (the signed PUT cannot store another size), hosts redaction of
baked LoRAs (the fake runner bakes nothing)."""

import hashlib
import json
import math
import random
import struct
import uuid
from urllib.parse import urlsplit

import pytest
from conftest import headers, make_client
from helpers import (
    CONTRACTS,
    NO_SUCH_ID,
    QUICK,
    SLOW,
    STUDIO,
    check,
    detail,
    new_session,
    ok,
    start_run,
    take_of_run,
    wait_for,
    wait_run,
)
from test_batches import BATCHES, FAST, finish, rows_of, submit, wait_batch
from test_user_workflows import copy_echo, doc
from test_user_workflows import put as put_workflow

LORAS = f"{STUDIO}/loras"
FIXTURES = CONTRACTS / "fixtures" / "loras"
GB2 = 2147483648
ROWS = "lora.rows"
SIZES = {"F32": 4, "F16": 2, "BF16": 2, "I64": 8}


# ---------------------------------------------------------------- synthetic files


def tensor_bytes(dtype: str, n: int, rnd: random.Random, value: float | None) -> bytes:
    vals = [value] * n if value is not None else [rnd.uniform(-0.01, 0.01) for _ in range(n)]
    f32 = struct.pack(f"<{n}f", *vals)
    if dtype == "F32":
        return f32
    if dtype == "F16":
        return struct.pack(f"<{n}e", *vals)
    if dtype == "BF16":  # the high half of each f32
        out = bytearray(2 * n)
        out[0::2], out[1::2] = f32[2::4], f32[3::4]
        return bytes(out)
    return bytes(SIZES[dtype] * n)


def safetensors(tensors: dict, values=None, metadata=None, seed: int = 0) -> bytes:
    """A safetensors file of these tensors ({key: {dtype, shape}}), keys in code-point order."""
    rnd, header, data = random.Random(seed), {}, bytearray()
    if metadata:
        header["__metadata__"] = metadata
    for key in sorted(tensors):
        t = tensors[key]
        blob = tensor_bytes(t["dtype"], math.prod(t["shape"]), rnd, (values or {}).get(key))
        header[key] = {
            "dtype": t["dtype"],
            "shape": t["shape"],
            "data_offsets": [len(data), len(data) + len(blob)],
        }
        data += blob
    h = json.dumps(header).encode()
    h += b" " * (-len(h) % 8)
    return struct.pack("<Q", len(h)) + h + bytes(data)


def fixture(name: str) -> dict:
    return json.loads((FIXTURES / f"{name}.json").read_text())


def fixture_file(name: str, seed: int = 0) -> bytes:
    fx = fixture(name)
    return safetensors(fx["tensors"], fx.get("values"), fx.get("metadata"), seed)


# ---------------------------------------------------------------- the upload steps


def start(client, filename: str, size: int) -> dict:
    return ok(client.post(LORAS, json={"filename": filename, "bytes": size}), 201)


def put(anon, up: dict, data: bytes) -> None:
    r = anon.put(
        up["upload_url"], content=data, headers={"Content-Type": "application/octet-stream"}
    )
    assert r.status_code == 204, f"PUT lora: {r.status_code} {r.text}"


def complete(client, lid: str):
    return client.post(f"{LORAS}/{lid}/complete")


def uploaded(client, anon, data: bytes, filename: str) -> dict:
    """A ready own LoRA."""
    up = start(client, filename, len(data))
    put(anon, up, data)
    return ok(complete(client, up["lora"]["id"]))


def row(lid: str, strength: float = 1.0, **extra) -> dict:
    return {"lora": {"lora_id": lid}, "strength": strength} | extra


def tag() -> str:
    return uuid.uuid4().hex[:8]


@pytest.fixture(scope="module")
def ul(new_user):
    """User L: this module's LoRAs (a fresh library, so lists and caps are exact)."""
    with make_client(headers(new_user("L")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def stack(ul) -> dict | None:
    return find_stack(ul)


def find_stack(client) -> dict | None:
    """The fake env's Model-free LoRA stack: {kind, family, in, out, field} (rows param `rows`, a Lora field, a Text
    input and output), or None."""
    groups = ok(client.get(f"{STUDIO}/nodes", params={"env": "fake"}))["groups"]
    for n in (n for g in groups for n in g["nodes"]):
        rows = {p["name"]: p for p in n["params"]}.get("rows")
        field = check.lora_field(rows) if rows else None
        text_in = [i["name"] for i in n["inputs"] if i["type"] == "Text"]
        text_out = [o["name"] for o in n["outputs"] if o["type"] == "Text"]
        if field and text_in and text_out:
            return {
                "kind": n["kind"],
                "family": n["family"],
                "in": text_in[0],
                "out": text_out[0],
                "field": field,
            }
    return None


@pytest.fixture(scope="module")
def need_stack(stack) -> dict:
    if stack is None:
        pytest.skip("the fake runner publishes no LoRA stack (echo/lora-stack)")
    return stack


def fits(stack: dict | None) -> str | None:
    """The family a file that fits the qwen-image module names is detected as on this stack (the fake echo family:
    this stack has no torch host, whose qwen-image detection prefers), or None when it cannot be told (no fake
    stack: unknown, or qwen-image when a torch host is in the registry)."""
    return stack["family"] if stack else None


@pytest.fixture(scope="module")
def shared(ul) -> list[dict]:
    return ok(ul.get(LORAS, params={"source": "shared"}))["loras"]


# ---------------------------------------------------------------- uploads


@pytest.mark.parametrize("style", ["peft", "kohya", "comfyui"])
def test_upload_each_key_style(ul, anon, stack, style):
    fx, data = fixture(style), fixture_file(style)
    name = f"{style} {tag()}"
    up = start(ul, f"  {name}.SafeTensors", len(data))
    lora, link = up["lora"], up["upload_url"]
    lid = lora["id"]
    assert (up["content_type"], up["bytes"]) == ("application/octet-stream", len(data))
    path = urlsplit(link).path  # absolute or relative, as the takes' upload links
    assert path.startswith("/files/u/") and path.endswith(f"/loras/{lid}.safetensors")
    assert {
        k: lora[k]
        for k in ("source", "name", "state", "rank", "key_style", "sha256", "hosts", "size_bytes")
    } == {
        "source": "mine",
        "name": name,
        "state": "pending",
        "rank": None,
        "key_style": None,
        "sha256": None,
        "hosts": [],
        "size_bytes": len(data),
    }
    assert detail(complete(ul, lid), 409) == "upload not received"
    assert ok(ul.get(f"{LORAS}/{lid}"))["state"] == "pending"
    put(anon, up, data)
    got = ok(complete(ul, lid))
    want = fx["expect"]
    assert {
        k: got[k] for k in ("id", "state", "rank", "key_style", "licence", "sha256", "reason")
    } == {
        "id": lid,
        "state": "ready",
        "rank": want["rank"],
        "key_style": want["key_style"],
        "licence": want["licence"],
        "sha256": hashlib.sha256(data).hexdigest(),
        "reason": None,
    }
    assert got["family"] == (fits(stack) or got["family"]) and got["family"] in {
        "unknown",
        "qwen-image",
        fits(stack),
    }
    assert ok(complete(ul, lid)) == got  # again: the same answer
    assert ok(ul.get(f"{LORAS}/{lid}")) == got
    assert lid in {x["id"] for x in ok(ul.get(LORAS, params={"source": "mine"}))["loras"]}


def header_only(length: int, body: bytes) -> bytes:
    return struct.pack("<Q", length) + body


PEFT = fixture("peft")["tensors"]
REJECTS = {
    "text": (b"just some text, not a safetensors file\n", "not a safetensors file"),
    "pickle": (
        b"\x80\x04\x95" + bytes(64),
        "pickle checkpoints are not accepted; convert it to safetensors",
    ),
    "zip": (
        b"PK\x03\x04" + bytes(64),
        "pickle checkpoints are not accepted; convert it to safetensors",
    ),
    "big-header": (header_only(8388616, b" " * 8388616), "safetensors header is larger than 8 MB"),
    "not-an-object": (header_only(8, b"[]      "), "not a safetensors file"),
    "full-model": (
        safetensors({"transformer_blocks.0.attn.to_q.weight": {"dtype": "F32", "shape": [4, 4]}}),
        "no LoRA keys found (is it a full model?)",
    ),
    "stray-key": (
        safetensors(
            PEFT
            | {
                "transformer.transformer_blocks.0.attn.to_q.weight": {
                    "dtype": "BF16",
                    "shape": [4, 4],
                }
            }
        ),
        "transformer.transformer_blocks.0.attn.to_q.weight is not a LoRA weight",
    ),
    "dtype": (
        safetensors(
            PEFT
            | {
                "transformer.transformer_blocks.0.attn.to_q.lora_A.weight": {
                    "dtype": "I64",
                    "shape": [4, 3072],
                }
            }
        ),
        "tensor transformer.transformer_blocks.0.attn.to_q.lora_A.weight has dtype I64; LoRAs must be F32, F16 or BF16",
    ),
    "rank": (
        safetensors(
            {
                "transformer.proj_out.lora_A.weight": {"dtype": "BF16", "shape": [4097, 1]},
                "transformer.proj_out.lora_B.weight": {"dtype": "BF16", "shape": [1, 4097]},
            }
        ),
        "rank 4097 is over 4096",
    ),
}


def outside() -> bytes:
    """A peft pair whose B claims bytes past the end of the data."""
    good = safetensors({k: v for k, v in PEFT.items() if ".to_q." in k})
    n = struct.unpack("<Q", good[:8])[0]
    head = json.loads(good[8 : 8 + n])
    key = "transformer.transformer_blocks.0.attn.to_q.lora_B.weight"
    start_, end = head[key]["data_offsets"]
    head[key]["data_offsets"] = [start_ + 2, end + 2]
    h = json.dumps(head).encode()
    h += b" " * (-len(h) % 8)
    return struct.pack("<Q", len(h)) + h + good[8 + n :]


REJECTS["outside"] = (
    outside(),
    "tensor transformer.transformer_blocks.0.attn.to_q.lora_B.weight lies outside the file",
)


@pytest.mark.parametrize("case", sorted(REJECTS))
def test_rejected_uploads(ul, anon, case):
    data, want = REJECTS[case]
    up = start(ul, f"reject {case}.safetensors", len(data))
    put(anon, up, data)
    lid = up["lora"]["id"]
    assert detail(complete(ul, lid), 422) == want
    got = ok(ul.get(f"{LORAS}/{lid}"))
    assert (got["state"], got["reason"], got["name"]) == ("rejected", want, f"reject {case}")
    assert detail(complete(ul, lid), 422) == want  # again: the same answer
    assert ok(ul.delete(f"{LORAS}/{lid}"), 204) is None


def test_broken_pairs_are_rejected(ul, anon):
    """to_k's ranks differ and to_v has no B: the contract names one of them (README, LoRA upload checks)."""
    data = fixture_file("broken-pairs")
    up = start(ul, "broken.safetensors", len(data))
    put(anon, up, data)
    got = detail(complete(ul, up["lora"]["id"]), 422)
    assert got in {
        "transformer_blocks.0.attn.to_k: lora_A and lora_B ranks differ",
        "transformer_blocks.0.attn.to_v: lora_B is missing",
    }, got
    ul.delete(f"{LORAS}/{up['lora']['id']}")


def test_create_rejects_bad_requests(ul):
    for body, want in (
        ({"filename": "style.ckpt", "bytes": 10}, "only .safetensors files can be uploaded"),
        (
            {"filename": "style.safetensors.zip", "bytes": 10},
            "only .safetensors files can be uploaded",
        ),
        ({"filename": "style.safetensors", "bytes": GB2 + 1}, "file is larger than 2 GB"),
    ):
        assert detail(ul.post(LORAS, json=body), 422) == want, body
    for body in (
        {"filename": "", "bytes": 10},
        {"filename": "a.safetensors", "bytes": 0},
        {"filename": "a.safetensors"},
        {"filename": "a.safetensors", "bytes": 10, "extra": 1},
    ):
        assert ul.post(LORAS, json=body).status_code == 422, body
    biggest = start(ul, "biggest.safetensors", GB2)  # exactly 2 GiB is allowed
    assert ok(ul.delete(f"{LORAS}/{biggest['lora']['id']}"), 204) is None


def test_caps(new_user):
    with make_client(headers(new_user("Lcount")["access_token"])) as c:
        ids = [start(c, f"n{i}.safetensors", 10)["lora"]["id"] for i in range(50)]
        r = c.post(LORAS, json={"filename": "one more.safetensors", "bytes": 10})
        assert detail(r, 409) == "You have 50 LoRAs; delete some first."
        assert ok(c.delete(f"{LORAS}/{ids[0]}"), 204) is None  # frees its count
        start(c, "one more.safetensors", 10)
    with make_client(headers(new_user("Lbytes")["access_token"])) as c:
        ids = [
            start(c, f"big{i}.safetensors", GB2)["lora"]["id"] for i in range(10)
        ]  # 20 GiB exactly
        r = c.post(LORAS, json={"filename": "byte.safetensors", "bytes": 1})
        assert detail(r, 409) == "Your LoRAs would use more than 20 GB; delete some first."
        assert ok(c.delete(f"{LORAS}/{ids[0]}"), 204) is None
        r = c.post(LORAS, json={"filename": "byte.safetensors", "bytes": 1})
        # its object stays until the sweep: delete and re-upload can't grow the store past the cap
        assert detail(r, 409) == "Your LoRAs would use more than 20 GB; delete some first."


# ---------------------------------------------------------------- the library


@pytest.fixture(scope="module")
def ready(ul, anon) -> dict:
    """L's ready LoRAs: peft and kohya (they fit the qwen-image names), other (Flux names: family unknown)."""
    return {
        "peft": uploaded(ul, anon, fixture_file("peft", 1), f"secret-peft-{tag()}.safetensors"),
        "kohya": uploaded(ul, anon, fixture_file("kohya", 1), f"kohya {tag()}.safetensors"),
        "other": uploaded(ul, anon, fixture_file("other-model"), "flux-style.safetensors"),
    }


def test_list_order_and_filters(ul, ready, shared, stack):
    loras = ok(ul.get(LORAS))["loras"]
    own = [x for x in loras if x["source"] == "mine"]
    assert loras[: len(own)] == own  # own first
    assert [x["created_at"] for x in own] == sorted((x["created_at"] for x in own), reverse=True)
    assert [x["id"] for x in loras[len(own) :]] == [x["id"] for x in shared]
    names = [(x["name"].casefold(), x["id"]) for x in shared]
    assert names == sorted(names)
    mine = ok(ul.get(LORAS, params={"source": "mine"}))["loras"]
    assert mine == own and all(x["hosts"] == [] for x in mine)
    other = ready["other"]
    assert other["family"] == "unknown" and other["rank"] == 32
    assert other["id"] in {
        x["id"] for x in ok(ul.get(LORAS, params={"family": "unknown"}))["loras"]
    }
    if stack:
        fam = {x["id"] for x in ok(ul.get(LORAS, params={"family": stack["family"]}))["loras"]}
        assert {ready["peft"]["id"], ready["kohya"]["id"]} <= fam and other["id"] not in fam
    words = ready["peft"]["name"].upper().split("-")[:2]  # "SECRET PEFT": every word, any case
    hits = ok(ul.get(LORAS, params={"q": " ".join(words)}))["loras"]
    assert ready["peft"]["id"] in {x["id"] for x in hits} and ready["kohya"]["id"] not in {
        x["id"] for x in hits
    }
    assert ul.get(LORAS, params={"source": "everyone"}).status_code == 422


def test_shared_loras(ul, shared):
    if not shared:
        pytest.skip("no live host publishes a shared LoRA")
    for x in shared:
        assert (x["source"], x["state"], x["id"], x["reason"]) == (
            "shared",
            "ready",
            x["sha256"],
            None,
        )
        assert x["hosts"] and x["hosts"] == sorted(x["hosts"])
        assert ok(ul.get(f"{LORAS}/{x['id']}")) == x
    lid = shared[0]["id"]
    assert (
        detail(ul.patch(f"{LORAS}/{lid}", json={"name": "mine now"}), 409)
        == "shared LoRAs are managed on their host"
    )
    assert detail(ul.delete(f"{LORAS}/{lid}"), 409) == "shared LoRAs are managed on their host"
    assert complete(ul, lid).status_code == 404


def test_rename(ul, anon):
    lora = uploaded(ul, anon, fixture_file("peft", 2), "to rename.safetensors")
    path = f"{LORAS}/{lora['id']}"
    got = ok(ul.patch(path, json={"name": "  Ink wash  "}))
    assert got == lora | {"name": "Ink wash"}
    for name, want in (
        ("   ", "name must be 1-80 characters"),
        ("x" * 81, "name must be 1-80 characters"),
        ("bell\x07", "name must not contain control characters"),
    ):
        assert detail(ul.patch(path, json={"name": name}), 422) == want
    assert ul.patch(path, json={"name": "a", "extra": 1}).status_code == 422
    assert ok(ul.get(path))["name"] == "Ink wash"


def test_delete_and_unknown_ids(ul, anon):
    lora = uploaded(ul, anon, fixture_file("kohya", 2), "to delete.safetensors")
    path = f"{LORAS}/{lora['id']}"
    assert ok(ul.delete(path), 204) is None
    for r in (
        ul.get(path),
        ul.patch(path, json={"name": "x"}),
        ul.delete(path),
        complete(ul, lora["id"]),
    ):
        assert r.status_code == 404, r.request.method
    assert lora["id"] not in {x["id"] for x in ok(ul.get(LORAS))["loras"]}
    for lid in (NO_SUCH_ID, "0" * 64, "not-an-id"):
        p = f"{LORAS}/{lid}"
        assert ul.get(p).status_code == 404
        assert ul.patch(p, json={"name": "x"}).status_code == 404
        assert ul.delete(p).status_code == 404
        assert complete(ul, lid).status_code == 404


# ---------------------------------------------------------------- runs with a LoRA stack


@pytest.fixture(scope="module")
def lwf(ul, need_stack) -> dict:
    """L's copy of echo with the stack between prompt and echo (rows [])."""
    return stack_workflow(ul, need_stack)


def stack_workflow(client, stack: dict, rows: list | None = None) -> dict:
    w = copy_echo(client, "Contract LoRA echo")
    d = doc(w)
    d["nodes"]["lora"] = {
        "kind": stack["kind"],
        "in": {stack["in"]: "prompt.text"},
        "params": {"rows": rows or []},
    }
    d["nodes"]["echo"]["in"]["text"] = f"lora.{stack['out']}"
    return put_workflow(client, w["id"], d, 1)


@pytest.fixture(scope="module")
def sid(ul) -> str:
    return new_session(ul, "Contract LoRAs")["id"]


def run_take(client, sid: str, wid: str, inputs: dict) -> dict:
    run = start_run(client, sid, QUICK | {"prompt.text": f"a fox {tag()}"} | inputs, wid)
    done = wait_run(client, run["id"])
    assert done["status"] == "succeeded", done
    return take_of_run(client, run["id"])


def refused(client, sid: str, wid: str, inputs: dict) -> str:
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": wid, "inputs": QUICK | inputs}
    )
    return detail(r, 422)


def test_empty_stack_is_the_identity(ul, sid, lwf):
    assert lwf["problems"] == []
    take = run_take(ul, sid, lwf["id"], {})
    assert not take["made_with"].get("loras")


def test_run_records_its_loras(ul, sid, lwf, ready):
    p, k = ready["peft"], ready["kohya"]
    take = run_take(
        ul, sid, lwf["id"], {ROWS: [row(p["id"], 0.8, mode="adapter"), row(k["id"], 1.5)]}
    )
    mw = take["made_with"]
    assert mw["loras"] == [
        {
            "node": "lora",
            "id": p["id"],
            "source": "mine",
            "name": p["name"],
            "strength": 0.8,
            "mode": "adapter",
            "sha256": p["sha256"],
        },
        {
            "node": "lora",
            "id": k["id"],
            "source": "mine",
            "name": k["name"],
            "strength": 1.5,
            "mode": "adapter",
            "sha256": k["sha256"],
        },
    ]
    assert [r["lora"] for r in mw["inputs"][ROWS]] == [{"lora_id": p["id"]}, {"lora_id": k["id"]}]
    assert ok(ul.get(f"{LORAS}/{p['id']}"))["thumb_url"] is not None  # its first take
    reuse = ok(ul.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert reuse["loras_status"] == [
        {"id": p["id"], "status": "current"},
        {"id": k["id"], "status": "current"},
    ]


def test_a_saved_row_without_strength_or_mode_runs_with_their_defaults(ul, sid, ready, need_stack):
    p = ready["peft"]
    w = stack_workflow(ul, need_stack, [{"lora": {"lora_id": p["id"]}}])
    take = run_take(ul, sid, w["id"], {})
    assert [(x["strength"], x["mode"]) for x in take["made_with"]["loras"]] == [(1, "adapter")]


def test_run_refusals(ul, anon, sid, lwf, ready, need_stack):
    wid, p = lwf["id"], ready["peft"]
    item = "lora.rows[1].lora"
    pending = start(ul, "pending.safetensors", 100)["lora"]["id"]
    bad = start(ul, "bad.safetensors", 66)
    put(anon, bad, b"\x80\x04" + bytes(64))
    assert complete(ul, bad["lora"]["id"]).status_code == 422
    for rows, want in (
        ([row(p["id"], 2.5)], "lora.rows[1].strength: must be between 0 and 2"),
        ([row(p["id"], -0.5)], "lora.rows[1].strength: must be between 0 and 2"),
        (
            [row(p["id"]), row(p["id"], 0.5)],
            f"lora.rows[2].lora: LoRA {p['name']} is already in row 1",
        ),
        ([row(NO_SUCH_ID)], f"{item}: LoRA {NO_SUCH_ID} not found"),
        ([row("0" * 64)], f"{item}: LoRA {'0' * 64} not found"),
        ([row(pending)], f"{item}: LoRA {pending} not found"),
        ([row(bad["lora"]["id"])], f"{item}: LoRA {bad['lora']['id']} not found"),
        (
            [row(ready["other"]["id"])],
            f"{item}: LoRA flux-style does not fit {need_stack['family']} (its model is unknown)",
        ),
        ([row(p["id"])] * 9, "lora.rows: at most 8 items"),
    ):
        assert refused(ul, sid, wid, {ROWS: rows}) == want


def test_a_deleted_lora(ul, anon, sid, lwf):
    lora = uploaded(ul, anon, fixture_file("comfyui"), f"gone {tag()}.safetensors")
    take = run_take(ul, sid, lwf["id"], {ROWS: [row(lora["id"], 0.6)]})
    assert ok(ul.delete(f"{LORAS}/{lora['id']}"), 204) is None
    assert take["made_with"]["loras"][0]["name"] == lora["name"]  # the take keeps it
    reuse = ok(ul.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert reuse["loras_status"] == [{"id": lora["id"], "status": "deleted"}]
    r = ul.post(
        f"{STUDIO}/sessions/{sid}/runs",
        json={"workflow_id": reuse["workflow_id"], "inputs": reuse["inputs"]},
    )
    assert detail(r, 422) == f"lora.rows[1].lora: LoRA {lora['id']} not found"


def test_deleted_before_release_fails_at_dispatch(ul, anon, sid, lwf):
    lora = uploaded(ul, anon, fixture_file("kohya", 3), f"late {tag()}.safetensors")
    slow = start_run(ul, sid, SLOW | {"prompt.text": f"hold the host {tag()}"})
    try:
        wait_for(
            lambda: ok(ul.get(f"{STUDIO}/runs/{slow['id']}"))["status"] == "running",
            what="slow run running",
        )
        queued = start_run(ul, sid, QUICK | {ROWS: [row(lora["id"])]}, lwf["id"])
        assert ok(ul.delete(f"{LORAS}/{lora['id']}"), 204) is None
    finally:
        ul.post(f"{STUDIO}/runs/{slow['id']}/cancel")
    done = wait_run(ul, queued["id"])
    assert (done["status"], done["error"]) == ("failed", f"LoRA {lora['name']} was deleted")


def test_batch_grid_over_strengths(ul, lwf, ready):
    lid = ready["peft"]["id"]
    axes = [{"param": ROWS, "values": [[row(lid, 0.5)], [row(lid, 1.0)]]}]
    batch = submit(
        ul, {"grid": {"axes": axes}}, FAST | {"prompt.text": f"grid {tag()}"}, workflow_id=lwf["id"]
    )
    try:
        assert wait_batch(ul, batch["id"])["status"] == "done"
        rows = rows_of(ul, batch["id"])
        assert [r["status"] for r in rows] == ["succeeded", "succeeded"]
        got = []
        for r in rows:
            (tid,) = r["take_ids"]
            got.append(ok(ul.get(f"{STUDIO}/takes/{tid}"))["made_with"]["loras"][0]["strength"])
        assert got == [0.5, 1.0]
    finally:
        finish(ul, batch["id"])


# ---------------------------------------------------------------- isolation


def test_isolation_of_the_library(ul, api_b, anon, ready, shared):
    lid = ready["peft"]["id"]
    path = f"{LORAS}/{lid}"
    for r in (
        api_b.get(path),
        api_b.patch(path, json={"name": "x"}),
        api_b.delete(path),
        complete(api_b, lid),
    ):
        assert r.status_code == 404, r.request.method
    assert ok(ul.get(path))["name"] == ready["peft"]["name"]  # untouched
    mine = uploaded(
        api_b, anon, fixture_file("peft", 1), "b's own.safetensors"
    )  # the same bytes: B's own LoRA
    ids = [x["id"] for x in ok(api_b.get(LORAS))["loras"]]
    assert set(ids) == {mine["id"]} | {x["id"] for x in shared}
    assert ok(api_b.get(LORAS, params={"q": "secret"}))["loras"] == [
        x for x in shared if "secret" in x["name"].casefold()
    ]


def no_name(text: str, lora: dict) -> None:
    assert lora["name"] not in text, f"leaked the name of another user's LoRA: {text}"


def test_isolation_of_runs(ul, api_b, ready, need_stack):
    a = ready["peft"]
    rows = [row(a["id"])]
    b_sid = new_session(api_b, "Contract B LoRAs")["id"]
    b_wf = stack_workflow(api_b, need_stack)
    want = f"lora.rows[1].lora: LoRA {a['id']} not found"
    assert refused(api_b, b_sid, b_wf["id"], {ROWS: rows}) == want

    # a batch naming A's LoRA: refused at Submit, or its row fails when its run is made
    body = {
        "workflow_id": b_wf["id"],
        "rows": {"grid": {"axes": [{"param": ROWS, "values": [rows]}]}},
        "inputs": FAST,
    }
    r = api_b.post(BATCHES, json=body)
    if r.status_code == 202:
        bid = r.json()["id"]
        try:
            wait_batch(api_b, bid)
            (got,) = rows_of(api_b, bid)
            assert got["status"] == "failed" and got["error"].endswith(want), got
        finally:
            finish(api_b, bid)
    else:
        text = detail(r, 422)
        assert text.endswith(want), text

    # B's workflow holding A's id: lora_unknown (blocks running only), and the run says nothing of A's LoRA
    d = doc(b_wf)
    d["nodes"]["lora"]["params"]["rows"] = rows
    saved = put_workflow(api_b, b_wf["id"], d, b_wf["version"])
    assert ("lora_unknown", "lora", "rows") in {
        (p["code"], p.get("node"), p.get("param")) for p in saved["problems"]
    }
    for p in saved["problems"]:
        no_name(p["message"], a)
    text = detail(
        api_b.post(
            f"{STUDIO}/sessions/{b_sid}/runs", json={"workflow_id": b_wf["id"], "inputs": QUICK}
        ),
        422,
    )
    no_name(text, a)

    # B's pipeline whose step names A's LoRA
    step = {
        "kind": "step",
        "workflow": b_wf["id"],
        "inputs": FAST | {ROWS: rows},
        "bind": {"prompt.text": "$prompt"},
    }
    pipe = {
        "title": "Contract B LoRA pipeline",
        "nodes": {
            "inputs": {"kind": "inputs", "inputs": [{"name": "prompt", "type": "Text"}]},
            "s1": step,
            "out": {"kind": "output", "from": "s1.takes", "collection": "Contract B LoRA"},
        },
        "edges": [["inputs", "s1"], ["s1", "out"]],
    }
    pid = ok(api_b.post(f"{STUDIO}/pipelines", json=pipe), 201)["id"]
    body = {"pipeline_id": pid, "rows": {"lines": {"param": "inputs.prompt", "values": ["x"]}}}
    r = api_b.post(BATCHES, json=body)
    if r.status_code == 202:
        bid = r.json()["id"]
        try:
            wait_batch(api_b, bid)
            (got,) = rows_of(api_b, bid)
            assert got["status"] == "failed" and "not found" in got["error"], got
            no_name(got["error"], a)
        finally:
            finish(api_b, bid)
    else:
        no_name(detail(r, 422), a)
    # B is shared with other modules (test_user_workflows' isolation expects B's own workflows only there)
    assert api_b.delete(f"{STUDIO}/pipelines/{pid}").status_code == 204
    assert api_b.delete(f"{STUDIO}/workflows/{b_wf['id']}").status_code == 204
