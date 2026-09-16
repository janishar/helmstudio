"""Record an output with the inputs that made it, then walk its provenance.

Run under helmstudio or helm dev by a studio holding `assets` and `gallery`.
"""

import os
import struct
import zlib

from helm_runtime_sdk import from_env

helm = from_env()


def solid_png(r, g, b, size=8):
    """A small single-colour PNG, standing in for what a model would make."""
    row = b"\x00" + bytes([r, g, b]) * size
    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)
    header = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header)
            + chunk(b"IDAT", zlib.compress(row * size)) + chunk(b"IEND", b""))


def adopt_png(name, rgb):
    """Write a file to the stage directory and adopt it: no copy, no upload."""
    path = os.path.join(os.environ["HELM_STAGE_DIR"], name)
    with open(path, "wb") as f:
        f.write(solid_png(*rgb))
    return helm.assets.adopt({"path": path, "kind": "image"})


# A first generation: a reference image, recorded with what made it.
reference = adopt_png("reference.png", (200, 120, 40))
first = helm.gallery.add({
    "kind": "image",
    "asset_id": reference["id"],
    "title": "a lighthouse at dusk",
    "params": {"prompt": "a lighthouse at dusk", "seed": 7, "steps": 4},
})

# A second, made from the first: its input names the asset and the role it
# played. That link is the provenance.
variation = adopt_png("variation.png", (40, 120, 200))
second = helm.gallery.add({
    "kind": "image",
    "asset_id": variation["id"],
    "title": "the same lighthouse, at night",
    "params": {"prompt": "the same lighthouse, at night", "seed": 8, "strength": 0.6},
    "inputs": [{"asset_id": reference["id"], "role": "reference"}],
    "tags": ["variation"],
})

# Upstream: the items whose outputs the second was made from.
upstream = helm.gallery.lineage(second["id"])
assert [i["id"] for i in upstream["items"]] == [first["id"]], upstream

# Downstream: every item ever made from the reference asset.
downstream = helm.assets.lineage(reference["id"])
assert second["id"] in [i["id"] for i in downstream["items"]], downstream

print("ok")
