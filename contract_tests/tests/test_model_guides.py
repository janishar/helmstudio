"""Model guides (API 1.71; contracts/README.md "Assistant: model guides") on the test stack: the fake runner publishes
the echo pack's guide (runner_service/packs/echo/assistant.md, model.yaml recipes); every user reads it; an unknown
family is 404; a turn's system part carries the cards of the held families only (echo's while a variant is present),
right after the rules; read_model_guide is offered and makes its "Read the … guide" line."""

import json
from pathlib import Path

from helpers import STUDIO, ok, wait_for
from test_assistant import TAB, assistant, screen  # noqa: F401  (assistant is a fixture)
from test_director import fake, fake_ctl  # noqa: F401

GUIDES = f"{STUDIO}/model-guides"
THREADS = f"{STUDIO}/assistant/threads"
ECHO = (
    Path(__file__).parents[2] / "runner_service" / "packs" / "echo" / "assistant.md"
).read_text()


def test_every_user_reads_a_guide_and_an_unknown_family_is_404(api):
    g = ok(api.get(f"{GUIDES}/echo"))
    assert (g["family"], g["title"], g["guide"]) == ("echo", "Echo (fake)", ECHO)
    assert g["card"].startswith("Echo (fake) is the test model") and "## " not in g["card"]
    assert [r["name"] for r in g["recipes"]] == ["quick"] and isinstance(g["downloaded"], bool)
    r = api.get(f"{GUIDES}/nope")
    assert r.status_code == 404 and r.json() == {"detail": "no guide for nope"}


def test_a_turn_carries_the_held_families_cards_and_reads_a_guide(api, assistant):  # noqa: F811
    held = ok(api.get(f"{GUIDES}/echo"))["downloaded"]
    fake("POST", "/__script", {assistant: [
        {"content": "Reading.", "tool_calls": [{"tool": "read_model_guide", "args": {"family": "echo"}}]},
        {"content": "Done."},
    ]})  # fmt: skip
    tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]
    ok(api.post(f"{THREADS}/{tid}/messages", json={"kind": "message", "client_id": TAB, "text": "how does echo work?",
                                                   "screen": screen()}), 202)  # fmt: skip
    got = wait_for(lambda: (g := ok(api.get(f"{THREADS}/{tid}")))["state"] == "idle" and g or None, timeout=60,
                   what="the answer")  # fmt: skip
    lines = [e["line"] for e in got["entries"] if e["kind"] == "line"]
    assert {"status": "done", "text": "Read the Echo (fake) guide"}.items() <= lines[0].items(), (
        lines
    )
    first, second = [r for r in fake("GET", "/__requests") if r["model"] == assistant][-2:]
    system = next(m["content"] for m in first["body"]["messages"] if m["role"] == "system")
    names = [t["function"]["name"] for t in first["body"].get("tools") or []]
    assert "read_model_guide" in names
    # the cards of held families only: echo's while one of its variants is present (no other pack publishes a guide)
    assert ("Echo (fake) (echo): Echo (fake) is the test model" in system) == held
    assert (
        "The models this install holds" in system
    ) == held and "## Settings that matter" not in system
    assert "echo.fail" in json.dumps(
        second["body"]["messages"]
    )  # the whole guide reached the model
    ok(api.delete(f"{THREADS}/{tid}"), 204)
