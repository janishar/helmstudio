import asyncio
import copy
import types

import pytest
from conftest import SCHEMAS, FakeQwen, command, qwen_command

from core import graph
from core.graph import Cache, Cancelled, Ctx, RunError
from core.pack import Node
from core.residency import Residency


def nodes(**echo_params) -> dict:
    return command(**echo_params)["data"]["workflow"]["nodes"]


def test_order_puts_each_node_after_its_inputs():
    assert graph.order(nodes()) == ["prompt", "echo", "output"]


def test_canonical_graph_is_valid():
    assert graph.validate(nodes(), SCHEMAS) == []


def broken(edit) -> list[str]:
    n = copy.deepcopy(nodes())
    edit(n)
    return graph.validate(n, SCHEMAS)


@pytest.mark.parametrize(
    ("edit", "error"),
    [
        (lambda n: n["echo"].update(kind="echo/nope"), "echo: unknown node kind 'echo/nope'"),
        (lambda n: n["echo"]["params"].update(seed=1), "echo.seed: unknown param"),
        (lambda n: n["echo"]["params"].pop("fail"), "echo.fail: missing"),
        (lambda n: n["echo"]["params"].update(steps=0), "echo.steps is below the minimum 1"),
        (lambda n: n["echo"]["params"].update(steps=201), "echo.steps is above the maximum 200"),
        (lambda n: n["echo"]["params"].update(steps=2.5), "echo.steps must be an int"),
        (lambda n: n["echo"]["params"].update(steps=True), "echo.steps must be an int"),
        (lambda n: n["echo"]["params"].update(fail="no"), "echo.fail must be a bool"),
        (lambda n: n["prompt"]["params"].update(text=3), "prompt.text must be a string"),
        (
            lambda n: n["prompt"]["params"].update(text="x" * 4001),
            "prompt.text is above the maximum 4000",
        ),
        (lambda n: n["echo"].pop("in"), "echo.text: not wired"),
        (lambda n: n["echo"]["in"].update(extra="prompt.text"), "echo.extra: unknown input"),
        (
            lambda n: n["echo"]["in"].update(text="ghost.text"),
            "echo.text: wired to a missing node 'ghost'",
        ),
        (
            lambda n: n["echo"]["in"].update(text="prompt.image"),
            "echo.text: prompt has no output 'image'",
        ),
        (lambda n: n["echo"].update(bypass=True), "echo: a bypassed node reached the runner"),
        (lambda n: n.pop("output"), "the graph has no output node"),
        (
            lambda n: n["output"]["in"].update(media="prompt.text"),
            "output.media: wants Image, prompt.text is Text",
        ),
    ],
)
def test_validate_reports(edit, error):
    assert error in broken(edit)


def test_validate_reports_a_cycle():
    # type-correct wiring is impossible to loop with the echo kinds, so use a schema that maps Text → Text
    schemas = SCHEMAS | {
        "t/t": {
            "kind": "t/t",
            "params": [],
            "inputs": [{"name": "x", "type": "Text"}],
            "outputs": [{"name": "x", "type": "Text"}],
        }
    }
    n = {
        "a": {"kind": "t/t", "params": {}, "in": {"x": "b.x"}},
        "b": {"kind": "t/t", "params": {}, "in": {"x": "a.x"}},
    }
    assert {"a: on a cycle", "b: on a cycle"} <= set(graph.validate(n, schemas))


def ctx(check=lambda: None, owner="a", residency=None) -> Ctx:
    async def progress(*a):
        pass

    return Ctx(progress=progress, output=None, check=check, owner=owner, residency=residency)


T = {"params": [], "inputs": [], "outputs": [{"name": "text", "type": "Text"}]}
SINK = {"params": [], "inputs": [{"name": "t", "type": "Text"}], "outputs": []}


async def test_run_passes_values_along_and_checks_before_each_node():
    seen, checks = [], []

    def first(ctx, params, inputs):
        return {"text": "hi"}

    def second(ctx, params, inputs):
        seen.append(inputs)
        return {}

    n = {"b": {"kind": "two", "in": {"t": "a.text"}}, "a": {"kind": "one"}}
    impls = {"one": Node(first), "two": Node(second)}
    await graph.run(n, impls, {"one": T, "two": SINK}, ctx(lambda: checks.append(1)), Cache())
    assert seen == [{"t": "hi"}] and len(checks) == 2


async def test_run_pulls_only_what_a_sink_needs():
    ran = []

    def node(name):
        def fn(ctx, params, inputs):
            ran.append(name)
            return {"text": name}

        return fn

    n = {"a": {"kind": "t"}, "lonely": {"kind": "t2"}, "out": {"kind": "s", "in": {"t": "a.text"}}}
    impls = {"t": Node(node("a")), "t2": Node(node("lonely")), "s": Node(node("out"))}
    await graph.run(n, impls, {"t": T, "t2": T, "s": SINK}, ctx(), Cache())
    assert ran == ["a", "out"]


async def test_run_stops_at_a_cancel_point():
    ran = []

    def node(ctx, params, inputs):
        ran.append(1)
        return {}

    def check():
        raise Cancelled

    with pytest.raises(Cancelled):
        await graph.run({"a": {"kind": "k"}}, {"k": Node(node)}, {"k": SINK | {"inputs": []}},
                        ctx(check), Cache())  # fmt: skip
    assert ran == []


