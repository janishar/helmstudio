"""An answer's markup, checked before a step is published (contracts/README.md, Assistant: pick one and refs; alpha.2
items 17 and 19): one ```options block of 2-4 choices, and [[kind:<id>]] refs the drawer shows as chips. A valid block
is rewritten compact, a broken one as a plain numbered list; a ref whose id the model never read becomes `?`."""

import json
import re

OPTIONS = re.compile(r"```[ \t]*options[ \t]*\n(.*?)\n?[ \t]*```", re.DOTALL | re.IGNORECASE)
REF = re.compile(r"\[\[(take|element|voice|workflow):([^\]\s|]{1,128})\]\]")

RULES = """- To offer choices for the next step, end your answer with one options block: a line ```options, a JSON list \
of 2 to 4 {"title": a few words, "line": one short line, "best": true on the one you suggest}, a line ```. The user's \
pick comes back as its title. Write no numbered list of the same choices.
- Name a take, element, voice or workflow you were given as [[take:<id>]], [[element:<id>]], [[voice:<id>]] or \
[[workflow:<id>]]: the user sees its picture and title. Never write an id any other way.
"""


def _options(body: str) -> list[dict] | None:
    """The block's options, compact and in order; None when it breaks a rule."""
    try:
        got = json.loads(body)
    except ValueError:
        return None
    if not isinstance(got, list) or not 2 <= len(got) <= 4:
        return None
    out = []
    for o in got:
        if not isinstance(o, dict) or not isinstance(o.get("title"), str):
            return None
        title, line = o["title"].strip(), o.get("line", "")
        if not isinstance(line, str) or not 1 <= len(title) <= 60 or len(line.strip()) > 120:
            return None
        out.append({"title": title} | ({"line": line.strip()} if line.strip() else {})
                   | ({"best": True} if o.get("best") is True else {}))  # fmt: skip
    if sum("best" in o for o in out) > 1 or len({o["title"] for o in out}) < len(out):
        return None
    return out


def _plain(body: str) -> str:
    """A broken block as the numbered list it stands for, or its lines as they were."""
    try:
        got = json.loads(body)
    except ValueError:
        got = None
    if isinstance(got, list) and got and all(isinstance(o, dict) and o.get("title") for o in got):
        return "\n".join(f"{i}. {o['title']}" + (f" · {str(o['line']).strip()}" if o.get("line") else "")
                         for i, o in enumerate(got, 1))  # fmt: skip
    return body.strip()


def tidy(text: str, read: str) -> str:
    """The answer as the drawer may show it: its first valid options block compact (any other as plain text), each
    ref whose id is not a whole id in `read` (what the model was given, not the rules) rewritten `?`."""
    kept = False

    def block(m: re.Match) -> str:
        nonlocal kept
        opts = None if kept else _options(m.group(1))
        if opts is None:
            return _plain(m.group(1))
        kept = True
        return (
            "```options\n" + json.dumps(opts, ensure_ascii=False, separators=(",", ":")) + "\n```"
        )

    text = OPTIONS.sub(block, text)
    # a whole id only: a prefix or fragment of one the model read is still unknown
    return REF.sub(
        lambda m: (
            m.group(0)
            if re.search(r"(?<![\w/-])" + re.escape(m.group(2)) + r"(?![\w/-])", read)
            else f"[[{m.group(1)}:?]]"
        ),
        text,
    )


def cut(text: str, n: int = 4000) -> str:
    """The text at most `n` long; an options block the cut opened is dropped, never left without its closing fence."""
    text = text[:n]
    i = text.rfind("```options")
    return text[:i].rstrip() if i >= 0 and "```" not in text[i + 10 :] else text
