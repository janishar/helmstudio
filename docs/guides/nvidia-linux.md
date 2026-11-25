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
- **SGLang 0.5.23**, optional (MiniMax H3 Base fp8), in its own venv:

  ```sh
  uv venv --python 3.12 ~/sglang
  VIRTUAL_ENV=~/sglang uv pip sync runner_service/sglang-requirements.txt
  export SGLANG_BIN=~/sglang/bin/sglang
  # SGLang's kernels link CUDA's runtime from <cu13>/lib64; the wheel ships lib/ and libcudart.so.13 only
  (cd ~/sglang/lib/python3.12/site-packages/nvidia/cu13 && ln -s lib lib64 && ln -s libcudart.so.13 lib/libcudart.so)
  ```

  SGLang compiles GPU kernels on its first run, so it also needs a C compiler and Python's headers:
  `sudo apt install build-essential python3.12-dev` (without them: "gcc: No such file or directory" or "Python.h:
  No such file or directory"). The first video takes a few minutes longer while it compiles.
  A machine whose hostname is long (a cloud VM's full internal name, e.g. 65 characters) breaks SGLang's start
  ("File name too long" from torch.distributed): give it a short one, `sudo hostnamectl set-hostname gpu1`.

## 2. Run

```sh
git clone https://github.com/janishar/helmstudio && cd helmstudio
make up GPU_HOSTS=gpu1                       # the stack, plus host gpu1's NATS user and S3 identity
GPU_HOST=gpu1 HELMSTUDIO_MODELS=/path/to/models make runners-nvidia
```

Then open http://localhost:8300. From another computer, tunnel the same port (`ssh -N -L 8300:127.0.0.1:8300 <machine>`,
then http://localhost:8300 there): the web app accepts sign-in only at its own address, so a different local port
fails with a server error; to use another address set `PUBLIC_BASE_URL` (and `HELMSTUDIO_PORT`) in `.env`. `make runners-nvidia` starts env torch, and env native when SGLang is found
(`SGLANG_BIN`, else `sglang` on PATH). The host name is `GPU_HOST` (default: the first of `GPU_HOSTS`, else the one
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
