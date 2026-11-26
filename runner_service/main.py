"""runner_service: `python main.py`. RUNNER_HOST / RUNNER_ENV say which runner: fake/fake (the compose fake
runner, the echo pack), mac/torch, mac/mlx or mac/native (`make runners`: the torch packs, the mlx packs, the native
packs: a stage runs a child process, core.child; core deps only, no torch or MLX).

Loads the packs of its env, indexes the host's shared LoRAs, stores the starter looks' thumbnails and voices' previews, checks and publishes its registry values, keeps its hosts key fresh (with memory and
resident components when it has a residency manager), then pulls cmd.run.<host>.<env> from CMD (durable
runner-<host>-<env>) and runs one command at a time (consumer.Consumer). SIGINT/SIGTERM stop pulling, stop the
current run at its next cancel point (reported failed), unload every component, delete the hosts key and exit.
It also answers runner.unload.<host>.<env> (contracts/README.md, Two envs on one host: the hand-over; Hosts
admin): idle, it unloads the components studio names (everything when it names none). Beside the runs it pulls
cmd.model.<host>.<env> (durable models-<host>-<env>: downloads and deletes, messaging.models), follows KV
host_settings key <host> (messaging.settings) and rescans its models root and LoRAs every RESCAN seconds. With ffmpeg
(core.media.detect: the hosts key's `media`) it also pulls cmd.media.<host>.<env> (durable media-<host>-<env>:
exports and frames, messaging.media), never in the GPU loop. With RUNNER_PLUGINS on (one runner per host) it keeps
this host's plugins that start themselves running (messaging.plugin_runs) and stops them when it stops.

The hosts key also carries the `device` (API 1.18) and, on CUDA, `host_ram`; the budget is cut from the device's
memory (VRAM on CUDA). A remote GPU host connects over TLS (NATS_CA_FILE, S3_CA_FILE: contracts/README.md "A remote
GPU host"); every consumer is created through the filtered CREATE subject (a name and a filter: nats-py's
add_consumer), host_settings is read by direct get."""

import asyncio
import contextlib
import importlib
import json
import logging
import signal
import ssl
import time

import nats
import nats.errors
from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy

import config
import health
from config import ENV, HOST, INBOX_PREFIX, NATS_URL
from consumer import ACK_WAIT, Consumer
from core import child, download, looks, lora, media, models, registry, voices
from core.graph import GPU
from core.residency import POLICIES, Residency, host_device, ram_gb
from messaging import media as media_jobs
from messaging import models as model_jobs
from messaging import plugin_runs, schemas
from messaging.hosts import Host
from messaging.settings import Settings
from storage import Store

log = logging.getLogger("runner")

DURABLE = f"runner-{HOST}-{ENV}"
SUBJECT = f"cmd.run.{HOST}.{ENV}"
IDLE_CHECK = 30  # seconds between keep_warm idle checks
RESCAN = 60  # seconds between rescans of the models root and the shared LoRAs
UNLOAD = "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json"


def residency_for(packs) -> tuple[Residency | None, str, object]:
    """(the residency manager, the device, the loader module) when a loaded pack has weight components, else
    (None, "cpu", None). The budget is cut from the device's memory (CUDA: VRAM, else RAM)."""
    runtimes = {p.runtime for p in packs if p.spec.get("components")}
    if not runtimes:
        return None, "cpu", None
    if len(runtimes) > 1:
        raise ValueError(f"packs of one env use several runtimes: {sorted(runtimes)}")
    loader = importlib.import_module(f"core.loaders_{runtimes.pop()}")
    if config.AFTER_RUN not in POLICIES:
        raise ValueError(f"RUNNER_AFTER_RUN must be one of {POLICIES}")
    dev = loader.device()
    if dev == "cuda" and hasattr(loader, "context_gb"):  # native on CUDA: none, budget is RAM
        loader.context_gb()  # the CUDA context's own memory, measured once before any tensor
    total = loader.total_gb()
    res = Residency(  # a reserve above the device's memory leaves none, never a negative budget
        max(0.0, total - config.RESERVE_GB),
        free=loader.free,
        measure=loader.measure_gb,
        pins=config.PINS,
    )
    res.total_gb = total
    return res, dev, loader


def tls_context() -> ssl.SSLContext | None:
    """The NATS client's TLS: verify against NATS_CA_FILE, with NATS_CERT_FILE / NATS_KEY_FILE as a client cert;
    None = nats-py's default (a tls:// URL then verifies against the system CAs)."""
    if not (config.NATS_CA_FILE or config.NATS_CERT_FILE):
        return None
    ctx = ssl.create_default_context(cafile=config.NATS_CA_FILE or None)
    if config.NATS_CERT_FILE:
        ctx.load_cert_chain(config.NATS_CERT_FILE, config.NATS_KEY_FILE or None)
    return ctx


