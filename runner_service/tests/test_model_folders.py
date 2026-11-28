"""Model folders (API 1.78, contracts/README.md "Model folders"): two scratch roots A and B and a third path that does
not exist (an unplugged drive). Priority, fallback, unplugged, shared parts across roots, download into a folder,
delete one copy, the default single root. Nothing here reaches ~/GenAI/models or /Volumes."""

import shutil
import types

import pytest
from fake_hub import FakeHub, Repo

import config
from core import download, models
from core import pack as packs
from core.download import JobError, Progress
from messaging.models import ModelJobs

BLOB = bytes(range(256)) * 40


def source(repo: str, patterns: list[str]) -> dict:
    return {"repo": repo, "revision": "main", "allow_patterns": patterns, "licence": "tiny",
            "must_accept": False, "gated": False}  # fmt: skip


@pytest.fixture
def roots(tmp_path, monkeypatch):
    a, b, gone = tmp_path / "A", tmp_path / "B", tmp_path / "Volumes/T9/models"
    a.mkdir(), b.mkdir()
    monkeypatch.setattr(config, "MODELS", a)
    monkeypatch.setattr(config, "ROOTS", [str(a), str(b), str(gone)])
    monkeypatch.setattr(config, "DOWNLOADS", 0)
    monkeypatch.setattr(config, "CACHE", tmp_path / "cache")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    monkeypatch.setattr(models, "seen", {})
    return a, b, gone


@pytest.fixture
def pack(tmp_path):
    (tmp_path / "pack/licences").mkdir(parents=True)
    (tmp_path / "pack/licences/tiny.md").write_text("# Tiny\n")
    spec = {"id": "tiny-1", "family": "tiny", "title": "Tiny", "media": ["image"],
            "runtimes": {"fake": {"env": "fake"}}, "licences": {"tiny": {"title": "Tiny"}},
            "variants": {
                "base": {"dir": "Tiny", "source": source("t/tiny", ["transformer/*", "vae/*"])},
                "turbo": {"dir": "Tiny-Turbo", "shares": ["vae"], "source": source("t/tiny-turbo", ["transformer/*"])}},
            "components": {"transformer": {"files": "transformer/*.safetensors"},
                           "vae": {"files": "vae/*.safetensors"}}}  # fmt: skip
    return packs.Pack("tiny", tmp_path / "pack", spec, "fake", types.SimpleNamespace())


def put(root, d="Tiny", parts=("transformer", "vae")):
    for c in parts:
        (root / d / c).mkdir(parents=True, exist_ok=True)
        (root / d / c / "w.safetensors").write_bytes(BLOB)


def variant(pack, vid="base") -> dict:
    models.refresh_seen()
    return next(v for v in models.entry(pack)["variants"] if v["id"] == vid)


def fid(root) -> str:
    return packs.folder_id(str(root))


def test_priority_then_fallback(roots, pack):
    a, b, _ = roots
    put(a), put(b)
    v = variant(pack)
    assert packs.place("Tiny") == a / "Tiny" and v["folder"] == fid(a) and v["presence"] == "present"
    assert [(c["folder"], c["state"]) for c in v["copies"]] == [
        (fid(a), "present"),
        (fid(b), "present"),
    ]
    assert v["copies"][0]["freed_bytes"] == 2 * len(BLOB)
    shutil.rmtree(a / "Tiny")
    v = variant(pack)
    assert packs.place("Tiny") == b / "Tiny" and v["folder"] == fid(b)
    shutil.rmtree(a)  # the first folder itself gone: skipped, never created
    assert variant(pack)["folder"] == fid(b) and not a.exists()


def test_a_model_only_on_an_unplugged_drive_is_unplugged_and_the_drive_is_never_created(roots, pack):
    _, _, gone = roots
    put(gone)
    variant(pack)  # seen while connected
    shutil.rmtree(gone.parent.parent)  # unplugged
    v = variant(pack)
    assert v["presence"] == "unplugged" and "folder" not in v
    assert v["copies"] == [{"folder": fid(gone), "state": "unplugged"}]
    assert variant(pack, "turbo")["presence"] == "missing"
    disks = models.disks()
    assert [(d["connected"], d["downloads"]) for d in disks] == [
        (True, True),
        (True, False),
        (False, False),
    ]
    assert not gone.exists() and models.check_folders([str(gone)])["folders"][0]["connected"] is False


