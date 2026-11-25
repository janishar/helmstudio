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

It starts itself: turn it on in the Plugins page (admins) and the Mac's runner runs `run.sh`. By hand:

    plugins/media-tools/run.sh

Needs [uv](https://docs.astral.sh/uv/) and ffmpeg + ffprobe on PATH (`brew install ffmpeg`; `FFMPEG`, `FFPROBE`
override). It listens on 127.0.0.1:8422 (`MEDIA_TOOLS_HOST`, `MEDIA_TOOLS_PORT`); OrbStack containers reach it as
`http://host.docker.internal:8422`. It calls the stack at the call's `X-Helm-Api` (only a loopback address), or
`HELM_API_URL` when set.

## Limits

ffmpeg runs as an argument list, never through a shell, with fixed codecs; a call has 27 s of work (the Assistant
waits 30); videos and sounds of at most 10 minutes; images at most 50 MP; a video file at most 2 GB, an image or sound
50 MB.

## Test

    uv run pytest
