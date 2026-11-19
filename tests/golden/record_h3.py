"""Record golden MiniMax H3 video (+ audio) takes from the vendored h3 binary, and measure the child's memory.

h3.c is pinned and vendored at runner_service/vendor/h3c (VERSION names the commit); build it first:

    make -C runner_service/vendor/h3c h3
    python3 -I tests/golden/record_h3.py golden t2v fl2va-first fl2va-first-last ref2va-image
    python3 -I tests/golden/record_h3.py measure --width 864 --height 480 --frames 124 --steps 4 [--first IMG]
        [--env H3_ZERO_COPY_WEIGHTS=0] [--extra --ssd-streaming] [--bin <other h3>] [--json out.jsonl]

Stdlib only (ffmpeg/ffprobe on PATH, as h3 needs them). The binary runs with cwd = its own folder (it opens
h3_shaders.metal by a relative path). Every run is sampled every 25 ms: the child's phys_footprint (what jetsam and
Activity Monitor count; Metal buffers included) plus its own children's (ffmpeg), its resident size (also counts the
zero-copy weight pages mapped from the shards) and the kernel's lifetime max footprint; once a second the system's
wired, compressor and swap. A guard stops the child past GUARD_GB. stderr progress lines (`\\r<phase> n/total`) give
the time and peak per phase.

`golden` runs each case twice (separate processes) with the pack's exact flags (seed 42, 4 steps, --layers 50
--reuse 1 --use-int8-row-fc2) and the default env, and writes runner_service/tests/golden/minimax-h3-<case>/: the
input PNGs (made here from minimax-h3-image-edit/input-turbo-768-s42.png with ffmpeg), the MP4, and <case>.json with
sha256 of the final frames as h3 wrote them (--frames-dir PPM), of the MP4's decoded RGB24 frames and f32le PCM, the
MP4's own sha256, ffprobe, time and memory, whether the two runs were bit-identical and, when not, the PSNR between
them. With --bin it records nothing: it prints the hashes of one run of each case with that binary (to compare
against the vendored one).
"""

from __future__ import annotations

import argparse
import ctypes
import hashlib
import json
import os
import platform
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
VENDOR = REPO / "runner_service" / "vendor" / "h3c"
GOLDEN = REPO / "runner_service" / "tests" / "golden"
SOURCE_IMG = GOLDEN / "minimax-h3-image-edit" / "input-turbo-768-s42.png"
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
GUARD_GB = 58.0  # the child (with its ffmpeg) is stopped past this footprint: the Mac has 64 GB
EXACT = ["--layers", "50", "--reuse", "1", "--use-int8-row-fc2"]  # the pack's speed "exact" on M5
# At h3.c 4cafd17 on this Mac T2VA repeats bit for bit; runs with an image input (FL2VA, Ref2VA) do not always:
# two runs of the same argv can differ by ~37-40 dB PSNR (frames and audio). None of H3_DISABLE_FUSED_PATCH_PACK,
# H3_CPU_SAMPLER or H3_QWEN_PREFETCH=0 made them repeatable, so the goldens run with the default env, as the pack does,
# and record the PSNR between the runs.

CASES = {
    "t2v": {"prompt": "A red fox trots through a snowy pine forest, snow falling softly, wind in the trees, "
                      "the crunch of paws on fresh snow.", "inputs": {}},
    "fl2va-first": {"prompt": "The fox lifts its head, looks around and trots out of frame to the right, "
                              "snow falling, a soft winter wind.", "inputs": {"first": "first"}},
    "fl2va-first-last": {"prompt": "The fox turns around in the snow to face the other way, a soft winter wind.",
                         "inputs": {"first": "first", "last": "last"}},
    "ref2va-image": {"prompt": "<Picture 1> runs across a wide snowy field toward the camera, birds calling "
                               "in the distance.", "inputs": {"ref": "first"}},
}  # fmt: skip
FLAG = {"first": "--first-frame", "last": "--last-frame", "ref": "--ref-image"}
SHAPE = {"width": 512, "height": 512, "frames": 22, "steps": 4, "seed": 42}

