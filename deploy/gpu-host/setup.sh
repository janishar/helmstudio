#!/bin/bash
# One-time setup of a fresh GPU host (Ubuntu 24.04, NVIDIA GPU): the newest NVIDIA open server driver the Ubuntu
# archive offers (615 on the M9 VM; Blackwell needs the open modules), its NVENC library, Docker Engine with Compose,
# the NVIDIA Container Toolkit, and /srv/helm folders owned by the container user (uid 10001). Idempotent; reboot after.
# From the stack's machine: gcloud compute ssh <vm> --zone <zone> --command 'bash -s' < deploy/gpu-host/setup.sh
# then: gcloud compute ssh <vm> --zone <zone> --command 'sudo reboot'   (README.md section 2 checks the result)
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -q
sudo apt-get install -yq ubuntu-drivers-common ca-certificates curl gnupg
ubuntu-drivers list --gpgpu || true
BR=$(ubuntu-drivers list --gpgpu 2>/dev/null | grep -oE 'nvidia-driver-[0-9]+-server-open' | sort -V | tail -1 | grep -oE '[0-9]+' | head -1)
[ -n "$BR" ] || BR=595
sudo apt-get install -yq "nvidia-driver-${BR}-server-open" "nvidia-utils-${BR}-server" "libnvidia-encode-${BR}-server" || \
  sudo apt-get install -yq "nvidia-headless-${BR}-server-open" "nvidia-utils-${BR}-server" "libnvidia-encode-${BR}-server"
# Docker Engine
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" | sudo tee /etc/apt/sources.list.d/docker.list
# NVIDIA Container Toolkit
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --batch --yes --dearmor -o /etc/apt/keyrings/nvidia-ctk.gpg
curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/etc/apt/keyrings/nvidia-ctk.gpg] https://#' | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update -q
sudo apt-get install -yq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo usermod -aG docker "$USER"
sudo mkdir -p /srv/helm/models /srv/helm/secrets
sudo chown -R 10001 /srv/helm/models
echo "BRANCH=$BR"
echo SETUP-DONE
