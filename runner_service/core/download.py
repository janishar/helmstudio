"""A model download (contracts/README.md "Models and model jobs"): the plan from the Hub's file list, then each file
fetched with huggingface_hub into `<root>/<dir>/` as a plain file (local_dir: no link into a cache; the per-job
cache_dir is a temporary folder), checked against its LFS SHA-256 and recorded. Runs on a worker thread, never the
GPU thread; `Progress` is read by the job's event loop.

Resume: files already complete are kept (their huggingface_hub sidecar names the commit) and skipped; a cut
connection resumes the file in flight with a Range request (huggingface_hub's retries). Cancel is checked between
files and on every chunk. ponytail: huggingface_hub 2.4 deletes the partial temp file of the file in flight when a
job stops (cancel, restart), so that one file starts again; keep own .part files if single huge files matter."""

import errno
import fnmatch
import logging
import os
import shutil
import tempfile
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path

from core import models
from core import pack as packs

log = logging.getLogger("download")

MARGIN = 2**30  # free space kept beyond the plan
DISK_EVERY = 256 * 2**20  # bytes streamed between free-space checks


class JobError(Exception):
    """A model job failed: `cls` is its failure class, the message its one-line detail."""

    def __init__(self, cls: str, detail: str) -> None:
        super().__init__(detail[:500])
        self.cls = cls


class Cancelled(Exception):
    pass


@dataclass
class Item:
    repo: str
    commit: str
    path: str  # repo path = path inside `dest`
    size: int
    sha256: str | None  # the Hub's LFS hash; None for a small non-LFS file
    dest: Path


@dataclass
class Progress:
    total_bytes: int | None = None
    files_total: int | None = None
    done_bytes: int = 0  # of completed files
    files_done: int = 0
    current: int = 0  # bytes of the file in flight
    file: str | None = None
    fetched: int = 0  # bytes this job transferred
    size: int | None = None  # the declared size of the file in flight
    left: int = 0  # bytes still to write (the files not yet complete), for the free-space checks
    checked: int = 0  # `fetched` at the last free-space check
    started: float = field(default_factory=time.monotonic)
    cancel: threading.Event = field(default_factory=threading.Event)

    def check(self) -> None:
        if self.cancel.is_set():
            raise Cancelled

    def data(self) -> dict:
        """evt.model.progress data (without seq and delivery)."""
        done = self.done_bytes + self.current
        out = {"done_bytes": done, "total_bytes": self.total_bytes, "files_done": self.files_done,
               "files_total": self.files_total}  # fmt: skip
        if self.file:
            out["file"] = self.file[:400]
        rate = self.fetched / max(time.monotonic() - self.started, 1e-3)
        if self.total_bytes is not None and rate > 0:
            out["eta_s"] = round(max(self.total_bytes - done, 0) / rate, 1)
        return out


def disk_free(path: Path) -> int:
    while not path.exists() and path != path.parent:
        path = path.parent
    return shutil.disk_usage(path).free


def classify(e: Exception, repo: str, revision: str, token: str | None) -> JobError:
    """A huggingface_hub or OS error as a failure class with its one-line detail (never a URL or the token)."""
    from huggingface_hub import errors

    if isinstance(e, JobError):
        return e
    if isinstance(e, errors.GatedRepoError) or (
        isinstance(e, errors.HfHubHTTPError)
        and getattr(e.response, "status_code", 0) in (401, 403)
        and not isinstance(e, errors.RepositoryNotFoundError)
    ):
        why = "the token was refused" if token else \
            "put a Hub token in this host's secrets file (RUNNER_HF_TOKEN_FILE)"  # fmt: skip
        return JobError("gated", f"repo {repo} is gated: {why}")
    if isinstance(e, errors.RevisionNotFoundError):
        return JobError("not_found", f"repo {repo} has no revision {revision}")
    if isinstance(e, errors.RemoteEntryNotFoundError):
        return JobError("not_found", f"repo {repo} has a file missing at revision {revision}")
    if isinstance(e, errors.RepositoryNotFoundError):
        return JobError("not_found", f"repo {repo} was not found")
    if isinstance(e, OSError) and e.errno == errno.ENOSPC:
        return JobError("disk_full", "the disk filled up during the download")
    if isinstance(e, errors.HfHubHTTPError):
        code = getattr(e.response, "status_code", "?")
        return JobError("network", f"cannot reach the hub: HTTP {code}")
    if isinstance(e, (OSError, errors.LocalEntryNotFoundError)) or "httpx" in type(e).__module__:
        return JobError("network", f"cannot reach the hub: {type(e).__name__}")
    log.error("download failed", exc_info=e)
    return JobError("internal", f"the download failed ({type(e).__name__})")


