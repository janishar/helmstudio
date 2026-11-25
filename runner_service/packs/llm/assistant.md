# Qwen3.8 27B (llm pack): notes for the Assistant

## Card
Qwen3.8 27B is the local vision-language model (MLX 4-bit, Mac only) behind the Ask workflow (`llm-ask`, "Ask
Qwen3.8 27B"): it answers one question about up to 8 image or video takes, as text or as JSON, greedy and
repeatable. Pipelines' judge, write and decide nodes and the Assistant's look_at (when your own model cannot see)
run through an ask workflow. Pick it for careful judging of takes against a checklist; pick Qwen Image's ask
(Qwen3-VL 8B) when Qwen Image is already loaded and a quick keep-one-of-N is enough. It cannot hear audio.

## Good at
- Seeing images and video stills: the maker reports strong image, document and video understanding (STEM diagrams,
  documents, long video). [card]
- Judging takes against a checklist: in the pack's test (4 takes, 3 criteria) it got 12 of 12 checks right, ranked
  them correctly and named the failed criterion in each reason; the 8B judge got 9 of 12. [pack]
- Strict JSON: with a schema the answer is constrained while it is written, and was valid on the first try in every
  measured run; a wrong answer gets one more try in the same run. [pack]
- Repeatable: temperature 0, thinking off, seed pinned; two fresh runs gave byte-identical answers. [pack]
- Long context: 262,144 tokens natively, far above any ask. [card] [pack]
- Reasoning and coding are strong on the maker's benchmarks (GPQA Diamond 89.2, LiveCodeBench v6 90.3), useful for
  write and decide nodes. [card]

## Weak at
- Audio: an ask sends only image and video takes; it cannot judge a voice line, music or a video's sound. [pack]
- Video is seen as 3 still keyframes (at 1/6, 1/2 and 5/6 of the clip), not motion: it cannot judge smoothness,
  flicker, timing or lip-sync. [pack]
- Fine detail: each take is shrunk to at most 1024 px on its long side (JPEG q90) before the model sees it; tiny text
  or small faces in a large still may be lost. [pack]
- Heavy: about 15 GB loaded and 16-21 GB at peak; it cannot stay loaded beside LTX on a 64 GB Mac, so a pipeline
  that alternates LTX and judging reloads it. [pack]
- Mac only (MLX); no NVIDIA host runs it. [pack]
- Thinking is always off here. The maker does not say whether its scores used thinking (it is on by default); with
  thinking off here, hard multi-step reasoning may be weaker: not measured. [card] [pack]
- Multilingual quality: not stated by the maker; not known yet. [card]
- Owner's takes and answers: not read, so nothing here is grounded in seen results yet.

## Modes and tasks
- **Ask Qwen3.8 27B** (`llm-ask`, media text): `llm/model` (variant `4bit`, precision `int4`, stored) → `llm/ask`
  → `answer`. One question, 0-8 takes, plain text or JSON by schema. [pack]
- **Ask Qwen Image (Qwen3-VL 8B)** (`qwen-image-ask`, in the qwen_image pack): the same node contract on Qwen
  Image's own text encoder: no load when Qwen Image is resident, about 9 s for a 4-take judge, good at keep-one-of-N and
  the first failed must, less exact on full checklists. [pack]
- Pipelines nodes: a judge (ranks takes against must/nice checks), write (writes text: prompts, titles) and decide
  (picks a route) each ask a model; `auto` picks the ask workflow of the family of the takes being judged (or of the
  nearest step for write and decide), else `llm-ask`. LTX has no model that sees, so LTX takes are judged by
  `llm-ask`. [pack]
- look_at (Assistant): with a model that sees, the takes simply join your next call's pictures (no run); with a
  text-only model, look_at starts an ask run on the user's machine after their Yes (no card for takes they attached
  or picked in this chat), up to 8 takes, counted against an hourly limit. Await the run's answer. [pack]

## Settings that matter
The ask stage's `system`, `prompt`, `media` and `schema` are written by pipelines or by look_at; only one setting is
meant for people.
- **Max tokens** `ask.max_tokens`: 16-4096, default 800. The answer's length limit. A judge of 4 takes needed about
  180 tokens with a schema (290 without); raise it for long writing or many takes, lower it for one-word answers.
  Each token costs about 0.06 s (17-18 tokens a second). An answer cut off by the limit fails its schema. [pack]
