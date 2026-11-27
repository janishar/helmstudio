"""Skills (API 1.64; contracts/README.md "Skills") on the test stack: the built-in Animator (plugins/animator) and a test
~/.helm/plugins (contract_tests/fake_helm/plugins, never the real one): a folder, a good .zip, a newer format and a bad
.zip. Off until on; needs_newer and broken never turn on; every user reads the skills that are on; the assistant's
skills line, use_skill and its Using skill line; a folder dropped in is found without a Rescan."""

import json
import shutil
import time
import uuid
from pathlib import Path

import pytest
from helpers import STUDIO, ok, wait_for
from test_assistant import TAB, assistant, screen  # noqa: F401  (assistant is a fixture)
from test_director import fake, fake_ctl  # noqa: F401

PLUGINS = f"{STUDIO}/admin/plugins"
SKILLS = f"{STUDIO}/skills"
THREADS = f"{STUDIO}/assistant/threads"
HELM = Path(__file__).parents[1] / "fake_helm" / "plugins"
ANIMATOR = json.loads(
    (Path(__file__).parents[2] / "plugins" / "animator" / "plugin.json").read_text()
)


def plugin(admin, name: str) -> dict | None:
    return next((p for p in ok(admin.get(PLUGINS))["plugins"] if p["name"] == name), None)


@pytest.fixture
def on(admin):
    turned = []

    def turn(name: str) -> dict:
        turned.append(name)
        return ok(admin.put(f"{PLUGINS}/{name}", json={"enabled": True}))

    yield turn
    for name in turned:
        admin.put(f"{PLUGINS}/{name}", json={"enabled": False})


def test_found_built_in_and_yours(admin):
    got = {p["name"]: p for p in ok(admin.post(f"{PLUGINS}/rescan"))["plugins"]}
    a = got["animator"]
    assert (
        (a["kind"], a["origin"], a["format"])
        == (
            "skill",
            "built_in",
            1,
        )
        and a["files"][0] == "SKILL.md"
        and "camera-moves.md" in a["files"]
    )
    assert a["recipes"]["ok"] + len(a["recipes"]["skipped"]) == len(
        ANIMATOR["recipes"]
    )  # every recipe is checked; the fake runner has no LTX or H3: those are skipped, with why
    f = got["fake-skill"]
    assert (f["origin"], f["files"]) == ("yours", ["SKILL.md", "notes.md"]) and f["status"] in (
        "off",
        "on",
    )
    assert f["recipes"] == {
        "ok": 0,
        "skipped": [{"name": "missing", "why": "this install has no workflow no-such-workflow"}],
    }
    assert got["zipped-skill"]["origin"] == "yours" and got["zipped-skill"]["status"] in (
        "off",
        "on",
    )
    fut = got["future-skill"]
    assert (fut["status"], fut["format"]) == ("needs_newer", 2) and "newer helmstudio" in fut[
        "problem"
    ]
    bad = got["bad-skill"]
    assert bad["status"] == "broken" and "outside its folder" in bad["problem"]
    for name in ("future-skill", "bad-skill"):
        assert admin.put(f"{PLUGINS}/{name}", json={"enabled": True}).status_code == 409


def test_every_user_reads_the_skills_that_are_on(admin, api, on):
    admin.put(f"{PLUGINS}/fake-skill", json={"enabled": False})
    assert "fake-skill" not in [s["name"] for s in ok(api.get(SKILLS))["skills"]]
    assert api.get(f"{SKILLS}/fake-skill").status_code == 404
    assert on("fake-skill")["status"] == "on"
    listed = ok(api.get(SKILLS))["skills"]
    assert {"name": "fake-skill", "title": "Fake skill", "version": "0.1.0"}.items() <= next(
        s for s in listed if s["name"] == "fake-skill").items()  # fmt: skip
    skill = ok(api.get(f"{SKILLS}/fake-skill"))
    assert (
        "FAKE-SKILL-READ" in skill["text"]
        and skill["files"] == ["notes.md"]
        and skill["recipes"] == []
    )
    assert skill["origin"] == "yours"
    assert "extra file" in ok(api.get(f"{SKILLS}/fake-skill/files/notes.md"))["text"]
    for file in ("plugin.json", "SKILL.md", "nope.md"):
        assert api.get(f"{SKILLS}/fake-skill/files/{file}").status_code == 404, file
    assert api.get(f"{SKILLS}/future-skill").status_code == 404


