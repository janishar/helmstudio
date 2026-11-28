"""Magic's cards (API 1.87, contracts/README.md "Magic: the Assistant"; board MagicBoard frame 8): the card of a
Magic card tool's preview and its final reply's line, for assistant.py. Pure functions."""

import math

# API 1.87 (contracts/README.md "Magic: the Assistant"): studio answers these with a preview first; the card is built
# from it, and the Yes asks studio again with `confirmed` (run_blocks within the limit answers at once: no card)
MAGIC_CARDS = (
    "edit_board",
    "restore_board",
    "run_blocks",
    "make_views",
    "send_to_timeline",
    "design_voice",
    "save_voice",
)
# API 1.93: Magic runs that start without a card count under Auto mode's hourly caps; past them they ask once
# alpha.6: design_voice too (one voice design run)
MAGIC_RUNS = ("run_blocks", "make_views", "design_voice")


def secs_text(seconds: float | None) -> str:
    """GPU time in the board's words: `about 40 s`, `about 6 min`, `no estimate yet`."""
    if seconds is None:
        return "no estimate yet"
    return f"about {math.ceil(seconds)} s" if seconds < 60 else f"about {round(seconds / 60)} min"


def and_list(xs: list[str]) -> str:
    return xs[0] if len(xs) < 2 else ", ".join(xs[:-1]) + " and " + xs[-1]


def _short(text: str, n: int = 300) -> str:
    return text if len(text) <= n else text[: n - 1] + "…"


def magic_card(c: dict, p: dict) -> dict:
    """The card of a Magic card tool's preview, and the lines of what already started: {card, lines}."""
    a, base = c["args"], {"id": c["id"], "state": "waiting"}
    lines = []
    # alpha.6: save_voice keeps the picked take as the voice's clip and sets the voice on the asset
    if c["name"] == "save_voice":
        card = base | {"kind": "board_edit", "title": "Save the voice?",
                       "rows": [{"label": "Voice", "value": _short(p["name"])},
                                {"label": "Change", "value": _short(p["summary"])}],
                       "note": "Keeps this take as the voice's clip. Yes means you have the right to use this voice.",
                       "tags": ["changes the board"],
                       "board": {"board_id": a["board_id"], "base_revision": a["revision"], "ops": p["ops"]}}  # fmt: skip
        return {"card": card, "lines": lines}
    if c["name"] == "edit_board":
        est = p.get("estimate") or {}
        rows = [{"label": "Change", "value": _short(p["summary"])}]
        if est.get("blocks"):
            run = secs_text(est.get("seconds")) + (f" · {est['host']}" if est.get("host") else "")
            rows.append({"label": "To run", "value": _short(run)})
        card = base | {
            "kind": "board_edit",
            "title": "Assistant's change",
            "rows": rows,
            "tags": ["changes the board"],
            "board": {
                "board_id": a["board_id"],
                "base_revision": a["base_revision"],
                "ops": a["ops"],
            },
        }
        if p.get("warnings"):
            card["note"] = _short("; ".join(p["warnings"]))
        return {"card": card, "lines": lines}
    if c["name"] == "restore_board":
        to = p["to"]
        who = "Assistant" if to.get("via") == "assistant" else "studio" if to.get("by") == "studio" else "you"
        what = f"rev {to['revision']} · by {who}" + (f" · {to['ops_summary']}" if to.get("ops_summary") else "")
        return {"card": base | {"kind": "board_edit", "title": f"Restore rev {to['revision']}?",
                                "rows": [{"label": "Revision", "value": _short(what)}],
                                "note": "Restore makes a new revision; nothing is lost.", "tags": ["changes the board"],
                                "board": {"board_id": a["board_id"], "base_revision": a["revision"],
                                          "to": to["revision"]}}, "lines": lines}  # fmt: skip
    if c["name"] in MAGIC_RUNS:
        plan, rest, frames, ran = p["plan"], p["rest"], p.get("frames") or [], None
        why = (f"Above your {plan['limit_minutes']:g} min limit." if rest.get("seconds") is not None
               else "It has no estimate yet.")  # fmt: skip
        if frames:
            names = and_list([f["name"] for f in frames])
            known = [f["seconds"] for f in frames if f.get("seconds") is not None]
            took = f" ({secs_text(sum(known)).removeprefix('about ')})" if len(known) == len(frames) else ""
            why += f" {names} ran on {'its' if len(frames) == 1 else 'their'} own{took}."
            lines.append(
                {
                    "id": c["id"],
                    "status": "done",  # started; each run reports on its own line as it ends
                    "undo": False,
                    "text": _short(f"Running {names}"),
                }
            )
            ran = f"{names} started without asking (storyboard frames never ask)"
        run = and_list(rest["names"]) + " · " + secs_text(rest.get("seconds")) + (
            f" · {rest['host']}" if rest.get("host") else "")  # fmt: skip
        verb = {"make_views": "Make", "design_voice": "Hear"}.get(c["name"], "Run")
        return {"card": base | {"kind": "magic_run", "title": _short(f"{verb} {rest['ask_text']}?", 120),
                                "rows": [{"label": "Runs", "value": _short(run)}], "note": _short(why),
                                "tags": ["uses the GPU"]}, "lines": lines, "ran": ran}  # fmt: skip
    plan = p["plan"]  # send_to_timeline
    rows = [{"label": "Name", "value": _short(plan["name"])}]
    if plan.get("goes"):
        rows.append({"label": "Goes", "value": _short(f"{len(plan['goes'])} clips")})
    if plan.get("bakes"):
        rows.append(
            {
                "label": "Bakes",
                "value": _short(
                    f"{plan['bakes']} join{'s' * (plan['bakes'] != 1)} · {secs_text(plan.get('bake_seconds'))}"
                ),
            }
        )
    card = base | {
        "kind": "send",
        "title": "Send to Timeline?",
        "rows": rows,
        "tags": ["uses the machine"],
    }
    if plan.get("warnings"):
        card["note"] = _short("; ".join(plan["warnings"]))
    return {"card": card, "lines": lines}


