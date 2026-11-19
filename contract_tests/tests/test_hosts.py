"""Several hosts (API 1.18, M6; contracts/README.md "Several hosts", "Hosts with a discrete GPU", "A remote GPU host:
credentials and TLS") on two fake hosts of env `fake`: `fake` (service runner) and a second one, HOST_B (default
`fakeb`, service RUNNER_B_SVC, default `runner-b`, its own NATS user `runner_<HOST_B>_fake` and models volume).

The second host is a compose service of the test stack that is created but not started (profile `hosts`): every other
module expects one fake host (positions, `host == "fake"`), so this module starts it with `docker start`, and stops it
at the end and waits until its key is offline (30 s). It skips when that container does not exist. Host loss is a
`docker kill` of the runner that holds the run (a spot preemption: no clean exit); it is started again after.

Tests run in file order and build on each other: the echo pack's tiny model is present on `fake` only, then
downloaded to HOST_B too (deleted again at the end, so later modules see it on one host)."""

import json
import os
import subprocess
import time

import pytest
from helpers import QUICK, STUDIO, new_session, ok, start_run, take_of_run, wait_for, wait_run
from test_hosts_admin import WARM
from test_models import (
    ADMIN,
    ENV,
    HOST,
    MODEL,
    TINY_RUN,
    ensure_present,
    residents,
    settings_applied,
    state,
    tiny_run,
    wait_job,
)

HOST_B = os.environ.get("HOST_B", "fakeb")
SERVICES = {HOST: "runner", HOST_B: os.environ.get("RUNNER_B_SVC", "runner-b")}
LOSS_TIMEOUT = 240  # key offline after 30 s, the lease sweep, attempt 2 (resilience.sh allows ~60 s for the sweep)
LONG = {"prompt.text": "a long fox", "echo.steps": 100, "echo.delay_ms": 300}  # ~30 s


def docker(*args: str) -> None:
    out = subprocess.run(["docker", *args], capture_output=True, timeout=90, check=False)
    if out.returncode:
        raise RuntimeError(out.stderr.decode()[-300:])


def container(host: str) -> str:
    return f"{os.environ.get('COMPOSE_PROJECT', 'helmstudio-test')}-{SERVICES[host]}-1"


def key(client, host: str) -> dict | None:
    """host.fake's entry of GET /hosts."""
    hosts = ok(client.get(f"{STUDIO}/hosts"))["hosts"]
    return next((h for h in hosts if (h["host"], h["env"]) == (host, ENV)), None)


def live(client, host: str) -> bool:
    return (k := key(client, host)) is not None and k["state"] != "offline"


def wait_live(client, *hosts: str) -> None:
    for h in hosts:
        wait_for(lambda h=h: live(client, h), timeout=120, interval=1, what=f"host {h} live")


def run_of(client, rid: str) -> dict:
    return ok(client.get(f"{STUDIO}/runs/{rid}"))


def running_on(client, rid: str, attempt: int = 1) -> dict:
    """The run once it is running at `attempt` (with its host)."""
    return wait_for(
        lambda: (r := run_of(client, rid))["status"] == "running" and r["attempt"] == attempt and r,
        timeout=LOSS_TIMEOUT,
        what=f"run {rid} running, attempt {attempt}",
    )


def wait_end(client, rid: str) -> dict:
    return wait_for(
        lambda: (r := run_of(client, rid))["status"] in ("succeeded", "failed", "cancelled") and r,
        timeout=LOSS_TIMEOUT,
        interval=1,
        what=f"run {rid} ended",
    )


def other(host: str) -> str:
    return HOST_B if host == HOST else HOST


def delete_variant(admin, host: str, variant: str) -> None:
    if state(admin, variant, host)["state"] == "missing":
        return
    ok(admin.post(f"{ADMIN}/hosts/{host}/unload", json={"all": True}), 202)
    wait_for(lambda: not residents(admin, host), timeout=60, what=f"nothing of echo on {host}")
    r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": host, "variant": variant})
    assert r.status_code in (200, 202), r.text
    assert wait_job(admin, r.json()["id"], 60)["state"] == "succeeded"
    wait_for(
        lambda: state(admin, variant, host)["state"] == "missing",
        timeout=90,
        what=f"{variant} missing on {host}",
    )