def test_the_assistant_uses_a_skill(admin, api, assistant, on):  # noqa: F811
    on("fake-skill")
    fake("POST", "/__script", {assistant: [
        {"content": "Reading the guide.", "tool_calls": [{"tool": "use_skill", "args": {"skill": "fake-skill"}}]},
        {"content": "Done."},
    ]})  # fmt: skip
    tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]
    ok(api.post(f"{THREADS}/{tid}/messages", json={"kind": "message", "client_id": TAB, "text": "use @fake-skill",
                                                   "screen": screen()}), 202)  # fmt: skip
    got = wait_for(lambda: (g := ok(api.get(f"{THREADS}/{tid}")))["state"] == "idle" and g or None, timeout=60,
                   what="the answer")  # fmt: skip
    lines = [e["line"] for e in got["entries"] if e["kind"] == "line"]
    assert {
        "status": "done",
        "text": "Using skill: Fake skill",
        "skill": "fake-skill",
    }.items() <= lines[0].items(), lines
    first, second = [r for r in fake("GET", "/__requests") if r["model"] == assistant][-2:]
    names = [t["function"]["name"] for t in first["body"].get("tools") or []]
    assert "use_skill" in names and "read_skill_file" in names
    assert "fake-skill: Fake skill." in json.dumps(first["body"]["messages"])
    assert "FAKE-SKILL-READ" in json.dumps(
        second["body"]["messages"]
    )  # the guide reached the model
    ok(api.delete(f"{THREADS}/{tid}"), 204)


def test_a_folder_dropped_in_is_found(admin):
    name = f"drop-{uuid.uuid4().hex[:6]}"
    folder = HELM / name
    folder.mkdir()
    try:
        (folder / "plugin.json").write_text(json.dumps({
            "name": name, "title": "Dropped", "version": "0.1.0", "kind": "skill", "format": 1, "description": "d",
            "author": "contract_tests", "may_do": ["m"], "when": "never"}))  # fmt: skip
        (folder / "SKILL.md").write_text("# Dropped\n")
        found = wait_for(lambda: plugin(admin, name), timeout=40, what="the watch to find it")
        assert (found["origin"], found["enabled"]) == ("yours", False)  # off until on
    finally:
        shutil.rmtree(folder)
    deadline = time.time() + 40
    while plugin(admin, name) and time.time() < deadline:
        time.sleep(1)
    assert plugin(admin, name) is None


def test_magic_director_is_built_in_and_always_on(admin, api):
    # API 1.96: on from the first scan, for every user; it can't be turned off or removed
    p = plugin(admin, "magic-director")
    assert (p["origin"], p["status"], p["title"]) == ("built_in", "on", "Magic director"), p
    assert {"consistency.md", "sample-plans.md"} <= set(p["files"])
    for r in (admin.put(f"{PLUGINS}/magic-director", json={"enabled": False}),
              admin.delete(f"{PLUGINS}/magic-director")):  # fmt: skip
        assert r.status_code == 409 and "always on for Magic" in r.json()["detail"], r.text
    assert "magic-director" in [s["name"] for s in ok(api.get(SKILLS))["skills"]]
    assert "# Magic director" in ok(api.get(f"{SKILLS}/magic-director"))["text"]


def test_the_assistant_reads_the_magic_director_on_magic_once(api, assistant):  # noqa: F811
    # API 1.96: on the Magic screen the guide is read before the first model call, once per chat; not elsewhere
    fake(
        "POST",
        "/__script",
        {assistant: [{"content": "Planned."}, {"content": "Again."}, {"content": "Hi."}]},
    )
    tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]

    def say(text: str, route: str) -> list[dict]:
        ok(api.post(f"{THREADS}/{tid}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                                       "screen": screen(route)}), 202)  # fmt: skip
        got = wait_for(lambda: (g := ok(api.get(f"{THREADS}/{tid}")))["state"] == "idle" and g or None,
                       timeout=60, what="the answer")  # fmt: skip
        return [e["line"] for e in got["entries"] if e["kind"] == "line"]

    try:
        assert say("hello", "/gallery") == []
        lines = say("plan a 30 s ad", "/magic")
        assert [(x["text"], x.get("skill")) for x in lines] == [
            ("Using skill: Magic director", "magic-director")
        ]
        asked = [r for r in fake("GET", "/__requests") if r["model"] == assistant][-1]
        # the guide reached the model
        assert "# Magic director" in json.dumps(asked["body"]["messages"])
        assert len(say("and the music", "/magic")) == 1  # once per chat
    finally:
        ok(api.delete(f"{THREADS}/{tid}"), 204)
