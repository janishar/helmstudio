"""Ask runs without a GPU (contracts/README.md, "The llm pack and agent nodes"): core.ask's thumbnails, keyframes,
schema check and one more try; the fake echo/ask end to end through the consumer (the answer on the terminal status,
on contract; Media takes read and checked; a repeat is a cache hit); text workflows' memory plans."""

import hashlib
import io
import json
import shutil

import pytest
from conftest import FakeJS, FakeMsg, command, consumer
from PIL import Image

from core import ask, images
from core.graph import RunError
from messaging import schemas
from packs.echo import mp4

OWNER = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
SCHEMA = json.dumps({"type": "object", "required": ["pick"], "additionalProperties": False,
                     "properties": {"pick": {"type": "integer", "minimum": 1}}})  # fmt: skip


class Ctx:
    """What core.ask sees of a run: cancel points, progress, the owner, a store."""

    node, owner = "ask", OWNER

    def __init__(self) -> None:
        self.stored: dict[str, bytes] = {}

    def check(self) -> None:
        pass

    def progress(self, *a) -> None:
        pass

    def put(self, key: str, data: bytes, content_type: str) -> None:
        self.stored[key] = data


def params(**kw) -> dict:
    return {
        "system": "",
        "prompt": "Which one?",
        "media": [],
        "schema": SCHEMA,
        "max_tokens": 800,
    } | kw


def scripted(*answers):
    """A generate() that answers in turn and records the prompts it got."""
    seen = []

    def generate(prompt, thumbs):
        seen.append(prompt)
        return answers[len(seen) - 1], 10, 3

    return generate, seen


@pytest.mark.parametrize(
    ("size", "mode", "want"),
    [((1536, 1024), "RGBA", (1024, 672)), ((800, 600), "RGB", (800, 576)), ((100, 50), "RGB", (384, 192)),
     ((4000, 40), "L", (1024, 32))],
)  # fmt: skip
def test_thumbnails_are_on_the_32_grid_at_most_1024_and_over_white(size, mode, want):
    img = Image.new(mode, size, 0)
    thumb = ask.thumbnail(img)
    assert thumb.size == want and thumb.mode == "RGB"
    assert thumb.width % 32 == thumb.height % 32 == 0 and max(thumb.size) <= 1024
    if mode == "RGBA":  # fully transparent → white
        assert thumb.getpixel((10, 10)) == (255, 255, 255)


def test_thumbnails_are_stored_once_by_content_and_listed_in_order():
    ctx, red = Ctx(), Image.new("RGB", (512, 512), "red")
    media = [{"take": {"take_id": "t1", "image": red}}, {"take": {"take_id": "t2", "image": red}}]
    thumbs, seen = ask.looked_at(ctx, media)
    assert [s["take_id"] for s in seen] == ["t1", "t2"] and len(thumbs) == 2
    (key,) = ctx.stored
    assert (
        seen[0]["key"] == seen[1]["key"] == key == f"u/{OWNER}/llm/thumbs/{seen[0]['sha256']}.jpg"
    )
    assert hashlib.sha256(ctx.stored[key]).hexdigest() == seen[0]["sha256"]
    assert (seen[0]["width"], seen[0]["height"], seen[0]["frame"]) == (512, 512, None)


@pytest.mark.skipif(not shutil.which("ffprobe"), reason="ffmpeg is not on PATH")
def test_a_video_take_is_three_keyframes_at_the_span_centres():
    clip = mp4.mp4(Image.new("RGB", (256, 128), "blue"), 25, 24)
    frames = ask.keyframes(clip, "t")
    assert [f for f, _ in frames] == [4, 12, 20] and frames[0][1].size == (256, 128)
    with pytest.raises(RunError, match="input video t is not a readable video"):
        ask.keyframes(b"not a video", "t")


@pytest.mark.parametrize(
    ("text", "why"),
    [('{"pick": 2}', None), ('```json\n{"pick": 2}\n```', None), ('{"pick": 0}', "0 is less than the minimum of 1"),
     ("no json", "Expecting value: line 1 column 1 (char 0)"), ('{"pick": 1, "x": 1}', "Additional properties")],
)  # fmt: skip
def test_the_answer_is_its_outermost_object_validated(text, why):
    got = ask.problem(text, ask.validator(Ctx(), SCHEMA))
    assert got is None if why is None else why in got


def test_a_bad_answer_is_asked_once_more_in_the_same_run():
    generate, prompts = scripted("nope", '{"pick": 3}')
    out = ask.ask(Ctx(), params(), generate)["answer"]
    assert out == '{"pick": 3}' and len(prompts) == 2
    assert prompts[0].endswith(ask.SCHEMA_HINT.format(SCHEMA))
    assert prompts[1].endswith("Your answer was invalid: Expecting value: line 1 column 1 (char 0). "
                               "Answer again with JSON only.")  # fmt: skip
    r = out.record
    assert [t["error"] for t in r["tries"]] == [
        "answer did not match the schema: Expecting value: line 1 column 1 (char 0)", None]  # fmt: skip
    assert (r["tokens_in"], r["tokens_out"], r["seen"]) == (20, 6, [])


