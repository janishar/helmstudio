"""MiniMax H3 Image (API 1.20, contracts/README.md "How the pieces meet", the minimax-h3-image bullet) through Kong.

Without a GPU: when a live torch runner publishes `minimax-h3-image`, its template and node schemas are the
canonical fixtures (contracts/fixtures/registry/minimax-h3-image.*.valid.json), the model lists a vae kept at fp32,
and "Use in…" on an image offers its Edit (contracts/fixtures/video/uses.json); otherwise these skip. fp32 pins on
the fake host: test_hosts_admin.

Opt-in (MINIMAX=1, the Mac torch runner joined to the stack BASE_URL points at, as test_qwen): one T2I with an
element mention (Mentions step 2: the auto canvas pinned to fixed, no user rows), one enhanced T2I (Run.resolved.texts
`enhance.text`, never in made_with) and one edit pre-filled by "Use in…" (the first ref's aspect). Each takes a few
minutes at the template's 2048 x 2048; a cold load is most of it."""

import io
import json
import os

import pytest
from conftest import headers, make_client, sign_in
from helpers import STUDIO, TERMINAL, new_session, ok, start_run, upload, wait_for
from PIL import Image
from test_elements import element
from test_qwen import events_of, get
from test_user_workflows import CONTRACTS, check
from test_video_source import mfacts, uses

WF = "minimax-h3-image"
REG = CONTRACTS / "fixtures" / "registry"
TIMEOUT = 15 * 60
SEED = 42
gpu = pytest.mark.skipif(
    os.environ.get("MINIMAX") != "1", reason="set MINIMAX=1 with the mac torch runner up"
)


@pytest.fixture(scope="module")
def mm_user(new_user) -> dict:
    return new_user("MiniMax")


@pytest.fixture(scope="module")
def mapi(mm_user):
    """User MiniMax; a 401 signs in again (a run can outlast the 15-minute access token)."""
    with make_client(headers(mm_user["access_token"])) as c:

        def renew(r):
            if r.status_code == 401 and "/auth/" not in r.request.url.path:
                fresh = sign_in(mm_user["email"], mm_user["password"])["access_token"]
                c.headers["Authorization"] = f"Bearer {fresh}"

        c.event_hooks["response"].append(renew)
        yield c


@pytest.fixture(scope="module")
def live(mapi) -> dict:
    """The published minimax-h3-image workflow, or skip."""
    r = mapi.get(f"{STUDIO}/workflows/{WF}")
    if r.status_code == 404:
        pytest.skip(f"no live torch runner publishes {WF}")
    return ok(r)


def canonical(name: str) -> dict:
    return json.loads((REG / f"{WF}.{name}.valid.json").read_text())


# ---------------------------------------------------------------- the registry (no GPU)


def test_template_is_the_canonical_one(live):
    (want,) = canonical("workflows")["workflows"]
    assert (live["title"], live["action"], live["family"], live["env"]) == (
        want["title"],
        want["action"],
        WF,
        "torch",
    )

    def shape(nodes: dict) -> dict:
        return {n: (v["kind"], v.get("in") or {}, bool(v.get("bypass"))) for n, v in nodes.items()}

    assert shape(live["nodes"]) == shape(want["nodes"])
    assert live["studio"] == want["studio"]
    nodes = {n["kind"]: n for n in canonical("nodes")["nodes"]}
    for kind, schema in live["node_schemas"].items():
        for key, value in nodes[kind].items():  # what the fixture states is what is served
            assert schema.get(key) == value, (kind, key)
    width = next(p for p in live["node_schemas"][f"{WF}/canvas"]["params"] if p["name"] == "width")
    assert (width["min"], width["max"]) == (256, 8192)


def test_torch_host_and_fp32_vae(mapi, live):
    hosts = get(mapi, f"{STUDIO}/hosts")["hosts"]
    assert [h for h in hosts if h["env"] == "torch" and h["state"] in ("idle", "busy")], hosts
    models = [m for m in get(mapi, f"{STUDIO}/models")["models"] if m["id"] == WF]
    if not models:
        pytest.skip(f"{WF} is not in the model catalog")
    (variant,) = [v for v in models[0]["variants"] if v["id"] == "base"]
    vae = {c["component"]: c for c in variant["components"]}["vae"]
    assert "fp32" in vae["precisions"], vae


def test_use_in_on_an_image_offers_the_edit(mapi, anon, live):
    img = upload(mapi, anon, gradient_png(64, 48))
    pal = live["node_schemas"]
    want = check.take_uses(mfacts(img), [{k: v for k, v in live.items() if v is not None}], pal)
    got = [u for u in uses(mapi, img["id"]) if u["workflow_id"] == WF]
    assert got == want and len(got) == 1, got
    assert (got[0]["action"], got[0]["param"]) == ("Edit", "encode.refs")
    used = ok(mapi.post(f"{STUDIO}/takes/{img['id']}/use", json={"workflow_id": WF}))
    assert (
        used["inputs"] == got[0]["inputs"] == {"encode.refs": [{"image": {"take_id": img["id"]}}]}
    )


