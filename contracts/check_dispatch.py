"""contracts/check.py, the dispatcher's hand-over (README, "The hand-over").
Moved out of check.py as is; check.py re-exports every name."""

import json

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import HERE, fail

# ---------------------------------------------------------------- the dispatcher's hand-over (README, "The hand-over")
SWAP_SKIPS, SWAP_WAIT_S = 4, 180  # the fairness bound: studio_service dispatcher.go SwapSkips, SwapWait
WINDOW = 16  # rows of one batch in flight (the pipelines engine)


def cgb(x: float) -> int:
    """Memory arithmetic is in integer hundredths of a GB."""
    return round(x * 100)


def entry_key(e: dict) -> tuple:
    """A resident entry's (or an unload component's) identity; no loras = the plain component."""
    loras = tuple((x["lora_id"], x["strength"]) for x in e.get("loras") or [])
    compute = None if e.get("compute") in (None, "float32") else e["compute"]  # API 1.39: absent = float32
    return e["component"], e["model"], e["variant"], e["precision"], loras, compute


def admit(host: str, entries: list[tuple], budget: int | None, env: str, peak: int | None, needs: set,
          referenced: set, running: str | None = None, ram_budget: int | None = None, ram_peak: int | None = None) -> dict:
    """The admission decision for releasing a run to `env` of `host`. entries: (env, key, gb, pinned, fresh, used)
    for every resident entry of the host's present keys (gb in hundredths, used sortable, None = oldest); budget: the
    host's (None: no key reports memory); peak: the run's planned peak (None: no plan = the whole budget); needs: the
    keys the run uses; referenced: (env, key) of the host's other queued runs; running: an env whose key still names
    a run (that run may use up to its budget_gb, more than its key declares: the run waits for that key's next write).
    ram_budget / ram_peak (API 1.18, cuda hosts): host_ram's smallest budget_gb and the plan's ram_peak_gb; entries held
    in host RAM (`device: cpu`) are not entries here."""
    if budget is None:
        return {"decision": "release", "unload": {}, "budget_gb": None, "error": None}
    p = budget if peak is None else peak
    counted = [x for x in entries if not (x[0] == env and x[1] in needs)]
    if peak is None:  # P = B takes what it can get: other envs' measured excess (never freed) lowers it, never holds it
        p -= sum(x[2] for x in counted if x[0] != env and x[1][0] == "(measured)")
    c = sum(x[2] for x in counted)
    own = sum(x[2] for x in counted if x[0] == env and x[1][0] == "(measured)")  # the run's own runner's excess
    if p > budget:  # a pin never fails a run: pinned entries are victims too, the last ones (below)
        error = f"needs {p / 100:.1f} GB at its peak; host {host} has {budget / 100:.1f} GB"
        return {"decision": "fail", "unload": {}, "budget_gb": None, "error": error}
    if ram_budget is not None and ram_peak is not None and ram_peak > ram_budget:
        error = f"needs {ram_peak / 100:.1f} GB of host RAM at its peak; host {host} has {ram_budget / 100:.1f} GB"
        return {"decision": "fail", "unload": {}, "budget_gb": None, "error": error}
    if running:
        return {"decision": "wait", "unload": {}, "budget_gb": None, "error": None}
    others = sum(x[2] for x in entries if x[0] != env)
    released = {"decision": "release", "unload": {}, "budget_gb": (budget - others) / 100, "error": None}
    if c + p <= budget:
        return released
    tier = lambda x: 0 if x[1][4] and (x[0], x[1]) not in referenced else 1  # an unreferenced baked set first
    victims = sorted((x for x in counted if x[4]),  # pinned ones last: taken only when nothing else makes it fit
                     key=lambda x: (x[3], tier(x), x[5] is not None, x[5] or 0, -x[2], x[0], x[1][:4]))
    unload: dict[str, list] = {}
    for x in victims:
        if c + p <= budget:
            break
        unload.setdefault(x[0], []).append(x[1])
        c -= x[2]
    if not unload and c - own + p <= budget:  # only its own excess is in the way: its runner drops its caches itself
        return released
    return {"decision": "unload" if unload else "wait", "unload": unload, "budget_gb": None, "error": None}