async def idle_unload(consumer: Consumer, host: Host, stop: asyncio.Event) -> None:
    """keep_warm: unload all but pinned components once the runner has been idle `consumer.idle_unload_s`
    (RUNNER_IDLE_UNLOAD_S or the host settings; 0 = never)."""
    res, loop = consumer.residency, asyncio.get_running_loop()
    while not stop.is_set():
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(stop.wait(), IDLE_CHECK)
        idle, limit = time.monotonic() - consumer.idle_since, consumer.idle_unload_s
        if (
            consumer.ctx is None
            and limit
            and idle > limit
            and any(not res.pinned(k) for k in list(res.entries))
        ):
            log.info("idle %.0f s: unloading", idle)
            await loop.run_in_executor(GPU, res.unload, True)
            await host.put()


async def unload_reply(consumer: Consumer, host: Host, request: dict) -> dict:
    """runner.unload, idle: unload exactly the named `components` it holds (pinned ones too: a last resort);
    none named (by hand, or the admin's Unload all): every component, pinned ones too, and cached handles;
    `cached_only`: the cached node results and the device cache, every entry stays. Then free the device memory,
    rewrite the hosts key and reply {unloaded_gb, freed, resident} (resident: what the key now lists). A run in
    progress → busy; a request off-contract or a failure → internal. A runner without weights (fake) only drops
    its cached node results."""
    log.info("unload asked for env %s (run %s): %s", request.get("for_env"), request.get("run_id"),
             request.get("components", "everything"))  # fmt: skip
    if consumer.ctx is not None:
        return {"error": "a run is in progress", "code": "busy"}
    if errs := schemas.errors(UNLOAD + "#/$defs/request", request):
        log.error("unload request off-contract: %s", "; ".join(errs[:3]))
        return {"error": "the unload request is off-contract", "code": "internal"}
    res, loop = consumer.residency, asyncio.get_running_loop()
    if res is None or request.get("cached_only"):
        consumer.cache.clear()
        if res is not None:
            await loop.run_in_executor(GPU, res.free)
        value = await host.put()
        resident = (value or {}).get("resident") or (res.state()["resident"] if res else [])
        return {"unloaded_gb": 0, "freed": [], "resident": resident}
    try:
        if "components" in request:
            freed, gb = await loop.run_in_executor(GPU, res.unload_named, request["components"])
            if freed:  # another env needs the memory: cached node results go too (they are not in `resident`)
                consumer.cache.clear()
        else:
            gb = res.used_gb
            freed = await loop.run_in_executor(GPU, res.unload)
            consumer.cache.clear()
        await loop.run_in_executor(GPU, res.free)
        value = await host.put()
    except Exception:
        log.exception("unload failed")
        return {"error": "unloading failed", "code": "internal"}
    resident = value["resident"] if value else res.state()["resident"]
    return {"unloaded_gb": round(gb, 2), "freed": freed, "resident": resident}