_libc = ctypes.CDLL(None)
_RU = (ctypes.c_uint64 * 64)()  # rusage_info_v4: a 16-byte uuid, then u64 fields


def rusage(pid: int) -> tuple[int, int, int, int] | None:
    """(phys_footprint, resident_size, lifetime_max_phys_footprint, disk bytes read) or None once the pid is gone."""
    if _libc.proc_pid_rusage(pid, 4, ctypes.byref(_RU)) != 0:
        return None
    return _RU[2 + 7], _RU[2 + 6], _RU[2 + 28], _RU[2 + 16]


def children(pid: int) -> list[int]:
    buf = (ctypes.c_int * 64)()
    n = _libc.proc_listchildpids(pid, buf, ctypes.sizeof(buf))
    return [buf[i] for i in range(max(n, 0)) if buf[i] > 0]


def system() -> dict:
    """GB wired, in the compressor, free; swap used (GB)."""
    out = subprocess.run(["vm_stat"], check=False, capture_output=True, text=True).stdout
    page = int(re.search(r"page size of (\d+)", out).group(1))
    pages = {k.strip(): int(v.strip(" .")) for k, v in re.findall(r"^([^:]+):\s+([\d.]+)$", out, re.MULTILINE)}
    swap = subprocess.run(["sysctl", "-n", "vm.swapusage"], check=False, capture_output=True, text=True).stdout
    used = re.search(r"used = ([\d.]+)M", swap)
    gb = lambda k: round(pages.get(k, 0) * page / 2**30, 2)
    return {"wired": gb("Pages wired down"), "compressor": gb("Pages occupied by compressor"),
            "free": gb("Pages free"), "swap_used": round(float(used.group(1)) / 1024, 2) if used else None}  # fmt: skip


def run_h3(binary: Path, args: list[str], env: dict[str, str]) -> dict:
    """One h3 process, sampled; returns exit code, per-phase time/peak, overall peaks, the last h3: lines."""
    proc = subprocess.Popen([str(binary), *args], cwd=binary.parent, env={**os.environ, **env},
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)  # fmt: skip
    t0 = time.monotonic()
    state = {"phase": "start", "order": ["start"], "first": {"start": 0.0}, "last": {"start": 0.0}, "total": {},
             "peak": {}, "io": {}, "max_fp": 0, "max_rss": 0, "life_max": 0, "max_child": 0, "diag": [],
             "guard": None, "prev": 0.0, "gap": (0.0, "")}  # fmt: skip
    sysmax = {"wired": 0.0, "compressor": 0.0, "swap_used": 0.0}
    sysmin_free = [1e9]
    sys0 = system()
    done = threading.Event()

    def read_stderr() -> None:
        buf = b""
        while chunk := proc.stderr.read1(4096):
            buf += chunk
            parts = re.split(rb"[\r\n]", buf)
            buf = parts.pop()
            for raw in parts:
                line = raw.decode(errors="replace").strip()
                if not line:
                    continue
                # a system log line (MPSGraph's "Failed to import MPS module") can land after a progress update
                m = re.match(r"^(.*?)\s+(\d+)/(\d+)(?:\s|$)", line)
                if m and not line.startswith("h3"):
                    ph, now = m.group(1).strip(), round(time.monotonic() - t0, 2)
                    if ph not in state["first"]:
                        state["order"].append(ph)
                        state["first"][ph] = now
                    state["last"][ph], state["total"][ph], state["phase"] = now, int(m.group(3)), ph
                    if now - state["prev"] > state["gap"][0]:  # the longest silence between two progress lines
                        state["gap"] = (round(now - state["prev"], 2), line.split("  ")[0] + " " + m.group(2))
                    state["prev"] = now
                else:
                    state["diag"] = (state["diag"] + [line])[-80:]

    def sample() -> None:
        tick = 0
        while not done.wait(0.025):
            r = rusage(proc.pid)
            if r is None:
                continue
            kids = sum((rusage(c) or (0, 0, 0, 0))[0] for c in children(proc.pid)) if tick % 4 == 0 else 0
            fp = r[0] + kids
            state["max_fp"], state["max_rss"] = max(state["max_fp"], fp), max(state["max_rss"], r[1])
            state["life_max"], state["max_child"] = max(state["life_max"], r[2]), max(state["max_child"], kids)
            ph = state["phase"]
            state["peak"][ph] = max(state["peak"].get(ph, 0), fp)
            state["io"][ph] = r[3]
            if fp > GUARD_GB * 2**30 and not state["guard"]:
                state["guard"] = f"footprint {fp / 2**30:.1f} GB in {ph}: stopped"
                os.killpg(proc.pid, signal.SIGTERM)
            if tick % 40 == 0:
                s = system()
                for k, v in sysmax.items():
                    sysmax[k] = max(v, s[k] or 0)
                sysmin_free[0] = min(sysmin_free[0], s["free"])
            tick += 1

    th = [threading.Thread(target=read_stderr, daemon=True), threading.Thread(target=sample, daemon=True)]
    for t in th:
        t.start()
    rc = proc.wait()
    wall = round(time.monotonic() - t0, 2)
    done.set()
    for t in th:
        t.join(5)
    order = state["order"]
    phases, io_prev = {}, 0
    for i, ph in enumerate(order):
        io = state["io"].get(ph, io_prev)
        end = state["first"][order[i + 1]] if i + 1 < len(order) else wall
        phases[ph] = {"start_s": state["first"][ph], "s": round(end - state["first"][ph], 2),
                      "units": state["total"].get(ph), "peak_gb": round(state["peak"].get(ph, 0) / 2**30, 2),
                      "read_gb": round((io - io_prev) / 2**30, 2)}  # fmt: skip
        io_prev = io
    g = lambda b: round(b / 2**30, 2)
    return {"rc": rc, "wall_s": wall, "phases": phases, "guard": state["guard"], "diag": state["diag"],
            "peak_footprint_gb": g(state["max_fp"]), "lifetime_max_footprint_gb": g(state["life_max"]),
            "peak_resident_gb": g(state["max_rss"]), "read_gb": g(max(state["io"].values(), default=0)),
            "peak_ffmpeg_child_gb": g(state["max_child"]), "longest_silence": state["gap"],
            "system_before": sys0, "system_peak": {**{k: round(v, 2) for k, v in sysmax.items()},
                                                   "min_free": sysmin_free[0]}}  # fmt: skip