def as_component(key: tuple) -> dict:
    out = dict(zip(("component", "model", "variant", "precision"), key[:4]))
    if key[5]:
        out["compute"] = key[5]
    if key[4]:
        out["loras"] = [{"lora_id": i, "strength": s} for i, s in key[4]]
    return out


def host_view(keys: dict) -> tuple[list, int | None, int | None, str | None]:
    """One host's present keys (fixture form: env -> {state fresh|offline|gone, budget_gb?, used_gb?, ram_budget_gb?,
    run_id?, resident}) -> (admission entries, budget, host-RAM budget, the env of an in-run key)."""
    from datetime import datetime
    entries, budgets, rams = [], [], []
    for env, k in keys.items():
        if k["state"] == "gone":
            continue
        if "budget_gb" in k:
            budgets.append(cgb(k["budget_gb"]))
        if "ram_budget_gb" in k:
            rams.append(cgb(k["ram_budget_gb"]))
        fresh = k["state"] == "fresh" and not k.get("run_id")  # a key still naming a run waits for its next write
        on_device = [e for e in k.get("resident", []) if e.get("device") != "cpu"]  # host RAM is not the budget
        for e in on_device:
            used = datetime.fromisoformat(e["last_used"].replace("Z", "+00:00")).timestamp() if "last_used" in e else None
            entries.append((env, entry_key(e), cgb(e["gb"]), e["pinned"], fresh, used))
        extra = cgb(k["used_gb"]) - sum(cgb(e["gb"]) for e in on_device) if "used_gb" in k else 0
        if extra > 0:  # measured beyond the entries: counted (the run's own env too), never a victim
            entries.append((env, ("(measured)", "", "", "", (), None), extra, False, False, None))
    running = next((env for env, k in sorted(keys.items()) if k["state"] != "gone" and k.get("run_id")), None)
    return entries, min(budgets) if budgets else None, min(rams) if rams else None, running


def admit_fixture(fx: dict) -> dict:
    run = fx["run"]
    entries, budget, ram_budget, running = host_view(fx["keys"])
    peak = None if run["peak_gb"] is None else cgb(run["peak_gb"])
    ram_peak = cgb(run["ram_peak_gb"]) if run.get("ram_peak_gb") is not None else None
    referenced = {(q["env"], entry_key(n)) for q in fx.get("queued", []) for n in q["needs"]}
    got = admit(fx["host"], entries, budget, run["env"], peak, {entry_key(n) for n in run["needs"]}, referenced,
                running, ram_budget, ram_peak)
    got["unload"] = {env: [as_component(k) for k in keys] for env, keys in got["unload"].items()}
    return got


def value_addons(p: dict, value, wired: set) -> list[str]:
    """API 1.41 (README, Optional add-ons: an add-on per value): the add-ons enum param `p` asks for at `value`: the
    lists of its `addons_with` names that are wired (an input wired, a rows param holding a row), together, else its
    `addons` list for the value."""
    by = p.get("addons_with", {})
    if any(w in wired for w in by):
        return list(dict.fromkeys(c for w in by if w in wired for c in by[w].get(value, [])))
    return p.get("addons", {}).get(value, [])


def param_addons(a: dict, node_values: dict, entries: list[dict]) -> list[str]:
    """A place fixture's add-ons per value of the param of entry `a` (README, Several hosts: fixtures): the param's
    `addons` / `addons_with` from the run's entries of that node and param, its value from the run's `params`."""
    p: dict = {"addons": {}, "addons_with": {}}
    for e in entries:
        if e["node"] == a["node"] and e.get("param") == a["param"]:
            to = p["addons_with"].setdefault(e["with"], {}) if "with" in e else p["addons"]
            to.setdefault(e["value"], []).append(e["component"])
    wired = {k for k, v in node_values.items() if isinstance(v, list) and v}
    return value_addons(p, node_values.get(a["param"]), wired)