def test_two_bad_answers_fail_the_run_and_keep_both_tries():
    generate, _ = scripted('{"pick": 0}', "{}")
    with pytest.raises(ask.AnswerError) as e:
        ask.ask(Ctx(), params(), generate)
    assert str(e.value) == "answer did not match the schema: 'pick' is a required property"
    assert len(e.value.answer["tries"]) == 2 and all(t["error"] for t in e.value.answer["tries"])


def test_plain_text_needs_no_schema_and_a_bad_schema_fails_before_any_call():
    generate, prompts = scripted("A lighthouse at dusk.")
    assert ask.ask(Ctx(), params(schema=""), generate)["answer"] == "A lighthouse at dusk."
    assert prompts == ["Which one?"]
    with pytest.raises(RunError, match=r"ask.schema: not a JSON schema"):
        ask.ask(Ctx(), params(schema='{"type": 5}'), generate)


# --- the fake echo/ask through the consumer


def png(color: str) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", (64, 48), color).save(buf, "PNG")
    return buf.getvalue()


def ask_command(prompt: str, schema: str = SCHEMA, media=()) -> dict:
    cmd = command()
    cmd["data"]["workflow"] = {"id": "echo-ask", "nodes": {
        "ask": {"kind": "echo/ask", "params": params(prompt=prompt, schema=schema, media=list(media))},
        "answer": {"kind": "answer", "in": {"text": "ask.answer"}, "params": {}}}}  # fmt: skip
    return cmd


def take(n: int, data: bytes, content_type: str = "image/png") -> dict:
    return {"take": {"take_id": f"9b2f4c1a-6e3d-4a8b-b7c2-1d5e8f0a3b6{n}",
                     "key": f"u/{OWNER}/runs/3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b32/1/{n}.png",
                     "content_type": content_type, "width": 64, "height": 48, "bytes": len(data),
                     "etag": hashlib.md5(data).hexdigest()}}  # fmt: skip


async def run(cmd, c=None):
    js = FakeJS()
    c = c or consumer(js)
    c.js = js
    await c.handle(FakeMsg(cmd, js))
    for e in js.events():
        assert schemas.message_errors(e) == []
    return js.events()[-1]["data"], c


async def test_the_fake_answers_what_the_prompt_scripts_on_the_terminal_status():
    end, _ = await run(ask_command('Pick. ECHO-ANSWER: {"pick": 2} (the take to keep)'))
    assert end["status"] == "succeeded"
    assert end["answer"]["tries"] == [{"text": '{"pick":2}', "error": None}]
    assert (end["answer"]["seen"], end["answer"]["tokens_in"], end["answer"]["seconds"]) == (
        [],
        0,
        0,
    )
    end, _ = await run(ask_command("Say something.", schema=""))
    assert end["answer"]["tries"][0]["text"] == "echo"
    end, _ = await run(ask_command('ECHO-ANSWER: "a JSON string is its text"', schema=""))
    assert end["answer"]["tries"][0]["text"] == "a JSON string is its text"


async def test_a_bad_scripted_answer_fails_twice_and_a_retry_script_passes_the_second_try():
    end, _ = await run(ask_command('ECHO-ANSWER: {"pick": 0}'))
    assert end["status"] == "failed" and end["error"].startswith(
        "answer did not match the schema: 0 is less"
    )
    assert len(end["answer"]["tries"]) == 2
    end, _ = await run(ask_command('ECHO-ANSWER: {"pick": 0} ECHO-RETRY: {"pick": 1}'))
    assert end["status"] == "succeeded"
    assert [t["text"] for t in end["answer"]["tries"]] == ['{"pick":0}', '{"pick":1}']


async def test_media_takes_are_read_and_checked_and_a_repeat_is_a_cache_hit():
    data = png("red")
    cmd = ask_command(
        'ECHO-ANSWER: {"pick": 1}', media=[take(0, data), take(1, b"\0" * 10, "video/mp4")]
    )
    c = consumer(FakeJS())
    c.store.objects[cmd["data"]["workflow"]["nodes"]["ask"]["params"]["media"][0]["take"]["key"]] = (data, "image/png")  # fmt: skip
    end, _ = await run(cmd, c)
    assert end["status"] == "failed" and "is gone" in end["error"]  # the video's object is missing
    cmd["data"]["workflow"]["nodes"]["ask"]["params"]["media"].pop()
    end, _ = await run(cmd, c)
    reads = len(c.store.reads)
    assert end["status"] == "succeeded"
    end, _ = await run(cmd, c)
    assert end["status"] == "succeeded" and len(c.store.reads) == reads  # cached: not read again
    assert end["answer"]["tries"] == [{"text": '{"pick":1}', "error": None}]


async def test_a_video_media_value_reaches_the_stage_as_its_bytes():
    clip = b"\0\0\0\x18ftypisom" + b"\0" * 20
    value = take(0, clip, "video/mp4")["take"]

    async def fetch(key, limit, etag):
        return clip

    spec = [{"name": "media", "type": "rows", "item": {"take": "Media"}}]
    out = await images.load(spec, {"media": [{"take": value}]}, OWNER, fetch)
    assert out["media"] == [{"take": {"take_id": value["take_id"], "mp4": clip}}]