def magic_done(c: dict, reply: dict) -> dict:
    """A Magic card tool's final reply as the turn keeps it: {status, result?, error?, line}."""
    line = {"id": c["id"], "undo": False}
    labels = {"edit_board": "Could not change the board", "restore_board": "Could not restore the board",
              "run_blocks": "Could not run", "make_views": "Could not make views",
              "send_to_timeline": "Could not send", "design_voice": "Could not design the voice",
              "save_voice": "Could not save the voice"}  # fmt: skip
    if "error" in reply:
        return {"status": "failed", "error": reply["error"],
                "line": line | {"status": "failed", "text": _short(f"{labels[c['name']]} · {reply['error']}")}}  # fmt: skip
    r = reply["result"]
    if c["name"] == "edit_board":
        text, status = f"Changed the board · {r['summary']}", "done"
    elif c["name"] == "save_voice":
        text, status = f"Saved the voice · {r['summary']}", "done"
    elif c["name"] == "restore_board":
        text, status = f"Restored rev {c['args']['to']} · now rev {r['revision']}", "done"
    elif c["name"] in MAGIC_RUNS:
        if c.get("ran"):
            r = r | {"also": c["ran"]}
        n = len(r["started"])
        text = f"Started {n} run{'s' * (n != 1)}" + (f" · {len(r['skipped'])} skipped" if r.get("skipped") else "")
        # the start is done; each run reports on its own line ("Run done · 1 take") as it ends
        status = "done"
    else:
        send = r.get("send") or {}
        baking = send.get("status") == "baking"
        text = f"{'Sending' if baking else 'Sent'} to Timeline · {send.get('name') or r['plan']['name']}"
        status = "running" if baking else "done"
    return {"status": "done", "result": r, "line": line | {"status": status, "text": _short(text)}}
