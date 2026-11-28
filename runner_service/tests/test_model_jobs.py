"""Model jobs against the fake hub (tests/fake_hub.py), under a scratch models root: download (full, resumed after a
cut connection, resumed after a stop, cancelled, gated, disk full, hash mismatch, unsafe names, shared components),
presence, delete rules, LoRA delete and the worker's events. Nothing here reaches the internet or ~/GenAI/models."""

import asyncio
import hashlib
import json
import types

import pytest
from conftest import CONTRACTS, FakeJS, FakeKV, FakeMsg
from fake_hub import FakeHub, Repo

import config
from core import download, models
from core import pack as packs
from core.download import Cancelled, JobError, Progress
from core.residency import Residency
from messaging import schemas
from messaging.models import ModelJobs

BLOB = bytes(range(256)) * 600  # 150 KB: a few chunks
LICENCE = "# Tiny Licence\n\nUse it nicely.\n"


def files(salt: bytes = b"") -> dict[str, bytes]:
    return {"model_index.json": b'{"name": "tiny"}', "transformer/a.safetensors": BLOB + salt,
            "transformer/b.safetensors": BLOB[::-1] + salt, "vae/v.safetensors": BLOB[:999] + salt,
            "vae/config.json": b"{}", "README.md": b"not in allow_patterns"}  # fmt: skip


def turbo_files() -> dict[str, bytes]:
    """Turbo's repo: its own transformer, the same vae as base."""
    return files() | {k: v + b"t" for k, v in files().items() if k.startswith("transformer/")}


def source(repo: str, patterns: list[str], **kw) -> dict:
    return {"repo": repo, "revision": "main", "allow_patterns": patterns, "licence": "tiny",
            "must_accept": False, "gated": False} | kw  # fmt: skip


@pytest.fixture
def pack(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path / "models")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    (tmp_path / "models").mkdir()
    (tmp_path / "pack/licences").mkdir(parents=True)
    (tmp_path / "pack/licences/tiny.md").write_text(LICENCE)
    spec = {"id": "tiny-1", "family": "tiny", "title": "Tiny", "media": ["image"],
            "runtimes": {"fake": {"env": "fake"}}, "licences": {"tiny": {"title": "Tiny Licence"}},
            "variants": {
                "base": {"dir": "Tiny", "title": "Base",
                         "source": source("t/tiny", ["model_index.json", "transformer/*", "vae/*"])},
                "turbo": {"dir": "Tiny-Turbo", "shares": ["vae"],
                          "source": source("t/tiny-turbo", ["model_index.json", "transformer/*"])}},
            "components": {"transformer": {"files": "transformer/*.safetensors"},
                           "vae": {"files": "vae/*.safetensors"}}}  # fmt: skip
    return packs.Pack("tiny", tmp_path / "pack", spec, "fake", types.SimpleNamespace())


def run(pack, vid="base", hub=None, token=None, progress=None, tmp=None):
    return download.download(pack, vid, endpoint=hub.url, token=token, cache=config.MODELS.parent / "cache",
                             host="t", progress=progress or Progress())  # fmt: skip


def presence(pack) -> dict[str, str]:
    return {v["id"]: v["presence"] for v in models.entry(pack)["variants"]}


def hub_of(**repos) -> FakeHub:
    return FakeHub({k.replace("_", "/", 1).replace("_", "-"): v for k, v in repos.items()})


