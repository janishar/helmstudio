# A remote GPU host

A Linux machine with one NVIDIA GPU that joins a running helmstudio stack as one more host (`gcp` in the M6 test
rig). It runs only `runner_service` as a container (env `torch`: Qwen Image, its LoRAs and the ask stage, on CUDA
device 0). The runner talks only to the stack's NATS and object store, both over TLS, with its own NATS user
`runner_<h>_torch` and S3 identity `runner-<h>` (contracts/README.md, A remote GPU host). Weights stay on this host's
disk, bind-mounted read-only (or read-write for model downloads), never in the image.

In the test rig nothing of the stack is published to the internet: an SSH reverse tunnel, opened from the stack's
machine, puts the stack's NATS and S3 https ports on the GPU host's loopback (`127.0.0.1:4222`, `127.0.0.1:8333`).

Below, `<vm>` and `<zone>` are the instance and its zone, `<h>` the host name (`gcp`).

## 1. On the stack's machine: credentials and TLS

```sh
make gpu-host-secrets GPU_HOSTS=<h>   # NATS user + S3 identity for <h>, reloads the running dev stack
# set STACK_TLS=on in deploy/.env:      NATS TLS and S3 https (deploy/compose/tls.yml) on the dev stack
make up
```

`.tools/make-secrets` writes, in `deploy/secrets/` (git-ignored): `nats_runner_<h>_torch_password` and
`nats_runner_<h>_torch_url` (`tls://runner_<h>_torch:…@127.0.0.1:4222`), `s3_runner_<h>_access_key_id`,
`s3_runner_<h>_secret_access_key`, and once for the stack `tls_ca`, `stack_tls_cert`, `stack_tls_key` (a self-signed
CA, whose key is not kept, and one server cert for NATS and S3: `localhost`, `127.0.0.1`, `nats`, `seaweedfs`). The
account `HOST_<H>` with the user `runner_<h>_torch` and its limits, and HELM's exports to it, go into
`deploy/nats/nats.conf` between the `gpu-hosts` markers, and the identity (policy `runner`) into `s3_config.json`.
`make gpu-host-reload` restarts NATS when a config file is newer than its start (a reload alone leaves a new account's
imports unwired) and restarts SeaweedFS.

## 2. On the GPU host: driver and container toolkit

On a fresh Ubuntu 24.04 VM, `setup.sh` (in this folder) installs all of this; reboot after it:
`gcloud compute ssh <vm> --zone <zone> --command 'bash -s' < deploy/gpu-host/setup.sh`.

```sh
nvidia-smi                                   # the driver sees the GPU
nvidia-ctk --version                         # NVIDIA Container Toolkit installed
docker info --format '{{json .Runtimes}}'    # lists "nvidia" (else: sudo nvidia-ctk runtime configure --runtime=docker
                                             #   && sudo systemctl restart docker)
docker compose version                       # Compose 2.20 or later
```

NVENC for media jobs needs the driver's encode library, which a headless or compute-only driver install leaves out
(Ubuntu: `sudo apt-get install libnvidia-encode-<branch>`, e.g. `libnvidia-encode-595-server`), then
`sudo systemctl restart nvidia-cdi-refresh` so the toolkit's CDI spec mounts it. Without it the runner logs
`media jobs on … (ffmpeg …, libx264)` and exports encode in software.

## 3. Copy the folder, the runner's source and the secrets

From the repo root on the stack's machine (no secret is printed; the archive goes straight into ssh). Only tracked
files of the commit go (`git archive`): never the git-ignored `runner_service/.env` (the make-dev env, which may hold
a local NATS password or S3 key), `.venv`, caches or uncommitted edits:

```sh
git archive --format=tar.gz HEAD runner_service \
    deploy/gpu-host/docker-compose.yml deploy/gpu-host/.env.example deploy/gpu-host/README.md \
  | gcloud compute ssh <vm> --zone <zone> -- 'mkdir -p ~/helmstudio && tar -xzf - -C ~/helmstudio'
tar -czf - -C deploy/secrets nats_runner_<h>_torch_url s3_runner_<h>_access_key_id \
    s3_runner_<h>_secret_access_key tls_ca \
  | gcloud compute ssh <vm> --zone <zone> -- 'umask 077; mkdir -p ~/helmstudio/deploy/gpu-host/secrets &&
      tar -xzf - -C ~/helmstudio/deploy/gpu-host/secrets'
```

On the GPU host, give the files to the container's user (uid 10001) and nobody else, and set the host's values:

```sh
cd ~/helmstudio/deploy/gpu-host
sudo chown -R 10001 secrets && sudo chmod 700 secrets && sudo chmod 600 secrets/*
cp .env.example .env                         # set GPU_HOST, MODELS_DIR, MODELS_MODE
sudo mkdir -p /mnt/models && sudo chown 10001 /mnt/models   # MODELS_DIR; chown only for MODELS_MODE=rw
```

A token for gated Hub repos is optional: put it in `secrets/hf_token` (same owner and mode).
Both runners set `HF_HUB_DISABLE_XET=1` (plain HTTP downloads): Hugging Face's Xet CDN answered 429 Too Many
Requests to tokenless downloads twice (M9), and a token alone may not avoid it.

## 4. The tunnel (test rig only)

On the stack's machine (needs `STACK_TLS=on`; answer gcloud's first-time key prompts once by hand with
`gcloud compute ssh <vm> --zone <zone> -- true`):

```sh
make gpu-host-tunnel VM=<vm> ZONE=<zone>     # background; restarts after a drop; log deploy/gpu-host/.run/tunnel.log
.tools/gpu-host-tunnel status
make gpu-host-tunnel-stop
```

It runs `gcloud compute ssh <vm> --zone <zone> -- -N -R 127.0.0.1:4222:127.0.0.1:4322 -R
127.0.0.1:8333:127.0.0.1:8443` (`NATS_HOST_PORT`, `S3_TLS_HOST_PORT`): no firewall rule, no IAM change, no public
port. On the GPU host, `ss -ltn | grep -E ':(4222|8333) '` shows both on 127.0.0.1.

## 5. Build and start

```sh
cd ~/helmstudio/deploy/gpu-host
docker compose build                         # runner_service/Dockerfile.cuda: CUDA torch wheels from the lockfile
docker compose up -d
docker compose ps                            # healthy = NATS connected over TLS
docker compose logs -f runner                # the device line: NVIDIA <name>, VRAM, capability, budget
```

A `tls://` or `https://` endpoint whose cert does not verify against `secrets/tls_ca` stops the runner at start.

## 5b. Choose models

The install does not download every model. Once the stack is up and the host shows in `helm hosts`, pick what this
host should have:

    helm model pick --host <h>

It lists every model variant the host publishes, with its size, licence and state there, and asks which numbers to
download (for example `1,3,5-7`; empty means none). For any licence that must be accepted, it shows the licence title
and link and asks before queuing; only a typed `y` accepts it. `--all-missing` picks every missing or partial variant
and still asks for each licence. Progress shows in `helm models`.

The `native` profile and image (SGLang, about 28 GB, the longest build) is only needed for MiniMax H3 video (base-fp8) and
ZONOS3. If you are not installing either, start with `docker compose up -d` (no `--profile native`).

## 6. See it in the app