def place(fx: dict) -> dict:
    """Several hosts (README, Several hosts): the run's eligible hosts and the one it is lined up on, or the error it
    fails with. hosts: name -> {keys (as admit fixtures), workflows: {env: [ids]}, variants: {env: {"model/variant":
    [precisions]}} (present ones only), addons?: {env: ["model/component"]} (1.36: present add-ons only), loras: {env:
    [shared LoRA names]}, free, idle_since (null = never ran), reserved_gb? (enabled local servers)};
    run: {env, workflow, model?, variant?, precision?, refs? (1.40: an Image value in its graph), addons? ([{node, kind,
    component, title, bypass?, variants?, refs? (1.40)}]: 1.36), loras
    (shared, graph order), peak_gb, ram_peak_gb?, needs, lora_component?, lost_host?, host? (1.35: the run is set to
    that host only)}."""
    run, hosts = fx["run"], fx["hosts"]
    env, pin = run["env"], run.get("host")
    if pin and run.get("lost_host"):
        gone = lambda m: f"host {pin} went offline; a run set to {pin} never moves to another host"
    else:
        gone = lambda m: (f"runner on host {run['lost_host']} stopped responding; no other live host for env {env}"
                          if run.get("lost_host") else m)
    cand = sorted(h for h, x in hosts.items() if x["keys"].get(env, {}).get("state") == "fresh" and pin in (None, h))
    cand = [h for h in cand if run["workflow"] in hosts[h]["workflows"].get(env, [])]
    if not cand:
        if pin:
            live = any(k.get("state") == "fresh" for k in hosts.get(pin, {"keys": {}})["keys"].values())
            return {"host": None, "eligible": [], "error": gone(
                f"host {pin} cannot run workflow {run['workflow']}" if live else f"host {pin} is offline")}
        return {"host": None, "eligible": [], "error": gone(f"no live host for env {env}")}
    if run.get("model"):
        mv = f"{run['model']}/{run['variant']}"
        cand = [h for h in cand if run["precision"] in hosts[h]["variants"].get(env, {}).get(mv, [])]
        if not cand:
            want = f"model {run['model']} variant {run['variant']} at {run['precision']}"
            return {"host": None, "eligible": [], "error": gone(
                f"host {pin} does not have {want}" if pin else f"no live host for env {env} has {want}")}
    values = run.get("params", {})
    for a in sorted(run.get("addons", []), key=lambda a: a["node"]):  # 1.36: Optional add-ons, by node id
        if a.get("bypass") or run.get("variant") not in a.get("variants", [run.get("variant")]):
            continue  # 1.40: an add-on scoped to other variants is not needed
        if a.get("refs", bool(run.get("refs"))) != bool(run.get("refs")):
            continue  # 1.40: nor one of the other task (Qwen's PE-T2I in an edit)
        if "param" in a and a["component"] not in param_addons(a, values.get(a["node"], {}), run["addons"]):
            continue  # 1.41: an add-on per value, of another value or swapped by a wired input
        cand = [h for h in cand if f"{run['model']}/{a['component']}" in hosts[h].get("addons", {}).get(env, [])]
        if not cand:
            return {"host": None, "eligible": [], "error": gone(
                f"{a['node']}: {a['title']} not downloaded on host {pin}" if pin
                else f"{a['node']}: {a['title']} not downloaded on any live host for env {env}")}
    has = lambda h, n: n in hosts[h]["loras"].get(env, [])
    with_loras = [h for h in cand if all(has(h, n) for n in run.get("loras", []))]
    if not with_loras:
        first = next(n for n in run["loras"] if not has(cand[0], n))
        if pin:
            return {"host": None, "eligible": [], "error": gone(f"host {pin} does not have LoRA {first}")}
        if not any(has(h, first) for h in cand):
            return {"host": None, "eligible": [], "error": gone(f"no live host for env {env} has LoRA {first}")}
        return {"host": None, "eligible": [], "error": gone(
            f"no live host for env {env} has all of its LoRAs ({first} is not on {cand[0]})")}
    peak = None if run["peak_gb"] is None else cgb(run["peak_gb"])
    ram_peak = cgb(run["ram_peak_gb"]) if run.get("ram_peak_gb") is not None else None
    needs = {entry_key(n) for n in run["needs"]}
    views = {h: host_view(hosts[h]["keys"]) for h in with_loras}
    # what enabled local servers reserve on a host is outside the app's control (decide subtracts it too)
    views = {h: (v[0], None if v[1] is None else v[1] - cgb(hosts[h].get("reserved_gb", 0)), *v[2:])
             for h, v in views.items()}
    decided = {h: admit(h, v[0], v[1], env, peak, needs, set(), v[3], v[2], ram_peak) for h, v in views.items()}
    eligible = [h for h in with_loras if decided[h]["decision"] != "fail"]
    if not eligible:  # too big everywhere: the largest host's reason (budget, then name)
        big = sorted(with_loras, key=lambda h: (-(views[h][1] or 0), h))[0]
        return {"host": None, "eligible": [], "error": gone(decided[big]["error"])}
    if run.get("lost_host") in eligible and len(eligible) > 1:  # attempt 2 goes elsewhere when it can
        eligible.remove(run["lost_host"])
    comp = run.get("lora_component")
    resident = lambda h: any(x[0] == env and x[1] in needs and (comp is None or x[1][0] == comp) for x in views[h][0])
    idle = lambda h: hosts[h].get("idle_since") or ""  # never ran = idle longest
    best = sorted(eligible, key=lambda h: (not hosts[h]["free"], not resident(h),
                                           decided[h]["decision"] != "release", idle(h), h))
    return {"host": best[0], "eligible": eligible, "error": None}


