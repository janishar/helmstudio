The sixth alpha of helmstudio, a local-first AI studio for images, video, voice, music and sound on your own Mac and NVIDIA machines. Magic is a new board for making a short film with the Assistant as its director, Create makes music and sound effects, audio gets fades, crossfades and ducking, and the Assistant now watches and listens to what it made. Expect rough edges and breaking changes between releases; please [report what breaks](https://github.com/janishar/helmstudio/issues).

Docs: **https://helmstudio.in**

## Install

**Mac, updating from 0.1.0-alpha.2 or later:** the app updates itself. Choose **Check for updates…** in its menu (or wait for its daily check), then **Install**. Your work and settings stay; on its first open it pulls the new images and removes the old ones.

**Mac, new install (Apple Silicon, prototype):** download `helmstudio-0.1.0-alpha.6.dmg` below, drag **helmstudio** to Applications and open it the first time with **right-click › Open** (ad-hoc signed). First open sets everything up in one window; no Docker needed. From 0.1.0-alpha.1, replace the app by hand once.

**Windows and Linux (Docker):**

1. Install Docker: Docker Desktop on Windows (with WSL2, its default); Docker Engine with the Compose plugin, or Docker Desktop, on Linux.
2. Make a folder and download `compose.yml` below into it.
3. In that folder, start it the first time with your admin account:

   ```sh
   HELMSTUDIO_ADMIN_EMAIL=you@example.com HELMSTUDIO_ADMIN_PASSWORD='10 characters or more' docker compose up -d --wait
   ```

   On Windows, put those two lines in a `.env` file beside `compose.yml` instead and run `docker compose up -d --wait`.
4. Open http://localhost:8300 and sign in.

Updating from 0.1.0-alpha.5: download the new `compose.yml` over the old one and run `docker compose pull && docker compose up -d --wait`; your data stays.

NVIDIA on Windows or Linux: the runners run on the machine itself, on your own NVIDIA driver (no CUDA image): install the driver, ffmpeg and uv (optionally SGLang for MiniMax H3 video), then `make runners-nvidia`. Guide: [docs/guides/nvidia-linux.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.6/docs/guides/nvidia-linux.md). Updates, settings and more: [deploy/release/README.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.6/deploy/release/README.md).

**From source:** see [Install and run](https://helmstudio.in/start/install/).

Download models on the **Models** screen; each keeps its own licence.

## What's new

- **Magic, the film-making board.** Lanes in time for the characters, props and places (with their views), the storyboard, the shots, and voice, music and sound tied to them. Play the board as a rough cut; **Export** makes the MP4 in one step, or Send to Timeline cuts it further. Each voice, music and sound block has its own loudness.
- **Magic's Assistant and the Magic director skill.** It plans the film and waits for your OK before any GPU time, then makes the views, frames, shots and sound, and gives each speaking character a voice.
- **Music and sound in Create.** Songs with lyrics (ACE-Step 1.5, MiniMax Music 3) and sound effects (Stable Audio 3, MMAudio from text or for a video).
- **Audio fades, crossfades and ducking** on the Timeline and in Magic, carried to the player, Send and Export.
- **The Assistant watches and listens.** It sees a video as a grid of frames with their times, reads exact facts (length, loudness, silence, cuts), and asks Qwen3-Omni about a take's sound (Qwen2.5-Omni-3B when Qwen3-Omni cannot run).
- **Drag the playhead by its line**, in Magic and on the Timeline.
- **A one-panel rail.** The logo shows a red dot while a runner or the studio is down; the Assistant's item shows a dot when it has something for you.
- **Also:** error states in the web and Mac app, search in long lists, several model folders per machine (such as an external drive), Turbo by default for image and video, one slider design, overlay scrollbars, and new helm mcp tools (Magic boards, voice design, listen).

## Known limits

- Windows is untested.
- NVIDIA: the runners run from a source checkout on the machine; MiniMax H3 video needs SGLang you install; ZONOS3 on NVIDIA is not ready yet; the Turbo LoRA does not run on H3 Base fp8.
- The listen tool runs on the Mac only and needs llama.cpp (`brew install llama.cpp`) and its models from the Models screen (Qwen3-Omni or Qwen2.5-Omni-3B, and Qwen3-ASR). The 3B fallback's answers are right but short.
- A take's facts are measured the first time they are asked for, so the first ask can take up to 20 s.
- Create's "Use in ›" menu on a take has no "Last / First frame in…" yet.
- The Mac app needs Apple Silicon and is ad-hoc signed; no uninstall tool yet (see the [Mac app guide](https://helmstudio.in/start/mac-app/)).
- A separate NVIDIA machine joining a Mac's stack is set up by hand.
- Your Claude subscription runs on the Mac only and needs Claude Code installed and signed in by you.
- Web search uses unofficial search pages: it can be rate-limited or break.
- An Assistant steer is kept in memory: if the pipelines service restarts mid-answer, it becomes the next message.
- No model weights ship with helmstudio; you download them and accept their licences (Qwen2.5-Omni-3B is non-commercial).

## Checksums

`SHA256SUMS` below covers `helmstudio-0.1.0-alpha.6.dmg` and `compose.yml`.

Container images: `ghcr.io/janishar/helmstudio-{web,studio,pipelines,auth,files,kong,seaweedfs,media-tools,setup}:0.1.0-alpha.6` (linux/amd64 and linux/arm64).

Full list of changes: [CHANGELOG.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.6/CHANGELOG.md).
