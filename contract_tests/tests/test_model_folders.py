"""Model folders (API 1.78, contracts/README.md "Model folders") on the fake host: its runner's scratch roots /models
and /models2 (volume fake-models-2) and a drive path that does not exist (/Volumes/T9/models, unplugged). The list is
checked by the runner (req.runner.folders.check), applied live, shown on the Models screen; tiny is downloaded into
/models2 as a second copy, the delete without a folder is refused, and the /models2 copy is deleted by folder. The
module ends with the one /models folder again. Never ~/GenAI/models or a real /Volumes."""

import hashlib
import re

import pytest
from helpers import STUDIO, docker_exec, ok, wait_for
from test_models import (
    ADMIN,
    HOST,
    MODEL,
    TITLE,
    ensure_present,
    settings_applied,
    state,
    unload_all,
    wait_job,
)

FOLDERS = f"{ADMIN}/hosts/{HOST}/folders"
MAIN, SECOND, DRIVE = "/models", "/models2", "/Volumes/T9/models"


def fid(path: str) -> str:
    return hashlib.sha256(path.encode()).hexdigest()[:12]


def put(admin, *paths: str, downloads: int = 0):
    return admin.put(
        FOLDERS, json={"folders": [{"path": p} for p in paths], "downloads": downloads}
    )


def fake_folders(client) -> list[dict]:
    (h,) = [h for h in ok(client.get(f"{STUDIO}/models"))["hosts"] if h["host"] == HOST]
    return h["folders"]


@pytest.fixture(scope="module")
def two(admin):
    """tiny present in /models; the list /models, /models2, the drive; back to /models alone after."""
    try:
        docker_exec("runner", "sh", "-c", f"rm -rf {SECOND}/*")
    except (OSError, RuntimeError) as e:
        pytest.skip(f"the fake runner is not reachable with docker exec: {e}")
    ensure_present(admin, "tiny")
    yield
    ok(put(admin, MAIN))
    wait_for(lambda: settings_applied(admin), timeout=60, what="one folder again on fake")
    docker_exec("runner", "sh", "-c", f"rm -rf {SECOND}/*")


def test_the_runner_checks_the_list(admin, api):
    assert put(api, MAIN).status_code == 403
    assert (
        admin.put(
            f"{ADMIN}/hosts/nohost/folders", json={"folders": [{"path": MAIN}], "downloads": 0}
        ).status_code
        == 404
    )
    for paths, downloads, detail in (
        (("models",), 0, "paths must be absolute or start with ~/"),
        ((MAIN, MAIN + "/"), 0, f"{MAIN}/ is listed twice"),
        ((MAIN,), 1, "downloads must be an index of folders"),
        ((MAIN, "/nowhere"), 0, f"/nowhere is not a folder on {HOST}"),
        ((MAIN, DRIVE), 1, "downloads must be a connected folder"),
        ((MAIN, "/proc"), 1, f"/proc is not writable on {HOST}"),
    ):
        r = put(admin, *paths, downloads=downloads)
        assert r.status_code == 422 and r.json()["detail"] == detail, (paths, r.text)


def test_the_list_is_applied_and_shown(admin, two):
    s = ok(put(admin, MAIN, SECOND, DRIVE))
    assert [f["path"] for f in s["folders"]] == [MAIN, SECOND, DRIVE] and s["downloads"] == fid(
        MAIN
    ), s
    wait_for(lambda: settings_applied(admin), timeout=60, what="folders applied on fake")
    folders = wait_for(
        lambda: len(f := fake_folders(admin)) == 3 and f, timeout=60, what="three folders on fake"
    )
    assert [(f["id"], f["connected"], f["downloads"]) for f in folders] == [
        (fid(MAIN), True, True),
        (fid(SECOND), True, False),
        (fid(DRIVE), False, False),
    ], folders
    assert folders[2]["free_bytes"] is None and folders[2]["name"] == "T9", folders
    assert state(admin, "tiny")["folder"] == fid(MAIN)


def test_a_second_copy_then_delete_by_folder(admin, two):
    j = admin.post(
        f"{ADMIN}/models/{MODEL}/download",
        json={"host": HOST, "variant": "tiny", "accept_licence": True, "folder": fid(MAIN)},
    )
    assert j.status_code == 409, j.text  # already there
    r = admin.post(
        f"{ADMIN}/models/{MODEL}/download",
        json={"host": HOST, "variant": "tiny", "accept_licence": True, "folder": fid(DRIVE)},
    )
    assert r.status_code == 409 and r.json()["detail"] == f"T9 is not connected on {HOST}", r.text
    j = ok(
        admin.post(
            f"{ADMIN}/models/{MODEL}/download",
            json={"host": HOST, "variant": "tiny", "accept_licence": True, "folder": fid(SECOND)},
        ),
        202,
    )
    assert wait_job(admin, j["id"])["state"] == "succeeded"
    h = wait_for(
        lambda: len((x := state(admin, "tiny")).get("copies") or []) == 2 and x,
        timeout=90,
        what="two copies of tiny",
    )
    assert h["folder"] == fid(MAIN) and [c["folder"] for c in h["copies"]] == [
        fid(MAIN),
        fid(SECOND),
    ], h
    assert docker_exec("runner", "sh", "-c", f"ls {SECOND}/Echo-Tiny/transformer").strip()
    unload_all(admin)
    r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "tiny"})
    assert r.status_code == 409, r.text
    assert re.fullmatch(
        rf"{re.escape(TITLE)} [Tt]iny is in 2 folders on {HOST}; pick one", r.json()["detail"]
    ), r.text
    j = ok(
        admin.delete(
            f"{ADMIN}/models/{MODEL}",
            params={"host": HOST, "variant": "tiny", "folder": fid(SECOND)},
        ),
        202,
    )
    assert wait_job(admin, j["id"], 60)["state"] == "succeeded"
    h = wait_for(
        lambda: len((x := state(admin, "tiny")).get("copies") or []) == 1 and x,
        timeout=90,
        what="one copy of tiny",
    )
    assert h["state"] == "present" and h["folder"] == fid(MAIN), h