def test_download_writes_plain_checked_files_and_the_variant_is_present(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        result = run(pack, hub=hub)
    root = config.MODELS / "Tiny"
    assert result == {
        "bytes": sum(len(v) for k, v in files().items() if k != "README.md"),
        "files": 5,
    }
    assert not (root / "README.md").exists()  # not in allow_patterns
    assert all(not p.is_symlink() for p in root.rglob("*"))
    assert not (root / models.MARKER).exists() and not list((config.MODELS.parent / "cache").glob("hf-*"))
    assert presence(pack) == {"base": "present", "turbo": "missing"}
    entry = models.entry(pack)
    base = entry["variants"][0]
    shas = {f["path"]: f.get("sha256") for f in base["files"]}
    assert shas["transformer/a.safetensors"] == hashlib.sha256(BLOB).hexdigest()  # from the sidecar
    assert [c["from_variant"] for c in entry["variants"][1]["components"]] == ["turbo", "base"]
    value = {"schema_version": 1, "host": "t", "env": "fake", "published_at": "2026-11-16T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []


def test_a_cut_connection_resumes_with_a_range_request(pack, monkeypatch):
    from huggingface_hub import constants

    monkeypatch.setattr(constants, "DOWNLOAD_CHUNK_SIZE", 1 << 14)  # 10 MB: resumes count whole chunks
    with hub_of(t_tiny=Repo(files())) as hub:
        hub.cut = {"transformer/a.safetensors"}
        run(pack, hub=hub)
    gets = [r for r in hub.requests if r[0] == "GET" and r[1].endswith("transformer/a.safetensors")]
    assert gets[0][2] is None and gets[1][2] == f"bytes={len(BLOB) // 2 // (1 << 14) * (1 << 14)}-"
    assert (config.MODELS / "Tiny/transformer/a.safetensors").read_bytes() == BLOB


def test_a_stopped_download_resumes_from_the_files_on_disk(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        p = Progress()
        hub.cut = {"vae/v.safetensors"}  # the job stops during the fourth file
        orig = download.verify

        def stop_after_three(it, progress):
            orig(it, progress)
            if progress.files_done == 2:
                p.cancel.set()

        download.verify = stop_after_three
        try:
            with pytest.raises(Cancelled):
                run(pack, hub=hub, progress=p)
        finally:
            download.verify = orig
        assert presence(pack)["base"] == "partial"  # some files, the marker: unfinished
        hub.requests.clear()
        result = run(pack, hub=hub)
    fetched = {r[1].rsplit("/resolve/main/", 1)[-1] for r in hub.requests if r[0] == "GET" and "/resolve/" in r[1]}
    assert "model_index.json" not in fetched and "transformer/a.safetensors" not in fetched
    assert result["files"] == 2 and presence(pack)["base"] == "present"


def test_cancel_stops_mid_file_and_keeps_finished_files(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        hub.delay = 0.05
        p = Progress()

        def cancel_soon():
            import time

            while p.files_done < 2:
                time.sleep(0.01)
            p.cancel.set()

        import threading

        threading.Thread(target=cancel_soon).start()
        with pytest.raises(Cancelled):
            run(pack, hub=hub, progress=p)
    assert (config.MODELS / "Tiny/model_index.json").is_file()
    assert presence(pack)["base"] == "partial"


def test_gated_repo_needs_the_hosts_token(pack):
    pack.spec["variants"]["base"]["source"]["gated"] = True
    with hub_of(t_tiny=Repo(files(), gated_token="hf_test")) as hub:
        with pytest.raises(JobError, match="is gated: put a Hub token") as e:
            run(pack, hub=hub)
        assert e.value.cls == "gated"
        with pytest.raises(JobError, match="the token was refused"):
            run(pack, hub=hub, token="hf_wrong")
        run(pack, hub=hub, token="hf_test")
        assert all(r[3] for r in hub.requests[-6:])  # the token went with every call
    assert presence(pack)["base"] == "present"


def test_disk_full_fails_before_fetching(pack, monkeypatch):
    monkeypatch.setattr(download, "disk_free", lambda p: 10**9)
    with (
        hub_of(t_tiny=Repo(files())) as hub,
        pytest.raises(JobError, match=r"host t has 1.0 GB free; this download needs 1.1 GB") as e,
    ):
        run(pack, hub=hub)
    assert e.value.cls == "disk_full" and not (config.MODELS / "Tiny").exists()


def test_a_file_whose_bytes_differ_from_the_hubs_hash_is_deleted(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        hub.corrupt = {"transformer/b.safetensors"}
        with pytest.raises(JobError, match=r"transformer/b.safetensors: SHA-256 differs") as e:
            run(pack, hub=hub)
    assert e.value.cls == "hash_mismatch"
    assert not (config.MODELS / "Tiny/transformer/b.safetensors").exists()


@pytest.mark.parametrize("name", ["../evil.safetensors", "transformer/../../evil.safetensors",
                                  ".cache/huggingface/download/x.metadata", "/etc/evil",
                                  ".Cache/huggingface/download/vae/v.safetensors.metadata",
                                  ".CACHE/helmstudio/download.json"])  # fmt: skip
def test_unsafe_names_from_the_hub_write_nothing(pack, name):
    pack.spec["variants"]["base"]["source"]["allow_patterns"] = ["*"]
    hub = hub_of(t_tiny=Repo(files() | {name: b"x"}))
    with hub, pytest.raises(JobError, match="lists an unsafe file name") as e:
        run(pack, hub=hub)
    assert e.value.cls == "internal"
    assert not (config.MODELS / "Tiny").exists() and not (config.MODELS.parent / "evil.safetensors").exists()


def test_names_equal_but_for_case_are_refused(pack):
    pack.spec["variants"]["base"]["source"]["allow_patterns"] = ["*"]
    hub = hub_of(t_tiny=Repo(files() | {"VAE/V.safetensors": b"x"}))
    with hub, pytest.raises(JobError, match="lists (vae/v|VAE/V).safetensors twice"):
        run(pack, hub=hub)


def test_a_symlinked_folder_in_the_variant_is_never_written_through(pack):
    turbo = files(b"t")
    pack.spec["variants"]["turbo"]["source"]["allow_patterns"] = ["transformer/*"]
    with hub_of(t_tiny=Repo(files()), t_tiny_turbo=Repo(turbo)) as hub:
        run(pack, "base", hub=hub)
        (config.MODELS / "Tiny-Turbo").mkdir()
        (config.MODELS / "Tiny-Turbo/vae").symlink_to(config.MODELS / "Tiny/vae")  # a hand-made share
        assert not packs.owns_copy(pack, "turbo", "vae")  # files under a linked folder are not its own
        # the owner's hashes differ from turbo's repo: the vae is planned into turbo's own (linked) folder
        with pytest.raises(JobError, match="under a symlinked folder") as e:
            run(pack, "turbo", hub=hub)
    assert e.value.cls == "internal"
    assert (config.MODELS / "Tiny/vae/v.safetensors").read_bytes() == BLOB[:999]  # base's file untouched


def test_a_stream_longer_than_declared_or_a_filling_disk_fails(pack, monkeypatch):
    progress = Progress()
    progress.size, progress.file = 10, "a.bin"
    bar = download.bar_class(progress)()
    with pytest.raises(JobError, match="sent more than 10 bytes") as e:
        bar.update(11)
    assert e.value.cls == "network"
    progress = Progress()
    progress.left = 10 * 2**30
    monkeypatch.setattr(download, "disk_free", lambda p: 2 * 2**30)
    with pytest.raises(JobError, match="the disk is filling up") as e:
        download.bar_class(progress)().update(download.DISK_EVERY)
    assert e.value.cls == "disk_full"


def test_a_xet_download_counts_each_byte_once():
    """huggingface_hub's Xet reporter (real Hub files are Xet-backed; the fake hub's are not) with our bar: one bar,
    written bytes counted once, network bytes ignored, its arithmetic on n/total works (M6: TypeError before)."""
    from huggingface_hub.utils._xet_progress_reporting import XetDownloadProgressReporter

    progress = Progress()
    progress.size = 100
    r = XetDownloadProgressReporter(reconstruction_desc="x", total=100, log_level=0,
                                    tqdm_class=download.bar_class(progress))  # fmt: skip
    report = types.SimpleNamespace(total_bytes=100, total_bytes_completed=60,
                                   total_transfer_bytes_completed=90, total_bytes_completion_rate=None,
                                   total_transfer_bytes_completion_rate=None)  # fmt: skip
    r.update_progress(report)
    report.total_bytes_completed = report.total_transfer_bytes_completed = 100
    r.update_progress(report)
    r.close()
    assert progress.current == progress.fetched == 100


def test_a_hash_failure_never_ends_the_hashes_thread(tmp_path, monkeypatch):
    h = models.Hashes()
    h.queue = {str(tmp_path / "a"): None}

    def broken(path, check=None):
        raise ValueError("boom")

    monkeypatch.setattr(h, "compute", broken)
    h.drain()  # returns: the path is dropped, the loop goes on
    assert not h.queue
    h.drain()  # an empty queue (another thread took the last path) returns too


def test_the_models_value_drops_file_manifests_past_its_byte_budget(pack, monkeypatch):
    from core import registry

    (config.MODELS / "Tiny/transformer").mkdir(parents=True)
    (config.MODELS / "Tiny/transformer/a.safetensors").write_bytes(b"x" * 10)
    variant = registry.models_value([pack], {}, {})["models"][0]["variants"][0]
    assert variant["files"] and variant["bytes"] == 10
    monkeypatch.setattr(registry, "MODELS_BUDGET", 100)
    variant = registry.models_value([pack], {}, {})["models"][0]["variants"][0]
    assert "files" not in variant and variant["bytes"] == 10 and variant["presence"] == "partial"


def test_missing_repo_and_revision_are_not_found(pack):
    with hub_of(t_other=Repo(files())) as hub:
        with pytest.raises(JobError, match="repo t/tiny was not found"):
            run(pack, hub=hub)
        pack.spec["variants"]["base"]["source"]["repo"] = "t/other"
        pack.spec["variants"]["base"]["source"]["revision"] = "v9"
        with pytest.raises(JobError, match="repo t/other has no revision v9") as e:
            run(pack, hub=hub)
    assert e.value.cls == "not_found"


def test_turbo_fetches_the_shared_vae_into_the_owner_then_reuses_it(pack):
    with hub_of(t_tiny=Repo(files()), t_tiny_turbo=Repo(turbo_files())) as hub:
        result = run(pack, "turbo", hub=hub)
        assert (config.MODELS / "Tiny/vae/v.safetensors").read_bytes() == BLOB[:999]  # base's source
        assert presence(pack) == {"base": "partial", "turbo": "present"}
        run(pack, "base", hub=hub)
        assert run(pack, "turbo", hub=hub)["reused"] == ["vae"]
    assert "reused" not in result and "separate" not in result


def test_a_shared_component_with_other_hashes_is_kept_separate(pack):
    turbo_files = files(b"t")
    pack.spec["variants"]["turbo"]["source"]["allow_patterns"] = ["transformer/*"]
    with hub_of(t_tiny=Repo(files()), t_tiny_turbo=Repo(turbo_files)) as hub:
        run(pack, "base", hub=hub)
        result = run(pack, "turbo", hub=hub)
    assert result["separate"] == ["vae"]
    assert (config.MODELS / "Tiny-Turbo/vae/v.safetensors").read_bytes() == turbo_files["vae/v.safetensors"]
    assert packs.ModelRef(pack, "turbo", "bf16").owner("vae") == "turbo"
    turbo = models.entry(pack)["variants"][1]
    assert {c["component"]: (c["from_variant"], c.get("separate")) for c in turbo["components"]} == {
        "transformer": ("turbo", None), "vae": ("turbo", True)}  # fmt: skip


def test_a_changed_file_is_rehashed_and_shows_mismatch(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        run(pack, hub=hub)
    f = config.MODELS / "Tiny/transformer/a.safetensors"
    import os
    import time

    f.write_bytes(b"other" + BLOB[5:])
    os.utime(f, (time.time() + 5, time.time() + 5))  # newer than its sidecar
    assert presence(pack)["base"] == "present"  # not hashed yet: queued, never at scan time
    models.hashes.drain()
    variant = models.entry(pack)["variants"][0]
    assert variant["presence"] == "mismatch" and variant["mismatch"] == ["transformer/a.safetensors"]


def test_delete_keeps_a_shared_component_another_variant_loads(pack):
    with hub_of(t_tiny=Repo(files()), t_tiny_turbo=Repo(turbo_files())) as hub:
        run(pack, "base", hub=hub)
        run(pack, "turbo", hub=hub)
    outside = config.MODELS / "elsewhere.bin"
    outside.write_bytes(b"keep me")
    (config.MODELS / "Tiny/transformer/link").symlink_to(outside)
    kept, only = models.kept_components(pack, {"base"})
    assert kept == ["vae"] and only == ["transformer/"]
    n, freed = models.delete_variant(pack, "base", only)
    assert (n, freed) == (2, 2 * len(BLOB))  # two weights; the link is removed, its target is not
    assert outside.read_bytes() == b"keep me"
    assert (config.MODELS / "Tiny/vae/config.json").is_file()  # the shared component's configs stay
    assert presence(pack) == {"base": "partial", "turbo": "present"}
    assert models.kept_components(pack, {"base", "turbo"}) == ([], None)
    models.delete_variant(pack, "turbo")
    models.delete_variant(pack, "base")
    assert not (config.MODELS / "Tiny").exists() and not (config.MODELS / "Tiny-Turbo").exists()
    assert presence(pack) == {"base": "missing", "turbo": "missing"}


def test_delete_keeps_files_another_model_links_to(pack):
    with hub_of(t_tiny=Repo(files())) as hub:
        run(pack, hub=hub)
    other = config.MODELS / "Other/text_encoder"
    other.mkdir(parents=True)
    (other / "a.safetensors").symlink_to(config.MODELS / "Tiny/transformer/a.safetensors")  # a file link
    (config.MODELS / "Other/vae").symlink_to(config.MODELS / "Tiny/vae")  # a folder link
    models.delete_variant(pack, "base")
    assert (config.MODELS / "Tiny/transformer/a.safetensors").is_file()
    assert not (config.MODELS / "Tiny/transformer/b.safetensors").exists()
    assert (config.MODELS / "Tiny/vae/config.json").is_file()
    assert (other / "a.safetensors").read_bytes() == BLOB


def test_delete_refuses_a_linked_folder(pack, tmp_path):
    (tmp_path / "theirs").mkdir()
    (tmp_path / "theirs/w.safetensors").write_bytes(b"x")
    (config.MODELS / "Tiny").symlink_to(tmp_path / "theirs")
    with pytest.raises(models.InUse, match="is a link"):
        models.delete_variant(pack, "base")
    assert (tmp_path / "theirs/w.safetensors").is_file()


# --- the worker


def job(kind: str, **data) -> dict:
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.model.job.valid.json").read_text())
    cmd["data"] = {"kind": kind} | data
    return cmd


def worker(pack, hub, tmp_path, residency=None, js=None):
    from core import lora

    scans = []

    async def rescan():
        scans.append(1)

    lib = lora.Library(config.MODELS, tmp_path / "cache")
    jobs = ModelJobs(js=js or FakeJS(), cancels_kv=FakeKV(), packs_=[pack], library=lib,
                     consumer=types.SimpleNamespace(residency=residency, ctx=None), rescan=rescan,
                     cache=tmp_path / "cache", endpoint=hub.url if hub else "", token_file=tmp_path / "token")  # fmt: skip
    return jobs, scans


async def test_worker_reports_running_progress_and_succeeded_then_acks(pack, tmp_path):
    pack.spec["variants"]["base"]["source"]["must_accept"] = True
    js = FakeJS()
    licence = {"id": "tiny", "sha256": hashlib.sha256(LICENCE.encode()).hexdigest()}
    with hub_of(t_tiny=Repo(files())) as hub:
        jobs, scans = worker(pack, hub, tmp_path, js=js)
        msg = FakeMsg(job("download", model="tiny-1", variant="base", licence=licence), js, "cmd.model.t.fake")
        await jobs.handle(msg)
    events = js.events()
    statuses = [e["data"]["status"] for e in events if e["type"] == "evt.model.status"]
    progress = [e["data"] for e in events if e["type"] == "evt.model.progress"]
    assert statuses == ["running", "succeeded"] and scans == [1]
    assert progress and progress[-1]["files_done"] == progress[-1]["files_total"] == 5
    assert [e["data"]["seq"] for e in events] == list(range(1, len(events) + 1))
    assert msg.log == [("ack", len(js.sent))]  # acked after the terminal status
    ids = [h["Nats-Msg-Id"] for _, _, h in js.sent]
    assert ids[0] == f"{msg_job(msg)}:1:1" and all(s.startswith("evt.model.fake.") for s, _, _ in js.sent)


def msg_job(msg) -> str:
    return json.loads(msg.data)["job_id"]


async def test_worker_fails_a_changed_licence_and_an_unknown_model(pack, tmp_path):
    pack.spec["variants"]["base"]["source"]["must_accept"] = True
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    bad = {"id": "tiny", "sha256": "0" * 64}
    await jobs.handle(FakeMsg(job("download", model="tiny-1", licence=bad), js, "cmd.model.t.fake"))
    await jobs.handle(FakeMsg(job("download", model="nope"), js, "cmd.model.t.fake"))
    failures = [e["data"]["failure"] for e in js.events() if e["data"].get("status") == "failed"]
    assert failures == [{"class": "licence", "detail": "licence tiny changed since it was accepted"},
                        {"class": "not_found", "detail": "nope is not published by fake.fake"}]  # fmt: skip


async def test_worker_cancel_key_written_before_start_cancels(pack, tmp_path):
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    cmd = job("download", model="tiny-1")
    await jobs.cancels_kv.put(cmd["job_id"], b"x")
    await jobs.handle(FakeMsg(cmd, js, "cmd.model.t.fake"))
    assert [e["data"]["status"] for e in js.events()] == ["cancelled"]


async def test_worker_cancel_during_a_download(pack, tmp_path):
    js = FakeJS()
    with hub_of(t_tiny=Repo(files())) as hub:
        hub.delay = 0.05
        jobs, _ = worker(pack, hub, tmp_path, js=js)
        cmd = job("download", model="tiny-1")
        task = asyncio.create_task(jobs.handle(FakeMsg(cmd, js, "cmd.model.t.fake")))
        while not any(e["type"] == "evt.model.progress" for e in js.events()):
            await asyncio.sleep(0.02)
        await jobs.cancels_kv.put(cmd["job_id"], b"x")
        await task
    assert [e["data"]["status"] for e in js.events() if e["type"] == "evt.model.status"] == [
        "running",
        "cancelled",
    ]


async def test_worker_delete_refuses_a_loaded_component_then_deletes(pack, tmp_path):
    with hub_of(t_tiny=Repo(files())) as hub:
        run(pack, hub=hub)
    res = Residency(10)
    res.get(("tiny-1", "base", "vae", "bf16"), 1, 0, object)
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, residency=res, js=js)
    await jobs.handle(FakeMsg(job("delete", model="tiny-1", variant="base"), js, "cmd.model.t.fake"))
    res.unload()
    await jobs.handle(FakeMsg(job("delete", model="tiny-1"), js, "cmd.model.t.fake"))
    ends = [e["data"] for e in js.events() if e["data"]["status"] in ("failed", "succeeded")]
    assert ends[0]["failure"] == {
        "class": "in_use",
        "detail": "Tiny base vae is loaded on fake.fake: unload it first",
    }
    assert ends[1]["result"]["files"] == 5 and not (config.MODELS / "Tiny").exists()


async def test_worker_delete_keeps_a_resident_shared_component_it_keeps(pack, tmp_path):
    with hub_of(t_tiny=Repo(files()), t_tiny_turbo=Repo(turbo_files())) as hub:
        run(pack, "base", hub=hub)
        run(pack, "turbo", hub=hub)
    res = Residency(10)
    res.get(("tiny-1", "base", "vae", "bf16"), 1, 0, object)  # turbo's vae, named by its owner variant
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, residency=res, js=js)
    await jobs.handle(FakeMsg(job("delete", model="tiny-1", variant="base"), js, "cmd.model.t.fake"))
    end = next(e["data"] for e in js.events() if e["data"]["status"] in ("failed", "succeeded"))
    assert end["status"] == "succeeded" and end["result"]["kept"] == ["vae"]
    assert (config.MODELS / "Tiny/vae/v.safetensors").is_file()


async def test_worker_lora_delete_refuses_a_prefused_file_and_deletes_a_shared_one(pack, tmp_path):
    from conftest import safetensors_file

    shapes = {
        "lora_unet_a.lora_down.weight": ("BF16", [4, 8]),
        "lora_unet_a.lora_up.weight": ("BF16", [8, 4]),
    }
    safetensors_file(config.MODELS / "loras/ink.safetensors", shapes)
    safetensors_file(config.MODELS / "Tiny/loras/fused.safetensors", shapes | {"lora_unet_b.alpha": ("F32", [])})
    pack.spec["lora"] = {
        "component": "transformer",
        "prefused": [{"file": "loras/fused.safetensors", "variant": "base"}],
    }
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    jobs.library.scan({})
    by_name = {p.name: s for p, s in jobs.library.files.items()}
    await jobs.handle(FakeMsg(job("lora_delete", lora_id=by_name["fused.safetensors"]), js, "cmd.model.t.fake"))
    await jobs.handle(FakeMsg(job("lora_delete", lora_id=by_name["ink.safetensors"]), js, "cmd.model.t.fake"))
    ends = [e["data"] for e in js.events() if e["data"]["status"] in ("failed", "succeeded")]
    assert ends[0]["failure"] == {"class": "in_use", "detail": "LoRA fused is pre-fused into Tiny"}
    assert ends[1]["result"]["files"] == 1 and not (config.MODELS / "loras/ink.safetensors").exists()


async def test_an_off_contract_job_is_dead_lettered(pack, tmp_path):
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    msg = FakeMsg(b"{}", js, "cmd.model.t.fake")
    await jobs.handle(msg)
    assert js.sent[0][0] == "dlq.cmd.model.t.fake" and msg.log[0][0] == "term"


# --- API 1.33: a community pre-quantized variant (configs from the official repo, weights from the community one)


def fp8_variant(pack, **more) -> dict:
    v = {"dir": "Tiny-FP8", "title": "FP8", "precision": ["fp8"], "component_precision": {"transformer": ["fp8"]},
         "source": source("t/tiny", ["model_index.json", "vae/config.json"]),
         "sources": [{"repo": "t/tiny-fp8", "revision": "main", "allow_patterns": ["transformer/*", "vae/*.safetensors"]}]} | more  # fmt: skip
    pack.spec["variants"]["fp8"] = v
    return v


def community() -> dict[str, bytes]:
    return {
        "transformer/a.safetensors": BLOB[:777],
        "vae/v.safetensors": BLOB[:999],
        "README.md": b"x",
    }


def test_a_variant_downloads_from_two_repos_into_one_folder(pack):
    fp8_variant(pack)
    with hub_of(t_tiny=Repo(files()), t_tiny_fp8=Repo(community())) as hub:
        result = run(pack, "fp8", hub=hub)
    root = config.MODELS / "Tiny-FP8"
    assert result["files"] == 4 and (root / "transformer/a.safetensors").read_bytes() == BLOB[:777]
    assert (root / "vae/config.json").read_bytes() == b"{}" and not (root / "transformer/b.safetensors").exists()
    entry = models.entry(pack)
    fp8 = next(v for v in entry["variants"] if v["id"] == "fp8")
    assert fp8["presence"] == "present" and fp8["precisions"] == ["fp8"] and fp8["sources"][0]["repo"] == "t/tiny-fp8"
    assert {c["component"]: c["precisions"] for c in fp8["components"]} == {
        "transformer": ["fp8"],
        "vae": ["bf16"],
    }
    assert entry["variants"][0]["precisions"] == ["bf16"]  # the official variants keep theirs
    value = {"schema_version": 1, "host": "t", "env": "fake", "published_at": "2026-11-21T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []


def test_a_source_with_a_dir_fills_a_components_own_folder(pack):
    """MiniMax H3 Image base-fp8's prompt enhancer: its own repo, into the component's own `dir`; the variant is
    present only with it, and a delete of the variant keeps that folder (base reads it too)."""
    pack.spec["components"]["enhancer"] = {"dir": "Tiny-PE", "files": "pe-*.safetensors"}
    v = fp8_variant(pack)
    v["sources"].append({"repo": "t/tiny-pe", "revision": "main", "dir": "Tiny-PE", "gated": False,
                         "allow_patterns": ["config.json", "pe-*.safetensors"]})  # fmt: skip
    pe = {"config.json": b"{}", "pe-1.safetensors": BLOB[:333], "README.md": b"x"}
    with hub_of(t_tiny=Repo(files()), t_tiny_fp8=Repo(community()), t_tiny_pe=Repo(pe)) as hub:
        assert run(pack, "fp8", hub=hub)["files"] == 6
    root = config.MODELS / "Tiny-PE"
    assert (root / "pe-1.safetensors").read_bytes() == BLOB[:333] and not (root / "README.md").exists()
    assert not (config.MODELS / "Tiny-FP8/config.json").exists() and not (root / models.MARKER).exists()
    assert presence(pack)["fp8"] == "present"
    entry = models.entry(pack)
    value = {"schema_version": 1, "host": "t", "env": "fake", "published_at": "2026-11-21T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []
    models.delete_variant(pack, "fp8")
    assert (root / "pe-1.safetensors").is_file()


def test_a_second_source_is_refused_by_its_own_name_and_may_not_repeat_a_path(pack):
    v = fp8_variant(pack)
    with hub_of(t_tiny=Repo(files()), t_tiny_fp8=Repo(community() | {"vae/config.json": b"{}"})) as hub:
        v["sources"][0]["revision"] = "v9"
        with pytest.raises(JobError, match="repo t/tiny-fp8 has no revision v9"):
            run(pack, "fp8", hub=hub)
        v["sources"][0] |= {
            "revision": "main",
            "allow_patterns": ["vae/*"],
        }  # vae/config.json: in both
        with pytest.raises(JobError, match="lists vae/config.json twice"):
            run(pack, "fp8", hub=hub)


def test_a_variant_for_other_devices_is_never_published(pack, monkeypatch):
    fp8_variant(pack, devices=["cuda"])
    monkeypatch.setattr(models, "device", lambda p: "mps")
    assert [v["id"] for v in models.entry(pack)["variants"]] == ["base", "turbo"]
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    assert models.entry(pack)["variants"][2]["devices"] == ["cuda"]


def test_a_variants_own_precision_reaches_the_model_node_and_its_components(pack):
    fp8_variant(pack)
    schema = packs.model_schema(pack)["params"][1]
    assert schema["enum"] == ["bf16", "fp8"]
    ref = packs.ModelRef(pack, "fp8", "fp8")
    assert (ref.precision_of("transformer"), ref.precision_of("vae")) == ("fp8", "bf16")
    assert packs.ModelRef(pack, "base", "fp8").precision_of("transformer") == "bf16"  # not offered: its first


def test_shares_from_names_the_lender_for_load_download_and_delete(pack):
    """turbo-fp8's case: a variant that shares from another than the first. A download of it first fetches the
    lender's shared files and configs (never its own transformer) from the lender's sources; delete of the lender
    keeps them."""
    fp8_variant(pack)
    pack.spec["variants"]["fp8t"] = {
        "dir": "Tiny-FP8-Turbo", "title": "FP8 Turbo", "precision": ["fp8"], "shares": ["vae"], "shares_from": "fp8",
        "source": source("t/tiny-turbo", ["model_index.json"]),
        "sources": [{"repo": "t/tiny-fp8-turbo", "revision": "main", "allow_patterns": ["transformer/*"]}]}  # fmt: skip
    repos = {"t_tiny": Repo(files()), "t_tiny_turbo": Repo(turbo_files()), "t_tiny_fp8": Repo(community()),
             "t_tiny_fp8_turbo": Repo({"transformer/a.safetensors": BLOB[:555]})}  # fmt: skip
    with hub_of(**repos) as hub:
        run(pack, "fp8t", hub=hub)
        lender = config.MODELS / "Tiny-FP8"
        assert sorted(p.as_posix() for p in models.own_files(lender)) == [
            "model_index.json", "vae/config.json", "vae/v.safetensors"]  # fmt: skip
        assert packs.ModelRef(pack, "fp8t", "fp8").owner("vae") == "fp8"
        assert presence(pack)["fp8t"] == "present" and presence(pack)["fp8"] == "partial"
        run(pack, "fp8", hub=hub)
        assert run(pack, "fp8t", hub=hub)["reused"] == ["vae"]
    assert models.kept_components(pack, {"fp8"}) == (["vae"], ["transformer/"])
    assert models.kept_components(pack, {"base"}) == (
        [],
        None,
    )  # turbo has no files: nothing kept for it


def test_a_component_publishes_the_dtype_most_of_its_bytes_are_stored_in(tmp_path):
    def st(name: str, tensors: dict) -> object:
        offs, h = 0, {}
        for k, (dtype, n) in tensors.items():
            h[k] = {"dtype": dtype, "shape": [n], "data_offsets": [offs, offs + n]}
            offs += n
        raw = json.dumps(h).encode()
        p = tmp_path / name
        p.write_bytes(len(raw).to_bytes(8, "little") + raw + b"\0" * offs)
        return p

    bf16 = st("a.safetensors", {"w": ("BF16", 100), "norm": ("F32", 8)})
    fp8 = st("b.safetensors", {"w": ("F8_E4M3", 100), "scale": ("F32", 8)})
    (gguf := tmp_path / "c.gguf").write_bytes(b"GGUF" + b"\xff" * 64)
    (empty := tmp_path / "d.safetensors").write_bytes(b"")
    assert models.stored_dtype([bf16]) == "bf16" and models.stored_dtype([fp8, empty]) == "fp8"
    assert models.stored_dtype([gguf, empty]) is None
    assert models.stored_dtype([st("e.safetensors", {"w": ("U8", 50)})]) is None  # NVFP4 says nothing


def enhancer(pack, own_dir: bool = True) -> dict:
    """API 1.36: an optional add-on of the tiny pack, in a folder of its own or in base's."""
    spec = {"files": "pe/*.safetensors" if not own_dir else "pe-*.safetensors",
            "addon": {"title": "Enhancer", "source": source("t/tiny-pe", ["config.json", "pe-*.safetensors"]
                                                            if own_dir else ["pe/*"])}}  # fmt: skip
    if own_dir:
        spec["dir"] = "Tiny-PE"
    pack.spec["components"]["enhancer"] = spec
    return spec


def test_an_addon_downloads_and_deletes_on_its_own_and_no_variant_needs_it(pack):
    """Optional add-ons: base is present without its add-on; the add-on's job fetches only its source into its own
    folder, its entry turns present; a delete of base keeps it, a delete of the add-on removes only it."""
    enhancer(pack)
    pe = {"config.json": b"{}", "pe-1.safetensors": BLOB[:333], "README.md": b"x"}
    with hub_of(t_tiny=Repo(files()), t_tiny_pe=Repo(pe)) as hub:
        run(pack, hub=hub)
        assert presence(pack)["base"] == "present"
        (addon,) = models.entry(pack)["addons"]
        assert (addon["component"], addon["presence"], addon["dir"]) == (
            "enhancer",
            "missing",
            "Tiny-PE",
        )
        result = download.download(pack, None, endpoint=hub.url, token=None, cache=config.MODELS.parent / "cache",
                                   host="t", progress=Progress(), addon="enhancer")  # fmt: skip
    assert result["files"] == 2 and not (config.MODELS / "Tiny-PE/README.md").exists()
    entry = models.entry(pack)
    assert entry["addons"][0]["presence"] == "present" and entry["addons"][0]["bytes"] == 335
    assert "enhancer" not in {c["component"] for c in entry["variants"][0]["components"]}
    value = {"schema_version": 1, "host": "t", "env": "fake", "published_at": "2026-11-22T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []
    models.delete_variant(pack, "base")
    assert (config.MODELS / "Tiny-PE/pe-1.safetensors").is_file()
    assert models.delete_addon(pack, "enhancer") == (2, 335) and not (config.MODELS / "Tiny-PE").exists()


def test_an_addon_in_the_first_variants_folder_is_never_that_variants(pack):
    """An add-on without a dir of its own lives in the first variant's folder: not in its files or bytes, kept by
    its delete; the add-on's own delete removes only its globs."""
    enhancer(pack, own_dir=False)
    with hub_of(t_tiny=Repo(files()), t_tiny_pe=Repo({"pe/e.safetensors": BLOB[:77]})) as hub:
        run(pack, hub=hub)
        download.download(pack, None, endpoint=hub.url, token=None, cache=config.MODELS.parent / "cache",
                          host="t", progress=Progress(), addon="enhancer")  # fmt: skip
    base = models.entry(pack)["variants"][0]
    assert base["presence"] == "present" and "pe/e.safetensors" not in {f["path"] for f in base["files"]}
    assert models.entry(pack)["addons"][0]["presence"] == "present"
    models.delete_variant(pack, "base")
    assert [p.relative_to(config.MODELS).as_posix() for p in config.MODELS.rglob("*.safetensors")] == [
        "Tiny/pe/e.safetensors"
    ]
    models.delete_addon(pack, "enhancer")
    assert not (config.MODELS / "Tiny").exists()


async def test_worker_addon_jobs_and_refusals(pack, tmp_path):
    enhancer(pack)
    js = FakeJS()
    pe = {"config.json": b"{}", "pe-1.safetensors": BLOB[:333]}
    res = types.SimpleNamespace(entries={("tiny-1", "base", "enhancer", "bf16"): None}, used=set())
    with hub_of(t_tiny_pe=Repo(pe)) as hub:
        jobs, _ = worker(pack, hub, tmp_path, residency=res, js=js)
        await jobs.handle(FakeMsg(job("download", model="tiny-1", addon="enhancer"), js, "cmd.model.t.fake"))
        await jobs.handle(FakeMsg(job("download", model="tiny-1", addon="nope"), js, "cmd.model.t.fake"))
        await jobs.handle(FakeMsg(job("delete", model="tiny-1", addon="enhancer"), js, "cmd.model.t.fake"))
        res.entries = {}
        await jobs.handle(FakeMsg(job("delete", model="tiny-1", addon="enhancer"), js, "cmd.model.t.fake"))
    ends = [e["data"] for e in js.events() if e["data"].get("status") in ("succeeded", "failed")]
    assert ends[0]["status"] == "succeeded"
    assert ends[1]["failure"] == {"class": "not_found", "detail": "Tiny has no add-on nope"}
    assert ends[2]["failure"] == {
        "class": "in_use",
        "detail": "Tiny base enhancer is loaded on fake.fake: unload it first",
    }
    assert ends[3]["status"] == "succeeded" and ends[3]["result"] == {
        "files": 2,
        "freed_bytes": 335,
    }
