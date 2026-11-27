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
        # Shot 2's take lands as a new revision: wait for it, so run_blocks' revision is not stale (a race before)
        rev = wait_for(
            lambda: (r := ok(user.get(url))["revision"]) > 2 and r, timeout=60, what="Shot 2's take"
        )
        fake("POST", "/__script", {assistant: [
            {"content": "Running.", "tool_calls": [{"tool": "run_blocks",
                                                    "args": {"board_id": bid, "revision": rev, "blocks": ["s1"]}}]},
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


def test_the_assistant_makes_a_board_and_asks_before_views_above_the_limit(api, api_b, assistant):  # noqa: F811
    # API 1.93 (owner, 2026-11-27: "Yes, fix them"): create_board needs no card; make_views waits for the magic_run card
    user = api_b
    turl = f"{THREADS}/{ok(user.post(THREADS, json={'client_id': TAB}), 201)['id']}"
    bid = ok(user.post(BOARDS, json={"name": "Views", "doc": run_doc()}), 201)["id"]
    url = f"{BOARDS}/{bid}"
    made = None

    def idle():
        got = ok(user.get(turl))
        return got if got["state"] == "idle" else None

    def waiting():
        got = ok(user.get(turl))
        cards = [
            e["card"]
            for e in got["entries"]
            if e["kind"] == "card" and e["card"]["state"] == "waiting"
        ]
        return cards[0] if cards else None

    def say(text: str) -> None:
        ok(user.post(f"{turl}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                               "screen": screen("/magic")}), 202)  # fmt: skip

    try:
        rev = ok(user.post(f"{url}/edits", json={"base_revision": 1, "ops": [{"op": "asset_add", "asset": {
            "id": "mara", "kind": "character", "handle": "mara", "name": "Mara"}}]}))["revision"]  # fmt: skip
        agent = ok(user.get(url, params={"view": "agent"}))
        assert agent["assets"][0]["sheet"] == [] and all("take_ids" in b for b in agent["blocks"])
        ok(user.put(SETTINGS, json={"ask_above_gpu_minutes": 0}))
        fake("POST", "/__script", {assistant: [
            {"content": "Making a board.", "tool_calls": [{"tool": "create_board", "args": {"name": "From the brief"}}]},
            {"content": "Views.", "tool_calls": [{"tool": "make_views", "args": {
                "board_id": bid, "revision": rev, "asset": "mara", "views": ["Front"]}}]},
            {"content": "Left it."},
        ]})  # fmt: skip
        say("Start a board and make Mara's front view")
        card = wait_for(waiting, timeout=60, what="the magic_run card")
        assert card["kind"] == "magic_run" and card["title"].startswith("Make 1 view"), card
        made = next(b for b in ok(user.get(BOARDS))["boards"] if b["name"] == "From the brief")
        ok(user.post(f"{turl}/messages", json={"kind": "answer", "client_id": TAB, "card_id": card["id"],
                                               "answer": "not_now"}), 202)  # fmt: skip
        got = wait_for(idle, timeout=60, what="the answer after Not now")
        assert "Made board From the brief" in [
            e["line"]["text"] for e in got["entries"] if e["kind"] == "line"
        ]
        assert ok(user.get(url))["asset_runs"] == []  # Not now: no view runs
    finally:
        user.delete(turl)
        user.delete(url)
        if made:
            user.delete(f"{BOARDS}/{made['id']}")
        user.put(SETTINGS, json={"ask_above_gpu_minutes": 5})


def test_the_assistant_stops_a_boards_runs_by_words_with_no_card(api_b, assistant):  # noqa: F811
    # alpha.6 (owner, 2026-11-27: "yes build it"): "stop the shots" is cancel_runs: no card, one line, the runs end
    user = api_b
    doc = run_doc()
    doc["blocks"] = [shot("s1", 0, "rain"), shot("s2", 24, "more rain")]
    for b in doc["blocks"]:
        b["request"]["advanced"] = {"video.delay_ms": 3000}
    bid = ok(user.post(BOARDS, json={"name": "Stop", "doc": doc}), 201)["id"]
    url = f"{BOARDS}/{bid}"
    turl = f"{THREADS}/{ok(user.post(THREADS, json={'client_id': TAB}), 201)['id']}"

    def idle():
        got = ok(user.get(turl))
        return got if got["state"] == "idle" else None

    try:
        started = ok(user.post(f"{url}/run", json={"revision": 1, "blocks": ["s1", "s2"]}), 202)[
            "started"
        ]
        assert len(started) == 2, started
        fake("POST", "/__script", {assistant: [
            {"content": "Stopping.", "tool_calls": [{"tool": "cancel_runs", "args": {"board_id": bid, "lane": "shots"}}]},
            {"content": "Stopped both shots."},
        ]})  # fmt: skip
        ok(user.post(f"{turl}/messages", json={"kind": "message", "client_id": TAB, "text": "stop the shots",
                                               "screen": screen("/magic")}), 202)  # fmt: skip
        got = wait_for(idle, timeout=60, what="the assistant's answer")
        texts = [e["line"]["text"] for e in got["entries"] if e["kind"] == "line"]
        assert "Stopped 2 runs" in texts, texts
        assert not [e for e in got["entries"] if e["kind"] == "card"]

        def ended():
            runs = ok(user.get(url))["runs"]
            return runs if all(r["status"] not in ("queued", "running") for r in runs) else None

        assert {r["status"] for r in wait_for(ended, timeout=60, what="the runs ended")} == {
            "cancelled"
        }
    finally:
        user.delete(turl)
        user.delete(url)


def test_the_assistant_designs_a_characters_voice_and_keeps_the_picked_take(api_b, assistant):  # noqa: F811
    # alpha.6 (owner, 2026-11-28: "Yes, build it"): design_voice saves a designed voice named after the asset and
    # runs the design workflow (Echo's voice-over here, within the limit: no card); save_voice waits for the
    # board_edit card, then keeps the take as the voice's clip and sets the voice on the asset
    user = api_b
    bid = ok(user.post(BOARDS, json={"name": "Voices", "doc": run_doc()}), 201)["id"]
    url = f"{BOARDS}/{bid}"
    turl = f"{THREADS}/{ok(user.post(THREADS, json={'client_id': TAB}), 201)['id']}"
    voice = None

    def idle():
        got = ok(user.get(turl))
        return got if got["state"] == "idle" else None

    def waiting():
        got = ok(user.get(turl))
        cards = [e["card"] for e in got["entries"] if e["kind"] == "card" and e["card"]["state"] == "waiting"]
        return cards[0] if cards else None

    def say(text: str) -> None:
        ok(user.post(f"{turl}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                               "screen": screen("/magic")}), 202)  # fmt: skip

    try:
        ok(user.put(SETTINGS, json={"ask_above_gpu_minutes": 5}))
        rev = ok(user.post(f"{url}/edits", json={"base_revision": 1, "ops": [{"op": "asset_add", "asset": {
            "id": "mara", "kind": "character", "handle": "mara", "name": "Mara"}}]}))["revision"]  # fmt: skip
        line = "Hello, I am Mara, and I have walked these hills for sixty long years."
        fake("POST", "/__script", {assistant: [
            {"content": "Designing.", "tool_calls": [{"tool": "design_voice", "args": {
                "board_id": bid, "revision": rev, "asset": "mara", "design": "Female, about 60, low and warm",
                "line": line}}]},
            {"content": "Have a listen."},
        ]})  # fmt: skip
        say("Give Mara a voice")
        got = wait_for(idle, timeout=60, what="the assistant's answer")
        assert "Started 1 run" in [e["line"]["text"] for e in got["entries"] if e["kind"] == "line"]
        voice = next(x for x in ok(user.get(f"{STUDIO}/library", params={"kind": "voice"}))["items"]
                     if x["name"] == "Mara" and x["group"] == "designed")  # fmt: skip
        assert voice["voice"]["design"] == "Female, about 60, low and warm"

        def take():
            for t in ok(user.get(f"{STUDIO}/takes", params={"kind": "audio", "limit": 5}))["takes"]:
                if [v["id"] for v in t["made_with"].get("voices") or []] == [voice["id"]]:
                    return t
            return None

        tid = wait_for(take, timeout=60, what="the voice's take")["id"]
        fake("POST", "/__script", {assistant: [
            {"content": "Keeping it.", "tool_calls": [{"tool": "save_voice", "args": {
                "board_id": bid, "revision": rev, "asset": "mara", "take_id": tid}}]},
            {"content": "Mara has her voice."},
        ]})  # fmt: skip
        say("Keep that one")
        card = wait_for(waiting, timeout=60, what="the board_edit card")
        assert card["kind"] == "board_edit" and card["board"]["ops"] == [
            {"op": "asset", "id": "mara", "set": {"voice_id": voice["id"]}}], card  # fmt: skip
        assert "voice_id" not in ok(user.get(url, params={"view": "agent"}))["assets"][0]  # nothing before the Yes
        ok(user.post(f"{turl}/messages", json={"kind": "answer", "client_id": TAB, "card_id": card["id"],
                                               "answer": "yes"}), 202)  # fmt: skip
        wait_for(idle, timeout=60, what="the answer after Yes")
        assert ok(user.get(url, params={"view": "agent"}))["assets"][0]["voice_id"] == voice["id"]
        kept = ok(user.get(f"{STUDIO}/library/{voice['id']}"))
        assert kept["voice"]["clip"]["take_id"] == tid and kept["voice"]["design"] == voice["voice"]["design"]
    finally:
        user.delete(turl)
        user.delete(url)
        if voice:
            user.delete(f"{STUDIO}/library/{voice['id']}")