@pytest.fixture(scope="module")
def two(admin):
    """Both fake hosts live, keep_warm without an idle timeout on both; HOST_B stopped and offline after."""
    try:
        docker("start", container(HOST_B))
    except (OSError, RuntimeError) as e:
        pytest.skip(f"no second fake host ({container(HOST_B)}): {e}")
    try:
        wait_live(admin, HOST, HOST_B)
        for h in (HOST, HOST_B):
            ok(admin.put(f"{ADMIN}/hosts/{h}/settings", json=WARM))
            wait_for(lambda h=h: settings_applied(admin, h), timeout=60, what=f"settings on {h}")
        yield
    finally:
        for h in (HOST, HOST_B):  # a loss test that failed half-way leaves a runner stopped
            docker("start", container(h))
        try:
            wait_live(admin, HOST, HOST_B)
            delete_variant(admin, HOST_B, "tiny")
        finally:
            docker("stop", container(HOST_B))
            wait_for(
                lambda: not live(admin, HOST_B), timeout=90, interval=1, what=f"{HOST_B} offline"
            )


def test_both_hosts_listed_with_device_and_budgets(api, two):
    for h in (HOST, HOST_B):
        k = key(api, h)
        assert k and k["state"] in ("idle", "busy"), k
        assert k["memory"]["budget_gb"] > 0 and k["host_memory"]["budget_gb"] > 0, k
        d = k["device"]
        assert d["kind"] in ("cuda", "mps", "cpu") and d["name"] and d["memory_total_gb"] > 0, d
        kind = {"cuda": "vram", "mps": "unified", "cpu": "ram"}[d["kind"]]
        assert k["host_memory"].get("kind", "unified") == kind, k
        assert ("host_ram" in k) == (d["kind"] == "cuda"), k  # the second limit: cuda only
    hosts = {h["host"] for h in ok(api.get(f"{STUDIO}/models"))["hosts"] if h["live"]}
    assert {HOST, HOST_B} <= hosts


def test_made_with_records_the_host(api, two):
    sid = new_session(api, "Contract hosts")["id"]
    run = wait_run(api, start_run(api, sid, QUICK)["id"])
    assert run["status"] == "succeeded" and run["host"] in (HOST, HOST_B), run
    assert run.get("lost_host") is None, run
    assert take_of_run(api, run["id"])["made_with"]["host"] == run["host"]


def test_busy_host_sends_the_next_run_to_the_other(api, two):
    sid = new_session(api, "Contract hosts")["id"]
    hold = start_run(api, sid, LONG)
    try:
        busy = running_on(api, hold["id"])["host"]
        run = wait_run(api, start_run(api, sid, QUICK)["id"])
        assert (run["status"], run["host"]) == ("succeeded", other(busy)), run
        assert run_of(api, hold["id"])["status"] == "running"  # it did not wait for the busy host
    finally:
        api.post(f"{STUDIO}/runs/{hold['id']}/cancel")
    assert wait_run(api, hold["id"])["status"] == "cancelled"


def test_model_on_one_host_pins_its_runs_there(admin, api, two):
    ensure_present(admin, "tiny")  # on fake
    delete_variant(admin, HOST_B, "tiny")
    sid = new_session(api, "Contract hosts")["id"]
    holds = [start_run(api, sid, LONG)["id"] for _ in range(2)]  # one on each host
    try:
        on = {running_on(api, r)["host"]: r for r in holds}
        assert set(on) == {HOST, HOST_B}, on
        tiny = start_run(api, sid, TINY_RUN, "echo-tiny")
        ok(
            api.post(f"{STUDIO}/runs/{on[HOST_B]}/cancel"), 202
        )  # HOST_B is free now, but has no tiny
        wait_run(api, on[HOST_B])
        for _ in range(3):
            r = run_of(api, tiny["id"])
            assert (r["status"], r["host"]) == ("queued", HOST), r
            time.sleep(1)
        ok(api.post(f"{STUDIO}/runs/{on[HOST]}/cancel"), 202)
        run = wait_run(api, tiny["id"])
        assert (run["status"], run["host"]) == ("succeeded", HOST), run
    finally:
        for r in holds:
            api.post(f"{STUDIO}/runs/{r}/cancel")


