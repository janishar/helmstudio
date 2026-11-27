"""The Assistant on a Magic board (API 1.87, plan 39 phase F; contracts/README.md "Magic: the Assistant") through
studio and pipelines on the fake stack: the model (the fake OpenAI-compatible server, scripted) reads the board,
proposes an edit (a board_edit card carrying the ops; nothing written before the Yes), the user's Yes writes it as
one revision via assistant with its summary, then runs a block within the limit (no card) and, above it, waits for
the user's Yes on the magic_run card (Not now: nothing runs). The other user never reaches the board. No GPU."""

from helpers import STUDIO, ok, wait_for
from test_assistant import TAB, assistant, screen  # noqa: F401  (assistant is a fixture)
from test_director import fake, fake_ctl  # noqa: F401  (fake_ctl is a fixture)
from test_magic import BOARDS
from test_magic_runs import run_doc, shot

THREADS = f"{STUDIO}/assistant/threads"
SETTINGS = f"{STUDIO}/magic/settings"


def test_the_assistant_proposes_an_edit_and_runs_after_the_users_yes(api, api_b, assistant):  # noqa: F811
    # user B's board, chat and settings, so user A's own estimates (test_magic_runs) stay the pack's
    user, other = api_b, api
    bid = ok(user.post(BOARDS, json={"name": "Assistant", "doc": run_doc()}), 201)["id"]
    url = f"{BOARDS}/{bid}"
    turl = f"{THREADS}/{ok(user.post(THREADS, json={'client_id': TAB}), 201)['id']}"

    def say(text: str) -> None:
        ok(user.post(f"{turl}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                               "screen": screen("/magic")}), 202)  # fmt: skip

    def answer(card: dict, yes: str) -> None:
        ok(user.post(f"{turl}/messages", json={"kind": "answer", "client_id": TAB, "card_id": card["id"],
                                               "answer": yes}), 202)  # fmt: skip

    def waiting():
        got = ok(user.get(turl))
        cards = [
            e["card"]
            for e in got["entries"]
            if e["kind"] == "card" and e["card"]["state"] == "waiting"
        ]
        return cards[0] if cards else None

    def idle():
        got = ok(user.get(turl))
        return got if got["state"] == "idle" else None

    try:
        ok(user.put(SETTINGS, json={"ask_above_gpu_minutes": 5}))
        add = {"op": "add", "mode": "insert", "block": shot("s2", 24, "the bus pulls away")}
        fake("POST", "/__script", {assistant: [
            {"content": "Reading.", "tool_calls": [{"tool": "show_board", "args": {"board_id": bid}}]},
            {"content": "Adding.", "tool_calls": [{"tool": "edit_board",
                                                   "args": {"board_id": bid, "base_revision": 1, "ops": [add]}}]},
            {"content": "Running.", "tool_calls": [{"tool": "run_blocks",
                                                    "args": {"board_id": bid, "revision": 2, "blocks": ["s2"]}}]},
            {"content": "Shot 2 is on the board and running."},
        ]})  # fmt: skip
        say("Add the bus leaving")
        card = wait_for(waiting, timeout=60, what="the board_edit card")
        assert card["kind"] == "board_edit" and card["tags"] == ["changes the board"]
        assert card["board"] == {"board_id": bid, "base_revision": 1, "ops": [add]}
        assert {"label": "Change", "value": "Added Shot 2"} in card["rows"]
        assert ok(user.get(url))["revision"] == 1  # nothing written before the Yes
        assert other.get(url).status_code == 404

        answer(card, "yes")
        got = wait_for(idle, timeout=60, what="the assistant's answer")
        texts = [e["line"]["text"] for e in got["entries"] if e["kind"] == "line"]
        assert "Changed the board · Added Shot 2" in texts and "Started 1 run" in texts, texts
        top = ok(user.get(f"{url}/revisions"))["revisions"][0]
        assert (
            top["revision"] == 2
            and top["via"] == "assistant"
            and top["ops_summary"] == "Added Shot 2"
        )
        assert [r["by"] for r in ok(user.get(url))["runs"] if r["block"] == "s2"] == ["assistant"]

        # above the limit (0 minutes: always) the run waits for the magic_run card; Not now runs nothing
        ok(user.put(SETTINGS, json={"ask_above_gpu_minutes": 0}))
        fake("POST", "/__script", {assistant: [
            {"content": "Running.", "tool_calls": [{"tool": "run_blocks",
                                                    "args": {"board_id": bid, "revision": 2, "blocks": ["s1"]}}]},
            {"content": "Left it."},
        ]})  # fmt: skip
        say("Run shot 1")
        card = wait_for(waiting, timeout=60, what="the magic_run card")
        assert card["kind"] == "magic_run" and card["title"].startswith("Run 1 shot")
        assert card["tags"] == ["uses the GPU"]
        answer(card, "not_now")
        wait_for(idle, timeout=60, what="the answer after Not now")
        assert not [r for r in ok(user.get(url))["runs"] if r["block"] == "s1"]
    finally:  # a chat left behind is summarised when the next one starts (another test's model calls)
        user.delete(turl)
        user.delete(url)
        user.put(SETTINGS, json={"ask_above_gpu_minutes": 5})