def simulate(fx: dict) -> dict:
    """One host, runs released one at a time in the dispatcher's order with host admission; every unload is answered
    at once. Plans: {env, peak_gb, lora_component, components (in order of last use)}; batches: two-step pipelines
    with row 1 first and a window of WINDOW rows."""
    budget, plans = cgb(fx["budget_gb"]), fx["plans"]
    keys_of = {n: [(c, entry_key(c | {"model": p["model"]})) for c in p["components"]] for n, p in plans.items()}
    kept = lambda n: [(c, k) for c, k in keys_of[n] if not c.get("unloads_after")]
    resident: dict[tuple, list] = {}  # (env, key) -> [gb, used]
    for i, n in enumerate(fx.get("resident", [])):
        for j, (c, k) in enumerate(kept(n)):
            resident[plans[n]["env"], k] = [cgb(c["gb"]), (i - len(fx["resident"])) * 1000 + j]
    runs, seq = [], [0]

    def add(run: dict) -> None:
        seq[0] += 1
        runs.append(run | {"seq": seq[0], "seconds": run.get("seconds", 10)})

    for r in fx.get("runs", []):
        add(r)
    batches = {b["id"]: b | {"started": 1, "ended": 0} for b in fx.get("batches", [])}
    for b in batches.values():
        add({"id": f"{b['id']}.1.1", "owner": b["owner"], "lane": "batch", "plan": b["steps"][0], "at": b["at"],
             "seconds": b["seconds"][0], "row": (b["id"], 1, 1)})

    def is_resident(r: dict) -> bool:
        p = plans[r["plan"]]
        return any((p["env"], k) in resident for c, k in keys_of[r["plan"]] if c["component"] == p["lora_component"])

    t, last, skips, first_skip = 0, {}, {}, {}
    releases, unloads, loads, fails = [], [], {}, {}
    while runs:
        ready = [r for r in runs if r["at"] <= t]
        if not ready:
            t = min(r["at"] for r in runs)
            continue
        lane = "interactive" if any(r["lane"] == "interactive" for r in ready) else "batch"
        owners: dict[str, list] = {}
        for r in sorted((r for r in ready if r["lane"] == lane), key=lambda r: (r["at"], r["seq"])):
            owners.setdefault(r["owner"], []).append(r)
        order = sorted(owners, key=lambda o: (o in last, last.get(o, 0), owners[o][0]["at"], owners[o][0]["seq"]))
        heads = [next((r for r in owners[o] if is_resident(r)), owners[o][0]) for o in order]
        overdue = lambda r: skips.get(r["id"], 0) >= SWAP_SKIPS or t - first_skip.get(r["id"], t) >= SWAP_WAIT_S
        pick = next((r for r in heads if is_resident(r) or overdue(r)), heads[0])
        for r in heads[: heads.index(pick)]:
            skips[r["id"]] = skips.get(r["id"], 0) + 1
            first_skip.setdefault(r["id"], t)
        plan = plans[pick["plan"]]
        entries = [(env, k, gb, False, True, used) for (env, k), (gb, used) in resident.items()]
        referenced = {(plans[r["plan"]]["env"], k) for r in ready if r is not pick for _, k in keys_of[r["plan"]]}
        got = admit("mac", entries, budget, plan["env"], cgb(plan["peak_gb"]), {k for _, k in keys_of[pick["plan"]]},
                    referenced)
        runs.remove(pick)
        end = t
        if got["decision"] == "fail":
            fails[pick["id"]] = got["error"]
        else:
            assert got["decision"] != "wait", "nothing in a simulation is stale"
            for env, ks in got["unload"].items():
                unloads.append({"before": pick["id"], "env": env, "components": [k[0] for k in ks]})
                for k in ks:
                    del resident[env, k]
            releases.append(pick["id"])
            if any((plan["env"], k) not in resident for _, k in kept(pick["plan"])):
                loads[pick["plan"]] = loads.get(pick["plan"], 0) + 1
            end = t + pick["seconds"]
            for j, (c, k) in enumerate(kept(pick["plan"])):
                resident[plan["env"], k] = [cgb(c["gb"]), end * 1000 + j]
            last[pick["owner"]] = t
        if "row" in pick:
            bid, row, step = pick["row"]
            b = batches[bid]
            if step < len(b["steps"]) and got["decision"] != "fail":
                add({"id": f"{bid}.{row}.{step + 1}", "owner": b["owner"], "lane": "batch", "plan": b["steps"][step],
                     "at": end, "seconds": b["seconds"][step], "row": (bid, row, step + 1)})
            else:
                b["ended"] += 1
                while b["started"] < b["rows"] and b["started"] - b["ended"] < WINDOW:  # row 1 has ended
                    b["started"] += 1
                    add({"id": f"{bid}.{b['started']}.1", "owner": b["owner"], "lane": "batch", "plan": b["steps"][0],
                         "at": end, "seconds": b["seconds"][0], "row": (bid, b["started"], 1)})
        t = end
    return {"releases": releases, "unloads": unloads, "loads": loads, "fails": fails}


