"""An answer's options block and refs (answer.py; alpha.2 items 17, 19): checked and rewritten before a step goes out."""

import json

import answer
import assistant

TAKE = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d"
OPTS = [{"title": "Fix a part", "line": "Repaint the hands"}, {"title": "Shot 2", "line": " Elias ", "best": True},
        {"title": "Shot 3 keyframe", "extra": 1, "best": False}]  # fmt: skip


def block(body) -> str:
    return (
        f"Next, pick one:\n```options\n{body if isinstance(body, str) else json.dumps(body)}\n```"
    )


def test_a_valid_block_is_kept_compact():
    got = answer.tidy(block(OPTS), "")
    body = got.split("```options\n")[1].split("\n```")[0]
    assert json.loads(body) == [{"title": "Fix a part", "line": "Repaint the hands"},
                                {"title": "Shot 2", "line": "Elias", "best": True},
                                {"title": "Shot 3 keyframe"}]  # fmt: skip
    assert got.startswith("Next, pick one:\n```options\n[{") and "\n" not in body
    assert (
        answer.tidy(block(OPTS).replace("```options", "``` Options "), "").count("```options") == 1
    )


def test_a_broken_block_becomes_a_plain_numbered_list():
    two_best = [OPTS[0] | {"best": True}, OPTS[1]]
    assert (
        answer.tidy(block(two_best), "")
        == "Next, pick one:\n1. Fix a part · Repaint the hands\n2. Shot 2 · Elias"
    )
    for bad in ([OPTS[0]], OPTS * 2, [OPTS[0], OPTS[0]], [{"title": ""}, OPTS[0]], [OPTS[0], {"title": "x" * 61}],
                [OPTS[0], {"title": "y", "line": 3}]):  # fmt: skip
        assert "```" not in answer.tidy(block(bad), ""), bad
    assert answer.tidy(block("1. Fix\n2. Shot 2"), "") == "Next, pick one:\n1. Fix\n2. Shot 2"
    # only the first good block is cards; a second is plain text
    twice = answer.tidy(block(OPTS) + "\n" + block(OPTS[:2]), "")
    assert twice.count("```options") == 1 and "1. Fix a part · Repaint the hands" in twice


def test_refs_to_ids_it_never_read_are_flagged():
    text = f"Animate [[take:{TAKE}]] with [[voice:qwen3-tts/ryan]], not [[take:0000]] or [[film:{TAKE}]]."
    got = answer.tidy(text, f'... "id": "{TAKE}" ... qwen3-tts/ryan')
    assert (
        got
        == f"Animate [[take:{TAKE}]] with [[voice:qwen3-tts/ryan]], not [[take:?]] or [[film:{TAKE}]]."
    )


def test_the_rules_are_in_the_system_prompt():
    assert answer.RULES in assistant.SYSTEM