def test_resident_host_is_preferred(admin, api, two):
    if state(admin, "tiny", HOST_B)["state"] != "present":
        r = admin.post(
            f"{ADMIN}/models/{MODEL}/download",
            json={"host": HOST_B, "variant": "tiny", "accept_licence": True},
        )
        assert r.status_code in (200, 202), r.text
        assert wait_job(admin, r.json()["id"])["state"] == "succeeded"
        wait_for(
            lambda: state(admin, "tiny", HOST_B)["state"] == "present",
            timeout=90,
            what=f"tiny present on {HOST_B}",
        )
    for h in (HOST, HOST_B):
        ok(admin.post(f"{ADMIN}/hosts/{h}/unload", json={"all": True}), 202)
        wait_for(lambda h=h: not residents(admin, h), timeout=60, what=f"nothing resident on {h}")
    first = tiny_run(api)["host"]
    assert residents(admin, first) and not residents(admin, other(first))
    # the other host is idle longer (or never ran): only the resident rule sends the next run back
    assert tiny_run(api)["host"] == first


def test_host_loss_retries_once_on_the_other_host(api, two):
    sid = new_session(api, "Contract hosts")["id"]
    rid = start_run(api, sid, LONG | {"echo.steps": 40})["id"]
    lost = running_on(api, rid)["host"]
    docker("kill", container(lost))
    try:
        run = wait_end(api, rid)
        assert (run["status"], run["attempt"], run["lost_host"]) == ("succeeded", 2, lost), run
        assert run["host"] == other(lost) and run["error"] is None, run
        take = take_of_run(api, rid)
        assert take["made_with"]["host"] == other(lost)
    finally:
        docker("start", container(lost))
        wait_live(api, lost)


def test_second_loss_fails_the_run(api, two):
    sid = new_session(api, "Contract hosts")["id"]
    rid = start_run(api, sid, LONG)["id"]
    first = running_on(api, rid)["host"]
    killed = [first]
    docker("kill", container(first))
    try:
        second = running_on(api, rid, attempt=2)["host"]
        assert second == other(first), second
        killed.append(second)
        docker("kill", container(second))
        run = wait_end(api, rid)
        assert (run["status"], run["attempt"], run["lost_host"]) == ("failed", 2, first), run
        assert (
            run["error"] == f"runner on host {second} stopped responding; gave up after 2 attempts"
        )
        assert run["outputs"] == [], run
    finally:
        for h in killed:
            docker("start", container(h))
        wait_live(api, *killed)


# A probe run inside HOST_B's runner container with that runner's own NATS credentials (NATS_URL_FILE, and
# NATS_CA_FILE when its URL is tls://): every subject of the other host must be refused, its own allowed.
PROBE = r"""
import asyncio, json, os, ssl, sys
import nats

me, them = sys.argv[1], sys.argv[2]


async def main():
    url = open(os.environ["NATS_URL_FILE"]).read().strip()
    ca = os.environ.get("NATS_CA_FILE")
    errors = []

    async def on_error(e):
        errors.append(str(e).lower())

    tls = {"tls": ssl.create_default_context(cafile=ca), "tls_handshake_first": True} if ca and url.startswith("tls://") else {}
    nc = await nats.connect(url, error_cb=on_error, allow_reconnect=False, **tls)
    probes = {
        "sub_unload": ("sub", f"runner.unload.{them}"),
        "sub_inbox": ("sub", f"_INBOX_runner_{them.replace('.', '_')}.x"),
        "pub_registry": ("pub", f"$KV.registry.{them}.probe"),
        "pub_cmd": ("pub", f"cmd.run.{them}"),
        "pub_dlq": ("pub", f"dlq.cmd.run.{them}"),
        "pub_model_evt": ("pub", f"evt.model.{them.split('.')[0]}.probe"),
        "pub_run_evt": ("pub", f"evt.run.{them.split('.')[0]}.probe.status"),  # run events: its own host only
        "pub_create": ("pub", f"$JS.API.CONSUMER.CREATE.CMD.runner-{them.replace('.', '-')}.cmd.run.{them}"),
        "pub_bare_create": ("pub", f"$JS.API.CONSUMER.CREATE.CMD.runner-{me.replace('.', '-')}"),
        "pub_durable_create": ("pub", f"$JS.API.CONSUMER.DURABLE.CREATE.CMD.runner-{me.replace('.', '-')}"),
        "pub_settings": ("pub", f"$JS.API.DIRECT.GET.KV_host_settings.$KV.host_settings.{them.split('.')[0]}"),
        "pub_read_registry": ("pub", f"$JS.API.DIRECT.GET.KV_registry.$KV.registry.{them}.workflows"),
        "pub_read_hosts": ("pub", f"$JS.API.DIRECT.GET.KV_hosts.$KV.hosts.{them}"),
        "pub_own_durable_their_filter": (
            "pub", f"$JS.API.CONSUMER.CREATE.CMD.runner-{me.replace('.', '-')}.cmd.run.{them}"),
        "own_sub_unload": ("sub", f"runner.unload.{me}"),
    }
    out = {}
    for name, (kind, subject) in probes.items():
        errors.clear()
        if kind == "sub":
            await nc.subscribe(subject)
        else:
            await nc.publish(subject, b"{}")
        await nc.flush()
        await asyncio.sleep(0.5)
        out[name] = any("permissions violation" in e and subject.lower() in e for e in errors)
    await nc.close()
    print(json.dumps(out))


asyncio.run(main())
"""


