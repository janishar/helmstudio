"""contracts/check.py, expressions, rules, memory per run (API 1.47).
Moved out of check.py as is; check.py re-exports every name."""

import copy
import json
import math
import re

from check_core import HERE, fail

# ---------------------------------------------------------------- expressions, rules, memory per run (API 1.47)
EXPR_TOKEN = re.compile(r"\s*(\d+(?:\.\d+)?|'[^']*'|\"[^\"]*\"|==|!=|<=|>=|&&|\|\||\*\*|[-+*/<>!(),]"
                        r"|[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)")
EXPR_WORDS = {"true": True, "false": False, "null": None, "True": True, "False": False, "None": None}


def expr_eval(text: str, get):
    """One expression language for `when`, rules and peak_gb formulas (contracts/README.md, Rules): literals
    (numbers, 'strings', true / false / null and Python's spellings), names (`get(name)`, `a` or `a.b`; get raises
    KeyError for one that resolves to nothing), `! - not`, `* / **`, `+ -`, comparisons, `&& and`, `|| or`,
    `a if c else b`, and len(x), min(...), max(...), gcd(a, b). ValueError on a syntax or type error."""
    tokens, at = [], 0
    while at < len(text):
        m = EXPR_TOKEN.match(text, at)
        if not m:
            if text[at:].strip():
                raise ValueError(f"unexpected {text[at:].strip()!r}")
            break
        tokens.append(m.group(1))
        at = m.end()
    i = 0

    def peek():
        return tokens[i] if i < len(tokens) else None

    def take(*ts):
        nonlocal i
        if peek() in ts:
            i += 1
            return tokens[i - 1]
        return None

    def num(v):
        if isinstance(v, bool) or not isinstance(v, int | float):
            raise ValueError(f"{v!r} is not a number")
        return v

    def call(name, args):
        if name == "len":
            return 0 if args[0] is None else len(args[0])
        if name in ("min", "max"):
            return (min if name == "min" else max)(*(num(a) for a in args))
        if name == "gcd":
            a, b = (num(x) for x in args)
            if a != int(a) or b != int(b):
                raise ValueError("gcd takes whole numbers")
            return math.gcd(int(a), int(b))
        raise ValueError(f"unknown function {name}")

    def atom():
        nonlocal i
        t = peek()
        if t is None:
            raise ValueError("unexpected end")
        i += 1
        if t == "(":
            v = ternary()
            if not take(")"):
                raise ValueError("missing )")
            return v
        if t[0] in "'\"":
            return t[1:-1]
        if t[0].isdigit():
            return float(t) if "." in t else int(t)
        if t in EXPR_WORDS:
            return EXPR_WORDS[t]
        if re.fullmatch(r"[A-Za-z_]\w*(\.[A-Za-z_]\w*)?", t):
            if take("("):
                args = []
                while not take(")"):
                    args.append(ternary())
                    take(",")
                return call(t, args)
            return get(t)
        raise ValueError(f"unexpected {t!r}")

    def unary():
        if take("!", "not"):
            return not unary()
        if take("-"):
            return -num(unary())
        v = atom()
        if take("**"):
            v = num(v) ** num(unary())
        return v

    def term():
        v = unary()
        while op := take("*", "/"):
            r = num(unary())
            v = num(v) * r if op == "*" else num(v) / r
        return v

    def total():
        v = term()
        while op := take("+", "-"):
            r = num(term())
            v = num(v) + r if op == "+" else num(v) - r
        return v

    def cmp():
        v = total()
        while op := take("==", "!=", "<", "<=", ">", ">="):
            r = total()
            if op in ("==", "!="):
                same = type(v) is type(r) or isinstance(v, int | float) and isinstance(r, int | float) and \
                    not isinstance(v, bool) and not isinstance(r, bool)
                v = (same and v == r) == (op == "==")
            else:
                a, b = num(v), num(r)
                v = {"<": a < b, "<=": a <= b, ">": a > b, ">=": a >= b}[op]
        return v

    def conj():
        v = cmp()
        while take("&&", "and"):
            r = cmp()
            v = v and r
        return v

    def disj():
        v = conj()
        while take("||", "or"):
            r = conj()
            v = v or r
        return v

    def ternary():
        v = disj()
        if take("if"):
            c = disj()
            if not take("else"):
                raise ValueError("missing else")
            other = ternary()
            return v if c else other
        return v

    out = ternary()
    if peek() is not None:
        raise ValueError(f"unexpected {peek()!r}")
    return out


def expr_text(v) -> str:
    """A value in a rule's text: a whole number without its point."""
    return str(int(v)) if isinstance(v, float) and v == int(v) else str(v)


