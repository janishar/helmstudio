"""Smoke test of the Python client against a running daemon.

Run by test/conformance (TestPythonAndNodeClientsSmoke) with HELM_API,
HELM_TOKEN and HELM_STAGE_DIR set for a studio holding kv, records, assets and
gallery. Exits non-zero, naming the failed check, on any mismatch.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from helm_runtime_sdk import HelmError, from_env  # noqa: E402


def check(cond, what):
    if not cond:
        print("FAIL: " + what)
        sys.exit(1)


helm = from_env()
me = helm.me.get()
check(me["studio_id"] == os.environ["SMOKE_STUDIO"], "me.studio_id")

doc = helm.kv.put("ui", "py", {"n": 1})
try:
    helm.kv.put("ui", "py", {"n": 2}, if_match="stale")
    check(False, "stale If-Match was accepted")
except HelmError as e:
    check(e.kind == "Conflict" and e.code == "etag_mismatch", "etag conflict kind: %s %s" % (e.kind, e.code))
patched = helm.kv.patch("ui", "py", {"m": 2}, if_match=doc["etag"])
check(patched["doc"] == {"n": 1, "m": 2}, "merge patch")

rec = helm.records.insert("takes", {"seed": 42, "prompt": "a cafe"})
page = helm.records.query("takes", where=["seed:eq:42"], limit=10)
check([r["id"] for r in page["items"]] == [rec["id"]], "records filter")

staged = os.path.join(os.environ["HELM_STAGE_DIR"], "py-take.mp4")
with open(staged, "wb") as f:
    f.write(b"python take")
asset = helm.assets.adopt({"path": staged, "kind": "video"})
check(not os.path.exists(staged), "adopt unlinked the stage entry")
body = helm.assets.read(asset["id"], range="bytes=0-5").read()
check(body == b"python", "range read: %r" % body)
up = helm.assets.upload(b"py upload", "application/octet-stream", kind="other", filename="u.bin")
check(up["bytes"] == 9, "upload")

item = helm.gallery.add({"kind": "video", "asset_id": asset["id"], "params": {"seed": 42}, "tags": ["py"]})
found = helm.gallery.query(tag=["py"])
check([i["id"] for i in found["items"]] == [item["id"]], "gallery tag query")

try:
    helm.jobs.list()
    check(False, "jobs without the capability")
except HelmError as e:
    check(e.kind == "Forbidden" and e.details.get("capability") == "jobs", "capability_required details")

print("ok")
