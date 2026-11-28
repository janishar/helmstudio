"""alpha.6 row 2: `make zonos3-get` unpacks the release's prebuilt CUDA zonos3-server only when it matches the
release's SHA256SUMS, and with no release file prints how to build it. A file:// folder stands in for the release."""

import hashlib
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest

RUNNER = Path(__file__).parent.parent
TAR = "zonos3-server-9.9.9-linux-x86_64-cuda13.tar.gz"

pytestmark = pytest.mark.skipif(not (shutil.which("curl") and shutil.which("make")), reason="no curl or make")


def get(tmp: Path, release: Path) -> subprocess.CompletedProcess:
    return subprocess.run(["make", "-s", "-C", str(RUNNER), "zonos3-get", "ZONOS3_RELEASE=v9.9.9",
                           f"ZONOS3_URL=file://{release}", f"ZONOS3_BUILD={tmp / 'build'}"],
                          check=False, capture_output=True, text=True)  # fmt: skip


def test_zonos3_get_checks_the_sum_and_hints_without_a_release(tmp_path):
    release, src = tmp_path / "release", tmp_path / "src"
    (src / "lib").mkdir(parents=True)
    release.mkdir()
    (src / "zonos3-server").write_text("#!/bin/sh\n")
    (src / "lib/libcudart.so.13").write_bytes(b"")
    with tarfile.open(release / TAR, "w:gz") as t:
        for name in ("zonos3-server", "lib"):
            t.add(src / name, name)
    good = hashlib.sha256((release / TAR).read_bytes()).hexdigest()

    (release / "SHA256SUMS").write_text(f"{'0' * 64}  {TAR}\n")
    r = get(tmp_path, release)
    assert r.returncode and "does not match the release's SHA256SUMS: refused" in r.stdout
    assert not (tmp_path / "build/zonos3-server").exists()

    (release / "SHA256SUMS").write_text(f"{good}  other.dmg\n{good}  {TAR}\n")
    r = get(tmp_path, release)
    assert r.returncode == 0, r.stdout + r.stderr
    assert (tmp_path / "build/zonos3-server").is_file() and (tmp_path / "build/lib/libcudart.so.13").is_file()

    (release / TAR).unlink()
    r = get(tmp_path, release)
    assert r.returncode and "make -C runner_service zonos3-cuda (nvcc 12.8+)" in r.stdout
