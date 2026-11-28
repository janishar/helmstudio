# MiniMax Music 3: notes for the Assistant

## Card
MiniMax Music 3 writes a whole song, vocals included, from a style in words and lyrics with section tags: 8B
language model, 2.4B flow-matching transformer and a vocoder, 44.1 kHz stereo, up to 5 minutes. One workflow,
MiniMax Music 3. The owner liked it best of the music models, so it is Create's default. Slow: about 5 s of work per
second of song on a Mac (30 s in about 2:30), about 27 GB. Pick ACE-Step instead for quick drafts, BPM or key, a
reference song, or Repaint, Cover and Extend, which this model cannot do.

## Good at
- Full songs with sung lyrics that follow the [verse] / [chorus] structure of the lyrics. [card] [pack]
- Style in plain words: genre, mood, voice, instruments, arrangement; tempo as words in the style ("100 bpm"). [pack]
- Instrumental pieces when the style says so and names the lead instrument. [pack]
- Repeats bit for bit: the same style, lyrics, Length and seed on the same machine give the same song. [pack]

## Weak at
- No BPM, key, time signature, language or reference-song settings: only what the style and lyrics say. [pack]
- No editing: Repaint, Cover and Extend on a MiniMax take run on ACE-Step. [pack]
- Slow (about 5x real time) and heavy (about 27 GB); a 5-minute song takes about 25 minutes. [pack]
- Length is an upper bound: the model may end a song earlier (a 30 s instrumental cue stopped at 22 s). [pack]
- Owner's takes: the samples were listened to ("minimax i liked the best"); no per-take notes yet.

## Modes and tasks
- **MiniMax Music 3** (`minimax-music3`): style (the prompt) + lyrics -> one song. Text to music only. [pack]

## Settings that matter
- **Lyrics** `lm.lyrics`: the words to sing, up to 4000 characters. Put each section tag ([verse], [chorus],
  [bridge], [intro], [outro]) on its own line; text after a tag on the same line is dropped. [pack]
- **Instrumental** `lm.instrumental`: no vocals. The pack adds "Instrumental, no vocals." to the style and sings only
  [instrumental]; empty lyrics do the same. Name the lead instrument in the style. [pack]
- **Length** `lm.seconds`: 10-300 s, or 0 = Auto (up to 5 minutes, the model ends it). Time grows with it, about
  5 s of work per second; start with 30 s to try a style. [pack]
- **Seed** `seed.seed`: -1 picks a new one; keep a liked take's seed to remake it exactly. [pack]

## Prompt style
- The prompt is the style, not the lyrics: "warm indie pop, acoustic guitar, female vocal, 100 bpm". Short and
  concrete: genre, mood, voice, lead instruments, tempo. [pack]
- Lyrics separately, one line per sung line, with section tags on their own lines. [pack]
- For an instrumental, say what carries the melody: "strings carry the melody, then piano". [pack]

## Sources
- [card] the model card (huggingface.co/MiniMaxAI/MiniMax-Music3). [pack] this pack and its measured runs
  (GOTCHAS.md, ~/GenAI/model-samples/minimax-music3/results.md).