Admin › Hosts lists `<h>` next to `mac` (`Linux · CUDA · container`, the GPU's name, VRAM used / budget, `reserve 2
GB`); Models shows each variant's presence per host. A Qwen Image run goes to whichever eligible host the dispatcher
lines up, and the run records it (`made_with.host`). Download Qwen Image onto this host from Models (needs
`MODELS_MODE=rw`) or copy the files into `MODELS_DIR`.

## The quantized path

An alternate path for NVIDIA hosts (M9, [plan 17](../../docs/plan/17-nvidia-quantized.md)): community
pre-quantized weights, FP8 where the community publishes it, else int8. The official weights stay the default
everywhere and nothing about them changes; the quantized ones are **extra variants** of the same packs, marked
`devices: [cuda]`, so the Mac never offers them. They are smaller to download and keep (about 145 GB for every pack
instead of about 466 GB) and FP8 runs natively on Blackwell. Publisher-measured quality (LPIPS against bf16) is
worse for FP8 than for int8 ConvRot in every case; the variant names say which one a run used.

| Pack | Variant | Weights | Download |
|---|---|---|---|
| Qwen Image 2.1 | `base-fp8` | unsloth FP8 transformer and text encoder, bf16 VAE (configs from the official repo) | 17.2 GB (official bf16: 33.1) |
| | `turbo-fp8` | unsloth Turbo FP8 transformer; text encoder and VAE shared with `base-fp8` | 7.1 GB more (official: 14.2) |
| MiniMax H3 Image | `base-fp8` | unsloth FP8 DiT (20.3 GB), Comfy-Org int8 ConvRot text encoder (27.1 GB), the official VAE and configs (2.4 GB); the official NVFP4 prompt enhancer (17.9 GB, downloaded with it into its own folder `MiniMax-H3-Image-PE`, kept on delete) | 67.7 GB (49.8 + 17.9) |
| MiniMax H3 (video) | `base-fp8` | Comfy-Org pruned FP8 FL2VA and Ref2VA DiTs (2 × 21.0 GB), Comfy-Org int8 ConvRot text encoder (27.1 GB), the official VAEs, configs and tokenizers of both partitions (22.1 GB), run by SGLang 0.5.23 | 91.2 GB (official: 210.3) |
| ZONOS3-Mini | `mini` (the official one) | the official GGUF q8_0 set, already int8: the same files on the Mac and here, run by a CUDA build of zonos3-server | 1.2 GB |
| LTX-2.5 | `distilled-fp8` | unsloth FP8 distilled transformer (19.1 GB) and bf16 connector shards (6.3 GB), DmitryDB int8 ConvRot Gemma 4 text encoder (15.5 GB), the official upscaler, VAEs and duration head (2.8 GB); run by the torch runner on the vendored ltx-core | 43.7 GB (official pair: 122.0) |
| | `dev-int8` | DmitryDB int8 ConvRot dev transformer; the rest shared with `distilled-fp8` (it must be downloaded too: `ltx/distill` runs its transformer for stage 2) | 21.6 GB more |

LTX on NVIDIA leaves out, for now (the run fails before anything loads): LoRA rows and Prompt Relay beats. Its
workflow templates stay the Mac's (variant `distilled` / `dev`); a graph picks `distilled-fp8` / `dev-int8` on the
model node. Both LTX variants are not yet run on the VM (plan 17, 21.6 step 6). Qwen3-TTS stays on its official bf16
weights.

**Download** with `MODELS_MODE=rw` (section 3): Admin › Models, the variant's Download for this host, or
`helm model download <model> --host <h> --variant <v>`; or copy the folders into `MODELS_DIR` by hand. Plan the disk
from the table (and the official variants' sizes if this host keeps both).

**ZONOS3 needs the native runner.** It is a second service from the same image (`runner-native`: `RUNNER_ENV=native`,
health port `NATIVE_HEALTH_PORT`, 8082), built in `Dockerfile.cuda`'s zonos3 stage (`make zonos3-cuda`: ggml-cuda
for sm_120, CUDA 13.2). It needs its own NATS user `runner_<h>_native` (contracts/README.md, A remote GPU host), so it
is behind a profile:

```sh
docker compose --profile native build        # the image's native target: + SGLang's venv (several GB)
docker compose --profile native up -d        # needs secrets/nats_runner_<h>_native_url
docker compose logs -f runner-native
```

**MiniMax H3 video (`base-fp8`) needs the native runner too**, and the whole card. Its generate starts `sglang serve` (the
`native` target's `/opt/sglang`, its own Python and torch, never the torch runner's) as a child of the run: one
server per run on 127.0.0.1, killed after it, so nothing stays in VRAM. It loads the partition the run needs (FL2VA
for text or frames to video, Ref2VA for references) with the FP8 DiT and the int8 text encoder as weight overrides,
the text encoder layerwise offloaded to host RAM; a cold start reads about 70 GB. SGLang's limits apply: 107 to 345
frames (4 to 15 s), and text-to-video or references only at 21:9, 16:9, 4:3, 1:1, 3:4 or 9:16 (864 × 480 is 16:9);
first/last frame runs take any canvas; `speed: fast` is h3's only. VRAM is not budgeted for the native runner (its
budget is host RAM): SGLang measured 95.8 GB peak on this card at 1344 × 768 × 345 with a 33 GB FP8 DiT, so keep the
torch runner empty while it runs (`after_run: unload`, no pins). The licence (`minimax-h3-community`, accepted by the
admin on download) excludes the EU, the UK, the Republic of Korea and the US: check where the host runs.

## Stopping, preemption, revoking

- `docker compose down` here is a clean exit: the runner deletes its hosts key. Then `make gpu-host-tunnel-stop`.
- A spot preemption needs nothing: the host's keys go offline after 30 s and expire after 1 h; its leased run is
  retried once on another eligible host or fails cleanly. After the VM restarts, `docker compose up -d` again
  (`restart: unless-stopped` does it when Docker starts) and restart the tunnel if it gave up.
- Revoke a host: on the stack's machine delete its `nats_runner_<h>_*` and `s3_runner_<h>_*` files, run
  `make gpu-host-secrets`: its user block leaves `nats.conf`, its identity `s3_config.json`, and the running stack
  reloads. (With no host left, `gcp` gets fresh credentials: the old ones stop working either way.)

## A real deployment (not the test rig)

Never publish the stack's NATS client port itself: it also accepts plain clients (`allow_non_tls`, so the stack's own
services stay plain on the compose network), and every NATS user, the unrestricted admin too, could then log in in
clear from the network. Either keep the SSH tunnel of section 4 (the simplest: nothing is published), or put a
TLS-only listener in front of NATS: a TLS-terminating proxy on the public address that forwards to `nats:4222` on the
compose network, firewalled to the GPU hosts, with the server cert made for the public name
(`STACK_TLS_NAMES="DNS:stack.example.com" .tools/make-secrets` after deleting `stack_tls_*` and `tls_ca`) and the
host's URL made with `GPU_HOST_NATS_ADDR=stack.example.com:<port>`. S3's https port (8334) is TLS-only already: publish
it the same way (firewalled) and set `S3_ENDPOINT=https://stack.example.com:8334` in this folder's `.env`. The runner
refuses a `nats://` URL or an `http://` endpoint while `NATS_CA_FILE` / `S3_CA_FILE` are set (they are in this
compose file): an edited URL stops it at start, it never falls back to plain.

## What the stack does not enforce on a GPU host

A GPU host's credentials limit it to its own subjects, keys and run events (contracts/README.md, A remote GPU host),
but a compromised host is still trusted beyond its own runs in these ways:
- **Object store**: its S3 identity reads only under `u/` and the looks, writes only under the runner prefixes (run
  outputs, exports, frames, ask and look thumbnails) and never deletes, but that holds for every owner: it can read
  any owner's objects and overwrite their outputs; only the runner's own claim-check rule keeps it to its commands'
  keys.
- **NATS**: its account caps it at 8 connections, 100 subscriptions per connection and 1 MB per message, but NATS
  has no publish rate limit: within those caps it can still flood its own event and DLQ subjects, slowing the
  projector and pushing the oldest event history out early (the streams' byte caps keep CMD from starving). The
  consumers it creates in HELM's JetStream stop at the KV_cancels cap (1000), but a host that fills it makes other
  runners' runs fail until an admin deletes those consumers.
- **Shared definitions**: it cannot replace a template, look or node schema the stack's own hosts publish, but it can
  add new ids that every user then sees.
Run a GPU host only on a machine you control, and revoke it (above) when in doubt.
