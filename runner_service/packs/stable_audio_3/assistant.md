# Stable Audio 3 Small-SFX: notes for the Assistant

## Card
Stable Audio 3 Small-SFX makes sound effects and ambiences from a text prompt: 44.1 kHz stereo, 0.5 to 30 s, about
half a second of work for a few seconds of sound on a Mac and about 4.5 GB. Four workflows: Sound (4 variations per
Generate, optionally from a sound), Repaint (make a span again), Vary (a new take of a sound) and Extend (add seconds
after or before). Pick it for foley, hits, ambiences and quick variations; pick MMAudio to score a video, ACE-Step or
MiniMax Music 3 for music.

## Good at
- Short sound events and steady ambiences from plain words: rain, footsteps, doors, impacts, machines, rooms. [pack]
- Speed: 8 steps; a 2 s sound takes about 0.5 s warm, so making 4 or 8 variations and keeping one is cheap. [pack]
- Editing a sound in place: Repaint keeps everything outside the span, Extend keeps the source as it was. [pack]
- Repeats exactly: the same prompt, settings and seed on the same machine give the same sound. [pack]

## Weak at
- Not music and not speech: no melody control, no words. [card]
- Levels are not normalised: some sounds come out quiet (a footstep take peaked near -20 dB in the sample run). [pack]
- No negative prompt: the maker says guidance and negative prompts have no effect on this post-trained model, so
  there is no Avoid box. [pack]
- Past about 30 s a single event gets vague; it was trained for effects, not long scenes. [pack]
- Owner's takes: not read, so nothing here is grounded in seen results yet.

## Modes and tasks
- **Stable Audio 3 Sound** (`sa3-sound`): text to sound, `sound.seconds` long; Generate makes 4 (up to 8). With
  `sound.from_sound` (one audio take or a video's sound) it starts from that sound at `sound.change`. [pack]
- **Stable Audio 3 Repaint** (`sa3-repaint`, action Repaint): the source with `repaint.span` made again from the
  prompt; offered on sound takes. [pack]
- **Stable Audio 3 Vary** (`sa3-vary`, action Vary): the source made again from the prompt, starting from it;
  `vary.change` low stays close, 1 is a new sound. [pack]
- **Stable Audio 3 Extend** (`sa3-extend`, action Extend): `extend.seconds` added at the end (`extend.where` end) or
  before the start (start); at most 60 s in all. [pack]

## Settings that matter
- **Length** `sound.seconds`: 0.5-30 s, default 4. Say a hit is 1-2 s, an ambience 10-30 s. [pack]
- **Change** `sound.change` / `vary.change`: 0.05-1, default 0.7. 0.5 keeps the timing and shape; 0.9 keeps only the
  gist. [pack]
- **Add** `extend.seconds`: 0.5-30 s; **Where** `extend.where`: end or start. [pack]
- **Seed** `seed.seed`: -1 picks a new one; keep a liked take's seed to remake it. [pack]

## Prompt style
- Name the source, the action and the place: "old wooden door creaking open", "heavy rain on a window, distant
  thunder", "footsteps on gravel, slow walk". [pack]
- For Repaint, Vary and Extend the prompt describes the sound wanted in the new part (or the whole for Vary). [pack]

## Sources
- [card] the maker's model card (huggingface.co/stabilityai/stable-audio-3-small-sfx) and inference guide.
- [pack] this pack and the sample run on the owner's Mac (plan 38, 44.4).