def listing(api, repo: str, revision: str) -> tuple[str, dict[str, tuple[int, str | None]]]:
    """(the commit, repo path → (size, LFS SHA-256 or None)) of `repo` at `revision`."""
    from huggingface_hub.hf_api import RepoFile

    commit = api.model_info(repo, revision=revision).sha
    tree = {f.path: (f.lfs.size if f.lfs else f.size, f.lfs.sha256 if f.lfs else None)
            for f in api.list_repo_tree(repo, revision=commit, recursive=True)
            if isinstance(f, RepoFile)}  # fmt: skip
    return commit, tree


def matching(paths, patterns: list[str]) -> list[str]:
    return sorted(p for p in paths if any(fnmatch.fnmatchcase(p, g) for g in patterns))


def component_patterns(pack, component: str, variant: str | None = None) -> list[str]:
    """A component's files with their configs: every file under its globs' (the variant's) folders."""
    return [
        g.rsplit("/", 1)[0] + "/*" if "/" in g else g
        for g in models.globs(pack, component, variant)
    ]


def file_sha(root: Path, rel: str, check=lambda: None) -> str:
    """The SHA-256 of a file on disk: the sidecar's when the file is unchanged since, else kept or computed."""
    p = root / rel
    rec, fresh = models.recorded(root, rel, p.stat())
    return rec if fresh else models.hashes.compute(p, check)


def addon_plan(pack, c: str, api) -> list[Item]:
    """An add-on's files (API 1.36): its `source` into its folder."""
    src = pack.component(c)["addon"]["source"]
    commit, tree = listing(api, src["repo"], src["revision"])
    dest = models.addon_dest(pack, c)
    return [
        Item(src["repo"], commit, p, *tree[p], dest) for p in matching(tree, src["allow_patterns"])
    ]


def checked(items: list[Item]) -> list[Item]:
    """The plan's items, refused when a name is unsafe, under a symlinked folder or twice (case folded)."""
    seen = set()
    for it in items:
        if not models.is_safe(it.path):
            raise JobError(
                "internal", f"repo {it.repo} lists an unsafe file name: {it.path[:200]!r}"
            )
        if models.linked(
            it.dest, it.path
        ):  # a linked folder: the write would land in another folder
            raise JobError("internal", f"{it.dest.name}/{it.path[:200]} is under a symlinked folder: "
                                       "remove the link by hand")  # fmt: skip
        if not models.inside(packs.models_root(), it.dest) or not models.inside(
            it.dest, it.dest / it.path
        ):
            raise JobError(
                "internal", f"repo {it.repo} lists an unsafe file name: {it.path[:200]!r}"
            )
        if (it.dest, it.path.casefold()) in seen:  # APFS folds case: one file on disk
            raise JobError("internal", f"repo {it.repo} lists {it.path[:200]} twice")
        seen.add((it.dest, it.path.casefold()))
    return items