def check_dispatch(registry: Registry) -> None:
    g = HERE / "fixtures" / "dispatch"
    reg = "https://helmstudio.local/contracts/registry.schema.json"
    unload = "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json"
    entry_v = Draft202012Validator({"$ref": f"{reg}#/$defs/host/properties/resident/items"}, registry=registry,
                                   format_checker=FormatChecker())
    comp_v = Draft202012Validator({"$ref": f"{unload}#/$defs/component"}, registry=registry)
    for p in sorted(g.glob("*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/dispatch/{p.name}"
        if p.name.startswith("admit."):
            shapes = [(entry_v, e) for k in fx["keys"].values() for e in k.get("resident", [])]
            shapes += [(comp_v, n) for q in [fx["run"], *fx.get("queued", [])] for n in q["needs"]]
            shapes += [(comp_v, c) for cs in fx["expect"]["unload"].values() for c in cs]
            got = admit_fixture(fx)
        elif p.name.startswith("place."):
            shapes = [(entry_v, e) for x in fx["hosts"].values() for k in x["keys"].values() for e in k.get("resident", [])]
            shapes += [(comp_v, n) for n in fx["run"]["needs"]]
            got = place(fx)
        elif p.name.startswith("order."):
            shapes = [(comp_v, c | {"model": pl["model"]}) for pl in fx["plans"].values()
                      for c in ({k: v for k, v in c.items() if k in ("component", "variant", "precision")}
                                for c in pl["components"])]
            got = simulate(fx)
            got = {k: v for k, v in got.items() if k in fx["expect"] or v}
        else:
            fail(f"{name}: not admit.*, place.* or order.*")
            continue
        if bad := [e for v, x in shapes for e in v.iter_errors(x)]:
            fail(f"{name}: {bad[0].json_path}: {bad[0].message}")
        elif got != fx["expect"]:
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(fx['expect'])}")
        else:
            print(f"ok   {name}")
