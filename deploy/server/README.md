# helmstudio server pack

Runs helmstudio on a Linux machine from the release images (ghcr.io/janishar/helmstudio-*, for Intel/AMD and ARM), with
nothing to build. The stack: Kong, auth, studio, files, web, pipelines, media-tools, NATS, Postgres and SeaweedFS.

## Linux

Needs Docker Engine with the Compose plugin 2.24 or later, curl, openssl, about 3 GB of disk for the images and 2 GB
of memory. Your user must be allowed to use Docker (the `docker` group). One command:

```sh
curl -fsSL https://github.com/janishar/helmstudio/releases/latest/download/install.sh | sh
curl -fsSL https://github.com/janishar/helmstudio/releases/latest/download/install.sh | sh -s -- 0.1.0-alpha.4   # a given version
```

While every helmstudio release is a prerelease, GitHub's `releases/latest` link does not resolve: use a release's
own link, `https://github.com/janishar/helmstudio/releases/download/v<version>/install.sh`.

It finds the newest release (or the version given), downloads its server pack and `SHA256SUMS`, refuses the pack if
the checksum does not match, unpacks it and runs the unpacked `install.sh`. It never uses sudo: if Docker is missing
it says how to install it and stops. Two folders:

- `~/helmstudio` (`HELMSTUDIO_DIR`): the downloaded pack (compose.yml, the scripts, this README). Each update
  replaces it whole.
- `~/.helmstudio` (`HELMSTUDIO_HOME`): what must survive updates. `helmstudio.env` (the settings: version, ports,
  address, the admin's email) and `secrets/` (folder 700: the stack's keys, `admin.env` with the admin's password,
  mode 600, and `providers/`, the model providers' keys). The pack links to them (`.env`, `deploy/secrets`).
- The data (Postgres, NATS, the object store) is in Docker volumes.

By hand instead: download `helmstudio-server-<version>.tar.gz` and `SHA256SUMS` from the release, run
`sha256sum -c --ignore-missing SHA256SUMS`, unpack, and run `./install.sh` in the unpacked folder.

`install.sh` asks for the admin email and password (12 characters or more); without a terminal set
`HELMSTUDIO_ADMIN_EMAIL` and `HELMSTUDIO_ADMIN_PASSWORD_FILE` (a file holding the password). It makes the settings and
secrets (kept on a re-run), starts the stack and waits until it is healthy, then prints the address:
`http://127.0.0.1:8300`. Every port is on 127.0.0.1 only: reach it through a reverse proxy with TLS (then set
`PUBLIC_BASE_URL` and `WEB_COOKIE_SECURE=true` in `~/.helmstudio/helmstudio.env`) or an SSH tunnel
(`ssh -L 8300:127.0.0.1:8300 <server>`). After changing a setting run `~/helmstudio/install.sh` again. To save
provider keys from Admin › Model providers, run once (install.sh says so):
`sudo chown 10001:10001 ~/.helmstudio/secrets/providers && sudo chmod 700 ~/.helmstudio/secrets/providers`.

- **Update:** `~/helmstudio/update.sh` (the newest release) or `~/helmstudio/update.sh 0.1.0-alpha.5`, or the
  one-line command again. It checks the new pack, stops the stack, replaces `~/helmstudio` and starts again; the
  settings, secrets and data stay.
- **Uninstall:** `~/helmstudio/uninstall.sh` stops it and removes the containers. It removes nothing else (not
  `~/.helmstudio`, not the volumes) and prints the commands that do.
- **Logs and status:** `cd ~/helmstudio && docker compose ps`, `docker compose logs -f studio`.

## Windows (WSL2), untested

Not tried yet. Install Docker Desktop with the WSL2 backend, open an Ubuntu shell in WSL2 and run the Linux command
there. Docker Desktop publishes 127.0.0.1 ports to Windows, so the browser on Windows opens `http://127.0.0.1:8300`.

## A GPU for the models

This pack runs the studio, not the models. A Linux (or WSL2) machine with an NVIDIA card runs them as a GPU host,
set up by hand as today: [deploy/gpu-host/README.md](https://github.com/janishar/helmstudio/blob/main/deploy/gpu-host/README.md)
and the docs at https://helmstudio.in. The GPU host needs the stack's NATS and object store over TLS
(`deploy/compose/tls.yml` in the repository), which this pack does not set up yet.
