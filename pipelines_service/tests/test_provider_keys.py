"""Plan 27 (API 1.52), a provider key pasted in Admin › Model providers: pipelines.providers.key.write checks it on
/models (401/403 refused, anything else unreachable, force skips the call), writes it into the first key folder only
(temp + rename, 0600) or removes it there, and never puts it in a reply or a log line. Nothing real is called."""

import json
import logging
import os

import fake_openai
import pytest

import config
import providers
from messaging import schemas

KEY = "sk-test-0123456789abcdef"


@pytest.fixture
def fake():
    srv, state = fake_openai.serve()
    state.url = f"http://127.0.0.1:{srv.server_address[1]}/v1"
    yield state
    srv.shutdown()


@pytest.fixture
def keys(monkeypatch, tmp_path):
    a, b = tmp_path / "a", tmp_path / "b"
    a.mkdir(), b.mkdir()
    monkeypatch.setattr(config, "PROVIDER_KEYS_DIRS", [a, b])
    return a, b


def snap(url: str) -> dict:
    return {"kind": "server", "api": "openai", "base_url": url, "timeout_s": 15}


async def ask(**req) -> dict:
    got = await providers.answer_write(json.dumps(req).encode())
    assert not schemas.providers_key_write_errors("reply", got), got
    assert KEY not in json.dumps(got)
    return got


def save(name: str, url: str, key: str = KEY, **kw) -> dict:
    return {"op": "save", "key_secret": name, "key": key, "provider": snap(url)} | kw


async def test_a_checked_key_is_written_to_the_first_folder_only(fake, keys, caplog):
    a, b = keys
    caplog.set_level(logging.DEBUG)
    got = await ask(**save("deepseek", fake.url))
    assert got == {"found": True, "writable": True, "saved": True, "check": "ok"}
    f = a / "deepseek"
    assert f.read_text() == KEY and (f.stat().st_mode & 0o777) == 0o600
    assert os.listdir(a) == ["deepseek"] and os.listdir(b) == []  # no temp file left
    sent = [r for r in fake.requests if r["path"].endswith("/models")]
    assert (
        len(sent) == 1 and sent[0]["headers"]["authorization"] == f"Bearer {KEY}"
    )  # only the snapshot's URL
    assert "key file deepseek written (check ok)" in caplog.text and KEY not in caplog.text


@pytest.mark.parametrize("status", [401, 403])
async def test_a_refused_key_writes_nothing(fake, keys, caplog, status):
    caplog.set_level(logging.DEBUG)
    fake.script["__models"] = [
        {"status": status, "body": {"error": {"message": f"bad key {KEY[:6]}…"}}}
    ]
    got = await ask(**save("openai", fake.url))
    assert got == {"found": False, "writable": True, "saved": False, "check": "refused",
                   "detail": "the provider refused this key; not saved"}  # fmt: skip
    assert os.listdir(keys[0]) == [] and KEY not in caplog.text


async def test_unreachable_writes_nothing_and_force_writes_without_a_call(fake, keys, monkeypatch):
    fake.script["__models"] = [{"status": 404}]
    got = await ask(**save("brave", fake.url))
    assert (got["check"], got["saved"], got["detail"]) == ("unreachable", False, "HTTP 404")
    monkeypatch.setattr(providers, "CHECK_TIMEOUT", 0.3)
    fake.script["__models"] = [{"delay_s": 1}]
    got = await ask(**save("brave", fake.url))
    assert (got["check"], got["detail"]) == ("unreachable", "timeout")
    monkeypatch.setattr(config, "REMOTE_MODELS", False)
    remote = save("brave", "https://api.search.brave.com/res/v1")
    remote["provider"]["kind"] = "remote"
    got = await ask(**remote)
    assert got["detail"] == "remote models are off on this deployment"
    assert os.listdir(keys[0]) == []
    before = len(fake.requests)
    got = await ask(**save("brave", fake.url, force=True))
    assert got == {"found": True, "writable": True, "saved": True, "check": "skipped"}
    assert len(fake.requests) == before and (keys[0] / "brave").read_text() == KEY


