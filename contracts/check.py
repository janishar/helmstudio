"""Contract self-check: every fixture against its schema, and a lint of openapi.yaml.

    python contracts/check.py

Needs jsonschema (+ rfc3339-validator for date-time), PyYAML, openapi-spec-validator. Fixtures are
fixtures/{messages,registry,http}/<name>.{valid,invalid}[.N].json: every valid one must pass, every invalid one must fail.
Registry fixtures may carry a family prefix (qwen-image.nodes.valid.json): <family>.<key>. HTTP fixtures are bodies
checked against openapi.yaml components/schemas/<name>. fixtures/graphs/ run through the reference graph rules below
(README, Graph rules) against fixtures/graphs/palette.json; fixtures/pipelines/ through the reference pipeline rules
(README, Pipeline rules) against fixtures/pipelines/workflows.json and the same palette; fixtures/mentions/ through
the reference mention rules (README, Mentions) against the same palette; fixtures/loras/ through the reference
LoRA normaliser (README, LoRA normaliser) against the canonical qwen-image lora block (or the fixture's `family`);
fixtures/decisions/ through the reference judge verdict and replay hash (README, Agent nodes);
fixtures/video/ through the reference auto length and keyframe position rules (README, Video); fixtures/dispatch/
through the reference host admission (`admit`), dispatch order (`simulate`) (README, The hand-over) and placement
among several hosts (`place`) (README, Several hosts);
fixtures/timelines/ through the reference timeline rules (`t_normalize`, `t_edit`, `snap_point`, `frame_at`,
`export_plan`, `shot_keyframes`, `rerender_steps`; from 1.49 `t_diff`, `t_agent_view`, `frame_at_time`) (README, Timeline rules), and
edits.json replayed through studio's edit.go and the web's lib/timeline.ts (needs go and node on PATH); from API 1.80
fixtures/magic/edits.json through studio's api/magic and the web's lib/magic.ts (README, Magic); fixtures/voices/ through the reference voice rules (README, Voices; from
API 1.32 Dialogue and Timeline audio: `expand_script`, `echo_dialogue`, `script_check`, `make_shot`); providers/presets.json
against its schema and the preset rules (README, Model sources: presets; API 1.51).
Exit code 1 on any failure.

The rules live by topic in the sibling check_*.py modules (check_core, check_graph, check_mentions, check_pipeline,
check_decisions, check_loras, check_rules, check_video, check_voices, check_timeline, check_dispatch, check_api);
this file re-exports every name, so `import check` (contract_tests) sees them all."""

import sys

from check_core import *  # noqa: F403
from check_graph import *  # noqa: F403
from check_mentions import *  # noqa: F403
from check_pipeline import *  # noqa: F403
from check_decisions import *  # noqa: F403
from check_loras import *  # noqa: F403
from check_rules import *  # noqa: F403
from check_video import *  # noqa: F403
from check_voices import *  # noqa: F403
from check_timeline import *  # noqa: F403
from check_dispatch import *  # noqa: F403
from check_api import *  # noqa: F403
from check_core import errors


def main() -> int:
    schemas, registry = load_schemas()
    check_fixtures(schemas, registry)
    check_canonical_registries()
    palette = check_graphs(registry)
    check_pipelines(registry, palette)
    check_decisions()
    check_mentions(registry, palette)
    check_loras()
    check_lora_detection()
    check_video(registry, palette)
    check_voices(registry, palette)
    check_timelines(registry, palette)
    check_edit_replay()
    check_magic_replay(registry)
    check_dispatch(registry)
    check_tools(schemas, registry)
    check_presets(schemas, registry)
    check_plugins(schemas, registry)
    check_openapi()
    check_names()
    print(f"\n{len(errors)} failure(s)" if errors else "\nall contract checks passed")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
