"""User-edited graphs: the graph-rule fixtures of contracts/fixtures/graphs, and running arbitrary graphs (several
outputs, a kind used twice, wired params, optional inputs, unused branches, id-independent caching)."""

import json

import pytest
from conftest import CONTRACTS, FakeQwen

from core import graph
from core.graph import Cache, Ctx
from core.pack import Node

GRAPHS = CONTRACTS / "fixtures/graphs"
PALETTE = {
    n["kind"]: n
    for g in json.loads((GRAPHS / "palette.json").read_text())["groups"]
    for n in g["nodes"]
}
# a runner's palette is its own env (another env's kind is unknown to it) and it gets no studio block
NOT_CHECKED = {"env_mismatch", "studio_unknown_path", "studio_duplicate", "preset_invalid"}


def filled(nodes: dict) -> dict:
    """The nodes with every param of a known kind filled with its default, as studio sends them."""
    out = json.loads(json.dumps(nodes))
    for n in out.values():
        if s := PALETTE.get(n["kind"]):
            n["params"] = {p["name"]: p["default"] for p in s["params"]} | n.get("params", {})
    return out


@pytest.mark.parametrize(
    "path", sorted(GRAPHS.glob("valid.*.json")) + sorted(GRAPHS.glob("invalid.*.json")), ids=str
)
def test_graph_fixture_problems(path):
    fx = json.loads(path.read_text())
    doc = fx["workflow"]
    got = {p[:4] for p in graph.problems(filled(doc["nodes"]), PALETTE, doc["family"])}
    want = {
        (x["code"], x.get("node"), x.get("port"), x.get("param"))
        for x in fx["problems"]
        if x["code"] not in NOT_CHECKED
    }
    assert got == want


def test_every_problem_message_names_its_node():
    doc = json.loads((GRAPHS / "invalid.handle-family.json").read_text())["workflow"]
    for code, node, _, _, message in graph.problems(filled(doc["nodes"]), PALETTE, doc["family"]):
        assert node is None or message.startswith(node), (code, message)


def test_params_must_be_filled():
    nodes = {"p": {"kind": "prompt"}, "o": {"kind": "output", "in": {"media": "p.text"}}}
    assert "p.text: missing" in graph.validate(nodes, PALETTE)


# --- running graphs: a small family `t` whose stages record what they see

TEXT = {"name": "text", "type": "Text"}
SCHEMAS = {
    "src": {"params": [{"name": "text", "type": "text"}], "inputs": [], "outputs": [TEXT]},
    "count": {"params": [{"name": "value", "type": "int"}], "inputs": [], "outputs": [{"name": "value", "type": "Int"}]},
    "t/enc": {"params": [], "inputs": [TEXT], "outputs": [{"name": "cond", "type": "Conditioning"}]},
    "t/sample": {"params": [{"name": "steps", "type": "int"}],
                 "inputs": [{"name": "cond", "type": "Conditioning"}, {"name": "extra", "type": "Text", "optional": True}],
                 "outputs": [{"name": "image", "type": "Image"}]},
    "output": {"params": [], "inputs": [{"name": "media", "type": "Image"}], "outputs": []},
}  # fmt: skip


class Recorder:
    def __init__(self):
        self.ran, self.seen, self.stored = [], [], []

        def stage(fn):
            def run(ctx, params, inputs):
                self.ran.append(ctx.node)
                self.seen.append((ctx.node, params, inputs))
                return fn(params, inputs)

            return Node(run)

        def output(ctx, params, inputs):
            ctx.output(inputs["media"], ext="png", kind="image", width=1, height=1)
            return {}

        self.impls = {
            "src": stage(lambda p, i: {"text": p["text"]}),
            "count": stage(lambda p, i: {"value": p["value"]}),
            "t/enc": stage(lambda p, i: {"cond": "c:" + i["text"]}),
            "t/sample": stage(lambda p, i: {"image": f"{i['cond']}/{p['steps']}/{i.get('extra')}"}),
            "output": Node(output),
        }

    async def run(self, nodes: dict, cache: Cache) -> list[str]:
        assert graph.validate(nodes, SCHEMAS) == []
        self.ran, self.stored = [], []

        async def progress(*a):
            pass

        async def output(index, data, **meta):
            self.stored.append((index, data))

        c = Ctx(progress=progress, output=output, check=lambda: None, owner="a")
        await graph.run(nodes, self.impls, SCHEMAS, c, cache)
        return self.ran


def two_chains(**extra) -> dict:
    return {
        "p": {"kind": "src", "params": {"text": "fox"}},
        "enc": {"kind": "t/enc", "params": {}, "in": {"text": "p.text"}},
        "s1": {"kind": "t/sample", "params": {"steps": 1}, "in": {"cond": "enc.cond"}},
        "s2": {"kind": "t/sample", "params": {"steps": 2}, "in": {"cond": "enc.cond"}},
        "out_a": {"kind": "output", "params": {}, "in": {"media": "s1.image"}},
        "out_b": {"kind": "output", "params": {}, "in": {"media": "s2.image"}},
    } | extra


async def test_two_chains_share_one_encode_and_each_output_is_a_take():
    r = Recorder()
    ran = await r.run(two_chains(), Cache())
    assert sorted(ran) == ["enc", "p", "s1", "s2"]  # enc once
    assert sorted(r.stored) == [(0, "c:fox/1/None"), (1, "c:fox/2/None")]


async def test_unused_branch_is_not_run_and_an_optional_input_is_absent():
    r = Recorder()
    nodes = two_chains(lonely={"kind": "t/enc", "params": {}, "in": {"text": "p.text"}})
    assert "lonely" not in await r.run(nodes, Cache())
    assert all("extra" not in i for n, _, i in r.seen if n.startswith("s"))


async def test_a_wired_param_gets_the_upstream_value():
    r = Recorder()
    nodes = two_chains(n={"kind": "count", "params": {"value": 7}})
    nodes["s1"]["in"]["steps"] = "n.value"  # its params entry (1) is ignored
    await r.run(nodes, Cache())
    assert sorted(r.stored) == [(0, "c:fox/7/None"), (1, "c:fox/2/None")]


async def test_cache_follows_inputs_not_ids():
    r, cache = Recorder(), Cache()
    await r.run(two_chains(), cache)
    renamed = json.loads(json.dumps(two_chains()).replace('"enc', '"encode_2'))
    assert "encode_2" in renamed
    assert await r.run(renamed, cache) == []  # all hits under new ids; only the outputs run
    same = two_chains(enc_2={"kind": "t/enc", "params": {}, "in": {"text": "p.text"}})
    # two identical encodes in one run: one computes, one hits
    same["s2"]["in"]["cond"] = "enc_2.cond"
    ran = await r.run(same, Cache())
    assert ran.count("enc") + ran.count("enc_2") == 1


async def test_a_wired_value_changes_the_cache_key():
    r, cache = Recorder(), Cache()
    nodes = two_chains(n={"kind": "count", "params": {"value": 7}})
    nodes["s1"]["in"]["steps"] = "n.value"
    await r.run(nodes, cache)
    nodes["n"]["params"]["value"] = 8
    assert sorted(await r.run(nodes, cache)) == ["n", "s1"]


def test_a_wired_seed_param_is_not_resolved(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    nodes = {"s": {"kind": "seed", "params": {"seed": -1}, "in": {"seed": "x.seed"}}}
    assert graph.resolve(nodes, q.schemas) == (nodes, {})
