# 42 · The Assistant listens and watches

alpha.6 row 83 (owner, 2026-11-28: "add all three after refactor", then "can we do it in parallel"; decided one by
one the same day). Claude takes text and pictures only, so the Assistant judges sound and moving video through
tools that turn them into facts, pictures and words.

## Decided by the owner (2026-11-28)

1. **A better look, plus a listen tool** ("Better look + listen"): the existing look tool gets, by itself, one
   picture of 12 to 16 frames with their times and exact ffmpeg facts (real length, loudness, peaks, silence at the
   ends, an abrupt stop, scene cuts). A new listen tool asks a local model a question about a take's sound and
   picture, with a transcript for voice.
2. **Models**: Qwen3-Omni-30B-A3B-Instruct, GGUF Q8_0 with its Q8_0 audio-and-vision part (ggml-org, about 34 GB),
   downloaded to the T9 ("download the qwen3 omni on the t9"; "GGUF Q8"), run by llama.cpp. Transcripts from
   Qwen3-ASR-1.7B. To make room, the small models moved to the T9 and their Mac copies were deleted ("delete few
   models from the mac which are already in t9 or copy if not exits ... smaller models will go there"; "Yes, move
   these"): Qwen2.5-Omni-3B, ZONOS3-Mini, Qwen3-ASR-1.7B, both Qwen3-TTS, ACE-Step-1.5, MMAudio, DFN5B CLIP,
   Stable-Audio-3-Small-SFX, bigvgan. The big models stay on the Mac.
3. **Qwen3-Omni first, Qwen2.5-Omni-3B as the fallback** ("Qwen3-Omni, 3B fallback"): Qwen3-Omni when it is there
   and passes a first test on the Mac; the 3B when it is missing or the T9 is unplugged. Built in alpha.6, in
   parallel with plan 41.
   The fallback is built (alpha.6 row 99, contract 1.109; owner: "Yes, build it", "use GGUF"): Qwen2.5-Omni-3B GGUF
   Q8_0 with its Q8_0 projector (ggml-org) on the Mac's own disk, omni variant `3b` and workflow omni-listen-3b on
   the same llama-server path; studio runs it when omni-listen cannot (T9 unplugged, not downloaded, no room) and the
   reply's note says so. First test on the Mac (llama.cpp 0.9.0): about 1 s load, 6.4 GB footprint with the 32k
   slot; a 2 s voice line in under 1 s ("clear, neutral" male voice), a 5 s fox-in-snow clip in about 4 s ("a fox
   walking in the snow"); right but terse, and with sampling on (llama-mtmd-cli, temperature 0.2) it can loop on a
   long answer, so the runner's greedy decoding stays.

4. **Facts on demand, then kept** ("can it not do on fly it want the fact and it does not exists", then "Only on
   demand"): no background facts loop. A take is measured the first time the Assistant or helm mcp asks for its
   facts (studio.see, look_at, get_take); studio waits up to 20 s and answers them in the same reply, else a plain
   note (being measured, or no runner on to measure). alpha.6 row 96, contract 1.108.

Every part also reaches helm mcp, and the director skill says when to look and when to listen.

## Next

A first test of Qwen3-Omni on the Mac (load time from the T9, answer time on a 20 s music take and a 5 s shot),
then the contract (the look tool's facts and frame grid, the listen tool), then the build: facts and the grid first,
the listen tool second.
