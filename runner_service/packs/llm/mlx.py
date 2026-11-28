"""llm/ask on MLX: Qwen3.8-27B-4bit through mlx-vlm (the mlx env's `llm` extra). The component `model` is the whole
vision-language model, loaded by mlx-vlm's own loader (its key sanitizing and stored 4-bit layout) and held by the
residency manager, never in a module global. The ask itself (thumbnails, schema check, one more try, the answer
record) is core/ask.py; here only the greedy call: thinking off, temperature 0, seed core.ask.SEED, the answer
constrained to the schema by llguidance where it compiles (else prompt + strict check, as on torch).

mlx-vlm is imported inside the functions, so the runner starts without the extra; a run then fails saying so."""

import functools
import json
import logging
import re
from pathlib import Path

from core import ask as asks
from core.graph import RunError

log = logging.getLogger("llm")
MISSING = "llm: mlx-vlm is not installed (uv sync --project envs/mlx --extra llm)"


def load_component(component: str, model, ctx):
    """core.loaders_mlx hook: mlx-vlm builds, sanitizes, quantizes and loads the checkpoint itself (evaluated)."""
    try:
        from mlx_vlm.utils import load_model
    except ImportError as e:
        raise RunError(MISSING) from e
    return load_model(model.folder(), lazy=False)


@functools.lru_cache(maxsize=1)
def processor(folder: str, eos):
    """The tokenizer and image processor (small files, no weights), read once per folder."""
    from mlx_vlm.utils import load_image_processor, load_processor

    proc = load_processor(Path(folder), True, eos_token_ids=list(eos) if isinstance(eos, tuple) else eos)
    if (images := load_image_processor(Path(folder))) is not None:
        proc.image_processor = images
    return proc


UNIMPLEMENTED = re.compile(r"Unimplemented keys: \[([^\]]*)\]")


def without(value, keys: set[str]):
    """The schema with every `keys` keyword dropped at any depth (a looser grammar; the answer is still validated)."""
    if isinstance(value, dict):
        return {k: without(v, keys) for k, v in value.items() if k not in keys}
    if isinstance(value, list):
        return [without(v, keys) for v in value]
    return value


def constrained(proc, schema: str) -> list:
    """llguidance's JSON-schema logits processor, or none when the schema has no grammar there (checked after).
    Keywords llguidance does not implement (the agent turn schema's `propertyNames` on rows.add / rows.edit inputs)
    are dropped from the grammar only, so the rest of the turn (tool names, args, caps) stays constrained."""
    if not schema.strip():
        return []
    from mlx_vlm.structured import build_json_schema_logits_processor

    grammar, dropped = schema, set()
    for _ in range(4):
        try:
            return [build_json_schema_logits_processor(proc.tokenizer, grammar)]
        except Exception as e:  # noqa: BLE001 (an unsupported keyword: the answer is still validated)
            keys = set(re.findall(r'"([^"]+)"', m.group(1))) if (m := UNIMPLEMENTED.search(str(e))) else set()
            if not keys or keys <= dropped:
                log.warning("llguidance cannot constrain this schema (%s): prompt + check only", e)
                return []
            dropped |= keys
            grammar = json.dumps(without(json.loads(schema), dropped))
    return []


def ask(ctx, params: dict, inputs: dict) -> dict:
    try:
        import mlx.core as mx
        from mlx_vlm import stream_generate
        from mlx_vlm.prompt_utils import apply_chat_template
    except ImportError as e:
        raise RunError(MISSING) from e
    model = ctx.component(inputs["model"], "model")
    eos = getattr(model.config, "eos_token_id", None)
    proc = processor(str(inputs["model"].folder()), tuple(eos) if isinstance(eos, list) else eos)

    def generate(prompt: str, thumbs: list) -> tuple[str, int, int]:
        turns = [{"role": "system", "content": params["system"]}] if params["system"] else []
        turns.append({"role": "user", "content": prompt})
        text = apply_chat_template(proc, model.config, turns, num_images=len(thumbs), enable_thinking=False)
        mx.random.seed(asks.SEED)
        parts, last = [], None
        for last in stream_generate(model, proc, text, image=thumbs or None, temperature=0.0,
                                    max_tokens=params["max_tokens"], seed=asks.SEED,
                                    logits_processors=constrained(proc, params["schema"])):  # fmt: skip
            ctx.check()  # a cancel point per token
            parts.append(last.text)
        return (
            "".join(parts),
            last.prompt_tokens if last else 0,
            last.generation_tokens if last else 0,
        )

    return asks.ask(ctx, params, generate)
