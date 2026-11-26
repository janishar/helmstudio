# mmaudio gotchas

- **The checkpoints are torch.save files** (`.pth`, open_clip's `.bin`, BigVGAN's `.pt`), so the pack loads its own
  components (`load_component`). Each is memory-mapped (`core.loaders_torch.state_dict`) and assigned to a module
  built on meta, then moved to the device at bf16: the process peaks at 7.5 GB for both goldens (upstream, which
  reads every checkpoint into RAM in fp32 first: 15.5 GB; the sample: 16.2 GB).
- **BigVGAN is built for real** (0.5 GB fp32 on the CPU, then cast): its checkpoint holds the old `weight_g` /
  `weight_v` keys, which only torch's weight-norm compatibility hook maps onto the parametrized model, and that hook
  does not run with `assign`. The VAE's weight norm (EDM2, its own) is removed in fp32 on the CPU, as upstream does.
- **The net's rotations and open_clip's causal mask are made again** after the meta build (both are buffers made in
  `__init__`, left on meta). Each run sets the net's sequence lengths (`update_seq_lengths`) before sampling, as the
  sample does, so the rotations follow the length.
- **Frames come from ffmpeg, not PyAV** (not in the env): RGB rawvideo, frame i at i / fps (a constant frame rate),
  picked for 8 and 25 fps exactly as `read_frames` picks them and resized one frame at a time (never all frames
  held). The video golden is byte-identical to upstream's PyAV run. A variable-frame-rate source is read as constant.
- **Auto length on a video** takes the source's length; the 25 fps sync frames end with the last picture, so
  MMAudio shortens the sound by up to 0.04 s (load_video's truncation) and the muxed video pads that with silence.
- **The video is cut with stream copy** (`-t`, H.264 copied, never re-encoded): it ends on a packet, a frame or two
  past Length (2.0 s asked gave 50 frames at 24 fps); `core.media.mux` pads or cuts the sound to the picture.
- **Repeats bit for bit** on the same device and torch: the seed goes into a device generator (`torch.Generator`
  on MPS), never the global one.
- **Output is 44.1 kHz mono**, not normalised: loud sources clip near full scale (the sample's truck hit 0 dBFS).
- **No torch.compile** (upstream's `compile()` is never called).
