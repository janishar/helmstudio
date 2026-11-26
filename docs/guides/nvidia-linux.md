# NVIDIA on Linux and Windows (WSL2)

[← Docs index](../README.md) · release: [plan 34](../plan/34-release-builds.md)

No NVIDIA image ships. The stack runs in Docker as usual, and the GPU runners run on the machine itself, from
source, on the card's own driver (like the Mac's `make runners`). On Windows do all of this inside WSL2 (Ubuntu).

## 1. Install

- **NVIDIA driver**, recent enough for CUDA 13, with its tools: `nvidia-smi` must work (on WSL2 install the Windows
  driver only). On Ubuntu, e.g. `sudo apt install nvidia-headless-no-dkms-595-server-open
  linux-modules-nvidia-595-server-open-$(uname -r | sed 's/.*-//') nvidia-utils-595-server`, then reboot; a minimal
  "headless" driver alone has no `nvidia-smi`. If `apt` downloads crawl, switch to `http://archive.ubuntu.com/ubuntu`
  in `/etc/apt/sources.list.d/ubuntu.sources`.
- **Docker** with Compose 2.26 or later, and `git`, `make`, `ffmpeg` (`sudo apt install git make ffmpeg`).
- **uv 0.13.8**: `curl -LsSf https://astral.sh/uv/0.13.8/install.sh | sh`. It installs into `~/.local/bin`: open a
  new shell, or `export PATH="$HOME/.local/bin:$PATH"` (a script or an SSH command does not read your login profile;
  without it `make runners-nvidia` fails with "uvx: No such file or directory"). The runner's torch comes with its
  own CUDA libraries; no CUDA toolkit is needed. The first start downloads them (a few GB): the host appears in
  System › Hosts only after that.
- **SGLang 0.5.23**, optional (MiniMax H3 Base fp8), in its own venv: `make sglang` in the clone (step 2) puts it in
  `~/sglang` on uv's own Python 3.12, and `make runners-nvidia` finds it there (`SGLANG_BIN` names another place).
  SGLang compiles GPU kernels on its first run, so it needs a C compiler: `sudo apt install build-essential`. The
  first video takes a few minutes longer while it compiles. The runner makes the two CUDA links SGLang's kernels
  expect in its venv by itself, and refuses to start SGLang without gcc or with a long hostname (a cloud VM's full
  internal name, e.g. 65 characters, breaks SGLang's start with "File name too long"): give the machine a short
  one, `sudo hostnamectl set-hostname gpu1`. `make sglang` checks both and prints the fix.

## 2. Run

```sh
git clone https://github.com/janishar/helmstudio && cd helmstudio
make up GPU_HOSTS=gpu1                       # the stack, plus host gpu1's NATS user and S3 identity
make sglang                                  # optional: SGLang for MiniMax H3 Base fp8
GPU_HOST=gpu1 HELMSTUDIO_MODELS=/path/to/models make runners-nvidia
```

Then open http://localhost:8300. From another computer, tunnel the same port (`ssh -N -L 8300:127.0.0.1:8300 <machine>`,
then http://localhost:8300 there): the web app accepts sign-in only at its own address, so a different local port
fails with a server error; to use another address set `PUBLIC_BASE_URL` (and `HELMSTUDIO_PORT`) in `.env`. `make runners-nvidia` starts env torch, and env native when SGLang is found
(`SGLANG_BIN`, else `~/sglang` from `make sglang`, else `sglang` on PATH). The host name is `GPU_HOST` (default: the first of `GPU_HOSTS`, else the one
host `make up` made, else the machine's short name); it must be one `make up GPU_HOSTS=…` made. Logs:
`runner_service/.run/runner-<host>-<env>.log`. Stop: `make runners-nvidia-stop`, then `make down`.

**With the Docker release stack** (deploy/release/compose.yml, no build): start it as its README says, then in a
clone run only the runners against it:

```sh
HELMSTUDIO_MODELS=/path/to/models make runners-nvidia STACK=release
```

The stack makes host `gpu`'s runner credentials on its first start (`HELMSTUDIO_GPU_HOST` in its `.env` names it
otherwise) and publishes NATS (4322) and S3 (8433) on 127.0.0.1 only; `STACK=release` copies the credentials out of
its `keys` volume into `deploy/secrets-release/`. Stop: `make runners-nvidia-stop STACK=release`.

## Windows (WSL2): untested

The same steps run inside WSL2 (Ubuntu 24.04). Differences:

- Install the NVIDIA driver on **Windows** only; WSL2 passes the card through (`nvidia-smi` works inside WSL2). Do
  not install a Linux driver inside WSL2.
- Install Docker Desktop with the WSL2 backend and turn on its integration for your Ubuntu distro (Settings ›
  Resources › WSL integration), so `docker` works inside WSL2 and the stack's ports on 127.0.0.1 reach the runners.
- Do everything else (git, ffmpeg, uv, build-essential, `make sglang`, `make runners-nvidia`) inside WSL2.
- Keep the repo and the models inside WSL2's own disk (e.g. `~/helmstudio`, `~/models`), not under `/mnt/c`: the
  Windows drive is much slower from WSL2.
- Open http://localhost:8300 in a Windows browser.

Nobody has run this on Windows yet: please report what breaks.