def sha_cmd(cmd: list[str]) -> tuple[str, int]:
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    h, n = hashlib.sha256(), 0
    while chunk := p.stdout.read(1 << 20):
        h.update(chunk)
        n += len(chunk)
    if p.wait() != 0:
        raise SystemExit(f"{cmd[0]} failed")
    return h.hexdigest(), n


def hashes(mp4: Path, frames_dir: Path | None) -> dict:
    out = {"mp4_sha256": hashlib.sha256(mp4.read_bytes()).hexdigest()}
    base = ["ffmpeg", "-v", "error", "-i", str(mp4)]
    out["rgb24_sha256"], n = sha_cmd([*base, "-map", "0:v:0", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"])
    out["rgb24_bytes"] = n
    out["pcm_f32le_sha256"], n = sha_cmd([*base, "-map", "0:a:0", "-f", "f32le", "-"])
    out["pcm_f32le_bytes"] = n
    if frames_dir:
        h, files = hashlib.sha256(), sorted(frames_dir.iterdir())
        for f in files:
            h.update(f.read_bytes())
        out["frames_ppm_sha256"], out["frames_ppm_count"] = h.hexdigest(), len(files)
    return out


def probe(mp4: Path) -> dict:
    keys = (
        "stream=index,codec_name,profile,codec_type,width,height,pix_fmt,r_frame_rate,sample_rate,channels,"
        "duration,nb_frames"
    )
    p = subprocess.run(["ffprobe", "-v", "error", "-show_entries", keys + ":format=duration,bit_rate", "-of", "json",
                        str(mp4)], capture_output=True, text=True, check=True)  # fmt: skip
    d = json.loads(p.stdout)
    return {"streams": d["streams"], "format": d["format"]}


def video_psnr(a: Path, b: Path) -> dict:
    """ffmpeg's psnr filter between two MP4s' video: average and worst frame (dB)."""
    p = subprocess.run(["ffmpeg", "-i", str(a), "-i", str(b), "-lavfi", "[0:v][1:v]psnr", "-f", "null", "-"],
                       capture_output=True, text=True, check=True)  # fmt: skip
    m = re.search(r"average:([\d.]+|inf) min:([\d.]+|inf)", p.stderr)
    return {"average": float(m.group(1)), "min_frame": float(m.group(2))}


def make_inputs(out: Path, case: dict) -> dict[str, Path]:
    """first = the image-edit golden's input scaled to the canvas; last = the same, mirrored."""
    w, h = SHAPE["width"], SHAPE["height"]
    vf = {"first": f"scale={w}:{h}:flags=bicubic", "last": f"scale={w}:{h}:flags=bicubic,hflip"}
    files = {}
    for role, which in case["inputs"].items():
        f = out / f"input-{which}-{w}x{h}.png"
        if not f.exists():
            subprocess.run(["ffmpeg", "-v", "error", "-y", "-i", str(SOURCE_IMG), "-vf", vf[which], "-frames:v", "1",
                            str(f)], check=True)  # fmt: skip
        files[role] = f
    return files


def case_args(case: dict, files: dict[str, Path], mp4: Path, frames_dir: Path | None) -> list[str]:
    a = ["-d", str(MODELS / "MiniMax-H3"), "-p", case["prompt"], "-o", str(mp4)]
    for k in ("width", "height", "frames", "steps", "seed"):
        a += [f"--{k}", str(SHAPE[k])]
    a += EXACT
    for role, f in files.items():
        a += [FLAG[role], str(f)]
    return a + (["--frames-dir", str(frames_dir)] if frames_dir else [])


def golden(names: list[str], binary: Path, record: bool) -> None:
    for name in names:
        case = CASES[name]
        out = GOLDEN / f"minimax-h3-{name}"
        out.mkdir(parents=True, exist_ok=True)
        files = make_inputs(out, case)
        runs, keep = [], tempfile.TemporaryDirectory(prefix="h3-takes-")
        for i in range(2 if record else 1):
            with tempfile.TemporaryDirectory(prefix="h3-run-") as tmp:
                mp4, frames = Path(tmp) / "take.mp4", Path(tmp) / "frames"
                frames.mkdir()
                r = run_h3(binary, case_args(case, files, mp4, frames), {})
                if r["rc"] != 0:
                    raise SystemExit(f"{name} run {i + 1}: h3 exited {r['rc']}: {r['diag'][-3:]}")
                r["hashes"] = hashes(mp4, frames)
                if i == 0 and record:
                    shutil.copy(mp4, out / f"{name}-512x512-22f-s42.mp4")
                    r["ffprobe"] = probe(mp4)
                shutil.copy(mp4, Path(keep.name) / f"{i + 1}.mp4")
                runs.append(r)
                print(name, i + 1, r["wall_s"], "s", r["peak_footprint_gb"], "GB", r["hashes"], flush=True)
        if not record:
            continue
        psnr = (
            None
            if runs[0]["hashes"]["rgb24_sha256"] == runs[1]["hashes"]["rgb24_sha256"]
            else video_psnr(Path(keep.name) / "1.mp4", Path(keep.name) / "2.mp4")
        )
        keep.cleanup()
        keys = ("frames_ppm_sha256", "rgb24_sha256", "pcm_f32le_sha256", "mp4_sha256")
        doc = {
            "name": name, "prompt": case["prompt"], **SHAPE, "fps": 24, "speed": "exact", "flags": EXACT,
            "argv_equivalent": "h3 -d <models>/MiniMax-H3 -p PROMPT -o OUT.mp4 "
                + " ".join(
                f"--{k} {SHAPE[k]}" for k in ("width", "height", "frames", "steps", "seed")) + " " + " ".join(EXACT)
                + "".join(f" {FLAG[r]} {f.name}" for r, f in files.items()) + " --frames-dir DIR",
            "inputs": {r: {"file": f.name, "sha256": hashlib.sha256(f.read_bytes()).hexdigest(),
                           "made_from": f"{SOURCE_IMG.relative_to(REPO)} (ffmpeg scale bicubic"
                                        + (", hflip)" if "last" in f.name else ")")} for r, f in files.items()},
            "mp4": {"file": f"{name}-512x512-22f-s42.mp4", **runs[0].pop("ffprobe")},
            **{k: runs[0]["hashes"][k] for k in keys}, "rgb24_bytes": runs[0]["hashes"]["rgb24_bytes"],
            "pcm_f32le_bytes": runs[0]["hashes"]["pcm_f32le_bytes"],
            "frames_ppm_count": runs[0]["hashes"]["frames_ppm_count"],
            "bit_identical_runs": {k: runs[0]["hashes"][k] == runs[1]["hashes"][k] for k in keys},
            "run2_vs_run1_video_psnr_db": psnr,
            "runs": [{k: r[k] for k in ("wall_s", "phases", "peak_footprint_gb", "lifetime_max_footprint_gb",
                                        "peak_resident_gb")} for r in runs],
            "env": {}, "h3c": (VENDOR / "VERSION").read_text().split("commit: ")[1].split()[0],
            "versions": {"ffmpeg": subprocess.run(["ffmpeg", "-version"], check=False, capture_output=True, text=True)
                         .stdout.splitlines()[0], "machine": f"{platform.system()} {platform.release()} "
                         f"{platform.machine()}", "macos": platform.mac_ver()[0]},
        }  # fmt: skip
        (out / f"{name}.json").write_text(json.dumps(doc, indent=2) + "\n")


def measure(a: argparse.Namespace) -> None:
    env = dict(e.split("=", 1) for e in a.env)
    with tempfile.TemporaryDirectory(prefix="h3-measure-") as tmp:
        mp4 = Path(tmp) / "take.mp4"
        args = ["-d", str(MODELS / "MiniMax-H3"), "-p", a.prompt, "-o", str(mp4), "--width", str(a.width),
                "--height", str(a.height), "--frames", str(a.frames), "--steps", str(a.steps), "--seed", "42",
                *(a.extra or EXACT)]  # fmt: skip
        args += ["--first-frame", a.first] if a.first else []
        r = run_h3(a.bin, args, env)
        r["shape"] = {"width": a.width, "height": a.height, "frames": a.frames, "steps": a.steps,
                      "mode": "fl2va" if a.first else "t2v", "env": env, "flags": a.extra or EXACT,
                      "mp": round(a.width * a.height / 1e6, 4)}  # fmt: skip
        if r["rc"] == 0:
            r["mp4_bytes"] = mp4.stat().st_size
    line = json.dumps(r)
    print(line, flush=True)
    if a.json:
        with open(a.json, "a") as f:
            f.write(line + "\n")


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("golden")
    g.add_argument("cases", nargs="+", choices=list(CASES))
    g.add_argument("--bin", type=Path, help="another h3 binary: print one run's hashes, record nothing")
    m = sub.add_parser("measure")
    m.add_argument("--width", type=int, required=True)
    m.add_argument("--height", type=int, required=True)
    m.add_argument("--frames", type=int, required=True)
    m.add_argument("--steps", type=int, default=4)
    m.add_argument("--first", help="first-frame image (FL2VA)")
    m.add_argument("--env", action="append", default=[], help="KEY=VALUE for the child, e.g. H3_ZERO_COPY_WEIGHTS=0")
    m.add_argument("--extra", nargs=argparse.REMAINDER, help="flags instead of the exact ones (last on the line)")
    m.add_argument("--prompt", default=CASES["t2v"]["prompt"])
    m.add_argument("--bin", type=Path, default=VENDOR / "h3")
    m.add_argument("--json", help="append the result line to this file")
    a = ap.parse_args()
    if a.cmd == "golden":
        golden(a.cases, (a.bin or VENDOR / "h3").resolve(), record=a.bin is None)
    else:
        a.bin = a.bin.resolve()
        measure(a)


if __name__ == "__main__":
    sys.exit(main())
