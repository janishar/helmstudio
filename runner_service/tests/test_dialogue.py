"""Dialogue (API 1.32, contracts/README.md "Dialogue and Timeline audio"): voice rows, cues, pauses, speech_seconds
over lines and the runner's check texts; echo/dialogue against the contract (check.py echo_dialogue); ZONOS3's style
words. The GPU goldens are tests/test_qwen3_tts_golden.py and tests/test_zonos3_golden.py."""

import copy
import json
import types

import pytest
from conftest import CONTRACTS, FakeJS, FakeMsg, FakeStore, consumer

from core import graph, media, nodes
from core.graph import RunError
from messaging import schemas
from packs.echo import fake
from packs.zonos3 import native

FIXTURES = CONTRACTS / "fixtures"
PALETTE = {n["kind"]: n for g in json.loads((FIXTURES / "graphs/palette.json").read_text())["groups"]
           for n in g["nodes"]}  # fmt: skip
ADA = {"item_id": "echo/ada", "version": 1, "source": "preset", "family": "echo", "speaker": "ada", "language": "en"}  # fmt: skip


def command() -> dict:
    cmd = json.loads((FIXTURES / "messages/cmd.run.execute.valid.13.json").read_text())
    cmd["data"]["workflow"]["nodes"]["dialogue"]["params"]["delay_ms"] = 0
    return cmd


def line(text: str, pause_s: float = 0.35, voice: dict = ADA, style: str = "") -> dict:
    return {"voice": voice, "text": text, "style": style, "pause_s": pause_s}


async def test_echo_dialogue_reads_the_clip_and_reports_the_contracts_cues():
    js, store = FakeJS(), FakeStore()
    cmd = command()
    clip = cmd["data"]["workflow"]["nodes"]["dialogue"]["params"]["lines"][1]["voice"]["clip"]
    store.objects[clip["key"]] = (bytes(clip["bytes"]), "audio/wav")
    c = consumer(js)
    c.store = store
    await c.handle(FakeMsg(cmd, js))
    evts = js.events()
    assert all(schemas.message_errors(e) == [] for e in evts)
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    out = next(e for e in evts if e["type"] == "evt.run.output")["data"]
    want = json.loads((FIXTURES / "messages/evt.run.output.valid.9.json").read_text())["data"]
    assert (
        out["audio"] == want["audio"] and out["bytes"] == want["bytes"]
    )  # the fixture is this take
    assert store.reads == [clip["key"]]
    steps = [e["data"] for e in evts if e["type"] == "evt.run.progress"]
    assert [(s["step"], s["of"]) for s in steps if s.get("node") == "dialogue"] == [(1, 2), (2, 2)]


def test_echo_dialogue_equals_check_py_echo_dialogue_on_the_script_fixtures():
    """The fake take of each scripts.json case with a `take` (duration and cues exactly), its silence where the
    pauses are."""
    fx = json.loads((FIXTURES / "voices/scripts.json").read_text())
    ctx = types.SimpleNamespace(node="dialogue", check=lambda: None, progress=lambda *a: None)
    for case in (c for c in fx["cases"] if "take" in c):
        rows = next(v for k, v in case["expect"]["inputs"].items() if k.endswith(".lines"))
        resolved = [
            r | {"voice": {"item_id": r["voice"]["voice_id"], "version": 1, "source": "preset"}}
            for r in rows
        ]
        audio = fake.dialogue(ctx, {"lines": resolved, "seed": 7, "delay_ms": 0}, {})["audio"]
        assert {"duration_s": audio["duration_s"], "cues": audio["cues"]} == case["take"], case[
            "description"
        ]
        a, b = audio["cues"][0]["end_s"], audio["cues"][1]["start_s"]
        assert audio["wav"][round(a * 24000) * 2 : round(b * 24000) * 2] == bytes(
            2 * round((b - a) * 24000)
        )


def test_stitch_puts_the_rounded_pause_after_each_line_but_the_last():
    lines = [
        line("a", 0.0000625),
        line("b", 1.5),
        line("c", 9),
    ]  # 0.0000625 s at 24 kHz is 1.5 samples: 2
    pcm, cues = media.stitch(lines, [b"\1\0" * 3, b"\2\0", b"\3\0" * 2], 24000)
    assert pcm == b"\1\0" * 3 + bytes(4) + b"\2\0" + bytes(2 * 36000) + b"\3\0" * 2
    assert [(c["line"], c["start_s"] * 24000, c["end_s"] * 24000) for c in cues] == [
        (1, 0, 3), (2, 5, 6), (3, 36006, 36008)]  # fmt: skip
    assert cues[0] == {
        "line": 1,
        "start_s": 0.0,
        "end_s": 3 / 24000,
        "voice_id": "echo/ada",
        "text": "a",
    }


