# NVIDIA on Linux and Windows (WSL2)

[← Docs index](../README.md) · release: [plan 34](../plan/34-release-builds.md)

No NVIDIA image ships. The stack runs in Docker as usual, and the GPU runners run on the machine itself, from
source, on the card's own driver (like the Mac's `make runners`). On Windows do all of this inside WSL2 (Ubuntu).

## 1. Install

- **NVIDIA driver**, recent enough for CUDA 13 (`nvidia-smi` must work; on WSL2 install the Windows driver only).
- **Docker** with Compose 2.26 or later, and `git`, `make`, `ffmpeg` (`sudo apt install git make ffmpeg`).
- **uv 0.13.8**: `curl -LsSf https://astral.sh/uv/0.13.8/install.sh | sh`. The runner's torch comes with its own
  CUDA libraries; no CUDA toolkit is needed.
- **SGLang 0.5.23**, optional (MiniMax H3 Base fp8), in its own venv:

  ```sh
  uv venv --python 3.12 ~/sglang
  VIRTUAL_ENV=~/sglang uv pip sync runner_service/sglang-requirements.txt
  export SGLANG_BIN=~/sglang/bin/sglang
  ```

## 2. Run

```sh
git clone https://github.com/janishar/helmstudio && cd helmstudio
make up GPU_HOSTS=gpu1                       # the stack, plus host gpu1's NATS user and S3 identity
GPU_HOST=gpu1 HELMSTUDIO_MODELS=/path/to/models make runners-nvidia
```

Then open http://localhost:8300. `make runners-nvidia` starts env torch, and env native when SGLang is found
(`SGLANG_BIN`, else `sglang` on PATH). The host name is `GPU_HOST` (default: the first of `GPU_HOSTS`, else the one
host `make up` made, else the machine's short name); it must be one `make up GPU_HOSTS=…` made. Logs:
`runner_service/.run/runner-<host>-<env>.log`. Stop: `make runners-nvidia-stop`, then `make down`.