def test_resolve_picks_a_seed_for_null_and_minus_one_only():
    nodes = {"s": {"kind": "seed", "params": {"seed": None}}}
    schemas = SCHEMAS | {"seed": {"params": [{"name": "seed", "type": "seed"}]}}
    out, resolved = graph.resolve(nodes, schemas)
    assert nodes["s"]["params"]["seed"] is None  # not modified in place
    assert 0 <= out["s"]["params"]["seed"] <= 2**53 - 1 and resolved == {
        "seed": out["s"]["params"]["seed"]
    }
    nodes["s"]["params"]["seed"] = -1
    assert graph.resolve(nodes, schemas)[0]["s"]["params"]["seed"] >= 0
    nodes["s"]["params"]["seed"] = 7
    assert graph.resolve(nodes, schemas) == (nodes, {"seed": 7})


def qwen_nodes(**edits) -> dict:
    nodes = qwen_command()["data"]["workflow"]["nodes"]
    nodes["seed"]["params"]["seed"] = 1
    for path, value in edits.items():
        nid, name = path.split("__")
        nodes[nid]["params"][name] = value
    return nodes


async def run_qwen(q: FakeQwen, cache: Cache, owner="a", res=None, **edits) -> list[str]:
    nodes = qwen_nodes(**edits)
    assert graph.validate(nodes, q.schemas) == []
    c = ctx(owner=owner, residency=res or Residency(100))
    c._output = None
    outputs = []

    async def output(index, data, **meta):
        outputs.append(meta)

    c._output = output
    await graph.run(nodes, q.impls, q.schemas, c, cache)
    assert len(outputs) == 1
    return q.ran()


async def test_cache_reruns_only_what_a_change_reaches(tmp_path, monkeypatch):
    q, cache, res = FakeQwen(tmp_path, monkeypatch), Cache(), Residency(100)
    assert await run_qwen(q, cache, res=res) == ["encode", "sample", "decode"]
    assert await run_qwen(q, cache, res=res) == []  # only the output node runs
    assert await run_qwen(q, cache, res=res, seed__seed=2) == ["sample", "decode"]  # no re-encode
    assert await run_qwen(q, cache, res=res, seed__seed=2, decode__tiled="on") == ["decode"]
    # base and turbo share the text encoder and the vae: no re-encode; the transformer differs
    assert await run_qwen(q, cache, res=res, seed__seed=2, model__variant="base") == [
        "sample", "decode",
    ]  # fmt: skip
    assert await run_qwen(q, cache, res=res, model__variant="base", model__precision="bf16") == [
        "encode", "sample", "decode",
    ]  # fmt: skip
    assert await run_qwen(q, cache, res=res, owner="b") == ["encode", "sample", "decode"]
    # each component loaded once: the transformer (turbo int8, base int8, base bf16), the text encoder (int8,
    # bf16) and the vae (bf16 only)
    assert len(q.loads) == len(set(q.loads)) == 6


def test_cache_drops_the_least_recently_used(monkeypatch):
    monkeypatch.setattr(Cache, "SIZE", 3)
    cache = Cache()
    for key in "abc":
        cache[key] = {}
    assert cache.get("a") == {}  # a used: b is now the oldest
    cache["d"] = {}
    assert list(cache) == ["c", "a", "d"]


async def test_cache_key_changes_with_the_weights_files(tmp_path, monkeypatch):
    q, cache = FakeQwen(tmp_path, monkeypatch), Cache()
    await run_qwen(q, cache)
    f = tmp_path / "Qwen-Image-2.1-Turbo/transformer/turbo.safetensors"
    f.write_bytes(f.read_bytes() + b" ")  # a re-downloaded transformer: new size and mtime
    assert await run_qwen(q, cache) == ["sample", "decode"]


async def test_admission_failure_fails_the_run_before_the_stage(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    with pytest.raises(RunError, match="does not fit in memory"):
        # sample's peak at 4096² is 0.9 + 5.1 * 16.8 GB
        await run_qwen(q, Cache(), res=Residency(20), canvas__width=4096, canvas__height=4096)
    assert q.ran() == ["encode"]


async def test_model_input_of_another_type_is_rejected_by_validate(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    nodes = qwen_nodes()
    nodes["sample"]["in"]["cond"] = "prompt.text"
    assert "sample.cond: wants Conditioning[qwen-image], prompt.text is Text" in graph.validate(
        nodes, q.schemas
    )


async def test_progress_eta_is_per_phase(monkeypatch):
    sent = []

    async def progress(*a):
        sent.append(a)

    clock = iter([10.0, 14.0, 15.0, 17.0])
    c = Ctx(progress=progress, output=None, check=lambda: None, loop=asyncio.get_running_loop())
    monkeypatch.setattr("core.graph.time", types.SimpleNamespace(monotonic=lambda: next(clock)))
    c.node, c.started = "sample", 0.0

    def steps():
        c.progress(5, 10, "loading transformer")  # t=10: 10 s for half → 10 s left
        c.progress(10, 10, "loading transformer")  # t=14
        c.progress(1, 4, "sampling")  # t=15: started at 14 → 1 s per step, 3 left
        c.progress(2, 4, "sampling")  # t=17

    await asyncio.to_thread(steps)
    assert [(a[3], a[4]) for a in sent] == [
        ("loading transformer", 10.0),
        ("loading transformer", 0.0),
        ("sampling", 3.0),
        ("sampling", 3.0),
    ]