def gradient_png(width: int, height: int) -> bytes:
    """A gradient (so an edit has something to keep) as PNG bytes."""
    buf = io.BytesIO()
    Image.linear_gradient("L").resize((width, height)).convert("RGB").save(buf, "PNG")
    return buf.getvalue()


# ---------------------------------------------------------------- runs (MINIMAX=1)


def run_minimax(client, sid: str, inputs: dict) -> tuple[dict, list[dict], dict]:
    """A succeeded minimax-h3-image run: (the run, its events, its take)."""
    run = start_run(client, sid, inputs, workflow_id=WF)
    done = wait_for(
        lambda: (r := get(client, f"{STUDIO}/runs/{run['id']}"))["status"] in TERMINAL and r,
        timeout=TIMEOUT,
        interval=2,
        what=f"{WF} run {run['id']}",
    )
    assert done["status"] == "succeeded", done
    events = events_of(client, sid, run["id"])
    sampling = [
        e["data"]
        for e in events
        if e["type"] == "progress" and e["data"].get("phase") == "sampling"
    ]
    assert sampling and sampling[-1]["step"] == sampling[-1]["of"], sampling
    (out,) = done["outputs"]
    assert out["kind"] == "image" and out["seed"] == done["resolved"]["seed"], out
    return done, events, get(client, f"{STUDIO}/takes/{out['take_id']}")


def size_of(anon, done: dict) -> tuple[int, int]:
    r = anon.get(done["outputs"][0]["url"])
    assert r.status_code == 200 and r.headers["content-type"] == "image/png", r.status_code
    return Image.open(io.BytesIO(r.content)).size


@gpu
def test_mention_pins_the_auto_canvas(mapi, anon, live):
    """An element in a T2I prompt becomes <Picture 1>; with no user rows the auto canvas is pinned at its own size."""
    owl = upload(mapi, anon, gradient_png(300, 600), "owl.png")[
        "id"
    ]  # 1:2, which auto would follow
    e = element(mapi, "mm-owl", [owl], name="Owl")
    groups = get(mapi, f"{STUDIO}/nodes")["groups"]
    palette = {n["kind"]: n for g in groups for n in g["nodes"]}
    prompt = "A portrait of @mm-owl"
    graph = json.loads(json.dumps(live["nodes"]))
    graph["prompt"].setdefault("params", {})["text"] = prompt
    library = [
        {
            "id": e["id"],
            "handle": "mm-owl",
            "name": "Owl",
            "version": 1,
            "text": "",
            "images": [owl],
        }
    ]
    want = check.resolve_mentions(graph, palette, library)
    assert want["inputs"]["canvas.mode"] == "fixed" and not want["errors"], want

    sid = new_session(mapi, "Contract minimax mention")["id"]
    done, _, take = run_minimax(mapi, sid, {"prompt.text": prompt, "seed.seed": SEED})
    mw = take["made_with"]
    for key, value in want["inputs"].items():
        assert mw["inputs"][key] == value, key
    assert (mw.get("typed") or {}, mw.get("elements") or []) == (want["typed"], want["elements"])
    assert size_of(anon, done) == (2048, 2048)  # the template's size, not the element's 1:2


@gpu
def test_enhanced_run_reports_the_text(mapi, anon, live):
    sid = new_session(mapi, "Contract minimax enhance")["id"]
    inputs = {
        "prompt.text": "a lighthouse on a cliff at dusk",
        "seed.seed": SEED,
        "enhance.bypass": False,
    }
    done, events, take = run_minimax(mapi, sid, inputs)
    texts = done["resolved"]["texts"]
    assert list(texts) == ["enhance.text"] and 0 < len(texts["enhance.text"]) <= 16384, texts
    phases = {e["data"].get("phase") for e in events if e["type"] == "progress"}
    assert {"enhancing", "sampling"} <= phases, phases
    (end,) = [
        e["data"] for e in events if e["type"] == "status" and e["data"]["status"] in TERMINAL
    ]
    assert end["resolved"]["texts"] == texts, end
    assert "enhance.text" not in json.dumps(
        take["made_with"]
    )  # served on the run, never written into made_with
    size_of(anon, done)


@gpu
def test_edit_from_use_in(mapi, anon, live):
    """The "Use in…" Edit of a 4:3 image: the output follows the first ref's aspect; the take's parent is the ref."""
    img = upload(mapi, anon, gradient_png(512, 384), "gradient.png")["id"]
    pre = ok(mapi.post(f"{STUDIO}/takes/{img}/use", json={"workflow_id": WF}))["inputs"]
    sid = new_session(mapi, "Contract minimax edit")["id"]
    inputs = pre | {"prompt.text": "<Picture 1> repainted as a watercolor", "seed.seed": SEED}
    done, events, take = run_minimax(mapi, sid, inputs)
    assert any(e["data"].get("phase") == "encoding" for e in events if e["type"] == "progress")
    w, h = size_of(anon, done)
    assert abs(w / h - 4 / 3) < 0.03 and w % 32 == 0 and h % 32 == 0, (w, h)
    assert take["parents"] == [img] and "texts" not in done["resolved"]
