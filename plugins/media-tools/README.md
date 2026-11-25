# media-tools

Media tools for the Assistant (an assistant-tools plugin, `plugin.json`; release 0.1.0-alpha.2 items 11 and 13).
Every edit makes a **new take** and the original stays; the chat line's Undo deletes the new take.

| Tool | What it makes |
|---|---|
| `edit_image` | crop, rotate, flip, resize, extend the canvas, convert (png, jpeg, webp), in that order → a new image take |
| `edit_video` | trim, speed 0.25–4×, mute, or the sound of an audio take → a new video take (H.264 + AAC) |
| `frame_image` | one frame of a video → a new image take ("frame as image") |
| `edit_audio` | trim, volume in dB (an audio take or a video's sound) → a new audio take (AAC m4a) |
| `join_audio` | 2–8 audio takes in order → a new audio take |
| `frames` | frames of a video (a Timeline export too) at given times, N evenly spaced or at scene changes, at most 8, with timecodes → no take: the Assistant looks at them |

No risk flags: nothing leaves the machine, nothing is deleted, no GPU. It reads and writes takes only through
studio's plugin API (`/v1/studio/plugin/takes`) with the call's pass (contracts/README.md, "Plugins: takes"): never a
user token, never the database, never another user's take.

## Start it

It runs as the stack's `media-tools` container (deploy/compose/media-tools.yml; owner, 2026-11-25: the same on the
Mac app's VM, dev and a Linux server, nothing to install): Alpine Python with ffmpeg from apk, the deps from `uv.lock`,
a non-root user, 0.0.0.0:8422 on the compose network only (studio and pipelines reach it as
`http://media-tools:8422`). Turn it on in the Plugins page (admins). It calls the stack at `HELM_API_URL`
(`http://kong:8000`) and reads the signed links studio gives it (made under the public base URL, the call's
`X-Helm-Api`) at that address too.

By hand, outside the stack (needs uv and ffmpeg + ffprobe on PATH; `FFMPEG`, `FFPROBE` override):

    uv run --frozen python server.py      # 127.0.0.1:8422 (MEDIA_TOOLS_HOST, MEDIA_TOOLS_PORT)

Without `HELM_API_URL` it calls the call's `X-Helm-Api`, only a loopback address.

## Limits

ffmpeg runs as an argument list, never through a shell, with fixed codecs; a call has 27 s of work (the Assistant
waits 30); videos and sounds of at most 10 minutes; images at most 50 MP; a video file at most 2 GB, an image or sound
50 MB.

## Test

    uv run pytest