def rule_errors(nodes: dict, palette: dict) -> list[str]:
    """Every rule that holds in a filled graph (API 1.47, contracts/README.md Rules), node id order, then param order,
    an item field's per item: `<node>: <error>` or `<node>.<param>[<i>].<field>: <error>`. A bypassed node is not
    checked; a rule whose expression fails (a reference to nothing) does not hold."""
    out = []

    def getter(nid, item=None):
        node = nodes[nid]

        def get(ref):
            a, _, b = ref.partition(".")
            if not b:
                return (item if item is not None else node["params"])[a]
            src = node.get("in", {}).get(a, "").partition(".")[0] or a
            return nodes[src]["params"][b]
        return get

    def holds(rule, get):
        try:
            if not expr_eval(rule["when"], get):
                return None
            return re.sub(r"\{([^{}]+)\}", lambda m: expr_text(expr_eval(m.group(1), get)), rule["error"])
        except (KeyError, ValueError, TypeError, ZeroDivisionError):
            return None

    for nid in sorted(nodes):
        node = nodes[nid]
        if node.get("bypass") or node["kind"] not in palette:
            continue
        for p in palette[node["kind"]]["params"]:
            for r in p.get("rules", []):
                if (e := holds(r, getter(nid))) is not None:
                    out.append(f"{nid}: {e}")
            for i, it in enumerate((node["params"].get(p["name"]) or []) if p["type"] == "rows" else [], 1):
                fields = {k: f.get("default") for k, f in p["item"].items() if isinstance(f, dict)} | it
                for k, f in p["item"].items():
                    for r in (f.get("rules", []) if isinstance(f, dict) else []):
                        if (e := holds(r, getter(nid, fields))) is not None:
                            out.append(f"{nid}.{p['name']}[{i}].{k}: {e}")
    return out


PEAK_SIZES = ("mp", "frames")


def run_peak(nodes: dict, palette: dict) -> float | None:
    """A run's own planned peak (API 1.47, contracts/README.md Memory per run): the largest of its nodes' `peak_gb`
    formulas over the node's params (rows as lists: len), mp (the largest width × height / 10^6 of any node) and
    frames (the largest numeric frames param); None when none has one or a formula names anything else."""
    mp = max((n["params"]["width"] * n["params"]["height"] / 1e6 for n in nodes.values()
              if isinstance(n["params"].get("width"), int | float) and isinstance(n["params"].get("height"), int | float)),
             default=0.0)  # fmt: skip
    frames = max((n["params"]["frames"] for n in nodes.values()
                  if isinstance(n["params"].get("frames"), int | float) and not isinstance(n["params"]["frames"], bool)),
                 default=0)  # fmt: skip
    peaks = []
    for n in nodes.values():
        if not (f := palette.get(n["kind"], {}).get("peak_gb")):
            continue
        sizes = {"mp": mp, "frames": frames}
        try:
            peaks.append(expr_eval(f, lambda ref: sizes[ref] if ref in sizes else n["params"][ref]))
        except (KeyError, ValueError, TypeError, ZeroDivisionError):
            return None
    return round(max(peaks), 2) if peaks else None


def filled(workflow: dict, palette: dict, inputs: dict) -> dict:
    """A workflow's nodes with every param filled (schema default, workflow value, then `<node>.<param>` inputs)."""
    nodes = copy.deepcopy(workflow["nodes"])
    for n in nodes.values():
        n["params"] = {p["name"]: p.get("default") for p in palette[n["kind"]]["params"]} | n.get("params", {})
    for k, v in inputs.items():
        nid, _, name = k.partition(".")
        nodes[nid]["params"][name] = v
    return nodes


def check_rules() -> None:
    """fixtures/video/rules.json: each case's rule errors and its run's planned peak (API 1.47)."""
    reg = HERE / "fixtures" / "registry"
    fx = json.loads((HERE / "fixtures" / "video" / "rules.json").read_text())
    pal, wfs = {}, {}
    for pre in fx["families"]:
        pal |= {n["kind"]: n for n in json.loads((reg / f"{pre}.nodes.valid.json").read_text())["nodes"]}
        wfs |= {w["id"]: w for w in json.loads((reg / f"{pre}.workflows.valid.json").read_text())["workflows"]}
    for c in fx["expressions"]:
        try:
            got = expr_eval(c["expr"], lambda ref: c["values"][ref])
        except (KeyError, ValueError):
            got = "error"
        if got != c["expect"]:
            fail(f"fixtures/video/rules.json expression {c['expr']!r}: got {json.dumps(got)}")
    for c in fx["cases"]:
        nodes = filled(wfs[c["workflow"]], pal, c["inputs"])
        if (got := rule_errors(nodes, pal)) != c["errors"]:
            fail(f"fixtures/video/rules.json {c['description']!r}: got {json.dumps(got, ensure_ascii=False)}")
        if "peak_gb" in c and (got := run_peak(nodes, pal)) != c["peak_gb"]:
            fail(f"fixtures/video/rules.json {c['description']!r}: peak {got}, want {c['peak_gb']}")
    print("ok   fixtures/video/rules.json")
