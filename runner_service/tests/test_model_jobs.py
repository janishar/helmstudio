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
    assert not (root / models.MARKER).exists() and not list(
        (config.MODELS.parent / "cache").glob("hf-*")
    )
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

    monkeypatch.setattr(
        constants, "DOWNLOAD_CHUNK_SIZE", 1 << 14
    )  # 10 MB: resumes count whole chunks
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
    fetched = {
        r[1].rsplit("/resolve/main/", 1)[-1]
        for r in hub.requests
        if r[0] == "GET" and "/resolve/" in r[1]
    }
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
    assert (
        not (config.MODELS / "Tiny").exists()
        and not (config.MODELS.parent / "evil.safetensors").exists()
    )


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
        (config.MODELS / "Tiny-Turbo/vae").symlink_to(
            config.MODELS / "Tiny/vae"
        )  # a hand-made share
        assert not packs.owns_copy(
            pack, "turbo", "vae"
        )  # files under a linked folder are not its own
        # the owner's hashes differ from turbo's repo: the vae is planned into turbo's own (linked) folder
        with pytest.raises(JobError, match="under a symlinked folder") as e:
            run(pack, "turbo", hub=hub)
    assert e.value.cls == "internal"
    assert (config.MODELS / "Tiny/vae/v.safetensors").read_bytes() == BLOB[
        :999
    ]  # base's file untouched


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
        assert (config.MODELS / "Tiny/vae/v.safetensors").read_bytes() == BLOB[
            :999
        ]  # base's source
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
    assert (config.MODELS / "Tiny-Turbo/vae/v.safetensors").read_bytes() == turbo_files[
        "vae/v.safetensors"
    ]
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
    assert variant["presence"] == "mismatch" and variant["mismatch"] == [
        "transformer/a.safetensors"
    ]


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
        msg = FakeMsg(
            job("download", model="tiny-1", variant="base", licence=licence), js, "cmd.model.t.fake"
        )
        await jobs.handle(msg)
    events = js.events()
    statuses = [e["data"]["status"] for e in events if e["type"] == "evt.model.status"]
    progress = [e["data"] for e in events if e["type"] == "evt.model.progress"]
    assert statuses == ["running", "succeeded"] and scans == [1]
    assert progress and progress[-1]["files_done"] == progress[-1]["files_total"] == 5
    assert [e["data"]["seq"] for e in events] == list(range(1, len(events) + 1))
    assert msg.log == [("ack", len(js.sent))]  # acked after the terminal status
    ids = [h["Nats-Msg-Id"] for _, _, h in js.sent]
    assert ids[0] == f"{msg_job(msg)}:1:1" and all(
        s.startswith("evt.model.fake.") for s, _, _ in js.sent
    )


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
    await jobs.handle(
        FakeMsg(job("delete", model="tiny-1", variant="base"), js, "cmd.model.t.fake")
    )
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
    res.get(
        ("tiny-1", "base", "vae", "bf16"), 1, 0, object
    )  # turbo's vae, named by its owner variant
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, residency=res, js=js)
    await jobs.handle(
        FakeMsg(job("delete", model="tiny-1", variant="base"), js, "cmd.model.t.fake")
    )
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
    safetensors_file(
        config.MODELS / "Tiny/loras/fused.safetensors", shapes | {"lora_unet_b.alpha": ("F32", [])}
    )
    pack.spec["lora"] = {
        "component": "transformer",
        "prefused": [{"file": "loras/fused.safetensors", "variant": "base"}],
    }
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    jobs.library.scan({})
    by_name = {p.name: s for p, s in jobs.library.files.items()}
    await jobs.handle(
        FakeMsg(job("lora_delete", lora_id=by_name["fused.safetensors"]), js, "cmd.model.t.fake")
    )
    await jobs.handle(
        FakeMsg(job("lora_delete", lora_id=by_name["ink.safetensors"]), js, "cmd.model.t.fake")
    )
    ends = [e["data"] for e in js.events() if e["data"]["status"] in ("failed", "succeeded")]
    assert ends[0]["failure"] == {"class": "in_use", "detail": "LoRA fused is pre-fused into Tiny"}
    assert (
        ends[1]["result"]["files"] == 1 and not (config.MODELS / "loras/ink.safetensors").exists()
    )


async def test_an_off_contract_job_is_dead_lettered(pack, tmp_path):
    js = FakeJS()
    jobs, _ = worker(pack, None, tmp_path, js=js)
    msg = FakeMsg(b"{}", js, "cmd.model.t.fake")
    await jobs.handle(msg)
    assert js.sent[0][0] == "dlq.cmd.model.t.fake" and msg.log[0][0] == "term"