def test_a_variant_in_b_loads_its_shared_parts_from_a(roots, pack):
    a, b, _ = roots
    put(a)  # base (the lender of vae) in A
    put(b, "Tiny-Turbo", ("transformer",))  # turbo's own files in B
    v = variant(pack, "turbo")
    assert v["presence"] == "present" and v["folder"] == fid(b)
    ref = packs.ModelRef(pack, "turbo", "bf16")
    assert ref.files("vae")[0].is_relative_to(a) and ref.files("transformer")[0].is_relative_to(b)


def test_download_into_folder_b_writes_only_there_and_reuses_a_lender_in_a(roots, pack):
    a, b, gone = roots
    put(a)
    repo = {"transformer/w.safetensors": BLOB + b"t", "vae/w.safetensors": BLOB}
    with FakeHub({"t/tiny-turbo": Repo(repo), "t/tiny": Repo(repo)}) as hub:
        out = ModelJobs.within(b, download.download, pack, "turbo", endpoint=hub.url, token=None,
                               cache=config.CACHE, host="t", progress=Progress())  # fmt: skip
        assert out["reused"] == ["vae"]
        assert (b / "Tiny-Turbo/transformer/w.safetensors").is_file() and not (a / "Tiny-Turbo").exists()
        with pytest.raises(JobError) as e:  # an unplugged folder: refused, not created
            ModelJobs.within(gone, download.download, pack, "base", endpoint=hub.url, token=None,
                             cache=config.CACHE, host="t", progress=Progress())  # fmt: skip
    assert e.value.cls == "not_found" and not gone.exists()
    with pytest.raises(JobError):
        ModelJobs.root(fid(gone))


def test_delete_one_copy_then_the_other_is_used(roots, pack):
    a, b, _ = roots
    put(a), put(b)
    files, freed = ModelJobs.within(ModelJobs.root(fid(a)), models.delete_variant, pack, "base")
    assert (files, freed) == (2, 2 * len(BLOB)) and not (a / "Tiny").exists() and (b / "Tiny").is_dir()
    assert variant(pack)["folder"] == fid(b)


def test_the_default_is_the_one_helmstudio_models_root(roots, pack, monkeypatch):
    a, _, _ = roots
    monkeypatch.setattr(config, "ROOTS", [])
    put(a)
    assert packs.roots() == [(packs.shown(a), a)] and packs.models_root() == a
    v = variant(pack)
    assert "copies" not in v and "folder" not in v and v["presence"] == "present"
    assert [d["id"] for d in models.disks()] == [packs.folder_id(packs.shown(a))]


async def test_settings_set_the_list_and_rescan_once_it_changes(roots, monkeypatch):
    import json

    from conftest import CONTRACTS, FakeJS, consumer

    from messaging.settings import Settings

    a, b, _ = roots
    monkeypatch.setattr("messaging.settings.ram_gb", lambda: 64.0)
    c = consumer(FakeJS())
    s, scans = Settings(c, c.host), []

    async def rescan():
        scans.append(list(config.ROOTS))

    s.rescan = rescan
    v = json.loads((CONTRACTS / "fixtures/registry/settings.valid.json").read_text())
    await s.offer(v | {"version": 10, "folders": [str(b), str(a)], "downloads": 1})
    assert config.ROOTS == [str(b), str(a)] and packs.downloads_root() == a and scans == [[str(b), str(a)]]
    await s.offer(v | {"version": 11, "folders": [str(b), str(a)], "downloads": 1, "after_run": "unload"})
    assert len(scans) == 1  # the list did not change
    await s.offer(None)  # the key deleted: HELMSTUDIO_MODELS alone
    assert config.ROOTS == [] and scans[-1] == []


def test_a_link_into_another_folder_is_not_a_copy_and_keeps_its_target(roots, pack):
    a, b, _ = roots
    put(b)
    (a / "Tiny").symlink_to(b / "Tiny")  # an old link in the first folder to the drive's copy
    v = variant(pack)
    assert v["folder"] == fid(b) and [c["folder"] for c in v["copies"]] == [fid(b)]
    assert packs.place("Tiny") == b / "Tiny"
    (a / "Other").mkdir()
    (a / "Other/vae").symlink_to(b / "Tiny/vae")  # another model reads the drive copy's vae
    (a / "Tiny").unlink()
    ModelJobs.within(ModelJobs.root(fid(b)), models.delete_variant, pack, "base")
    assert (b / "Tiny/vae/w.safetensors").is_file() and not (b / "Tiny/transformer").exists()