def in_runner_b(*args: str) -> str:
    out = subprocess.run(
        ["docker", "exec", "-i", container(HOST_B), *args],
        capture_output=True,
        timeout=60,
        check=False,
    )
    if out.returncode:
        raise RuntimeError(out.stderr.decode()[-300:])
    return out.stdout.decode()


def test_remote_host_user_is_confined_to_its_host(two):
    refused = json.loads(in_runner_b("python", "-c", PROBE, f"{HOST_B}.{ENV}", f"{HOST}.{ENV}"))
    assert refused.pop("own_sub_unload") is False, "its own unload subject must be allowed"
    assert all(refused.values()), refused  # every other host's subject: a permissions violation


# The link of HOST_B: with a tls:// NATS URL and an https:// S3 endpoint it verifies against the stack's CA and
# fails without it (a self-signed CA is not in the system store).
TLS = r"""
import asyncio, json, os, ssl, urllib.error, urllib.request
import nats

url = open(os.environ["NATS_URL_FILE"]).read().strip()
s3 = os.environ.get("S3_ENDPOINT", "")
out = {"nats_tls": url.startswith("tls://"), "s3_tls": s3.startswith("https://")}


async def nats_ok(ctx):
    try:
        nc = await nats.connect(url, tls=ctx, tls_handshake_first=True, allow_reconnect=False,
                                max_reconnect_attempts=0, connect_timeout=5)
        await nc.close()
        return True
    except Exception:
        return False


def s3_ok(ctx):
    try:
        urllib.request.urlopen(s3, context=ctx, timeout=5)
    except urllib.error.HTTPError:
        return True  # TLS held; S3 answered (403 without a signature)
    except Exception:
        return False
    return True


if out["nats_tls"]:
    ca = ssl.create_default_context(cafile=os.environ["NATS_CA_FILE"])
    out["nats_with_ca"] = asyncio.run(nats_ok(ca))
    out["nats_without_ca"] = asyncio.run(nats_ok(ssl.create_default_context()))
if out["s3_tls"]:
    out["s3_with_ca"] = s3_ok(ssl.create_default_context(cafile=os.environ["S3_CA_FILE"]))
    out["s3_without_ca"] = s3_ok(ssl.create_default_context())
print(json.dumps(out))
"""


def test_remote_host_link_is_tls_verified_by_the_stack_ca(two):
    r = json.loads(in_runner_b("python", "-c", TLS))
    if not r["nats_tls"]:
        pytest.skip(f"{HOST_B} connects to NATS without TLS on this stack")
    assert r["nats_with_ca"] and not r["nats_without_ca"], r
    if r["s3_tls"]:
        assert r["s3_with_ca"] and not r["s3_without_ca"], r