def plan(pack, vid: str, api, progress: Progress) -> tuple[list[Item], list[str], list[str]]:
    """(the files to have on disk, shared components reused, shared components fetched separately). The variant's
    `source`, then each of its `sources` (API 1.33: weights from a community repo beside the official configs), all
    into its folder (a source with a `dir`: into that folder, a component's own, as MiniMax's prompt enhancer)."""
    spec = pack.spec
    v = spec["variants"][vid]
    first = packs.share_owner(pack, vid)  # the lender of its shares
    src = v["source"]
    commit, tree = listing(api, src["repo"], src["revision"])
    own = models.folder(pack, vid)
    items = [
        Item(src["repo"], commit, p, *tree[p], own) for p in matching(tree, src["allow_patterns"])
    ]
    for more in v.get("sources", []):
        try:
            mcommit, mtree = listing(api, more["repo"], more["revision"])
        except Exception as e:  # noqa: BLE001 (named by its own repo)
            raise classify(e, more["repo"], more["revision"], api.token or None) from None
        dest = packs.models_root() / more["dir"] if "dir" in more else own
        items += [Item(more["repo"], mcommit, p, *mtree[p], dest)
                  for p in matching(mtree, more["allow_patterns"])]  # fmt: skip
    reused, separate, lacking = [], [], []
    owner = models.folder(pack, first)
    # API 1.78: the lender's complete copy in any connected folder is reused (read there), never fetched again
    lender = packs.found(spec["variants"][first]["dir"]) or owner
    have = {r.as_posix() for r in models.own_files(lender)} if first != vid else set()
    for c in [
        c for c in v.get("shares", []) if c in packs.needed(pack, vid)
    ]:  # an add-on downloads on its own
        theirs = {p: tree[p] for p in matching(tree, component_patterns(pack, c, first))}
        lfs = {p: sha for p, (_, sha) in theirs.items() if sha}
        if packs.owns_copy(pack, vid, c) or (lfs and set(lfs) <= have and any(
                file_sha(lender, p, progress.check) != sha for p, sha in lfs.items())):  # fmt: skip
            separate.append(c)  # the owner's copy differs: this variant's own, in its folder
            items += [Item(src["repo"], commit, p, *theirs[p], own) for p in sorted(theirs)]
        elif matching(have, models.globs(pack, c, first)):
            reused.append(c)
        else:
            lacking.append(c)
    # the owner lacks them: what its sources fetch of them, with its configs, into its folder
    if lacking:
        lv = spec["variants"][first]
        if "source" not in lv:
            raise JobError("not_downloadable", f"{spec['title']} {first} has no download source")
        for osrc in [lv["source"], *lv.get("sources", [])]:
            ocommit, otree = listing(api, osrc["repo"], osrc["revision"])
            odest = packs.models_root() / osrc["dir"] if "dir" in osrc else owner
            items += [Item(osrc["repo"], ocommit, p, *otree[p], odest)
                      for p in matching(otree, osrc["allow_patterns"])
                      if models.component_of(pack, p, first) in (None, *lacking)]  # fmt: skip
    return checked(items), reused, separate


def complete(it: Item, progress: Progress) -> bool:
    """Whether the file is already on disk at its size and the Hub's hash (hashed now when not known)."""
    p = it.dest / it.path
    if not p.is_file() or p.stat().st_size != it.size:
        return False
    if it.sha256 is None:
        return (it.dest / models.SIDECARS / f"{it.path}.metadata").is_file()
    return file_sha(it.dest, it.path, progress.check) == it.sha256


def bar_class(progress: Progress):
    """A tqdm stand-in for huggingface_hub: counts the file in flight's bytes and is a cancel point."""

    class Bar:
        # Real `n`/`total` (huggingface_hub's Xet reporter does arithmetic on them), and a class-level
        # update_transfer: the reporter then uses one bar, counting written bytes in update() and sending network
        # bytes here (ignored), instead of a second bar whose update() would count every byte twice.
        def __init__(self, *a, initial: int = 0, total: int | None = None, **kw) -> None:
            self.n, self.total = initial, total
            progress.current = initial

        def update_transfer(self, n: int = 1) -> None:
            progress.check()

        def __enter__(self):
            return self

        def __exit__(self, *exc) -> bool:
            return False

        def update(self, n: int = 1) -> None:
            progress.check()
            self.n += n
            progress.current += n
            progress.fetched += n
            if progress.size is not None and progress.current > progress.size:
                raise JobError(
                    "network", f"the hub sent more than {progress.size} bytes for {progress.file}"
                )
            if progress.fetched - progress.checked >= DISK_EVERY:
                progress.checked = progress.fetched
                if (
                    free := disk_free(packs.models_root())
                ) < progress.left - progress.current + MARGIN // 2:
                    raise JobError(
                        "disk_full", f"the disk is filling up ({free / 1e9:.1f} GB free)"
                    )

        def __getattr__(self, name):  # set_description, refresh, close, ...: nothing to show
            return lambda *a, **kw: None

    return Bar


