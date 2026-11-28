# Qwen3-Omni 30B (omni pack): notes for the Assistant

## Card
Qwen3-Omni 30B is the local model that hears: the listen tool asks it one question about a video or audio take
(the sound, and a video's picture at 4 frames a second) and Qwen3-ASR 1.7B writes down the speech. Use it when sound
matters: an abrupt end, a voice's clarity, music against the picture. It costs a model run: about a minute the first
time (the weights load), then a few seconds while it stays loaded.

## Good at
- Hearing: voice clarity and tone, an abrupt or clipped end, music mood, sound that does not match the picture. [pack]
- Speech to text in 11 languages (the transcript, Qwen3-ASR). [card]

## Not good at
- Exact numbers: take loudness, length and silence from the take's facts (get_take), not from its words. [pack]
