"""Model folders (API 1.78, contracts/README.md "Model folders"): an ordered list of roots; a model folder loads
from the first connected root with a complete copy. A root that is not a directory now is skipped, never created."""

import hashlib
import threading
from contextlib import contextmanager
from pathlib import Path

import config

MARKER = Path(".cache/helmstudio/download.json")  # written when a download starts, removed when it succeeds

_scope = threading.local()  # rooted(): (root, strict) of this thread's job or scan


def shown(path: Path) -> str:
    """`path` with the home directory written `~`."""
    home = str(Path.home())
    return "~" + str(path)[len(home) :] if str(path).startswith(home + "/") else str(path)


def folder_id(text: str) -> str:
    """A folder's id: the first 12 hex of the SHA-256 of its path as stored."""
    return hashlib.sha256(text.encode()).hexdigest()[:12]


def roots() -> list[tuple[str, Path]]:
    """(path as stored, path) of each model folder in priority order: host_settings `folders`, else the one
    HELMSTUDIO_MODELS."""
    return [(x, Path(x).expanduser()) for x in config.ROOTS] or [(shown(config.MODELS), config.MODELS)]


def connected() -> list[Path]:
    return [r for _, r in roots() if r.is_dir()]


def downloads_root() -> Path:
    """The downloads folder, else the next connected one after it (the list wraps), else the downloads folder."""
    rs = [r for _, r in roots()]
    i = config.DOWNLOADS if 0 <= config.DOWNLOADS < len(rs) else 0
    return next((r for r in rs[i:] + rs[:i] if r.is_dir()), rs[i])


def models_root() -> Path:
    """Where writes go: the root of a strict rooted() scope, else the downloads folder."""
    s = getattr(_scope, "root", None)
    return s[0] if s and s[1] else downloads_root()


def complete(path: Path) -> bool:
    """A model folder with files and no unfinished download (core.models.MARKER)."""
    try:
        return not (path / MARKER).exists() and any(not x.name.startswith(".") for x in path.iterdir())
    except OSError:
        return False


def found(rel: str) -> Path | None:
    """The first connected root's complete copy of `rel`, whatever the scope; None when none has one. A real copy
    wins over a link to one (an old link into another folder of the list: that folder's copy is the one used)."""
    live = [r / rel for r in connected() if complete(r / rel)]
    return next((p for p in live if not p.is_symlink()), live[0] if live else None)


def place(rel: str) -> Path:
    """Where the model folder `rel` is read from: the first connected root with a complete copy, else the first
    with some of it, else the downloads folder. In a strict rooted() scope: that root; a loose one prefers its root
    when the folder is there. ponytail: complete = files and no marker, not every glob; a partial copy first in the
    list that lacks a weight still wins over a full one later."""
    if (s := getattr(_scope, "root", None)) and (s[1] or (s[0] / rel).exists()):
        return s[0] / rel
    live = connected()
    return found(rel) or next((r / rel for r in live if (r / rel).exists()), None) or models_root() / rel


def root_of(path: Path) -> tuple[str, Path] | None:
    """The (stored, path) root holding `path`, the first that does."""
    return next(((t, r) for t, r in roots() if path.is_relative_to(r)), None)


@contextmanager
def rooted(root: Path, strict: bool = True):
    """Within it this thread reads and writes model folders in `root` only (strict: a job's download or delete), or
    prefers `root` (loose: a scan of one copy)."""
    old, _scope.root = getattr(_scope, "root", None), (root, strict)
    try:
        yield
    finally:
        _scope.root = old