async def test_names_symlinks_and_folders(fake, keys, tmp_path):
    a, _ = keys
    for bad in ("../deepseek", ".hidden", "a/b", "UPPER"):
        got = await ask(op="remove", key_secret=bad)
        assert got["code"] == "internal"
    target = tmp_path / "elsewhere"
    target.write_text("untouched")
    (a / "linked").symlink_to(target)
    assert (await ask(**save("linked", fake.url, force=True)))["saved"] is True
    assert not (a / "linked").is_symlink() and (a / "linked").read_text() == KEY
    assert target.read_text() == "untouched"
    (a / "folder").mkdir()
    got = await ask(**save("folder", fake.url, force=True))
    assert got["code"] == "not_file" and os.listdir(a / "folder") == []


async def test_a_failed_write_leaves_no_temp_file(keys, monkeypatch):
    a, _ = keys

    def broken(*_a, **_kw):
        raise OSError("disk full")

    monkeypatch.setattr(os, "replace", broken)
    with pytest.raises(OSError):
        providers.write_key("x", KEY)
    assert os.listdir(a) == []


async def test_a_read_only_first_folder(keys, monkeypatch, fake):
    a, _ = keys
    a.chmod(0o500)
    try:
        got = await ask(**save("x", fake.url, force=True))
        assert got == {"error": "keys are set in files on this deployment", "code": "readonly"}
        assert providers.answer_key(b'{"key_secret": "x"}') == {"found": False, "writable": False}
        assert (await ask(op="remove", key_secret="x"))["code"] == "readonly"
    finally:
        a.chmod(0o700)


async def test_remove_deletes_only_the_first_folders_file(keys, caplog):
    a, b = keys
    caplog.set_level(logging.DEBUG)
    (a / "fake-openai").write_text(KEY)
    (b / "fake-openai").write_text("placeholder")
    assert await ask(op="remove", key_secret="fake-openai") == {"found": True, "writable": True}
    assert not (a / "fake-openai").exists() and (b / "fake-openai").exists()
    assert await ask(op="remove", key_secret="fake-openai") == {
        "found": True,
        "writable": True,
    }  # missing: fine
    assert await ask(op="remove", key_secret="nope") == {"found": False, "writable": True}
    assert "key file fake-openai removed" in caplog.text


def test_the_first_folder_is_made_at_start(monkeypatch, tmp_path):
    first = tmp_path / "provider-keys"
    monkeypatch.setattr(config, "PROVIDER_KEYS_DIRS", [first, tmp_path / "b"])
    providers.make_first_folder()
    assert first.is_dir() and (first.stat().st_mode & 0o777) == 0o700 and providers.writable()


async def test_a_bad_key_never_reaches_a_reply_or_the_log(keys, caplog, fake):
    caplog.set_level(logging.DEBUG)
    for bad in ("sk with space", "ské", "x" * 1025):
        got = await providers.answer_write(json.dumps(save("x", fake.url, key=bad)).encode())
        assert got["code"] == "internal" and bad not in json.dumps(got)
        assert bad not in caplog.text
    assert os.listdir(keys[0]) == []


async def test_a_slow_name_lookup_counts_in_the_15_s(keys, monkeypatch):
    # the whole check (the host rule's lookup too) fits in CHECK_TIMEOUT, so studio's 30 s wait always gets the answer
    # (else studio says "did not answer" while pipelines still checks and may write the key afterwards)
    import asyncio

    async def slow(*_a):
        await asyncio.sleep(5)

    monkeypatch.setattr(providers, "_lookup", slow)
    monkeypatch.setattr(providers, "CHECK_TIMEOUT", 0.3)
    got = await asyncio.wait_for(ask(**save("slow", "http://slow.invalid/v1")), 2)
    assert (got["check"], got["detail"]) == ("unreachable", "timeout")
    assert os.listdir(keys[0]) == []


async def test_a_non_ascii_key_is_refused_with_our_text_and_no_request(fake, keys):
    # a cut copy ("…"), a curly quote, a non-breaking or zero-width space: never "could not connect", never a call
    for bad in ("sk-ant-api03-abc\u2026", "sk-\u201cabc\u201d", "sk-a\u00a0b", "sk-a\u200bb"):
        before = len(fake.requests)
        got = await providers.check_key("anthropic", snap(fake.url), bad)
        assert got == ("refused", providers.KEY_CHARS_TEXT), got
        assert len(fake.requests) == before
    assert os.listdir(keys[0]) == []


def test_a_local_encoding_error_is_never_could_not_connect():
    try:
        "sk-\u2026".encode("ascii")
    except UnicodeEncodeError as e:
        got = str(providers._classify("anthropic", e))
    assert got == f"anthropic: {providers.KEY_CHARS_TEXT}"
