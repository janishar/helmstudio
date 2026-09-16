"""Put outputs in a sequence the framework owns, then export it.

Run under helmstudio or helm dev by a studio holding `timeline`, `assets` and
`gallery`. Exporting needs ffmpeg on the machine running helmstudio.
"""

import os
import struct
import time
import wave
import zlib

from helm_runtime_sdk import from_env

helm = from_env()


def still(name, rgb, width=640, height=360):
    """Adopt a single-colour still, standing in for a generated frame."""
    row = b"\x00" + bytes(rgb) * width
    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)
    png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(row * height)) + chunk(b"IEND", b""))
    path = os.path.join(os.environ["HELM_STAGE_DIR"], name)
    with open(path, "wb") as f:
        f.write(png)
    return helm.assets.adopt({"path": path, "kind": "image"})


def tone(name, seconds=3, rate=48000):
    """Adopt a short sound: silence, standing in for a generated voice line."""
    path = os.path.join(os.environ["HELM_STAGE_DIR"], name)
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(b"\x00\x00" * rate * seconds)
    # A sound's length is a hint the studio gives; an export measures it again.
    return helm.assets.adopt({"path": path, "kind": "audio", "duration_s": float(seconds)})


dawn = still("dawn.png", (230, 160, 90))
dusk = still("dusk.png", (60, 50, 120))

# A sequence is a document the framework keeps, not a file the studio writes.
# Clips given without positions are laid end to end, video and stills on V1 and
# sound on A1; a still is held.
sequence = helm.timeline.create({
    "name": "a day at the lighthouse",
    "target": {"width": 640, "height": 360, "fps": 24},
    "clips": [{"asset_id": dawn["id"], "hold": 2}, {"asset_id": dusk["id"], "hold": 2}],
})
assert sequence["duration_s"] == 4.0, sequence["duration_s"]

# Adding to the end of a track needs no etag, so a studio can hand things over
# as it makes them. It takes video and sound; a still needs a hold, which
# append has no way to give.
voice = tone("voice.wav")
sequence = helm.timeline.append({"asset_id": voice["id"], "timeline_id": sequence["id"]})
assert [t["name"] for t in sequence["tracks"]] == ["V1", "A1"], sequence["tracks"]

# Every other edit is a merge patch against the revision read, and each one is
# a revision that can be reverted to. Here, the voice comes down 6 dB.
tracks = sequence["tracks"]
tracks[1]["clips"][0]["gain_db"] = -6
edited = helm.timeline.update(sequence["id"], {"tracks": tracks}, if_match=sequence["etag"])
assert edited["revision"] == sequence["revision"] + 1, edited["revision"]

# Undo is writing the earlier revision back, as the newest one.
reverted = helm.timeline.revert(edited["id"], {"revision": sequence["revision"]}, if_match=edited["etag"])
assert "gain_db" not in reverted["tracks"][1]["clips"][0], reverted["tracks"][1]
sequence = helm.timeline.update(reverted["id"], {"tracks": tracks}, if_match=reverted["etag"])

# The plan says whether the picture can be copied or must be re-encoded, and
# why. A still is always drawn, so this sequence is re-encoded.
plan = helm.timeline.plan(sequence["id"], preset="h264")
assert plan["mode"] == "conform", plan

job = helm.timeline.export(sequence["id"], {"preset": "h264"})
while True:
    job = next(j for j in helm.timeline.exports(sequence["id"])["items"] if j["id"] == job["id"])
    if job["state"] not in ("queued", "running"):
        break
    time.sleep(0.2)
assert job["state"] == "succeeded", job

# The export is an item in the gallery, labelled with the sequence it came from.
exported = helm.gallery.query(kind="video")
assert any(i.get("timeline_id") == sequence["id"] for i in exported["items"]), exported

print("ok")
