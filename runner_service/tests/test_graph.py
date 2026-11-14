import copy

import pytest
from conftest import command

from core import graph, registry
from core.graph import Cancelled, Ctx

SCHEMAS = registry.node_schemas(registry.values())


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
            "echo.text: wants Text, prompt.image is missing",
        ),
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
    assert any(e.startswith("the graph has a cycle") for e in graph.validate(n, schemas))


async def test_run_passes_values_along_and_checks_before_each_node():
    seen, checks = [], []

    async def first(ctx, params, inputs):
        return {"text": "hi"}

    async def second(ctx, params, inputs):
        seen.append(inputs)
        return {}

    ctx = Ctx(None, None, lambda: checks.append(1))
    n = {"b": {"kind": "two", "in": {"t": "a.text"}}, "a": {"kind": "one"}}
    await graph.run(n, {"one": first, "two": second}, ctx)
    assert seen == [{"t": "hi"}] and len(checks) == 2


async def test_run_stops_at_a_cancel_point():
    ran = []

    async def node(ctx, params, inputs):
        ran.append(1)
        return {}

    def check():
        raise Cancelled

    with pytest.raises(Cancelled):
        await graph.run({"a": {"kind": "k"}}, {"k": node}, Ctx(None, None, check))
    assert ran == []