def test_output_reports_a_dialogues_cues_and_a_voice_over_none():
    got = []
    ctx = types.SimpleNamespace(node="output", output=lambda data, **kw: got.append(kw))
    cues = [{"line": 1, "start_s": 0.0, "end_s": 0.5, "voice_id": "echo/ada", "text": "Hi."}]
    nodes.output(
        ctx,
        {},
        {"media": {"wav": bytes(24000), "sample_rate": 24000, "duration_s": 0.5, "cues": cues}},
    )
    nodes.output(ctx, {}, {"media": {"wav": bytes(24000), "sample_rate": 24000, "duration_s": 0.5}})
    assert got[0]["audio"]["cues"] == cues and "cues" not in got[1]["audio"]


def test_speech_seconds_of_a_dialogue_is_its_lines_and_pauses_capped():
    n = {
        "d": {
            "kind": "echo/dialogue",
            "params": {"lines": [line("x" * 30, 0.5), line("y" * 15, 2)]},
        }
    }
    assert graph.speech_seconds(n, PALETTE, "d") == 3.5  # the last line's pause is not used
    assert graph.speech_seconds(n, PALETTE, "d", 3) == 3
    assert (
        graph.speech_seconds(
            {"d": {"kind": "echo/dialogue", "params": {"lines": []}}}, PALETTE, "d"
        )
        == 0
    )


def dialogue(lines: list) -> dict:
    wf = json.loads((FIXTURES / "registry/workflows.valid.json").read_text())
    nodes_ = copy.deepcopy(next(w for w in wf["workflows"] if w["id"] == "echo-dialogue")["nodes"])
    nodes_["dialogue"]["params"]["lines"] = lines
    return nodes_


@pytest.mark.parametrize(
    ("lines", "why"),
    [
        ([line("Hi.")], None),
        ([], None),  # a workflow holds []; the node itself says nothing to say
        ([line("Hi.", voice=None)], "dialogue.lines item 1.voice: a voice is required"),
        ([line("Hi."), line("Yo.", voice=ADA | {"family": "qwen3-tts"})], "dialogue.lines[2].voice: voice echo/ada is for qwen3-tts"),
    ],
)  # fmt: skip
def test_a_run_needs_a_fitting_voice_on_every_line(lines, why):
    assert graph.validate(dialogue(lines), PALETTE) == ([] if why is None else [why])


def test_the_dialogue_check_texts():
    ctx = types.SimpleNamespace(node="dialogue", check=lambda: None, progress=lambda *a: None)
    with pytest.raises(RunError, match="^dialogue: nothing to say$"):
        fake.dialogue(ctx, {"lines": [], "seed": 1, "delay_ms": 0}, {})
    assert media.lines_problem([line("x" * 9000, 0)]) is None  # 600 s exactly
    assert (
        media.lines_problem([line("x" * 9000, 1), line("y")])
        == ": about 601 s of speech; a take is at most 600 s"
    )
    qwen = pytest.importorskip("packs.qwen3_tts.torch")
    model = lambda variant: {"model": types.SimpleNamespace(variant=variant)}
    assert qwen.check_dialogue({"lines": []}, set(), model("voice_design")) == (
        ": a dialogue needs model variant custom_voice (it speaks preset voices only)")  # fmt: skip
    assert qwen.check_dialogue({"lines": []}, set(), model("custom_voice")) is None


def test_zonos3_maps_a_lines_style_words_onto_its_delivery_and_ignores_the_rest():
    base = {
        "language": "en-US",
        "emotion": 0.5,
        "energy": 0.5,
        "whisper": 0,
        "speaking_rate": 1,
        "guidance": 2,
    }
    assert native.styled(base, "") == base
    assert native.styled(base, "with a shrug") == base
    assert native.styled(base, "Whispering") == base | {
        "whisper": 0.9,
        "energy": 0.25,
        "speaking_rate": 0.9,
    }
    assert native.styled(base, "quiet, then angry") == base | {
        "emotion": 0.2,
        "energy": 0.9,
    }  # the later word wins
    assert native.delivery(native.styled(base, "slowly")) == {"condition_values": {"speaking_rate": 12.75},
                                                              "speaker_cfg_scale": 2}  # fmt: skip
