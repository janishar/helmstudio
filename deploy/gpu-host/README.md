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
user block for `runner_<h>_torch` goes into `deploy/nats/nats.conf` between the `gpu-hosts` markers, and the
identity into `s3_config.json`. NATS takes a new host on HUP, SeaweedFS restarts alone (`make gpu-host-reload`).

## 2. On the GPU host: driver and container toolkit

```sh
nvidia-smi                                   # the driver sees the GPU
nvidia-ctk --version                         # NVIDIA Container Toolkit installed
docker info --format '{{json .Runtimes}}'    # lists "nvidia" (else: sudo nvidia-ctk runtime configure --runtime=docker
                                             #   && sudo systemctl restart docker)
docker compose version                       # Compose 2.20 or later
```

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

## 6. See it in the app

Admin › Hosts lists `<h>` next to `mac` (`Linux · CUDA · container`, the GPU's name, VRAM used / budget, `reserve 2
GB`); Models shows each variant's presence per host. A Qwen Image run goes to whichever eligible host the dispatcher
lines up, and the run records it (`made_with.host`). Download Qwen Image onto this host from Models (needs
`MODELS_MODE=rw`) or copy the files into `MODELS_DIR`.

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
- **Object store**: its S3 identity can read, overwrite and delete any key of the media bucket (SeaweedFS cannot
  scope an identity to a prefix); only the runner's own claim-check rule keeps it to its commands' keys.
- **JetStream**: the streams have byte caps (CMD never starves), but there is no per-user quota: it can flood its
  own event and DLQ subjects, slowing the projector and pushing the oldest event history out early.
- **Shared definitions**: it cannot replace a template, look or node schema the stack's own hosts publish, but it can
  add new ids that every user then sees.
Run a GPU host only on a machine you control, and revoke it (above) when in doubt.