async def main() -> None:
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s"
    )
    if why := config.plain_with_ca():
        raise SystemExit(why)
    download.environment(config.CACHE)  # before huggingface_hub is imported
    await asyncio.to_thread(child.sweep)  # a child (h3) a killed runner of this env left running
    models.hashes = models.Hashes(config.CACHE / f"model-hashes-{ENV}.json")
    models.hashes.start()  # hashes what no sidecar records, in the background
    values, impls, packs = (
        registry.build()
    )  # an off-contract pack stops the runner before it connects
    residency, device, loader = residency_for(packs)
    machine = models.machine(device, ram_gb())
    store = Store()
    library = lora.Library(  # None: the loras/ of every connected model folder (API 1.78)
        None, config.CACHE, store=store, host=HOST, cache_gb=config.LORA_CACHE_GB
    )
    blocks = registry.lora_blocks(packs)
    values["loras"] = registry.scanned_value("loras", await asyncio.to_thread(library.scan, blocks))
    registry.fill_prefused(values, packs, library.files)
    values["looks"] = registry.scanned_value("looks", looks.entries(packs, library, config.MODELS))
    await looks.put_thumbs(packs, store)
    await voices.put_previews(packs, store)
    connected = False

    async def nats_error(e: Exception) -> None:
        """A TLS failure before the first connect stops the start: a cert that does not verify, or (M6) a server
        that fell back to plain before our ClientHello arrived (`WRONG_VERSION_NUMBER`, a slow link): nats-py would
        otherwise hang in connect forever, unhealthy; exiting lets the container's restart policy try again."""
        if not connected and isinstance(e, ssl.SSLError):
            raise e
        log.warning("NATS: %s", e)

    nc = await nats.connect(
        NATS_URL,
        name=DURABLE,
        max_reconnect_attempts=-1,
        inbox_prefix=INBOX_PREFIX,
        tls=tls_context(),
        # the server allows plain clients too (allow_non_tls clears tls_required): without the handshake first,
        # nats-py would send the password in clear on a tls:// URL
        tls_handshake_first=NATS_URL.startswith("tls://"),
        error_cb=nats_error,
    )
    connected = True
    js = nc.jetstream()
    await registry.publish(js, values)
    settings: Settings | None = None
    can_media = await asyncio.to_thread(media.detect)  # ffmpeg here: this env also runs media jobs

    def extra() -> dict:
        """The hosts key beyond idle/busy: memory and resident (with weights), disk, machine, token, policy."""
        tok = config.HF_TOKEN_FILE
        out = residency.state() if residency else {}
        out["device"] = loader.device_info() if loader else host_device("cpu")
        if device == "cuda":  # host RAM is the second limit
            out["host_ram"] = loader.host_ram()
        return out | {"models_disk": models.disk(models.packs.downloads_root()), "models_disks": models.disks(), "machine": machine | {"gpu_limit_gb": settings.gpu_limit_gb},
                      "hub_token": tok.is_file() and tok.stat().st_size > 0,
                      "policy": settings.policy()} | ({"media": can_media} if can_media else {})  # fmt: skip

    host = Host(await js.key_value("hosts"), extra=extra)
    consumer = Consumer(
        js=js,
        store=store,
        cancels_kv=await js.key_value("cancels"),
        host=host,
        impls=impls,
        node_schemas=registry.node_schemas(values),
        residency=residency,
        device=device,
        after_run=config.AFTER_RUN,
        loras=library,
    )
    consumer.idle_unload_s = config.IDLE_UNLOAD_S
    settings = Settings(consumer, host)
    node_schemas = registry.node_schemas(values)
    scan_lock = asyncio.Lock()

    async def rescan() -> None:
        """Rebuild the loras and models values from disk; publish those that changed; rewrite the hosts key."""
        async with scan_lock:
            loras = registry.scanned_value("loras", await asyncio.to_thread(library.scan, blocks))
            new = {"loras": loras, "models": await asyncio.to_thread(
                registry.models_value, packs, impls, node_schemas)}  # fmt: skip
            registry.fill_prefused(new, packs, library.files)
            if changed := {k: v for k, v in new.items() if registry.changed(values[k], v)}:
                values.update(changed)
                await registry.publish(js, changed)
            await host.put()

    settings.rescan = rescan  # API 1.78: a new folder list rescans at once

    async def rescans(stop: asyncio.Event) -> None:
        while not stop.is_set():
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), RESCAN)
            if not stop.is_set():
                try:
                    await rescan()
                except Exception:
                    log.exception("rescan failed")

    # create or update: a changed config here replaces the old one on the server
    await js.add_consumer(
        "CMD",
        ConsumerConfig(
            name=DURABLE,
            durable_name=DURABLE,
            filter_subject=SUBJECT,
            ack_policy=AckPolicy.EXPLICIT,
            ack_wait=ACK_WAIT,
            max_deliver=1,  # a run is retried by studio's dispatcher, never by redelivery
            max_ack_pending=1,
            deliver_policy=DeliverPolicy.ALL,
        ),
    )
    sub = await js.pull_subscribe_bind(durable=DURABLE, stream="CMD")
    await js.add_consumer(
        "CMD",
        ConsumerConfig(
            name=model_jobs.DURABLE,
            durable_name=model_jobs.DURABLE,
            filter_subject=model_jobs.SUBJECT,
            ack_policy=AckPolicy.EXPLICIT,
            ack_wait=model_jobs.ACK_WAIT,
            max_deliver=10,  # a restart redelivers (the download resumes); a terminal status acks it
            max_ack_pending=1,
            deliver_policy=DeliverPolicy.ALL,
        ),
    )
    jobs_sub = await js.pull_subscribe_bind(durable=model_jobs.DURABLE, stream="CMD")
    jobs = model_jobs.ModelJobs(
        js=js, cancels_kv=consumer.cancels_kv, packs_=packs, library=library, consumer=consumer,
        rescan=rescan, cache=config.CACHE, endpoint=config.HF_ENDPOINT, token_file=config.HF_TOKEN_FILE,
    )  # fmt: skip

    stop = asyncio.Event()
    side_jobs = [model_jobs.pull(jobs_sub, jobs, stop)]
    if can_media:  # exports and frames: ffmpeg, never the GPU loop
        await js.add_consumer("CMD", ConsumerConfig(
            name=media_jobs.DURABLE, durable_name=media_jobs.DURABLE, filter_subject=media_jobs.SUBJECT,
            ack_policy=AckPolicy.EXPLICIT, ack_wait=media_jobs.ACK_WAIT, max_deliver=3, max_ack_pending=1,
            deliver_policy=DeliverPolicy.ALL))  # fmt: skip
        media_sub = await js.pull_subscribe_bind(durable=media_jobs.DURABLE, stream="CMD")
        mjobs = media_jobs.MediaJobs(js=js, cancels_kv=consumer.cancels_kv, store=store,
                                     encoders=can_media["encoders"])  # fmt: skip
        side_jobs.append(model_jobs.pull(media_sub, mjobs, stop))
        log.info("media jobs on %s (ffmpeg %s, %s)", media_jobs.SUBJECT, can_media["ffmpeg"],
                 ", ".join(can_media["encoders"]))  # fmt: skip

    def on_signal() -> None:
        stop.set()
        consumer.stopping = True

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, on_signal)

    async def on_unload(msg) -> None:
        try:
            request = json.loads(msg.data or b"{}")
        except ValueError:
            request = {}
        await msg.respond(json.dumps(await unload_reply(consumer, host, request)).encode())

    await nc.subscribe(f"runner.unload.{HOST}.{ENV}", cb=on_unload)
    beat = asyncio.create_task(host.beat(stop))
    idle = asyncio.create_task(idle_unload(consumer, host, stop)) if residency else None
    side = [asyncio.create_task(t) for t in (*side_jobs, settings.watch(js, stop), rescans(stop))]
    plugins = None
    if config.PLUGINS:  # this host's plugins that start themselves (API 1.67)
        plugins_kv = await js.key_value("registry")

        async def put_plugins(value: dict) -> None:
            await plugins_kv.put(plugin_runs.KEY, json.dumps(value).encode())

        plugins = plugin_runs.Supervisor(config.PLUGINS_DIRS, config.HELM_HOME, put_plugins)
        side.append(asyncio.create_task(plugins.watch(js, stop)))

        # API 1.73: a plugin's key from its file in HELM_HOME (req.runner.plugins.key), never logged
        async def on_key(msg) -> None:
            try:
                name = json.loads(msg.data or b"{}").get("plugin")
            except ValueError, AttributeError:
                name = None
            reply = plugin_runs.host_key(config.PLUGINS_DIRS, config.HELM_HOME, name)
            log.info("plugin key asked for %s: %s", str(name)[:40], reply.get("code", "sent"))
            await msg.respond(json.dumps(reply).encode())

        await nc.subscribe(f"runner.plugins.key.{HOST}", cb=on_key)

        # API 1.78: what each model folder path is here now (req.runner.folders.check), before studio stores a list
        async def on_folders(msg) -> None:
            try:
                paths = json.loads(msg.data or b"{}").get("paths")
            except ValueError, AttributeError:
                paths = None
            await msg.respond(
                json.dumps(await asyncio.to_thread(models.check_folders, paths)).encode()
            )

        await nc.subscribe(f"runner.folders.check.{HOST}", cb=on_folders)
    server = await health.serve(nc)
    log.info("runner %s.%s ready on %s (device %s)", HOST, ENV, SUBJECT, device)
    try:
        while not stop.is_set():
            try:
                msgs = await sub.fetch(1, timeout=2)
            except nats.errors.TimeoutError:
                continue
            except Exception:
                log.exception("fetch failed; retrying")
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), 2)
                continue
            for m in msgs:
                await consumer.handle(m)
                await settings.run_ended()
    finally:
        log.info("shutting down")
        server.close()
        stop.set()
        await beat
        for t in side:  # a running model job stops at its next chunk; its command is redelivered
            t.cancel()
            with contextlib.suppress(BaseException):
                await t
        if idle:
            await idle
        if plugins:  # the plugins stop with the runner (TERM, then KILL)
            await plugins.close()
        if residency:
            await loop.run_in_executor(GPU, residency.unload)
        await host.offline()
        # close, not drain: every publish was awaited (JetStream acks); draining the pull subscription took 30 s
        await nc.close()


if __name__ == "__main__":
    asyncio.run(main())