# HOST_B's own S3 identity (runner-<HOST_B>: the policy runner; contracts/README.md, Object store): get under u/ and
# the looks' thumbnails, put only under the prefixes a runner writes; delete, a put elsewhere, listing, tagging, a new
# bucket or another bucket no.
S3 = r"""
import json, os
import boto3
from botocore.config import Config

read = lambda n: open(os.environ[n]).read().strip()
s3 = boto3.client("s3", endpoint_url=os.environ["S3_ENDPOINT"], region_name="us-east-1",
                  aws_access_key_id=read("S3_ACCESS_KEY_ID_FILE"), aws_secret_access_key=read("S3_SECRET_ACCESS_KEY_FILE"),
                  verify=os.environ.get("S3_CA_FILE") or None,
                  config=Config(s3={"addressing_style": "path"}, retries={"max_attempts": 1}))
b, out = os.environ.get("S3_BUCKET", "helmstudio"), {}
u = "u/contract-hosts/"
def t(name, f):
    try:
        f()
        out[name] = True
    except Exception:
        out[name] = False
for prefix in ("runs/r1/1/", "exports/j1/", "frames/j1/", "llm/thumbs/"):
    t("put " + prefix, lambda: s3.put_object(Bucket=b, Key=u + prefix + "0.txt", Body=b"x"))
t("put look", lambda: s3.put_object(Bucket=b, Key="packs/contract-hosts/looks/a.txt", Body=b"x"))
t("get", lambda: s3.get_object(Bucket=b, Key=u + "runs/r1/1/0.txt")["Body"].read())
t("head", lambda: s3.head_object(Bucket=b, Key=u + "runs/r1/1/0.txt"))
t("get look", lambda: s3.get_object(Bucket=b, Key="packs/contract-hosts/looks/a.txt")["Body"].read())
t("put uploads", lambda: s3.put_object(Bucket=b, Key=u + "uploads/x.png", Body=b"x"))
t("put loras", lambda: s3.put_object(Bucket=b, Key=u + "loras/x.safetensors", Body=b"x"))
t("put outside u/", lambda: s3.put_object(Bucket=b, Key="contract-hosts/x.txt", Body=b"x"))
t("delete", lambda: s3.delete_object(Bucket=b, Key=u + "runs/r1/1/0.txt"))
t("list", lambda: s3.list_objects_v2(Bucket=b, MaxKeys=1))
t("tagging", lambda: s3.put_object_tagging(Bucket=b, Key=u + "runs/r1/1/0.txt", Tagging={"TagSet": [{"Key": "a", "Value": "b"}]}))
t("create_bucket", lambda: s3.create_bucket(Bucket="contract-hosts-other"))
out["buckets"] = [x["Name"] for x in s3.list_buckets()["Buckets"]]
print(json.dumps(out))
"""


def test_remote_host_s3_identity_is_limited_to_the_runner_prefixes(two):
    r = json.loads(in_runner_b("python", "-c", S3))
    allowed = ["put runs/r1/1/", "put exports/j1/", "put frames/j1/", "put llm/thumbs/", "put look", "get", "head",
               "get look"]  # fmt: skip
    refused = [
        "put uploads",
        "put loras",
        "put outside u/",
        "delete",
        "list",
        "tagging",
        "create_bucket",
    ]
    assert all(r[k] for k in allowed) and not any(r[k] for k in refused) and r["buckets"] == [], r