def download(pack, vid: str | None, *, endpoint: str, token: str | None, cache: Path, host: str,
             progress: Progress, addon: str | None = None) -> dict:  # fmt: skip
    """Fetch the variant (and the shared components it lacks), or with `addon` that add-on alone; returns
    evt.model.status `result`. JobError, Cancelled."""
    from huggingface_hub import HfApi, hf_hub_download

    import config

    spec = pack.spec
    if config.ROOTS and not packs.models_root().is_dir():  # API 1.78: a folder is never created
        raise JobError(
            "not_found", f"{models.folder_name(packs.models_root())} is not connected on {host}"
        )
    if addon:
        src = pack.component(addon)["addon"].get("source")
        what = f"{spec['title']} {pack.component(addon)['addon']['title']}"
    else:
        src, what = spec["variants"][vid].get("source"), f"{spec['title']} {vid}"
    if src is None:
        raise JobError("not_downloadable", f"{what} has no download source")
    api = HfApi(endpoint=endpoint, token=token or False)
    try:
        if addon:
            items, reused, separate = checked(addon_plan(pack, addon, api)), [], []
        else:
            items, reused, separate = plan(pack, vid, api, progress)
    except JobError, Cancelled:
        raise
    except Exception as e:  # noqa: BLE001 (every hub error has a class)
        raise classify(e, src["repo"], src["revision"], token) from None
    progress.total_bytes = sum(it.size for it in items)
    progress.files_total = len(items)
    todo = [it for it in items if not complete(it, progress)]
    need, free = sum(it.size for it in todo), disk_free(packs.models_root())
    if free < need + MARGIN:
        raise JobError("disk_full", f"host {host} has {free / 1e9:.1f} GB free; this download needs "
                                    f"{(need + MARGIN) / 1e9:.1f} GB")  # fmt: skip
    for d in {it.dest for it in todo}:
        (d / models.MARKER).parent.mkdir(parents=True, exist_ok=True)
        (d / models.MARKER).write_text(
            f'{{"repo": "{src["repo"]}", "variant": "{addon or vid}"}}\n'
        )
    written, progress.left = 0, need
    cache.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        prefix="hf-", dir=cache
    ) as tmp:  # never the Hugging Face cache
        for it in items:
            progress.check()
            progress.file, progress.current, progress.size = it.path, 0, it.size
            if it in todo:
                (it.dest / it.path).unlink(
                    missing_ok=True
                )  # an older or broken copy: fetched again
                (it.dest / models.SIDECARS / f"{it.path}.metadata").unlink(missing_ok=True)
                try:
                    hf_hub_download(it.repo, it.path, revision=it.commit, local_dir=it.dest,
                                    cache_dir=tmp, endpoint=endpoint, token=token or False,
                                    tqdm_class=bar_class(progress))  # fmt: skip
                except Cancelled:
                    raise
                except Exception as e:  # noqa: BLE001
                    raise classify(e, it.repo, src["revision"], token) from None
                verify(it, progress)
                written, progress.left = written + 1, progress.left - it.size
            progress.done_bytes += it.size
            progress.files_done += 1
            progress.current = 0
    progress.file = None
    for d in {it.dest for it in items}:
        (d / models.MARKER).unlink(missing_ok=True)
    out: dict = {"bytes": progress.fetched, "files": written}
    if reused:
        out["reused"] = reused
    if separate:
        out["separate"] = separate
    return out


def verify(it: Item, progress: Progress) -> None:
    """Check a landed file against the Hub's LFS hash and keep its hash; a different one deletes it."""
    p = it.dest / it.path
    if p.stat().st_size != it.size:
        raise JobError("network", f"cannot reach the hub: {it.path[:200]} arrived incomplete")
    sha = models.sha256_file(p, progress.check)
    if it.sha256 and sha != it.sha256:
        p.unlink()
        (it.dest / models.SIDECARS / f"{it.path}.metadata").unlink(missing_ok=True)
        raise JobError("hash_mismatch", f"{it.path[:300]}: SHA-256 differs from the Hub's")
    models.hashes.put(p, sha)


def read_token(path: Path) -> str | None:
    """The Hub token from the host's secrets file, or None (absent or empty). Never logged."""
    try:
        return path.read_text().strip() or None
    except OSError:
        return None


def environment(cache: Path) -> None:
    """Point huggingface_hub's own caches at the runner's (call before it is imported): the Xet chunk cache is
    a folder the runner empties after every job; telemetry off."""
    os.environ["HF_XET_CACHE"] = str(cache / "hf-xet")
    os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
