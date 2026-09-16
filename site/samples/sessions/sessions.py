"""Keep a working session: its state, and the outputs made during it.

Run under helmstudio or helm dev by a studio holding `kv`, `assets` and
`gallery`.
"""

import os

from helm_runtime_sdk import HelmError, from_env

helm = from_env()

# A session holds whatever the studio's own interface needs to come back to.
session = helm.sessions.create({"name": "lighthouse series", "state": {"prompt": "a lighthouse", "seed": 7}})

# An output made during it says so.
path = os.path.join(os.environ["HELM_STAGE_DIR"], "session-take.png")
with open(path, "wb") as f:
    f.write(bytes.fromhex(
        "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
        "0000000d4944415478da63f8cfc0f01f0005000201ddb1c61c0000000049454e44ae426082"))
asset = helm.assets.adopt({"path": path, "kind": "image"})
helm.gallery.add({"kind": "image", "asset_id": asset["id"], "session_id": session["id"],
                  "params": {"prompt": "a lighthouse", "seed": 7}})
found = helm.gallery.query(session_id=session["id"])
assert len(found["items"]) == 1, found

# State changes as a merge patch, against the etag read: a second tab editing
# the same session cannot overwrite this one without seeing it.
session = helm.sessions.update(session["id"], {"state": {"seed": 8}}, if_match=session["etag"])
assert session["state"] == {"prompt": "a lighthouse", "seed": 8}, session["state"]
try:
    helm.sessions.update(session["id"], {"state": {"seed": 9}}, if_match="stale")
    raise SystemExit("a stale etag was accepted")
except HelmError as e:
    assert e.kind == "Conflict", e.kind

# Try something else without losing where you were.
copy = helm.sessions.duplicate(session["id"], {"name": "lighthouse series, at night"})
assert copy["state"] == session["state"], copy

print("ok")