# HOST_B's NATS account (HOST_<HOST_B>: contracts/README.md, A remote GPU host: per-host limits) as seen from inside
# its runner with its own credentials: the server cuts off a message over 1 MB (a raw publish: the client would
# refuse it first) but reads one of the runner's registry budget (768 KB), a 9th connection of the account and a 101st
# subscription of one connection.
HEAD = r"""
import asyncio, contextlib, json, os, socket, ssl
from urllib.parse import urlparse
import nats

url = open(os.environ["NATS_URL_FILE"]).read().strip()
p = urlparse(url)
ca = os.environ.get("NATS_CA_FILE")
tls = url.startswith("tls://")
kw = {"tls": ssl.create_default_context(cafile=ca), "tls_handshake_first": True} if tls else {}
inbox = f"_INBOX_{p.username}"
out = {}
"""
LIMITS = r"""


def raw_publish(size, subject, body=False):
    s = socket.create_connection((p.hostname, p.port or 4222), timeout=10)
    if tls:
        s = ssl.create_default_context(cafile=ca).wrap_socket(s, server_hostname=p.hostname)
    f = s.makefile("rb")
    info = json.loads(f.readline()[5:])
    s.sendall(b"CONNECT " + json.dumps({"user": p.username, "pass": p.password, "verbose": False,
                                         "pedantic": False, "tls_required": tls}).encode() + b"\r\nPING\r\n")
    f.readline()  # PONG (or an -ERR)
    # too big: the PUB line alone (the server answers its size at once and closes; the bytes would meet a closed
    # socket); else the bytes too, then the server's first answer
    s.sendall(b"PUB %s %d\r\n" % (subject.encode(), size) + (b"x" * size + b"\r\nPING\r\n" if body else b""))
    try:
        return info["max_payload"], f.readline().decode().strip()
    finally:
        s.close()


async def main():
    nc = await nats.connect(url, allow_reconnect=False, inbox_prefix=inbox, **kw)
    for n in (99, 101):  # past the cap the server closes the connection (nats-py shows no -ERR text for it)
        sub_nc = await nats.connect(url, allow_reconnect=False, inbox_prefix=inbox, **kw)
        for i in range(n):
            await sub_nc.subscribe(f"{inbox}.limits.{i}")
        with contextlib.suppress(Exception):
            await sub_nc.flush(timeout=3)
        await asyncio.sleep(1)
        out[f"connected_with_{n}_subs"] = sub_nc.is_connected
        if sub_nc.is_connected:
            await sub_nc.close()

    extra, refused = [], ""
    for _ in range(9):
        try:
            extra.append(await nats.connect(url, allow_reconnect=False, max_reconnect_attempts=0,
                                            connect_timeout=5, inbox_prefix=inbox, **kw))
        except Exception as e:
            refused = str(e).lower()
            break
    out["connections_opened"], out["connections_refused"] = len(extra) + 1, refused
    for c in extra:
        await c.close()
    await nc.close()


asyncio.run(main())
out["raw"] = raw_publish(1024 * 1024 + 1, f"evt.run.{os.environ['RUNNER_HOST']}.probe.status")
# a subject outside its permissions, so nothing is stored: refused for the subject, not the size
out["fits"] = raw_publish(768 * 1024, "probe.limits", body=True)[1]
print(json.dumps(out))
"""


def test_remote_host_account_limits_cut_off_a_flood(two):
    r = json.loads(in_runner_b("python", "-c", HEAD + LIMITS))
    # one number: the account's max_payload is the server's 1 MB, which the runner's registry budget fits in
    assert r["raw"] == [1024 * 1024, "-ERR 'Maximum Payload Violation'"], r
    assert r["fits"].startswith("-ERR 'Permissions Violation"), r
    assert r["connected_with_99_subs"] and not r["connected_with_101_subs"], r
    # the runner holds one of the 8 connections (a probe or a reconnect may hold another for a moment)
    assert (
        r["connections_opened"] <= 8
        and "maximum account active connections" in r["connections_refused"]
    ), r


# The consumers a host creates live in HELM, outside its account's caps: KV_cancels caps them (.tools/nats-setup). A
# flood of durable consumers (no inactive threshold: they would stay) through the host's own CREATE export stops at
# the cap; the probe deletes what it made through the DELETE export.
CONSUMERS = r"""

async def main():
    nc = await nats.connect(url, allow_reconnect=False, inbox_prefix=inbox, **kw)
    api = lambda s, body=b"": nc.request(s, body, timeout=5)
    made, refused = [], ""
    try:
        for i in range(1100):
            name = f"flood-probe-{i}"
            cfg = {"stream_name": "KV_cancels", "config": {"durable_name": name, "name": name,
                   "filter_subject": "$KV.cancels.flood-probe", "ack_policy": "explicit"}}
            r = json.loads((await api(f"$JS.API.CONSUMER.CREATE.KV_cancels.{name}.$KV.cancels.flood-probe",
                                      json.dumps(cfg).encode())).data)
            if "error" in r:
                refused = r["error"]["description"]
                break
            made.append(name)
        info = json.loads((await api("$JS.API.STREAM.INFO.KV_cancels")).data)
        out["count"], out["cap"] = info["state"]["consumer_count"], info["config"]["max_consumers"]
    finally:
        for name in made:
            await api(f"$JS.API.CONSUMER.DELETE.KV_cancels.{name}")
        await nc.close()
    out["made"], out["refused"] = len(made), refused.lower()


asyncio.run(main())
print(json.dumps(out))
"""


def test_remote_host_consumer_flood_stops_at_the_cap(two):
    r = json.loads(in_runner_b("python", "-c", HEAD + CONSUMERS))
    assert r["cap"] == 1000 and r["count"] == 1000 and 0 < r["made"] <= 1000, r
    assert "maximum consumers" in r["refused"], r
