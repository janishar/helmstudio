"""Compare two PNGs: prints PSNR (dB) and mean absolute difference (0-255), exits 0 when PSNR >= threshold.

    python compare.py golden.png candidate.png [--threshold 30]

Both images are compared as RGBA 8-bit; different sizes fail outright (exit 2). Needs Pillow and numpy.
"""

from __future__ import annotations

import argparse
import math
import sys

import numpy as np
from PIL import Image


def diff(a: np.ndarray, b: np.ndarray) -> tuple[float, float]:
    """(PSNR in dB, mean absolute difference) of two uint8 arrays; PSNR is inf when identical."""
    d = a.astype(np.float64) - b.astype(np.float64)
    mse = float(np.mean(d * d))
    psnr = math.inf if mse == 0 else 10 * math.log10(255.0**2 / mse)
    return psnr, float(np.mean(np.abs(d)))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("a")
    ap.add_argument("b")
    ap.add_argument("--threshold", type=float, default=30.0, help="minimum PSNR in dB (default 30)")
    args = ap.parse_args()
    a, b = (np.asarray(Image.open(p).convert("RGBA")) for p in (args.a, args.b))
    if a.shape != b.shape:
        print(f"size mismatch: {a.shape} vs {b.shape}")
        return 2
    psnr, mad = diff(a, b)
    print(f"psnr_db={psnr:.2f} mean_abs_diff={mad:.3f} threshold_db={args.threshold}")
    return 0 if psnr >= args.threshold else 1


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        x = np.zeros((4, 4, 4), np.uint8)
        assert diff(x, x) == (math.inf, 0.0)
        p, m = diff(x, x + 1)  # every channel off by one: mse 1
        assert abs(p - 48.13) < 0.01 and m == 1.0, (p, m)
        print("ok")
        sys.exit(0)
    sys.exit(main())