- **Takes** `ask.media`: 0-8 image or video takes, in order; each video counts as 3 pictures. More pictures raise
  time and memory (about 0.5 GB each at the peak). [pack]
- **Schema** `ask.schema`: a JSON schema the answer must match, up to 16 KB; empty means plain text. Use one for
  anything a program reads: it is faster (about 38 % fewer tokens) and always parses. [pack]
- Not settable: temperature (always 0), thinking (always off), seed (pinned), thumbnail size (1024 px), keyframes
  (3). The maker's own sampling advice (temperature 0.7) does not apply here. [pack] [card]

## Prompt style
- Ask one clear question; name the takes by their order ("take 1", "take 2"), the order of `media`. [pack]
- For judging, give a checklist with must and nice items and ask for a check per take, a reason and a ranking:
  that is the tested shape. [pack]
- Ask for the answer only; with a schema the pack adds "Answer only with JSON matching this schema" itself. [pack]
- Ask about what a still shows (subject in frame, sharpness, colour, text, composition, likeness to a reference);
  do not ask about motion, sound or timing. [pack]
- The prompt template may be up to 32,768 characters and the system turn up to 4,000. [pack]

## Speed and memory
- Load: 1-2 s when the files are in the page cache; a cold 16 GB read is not measured. [pack]
- Text only: a short answer (27 tokens) in 2-3.4 s. [pack]
- Judging 4 takes at 512 px (about 1,400 prompt tokens): 13.4-14.3 s; at 1024 px (4,500 tokens): about 21.5 s.
  Prefill 360-460 tokens a second, writing 17-18 tokens a second. [pack]
- Picture cost: (width / 32) x (height / 32) tokens each, so a 1024 x 1024 thumbnail is 1,024 tokens and a 1024 x
  576 one 576. [pack]
- Memory: 14.95 GB of weights, 15.9 GB after load; process peak 16.2 GB text only, 19.4 GB with 4 x 512 px, 20.8 GB
  with 4 x 1024 px; `peak_gb` 4 + 0.5 x pictures above the weights. [pack]

## Pitfalls
- It never hears audio: for a voice take, ask the user to listen, or judge the words from the script. [pack]
- Video judgements rest on 3 stills: say so when you report on motion. [pack]
- Not on NVIDIA hosts: on a host without MLX, use Qwen Image's ask or the Assistant's own model. [pack]
- Its 15 GB load can push other models out of memory; prefer the family's own ask when that model is loaded.
  [pack]
- More than 8 takes in one ask is refused; split a big batch. [pack]
- A too-small Max tokens cuts the JSON and fails the run ("answer did not match the schema"). [pack]

## Recipes

The exact values are the recipes sent with this guide (model.yaml `recipes`); set each param with change. When to use each:

- judge-takes (a schema with per-take checks, reason and ranking; up to 4 stills), quick-yes-no (1 take),
  write-prompt (no takes), many-takes (8 stills or 2 videos plus 2 stills).
- Not settings: use `qwen-image-ask` when Qwen Image is loaded and only the best take is wanted [pack].

## Sources
- Maker: https://huggingface.co/mlx-community/Qwen3.8-27B-4bit (revision b935172e4bb5dcd0543774a34748fb92422e3027),
  https://huggingface.co/Qwen/Qwen3.8-27B (base model card: capabilities, context, sampling).
- Pack: runner_service/packs/llm/model.yaml, mlx.py, workflows/text-llm-ask.json; runner_service/core/ask.py
  (thumbnails, keyframes, retry); runner_service/packs/qwen_image/workflows/text-qwen-image-ask.json;
  docs/reference-notes/llm.md sections 1-5 (measured tests); contracts/README.md (look_at, auto model);
  pipelines_service/assistant.py and gen/tools/catalog.json (look_at); pipelines_service/assistant_knowledge.md.
- Owner's takes: not read. The CLI and the helmstudio MCP both answered "session expired" (401) and a fresh login
  is not allowed in this task, so there are no [takes] claims.
