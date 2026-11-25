# helmstudio with Docker (Windows and Linux)

One file, `compose.yml`, attached to every release from 0.1.0-alpha.4. It runs the release images
(`ghcr.io/janishar/helmstudio-*`, for Intel/AMD and ARM): nothing to build, no install script. On a Mac use the
app (the release's `.dmg`) instead: it needs no Docker.

Needs Docker with Compose 2.26 or later, about 3 GB of disk for the images and 2 GB of memory.

## Windows

1. Install [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) with the WSL2 backend
   (its default) and start it.
2. Make a folder, download the release's `compose.yml` into it, and add a file `.env` beside it:

   ```
   HELMSTUDIO_ADMIN_EMAIL=you@example.com
   HELMSTUDIO_ADMIN_PASSWORD=10 characters or more
   ```

3. In that folder (PowerShell): `docker compose up -d --wait`, then open http://localhost:8300 and sign in.

The admin is read on the first start only: delete the two lines from `.env` afterwards if you like. A password
changed in the app is kept.

## Linux

Install [Docker Engine](https://docs.docker.com/engine/install/) with the Compose plugin (or Docker Desktop), then in
a folder holding the release's `compose.yml`:

```sh
HELMSTUDIO_ADMIN_EMAIL=you@example.com HELMSTUDIO_ADMIN_PASSWORD='10 characters or more' docker compose up -d --wait
```

Open http://localhost:8300. Every port is on 127.0.0.1: from another machine use an SSH tunnel or a reverse proxy
(then set `PUBLIC_BASE_URL=https://…` and `WEB_COOKIE_SECURE=true` in `.env`).

## An NVIDIA card

`docker compose --profile nvidia up -d --wait` adds the GPU runner (host `gpu` on the Hosts board). It needs the
NVIDIA driver; on Linux also the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html);
Docker Desktop on WSL2 passes the card through on Windows. Intel/AMD machines only. Models download into the
volume `models`; to use a folder of your own, set `HELMSTUDIO_MODELS_DIR=/path/to/models` in `.env`. Use the same
`--profile nvidia` on every later command (or `COMPOSE_PROFILES=nvidia` in `.env`).

## Update

Download the new release's `compose.yml` over the old one (or set `HELMSTUDIO_VERSION=<version>` in `.env`), then:

```sh
docker compose pull && docker compose up -d --wait
```

The data (Postgres, NATS, the object store) and the keys stay in Docker volumes. `docker compose down` stops
everything and keeps them; `docker compose down -v` deletes them too.

## Settings (`.env`)

| Name | Default | |
|---|---|---|
| `HELMSTUDIO_PORT` | 8300 | the web app's port on 127.0.0.1 |
| `PUBLIC_BASE_URL` | http://localhost:8300 | the address people open |
| `WEB_COOKIE_SECURE` | false | true behind TLS |
| `REMOTE_MODELS` | off | on: remote model providers (OpenAI and others) |
| `HELMSTUDIO_MODELS_DIR` | the volume `models` | the NVIDIA runner's models folder |
| `HELMSTUDIO_VERSION` | the file's release | another release's images |
| `HELM_NET` | 172.30.97 | the stack network's first three numbers, if it clashes with yours |

## From source

`git clone https://github.com/janishar/helmstudio && cd helmstudio && make up` (the repository's README).

## How it works

`secrets` (the `helmstudio-setup` image, `setup.Dockerfile`) runs on every `up`: `.tools/make-secrets` into the volume
`keys` (it makes what is missing and never overwrites a secret), the admin saved on the first start, each service's
own secrets copied to `keys/run/<service>/` and mounted at its `/run/secrets`, and this version's config files
(nats.conf, postgres init.sh, the one-shots' scripts, the built-in plugins) into the volume `config`. `admin` then
runs `auth create-admin` (as `.tools/create-admin`); when the account exists it changes nothing.
