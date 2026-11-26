import json

import pytest
from conftest import CONTRACTS, GEN, IMPLS, VALUES, FakeKV

from core import registry
from core.graph import workflow_sound
from core.residency import Residency
from messaging import schemas
from messaging.hosts import KEY, Host
from messaging.publisher import RunEvents


def test_fake_registry_values_are_on_contract_and_the_canonical_fixtures():
    for kind, value in VALUES.items():
        assert schemas.registry_errors(kind, value) == []
        fixture = json.loads((CONTRACTS / f"fixtures/registry/{kind}.valid.json").read_text())
        strip = {"published_at", "published_by"}
        assert {k: v for k, v in value.items() if k not in strip} == {
            k: v for k, v in fixture.items() if k not in strip
        }


def test_every_published_node_kind_has_an_implementation():
    assert registry.node_schemas(VALUES).keys() == IMPLS.keys()


SOUND = {
    "ace-step": "music",
    "minimax-music3": "music",
    "stable-audio-3": "sound",
    "mmaudio": "sound",
}


def test_torch_registry_joins_the_four_music_and_sound_families():
    """Plan 38 P3: one torch runner publishes ACE-Step, MiniMax Music 3, Stable Audio 3 and MMAudio together, on
    contract; each family's model is its canonical fixture's (the variants' sizes are the host's: the packs' own tests)
    and every workflow of it is of the model's class (MMAudio's video workflow too: sound)."""
    pytest.importorskip("torch")
    values, impls, _ = registry.build("torch")
    for kind, value in values.items():
        assert schemas.registry_errors(kind, value) == [], kind
    assert registry.node_schemas(values).keys() == impls.keys()
    nodes = registry.node_schemas(values)
    for fam, cls in SOUND.items():
        (got,) = [m for m in values["models"]["models"] if m["family"] == fam]
        (want,) = json.loads(
            (CONTRACTS / f"fixtures/registry/{fam}.models.valid.json").read_text()
        )["models"]
        top = ("id", "family", "title", "media", "runtimes", "sound")
        assert {k: got.get(k) for k in top} == {k: want.get(k) for k in top} and got["sound"] == cls
        wfs = [w for w in values["workflows"]["workflows"] if w["family"] == fam]
        assert wfs and {w["id"]: workflow_sound(w["nodes"], nodes) for w in wfs} == {
            w["id"]: cls for w in wfs
        }


@pytest.mark.skipif(not CONTRACTS.is_dir(), reason="contracts/ is not next to the service")
def test_gen_matches_contracts():
    """`make gen` was run after the last contract change."""
    pairs = [(p, GEN / "messages" / p.name) for p in CONTRACTS.glob("messages/*.schema.json")]
    pairs.append((CONTRACTS / "registry.schema.json", GEN / "registry.schema.json"))
    for src, copy in pairs:
        assert copy.read_bytes() == src.read_bytes(), f"{copy} is stale: run make gen"


async def test_host_value_is_on_contract_idle_busy_and_offline():
    host = Host(FakeKV())
    await host.put()
    assert json.loads(host.kv.data[KEY])["state"] == "idle"
    host.run_id, host.attempt = "3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b35", 2
    await host.put()
    value = json.loads(host.kv.data[KEY])
    assert value["state"] == "busy" and value["run_id"] == host.run_id and value["attempt"] == 2
    assert "memory" not in value  # the fake runner has no residency manager
    assert schemas.registry_errors("host", value) == []
    await host.offline()
    assert KEY not in host.kv.data


async def test_host_value_carries_memory_and_resident():
    res = Residency(52, pins={"vae"})
    res.get(("qwen-image-2.1", "turbo", "transformer", "int8"), 7.4, 0, object)
    res.get(("qwen-image-2.1", "base", "vae", "bf16"), 1.3, 0, object)
    res.done()
    host = Host(FakeKV(), extra=res.state)
    await host.put()
    value = json.loads(host.kv.data[KEY])
    assert schemas.registry_errors("host", value) == []
    assert value["memory"] == {"budget_gb": 52, "used_gb": 8.7}
    assert [(r["component"], r["variant"], r["pinned"]) for r in value["resident"]] == [
        ("transformer", "turbo", False),
        ("vae", "base", True),
    ]


def test_off_contract_event_is_refused_without_using_a_seq():
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.run.execute.valid.json").read_text())
    events = RunEvents(None, cmd)
    with pytest.raises(ValueError, match="off-contract"):
        events.build("status", {"status": "failed"})  # failed needs an error
    assert events.build("status", {"status": "running"})["data"]["seq"] == 1


async def test_attempt_2_events_carry_it_and_restart_seq():
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.run.execute.valid.json").read_text())
    cmd["data"]["attempt"] = 2
    sent = []

    class JS:
        async def publish(self, subject, body, headers):
            sent.append(headers["Nats-Msg-Id"])

    msg = await RunEvents(JS(), cmd).status("running")
    assert msg["data"]["attempt"] == 2 and msg["data"]["seq"] == 1
    assert sent == [f"{cmd['job_id']}:2:1"]
