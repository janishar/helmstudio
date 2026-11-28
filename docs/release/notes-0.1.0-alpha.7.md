The seventh alpha of helmstudio, a local-first AI studio for images, video, voice, music and sound on your own Mac and NVIDIA machines. A short fix release for two bugs in alpha.6. Expect rough edges and breaking changes between releases; please [report what breaks](https://github.com/janishar/helmstudio/issues).

Docs: **https://helmstudio.in**

## Install

**Mac, updating from 0.1.0-alpha.2 or later:** the app updates itself. Choose **Check for updates…** in its menu (or wait for its daily check), then **Install**. Your work and settings stay; on its first open it pulls the new images and removes the old ones.

**Mac, new install (Apple Silicon, prototype):** download `helmstudio-0.1.0-alpha.7.dmg` below, drag **helmstudio** to Applications and open it the first time with **right-click › Open** (ad-hoc signed). First open sets everything up in one window; no Docker needed. From 0.1.0-alpha.1, replace the app by hand once.

**Windows and Linux (Docker):**

1. Install Docker: Docker Desktop on Windows (with WSL2, its default); Docker Engine with the Compose plugin, or Docker Desktop, on Linux.
2. Make a folder and download `compose.yml` below into it.
3. In that folder, start it the first time with your admin account:

   ```sh
   HELMSTUDIO_ADMIN_EMAIL=you@example.com HELMSTUDIO_ADMIN_PASSWORD='10 characters or more' docker compose up -d --wait
   ```

   On Windows, put those two lines in a `.env` file beside `compose.yml` instead and run `docker compose up -d --wait`.
4. Open http://localhost:8300 and sign in.

Updating from 0.1.0-alpha.6: download the new `compose.yml` over the old one and run `docker compose pull && docker compose up -d --wait`; your data stays.

NVIDIA on Windows or Linux: the runners run on the machine itself, on your own NVIDIA driver (no CUDA image): install the driver, ffmpeg and uv (optionally SGLang for MiniMax H3 video), then `make runners-nvidia`. Guide: [docs/guides/nvidia-linux.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.7/docs/guides/nvidia-linux.md). Updates, settings and more: [deploy/release/README.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.7/deploy/release/README.md).

**From source:** see [Install and run](https://helmstudio.in/start/install/).

Download models on the **Models** screen; each keeps its own licence.

## What's new

- **The Mac app now includes the MiniMax H3 and ZONOS3 programs.** In alpha.6's `.dmg` those two packs failed with "the h3 binary is not built".
- **The Assistant no longer fails to design a voice or run blocks while other runs finish** ("board changed since you opened it").

## Known limits

Same as 0.1.0-alpha.6; see its [notes](https://github.com/janishar/helmstudio/releases/tag/v0.1.0-alpha.6).

## Checksums

`SHA256SUMS` below covers `helmstudio-0.1.0-alpha.7.dmg` and `compose.yml`.

Container images: `ghcr.io/janishar/helmstudio-{web,studio,pipelines,auth,files,kong,seaweedfs,media-tools,setup}:0.1.0-alpha.7` (linux/amd64 and linux/arm64).

Full list of changes: [CHANGELOG.md](https://github.com/janishar/helmstudio/blob/v0.1.0-alpha.7/CHANGELOG.md).
