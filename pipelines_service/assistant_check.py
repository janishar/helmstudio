"""The assistant's answer check (assistant.py): every call offered and on its input schema, actions with allowed
@handles only, memories and shot lists in plain words, no guessed shot and no stale voice prompt. Pure functions."""

import functools
import json
import re

from jsonschema import Draft202012Validator, FormatChecker

import agents


@functools.lru_cache(maxsize=256)
def _validator(schema: str) -> Draft202012Validator:
    return Draft202012Validator(json.loads(schema), format_checker=FormatChecker())


def _handles(v) -> set[str]:
    """Every `handle` value in a result (a library read, a dialog's new item)."""
    if isinstance(v, dict):
        own = {v["handle"].lstrip("@").lower()} if isinstance(v.get("handle"), str) else set()
        return own.union(*(_handles(x) for x in v.values()))
    if isinstance(v, list):
        return set().union(*(_handles(x) for x in v))
    return set()


def allowed_handles(s: dict) -> set[str]:
    """Handles the user wrote, the form holds, or a library read or a dialog returned (22.9 Mentions)."""
    out = {h for e in s["chat"] if e["role"] == "user" for h in agents.mentions(e["text"])}
    out |= set(s.get("handles") or [])  # the folded part's (API 1.66)
    out |= set(agents.mentions(json.dumps(s.get("screen") or {}))) | _handles(s.get("screen"))
    for e in s["chat"]:
        if e["role"] == "call" and (e["name"] == "list_library" or e["kind"] == "action"):
            out |= _handles(e.get("result"))
    return out


def check(got: dict, off: dict, s: dict) -> tuple[list | None, str | None]:
    """The answer's calls, each offered and matching its input schema, actions with allowed @handles only; else
    why the answer is invalid (nothing of it runs)."""
    if got.get("error"):
        return None, got["error"]
    calls, allowed = [], None
    for c in got["tool_calls"]:
        o = off.get(c["name"])
        if not o:
            return None, f"{c['name'] or 'a call'} is not one of your tools now"
        try:
            args = json.loads(c["arguments"] or "{}")
        except ValueError:
            return None, f"{c['name']}: the arguments are not JSON"
        try:
            errs = [e.message for e in _validator(json.dumps(o["spec"]["input"])).iter_errors(args)]
        except Exception:  # noqa: BLE001 - a broken schema must not end the turn's handling (NAK, redelivery)
            return None, f"{c['name']}: the plugin's input schema is broken"
        if errs:
            return None, f"{c['name']}: {errs[0]}"[:500]
        if o["kind"] == "action":
            allowed = allowed if allowed is not None else allowed_handles(s)
            if bad := [h for h in agents.mentions(json.dumps(args)) if h not in allowed]:
                return None, (f"{c['name']}: @{bad[0]} is not a handle you were given; use only handles the "
                              "user wrote, the form holds or a library read returned")  # fmt: skip
        if c["name"] == "remember" and (why := not_plain(args["text"])):
            return None, f"remember: {why}; write it again as one plain sentence"
        if c["name"] == "save_shots" and (why := next(filter(None, map(not_plain, [args["film"]] + [
                x["what"] for x in args.get("shots") or [] if "what" in x])), None)):  # fmt: skip
            return None, f"save_shots: {why}; write the title and what happens in plain words"
        calls.append({"name": c["name"], "args": args, "kind": o["kind"]})
    if why := unplanned(s, calls):
        return None, why
    if _voice_unwritten(s, calls):
        return None, ("generate: the voice workflow's box still holds the old prompt; write what is spoken with change "
                      'first (Voice-over: the spoken line only; Dialogue: one "@name: text" line each)')  # fmt: skip
    return calls, None


_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", re.IGNORECASE)
_JSON = re.compile(r'[{\[]\s*"\w+"\s*[:,\]]')  # {"key": or ["a", ; not 'Call it "Warm": …'
# voice_id, settings.source.model; never an @handle (old_man), a file name (final.cut.mp4) or a domain/URL
_FIELD = re.compile(r"(?<![@\w-])[a-z]\w*_\w+\b|(?<![@\w-])[a-z]\w*(?:\.[a-z]\w*){2,}\b", re.IGNORECASE)
_WEB = re.compile(
    r"\S*(?:://|www\.)\S*|\S+\.(?:com|org|net|io|ai|dev|in|mp4|mov|webm|png|jpe?g|webp|gif|wav|mp3|flac)\b",
    re.IGNORECASE,
)


def not_plain(text: str) -> str | None:
    """Why a memory is not plain words (the owner saw `Remembered: … {"voice_id": "<id>"}`), else None."""
    if _JSON.search(text):
        return "it has JSON"
    if _UUID.search(text):
        return "it has an id (UUID); name the thing by its @handle or title"
    if m := _FIELD.search(_WEB.sub(" ", text)):
        return f"it has a field name ({m.group()})"
    return None


_SHOT = re.compile(r"\bshot\s*#?\s*(\d{1,3})\b|\b(next)\s+shot\b", re.IGNORECASE)


def unplanned(s: dict, calls: list) -> str | None:
    """Item 29: a generation (generate, or the Timeline's shot kind new) for a shot the user named ("shot 3", "the
    next shot") that no saved shot list holds (this answer's save_shots count): the model must ask, never guess."""
    if not any(c["name"] == "generate" or (c["name"] == "shot" and c["args"].get("kind") == "new") for c in calls):
        return None
    words = " ".join(e["text"] for e in s["chat"] if e["role"] == "user" and e.get("msg") == s["msg"])
    named = _SHOT.findall(words)
    if not named:
        return None
    plan = [tuple(x) for p in s.get("plan") or [] for x in p]
    plan += [(x["n"], "take_id" in x) for c in calls if c["name"] == "save_shots" for x in c["args"].get("shots") or []]
    for n, nxt in named:
        if (n and int(n) not in {m for m, _ in plan}) or (nxt and all(took for _, took in plan)):
            which = f"shot {int(n)}" if n else "the next shot"
            return (f"generate: {which} is not in a saved shot list: if this chat planned it, save it with "
                    "save_shots first; else ask which shot with an options block, never guess")  # fmt: skip
    return None


# the spoken-line field of a voice workflow (`node.param`): the text or dialogue script; not voice, seed, speed, style
_SPOKEN = re.compile(r"(?:^|\.)(?:prompt|text|script)$")


def _voice_unwritten(s: dict, calls: list) -> bool:
    """A generate in this answer after a switch to a voice workflow (audio of class voice, or of no class: API 1.79,
    music and sound prompts are not spoken) with no change since: the old image prompt would be spoken. Over this
    message's done calls, then the answer's, by the screen's workflows list."""
    media = {}
    for w in ((s.get("screen") or {}).get("fields") or {}).get("workflows") or []:
        spoken = w.get("media") == "audio" and w.get("sound", "voice") == "voice"
        media[w["id"]] = media[w["title"].lower()] = spoken
    if not media:
        return False
    msg = f"{s['thread_id']}:{s['msg']}:"
    done = [e for e in s["chat"] if e["role"] == "call" and e["id"].startswith(msg) and e["status"] == "done"]
    voice = False
    for i, c in enumerate(done + calls):
        if c["name"] == "switchTo":
            w = c["args"]["workflow"]
            voice = bool(media.get(w) or media.get(w.lower()))
        elif c["name"] == "change" and _SPOKEN.search(c["args"]["key"]):
            voice = False
        elif c["name"] == "generate" and voice and i >= len(done):
            return True
    return False
