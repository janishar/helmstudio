# stable_audio_3 gotchas

- **Not diffusers' pipelines, diffusers' classes.** The checkpoint is an inpaint model (model_config.json
  `diffusion_cond_inpaint`): the reference feeds the DiT its local conditioning [mask, masked latents] on every run,
  zeros when nothing is kept. diffusers 0.43's `StableAudio3Pipeline` and `StableAudio3AudioToAudioPipeline` leave it
  out (a different model state: `to_local_embed(0)` is not 0), and none of the three pipelines has the reference's
  6 s headroom or padding mask. `torch.py` `sample` runs the reference's loop on `StableAudio3DiTModel`.
- **This SAME-S is not diffusers' SAME-S.** diffusers' `AutoencoderSAME` attends in a sliding band; this
  checkpoint's autoencoder attends in chunks of two segments, the second half of the layers shifted by one segment
  (`chunk_size 32`, `chunk_midpoint_shift`), and its decoder maps channels with a 3-tap conv (`conv_mapping`): the
  pack patches the two resampling blocks (`_encode`, `_decode`) and the decoder's mapping.
- **qkv rows.** The reference's differential attention splits `to_qkv` as [q, k, v, q', k']; diffusers' as
  [q, q', k, k', v]. `tensor_hook` reorders the autoencoder's rows at load (the DiT is not differential).
- **Inference noise in the decoder.** The reference adds `randn * running_std * 1e-3` in the bottleneck and
  `randn * 0.01` to the decoder's new tokens even at inference (`noise_regularize`, `mask_noise`); the pack draws
  both, after the 8 step noises, so the takes repeat the reference's.
- **randn_like follows strides.** The SAME encoder's latents come out transposed; mixed into the start noise they
  would make every step's `randn_like` land on other positions. `encode` returns them contiguous, as the
  reference's `.repeat(batch)` does (found by Vary: 75 dB against the reference after, -3 dB before).
- **T5Gemma runs bf16, eager.** The reference's `T5GemmaEncoderModel.from_pretrained` keeps the stored bf16
  (transformers 5 loads `dtype="auto"`), and T5Gemma has no SDPA path. The DiT and the autoencoder run fp32, as the
  maker's code does without CUDA. The model node's precision reads bf16 (the contract's precisions have no fp32).
- **Seeding is global** (`torch.manual_seed`, noise drawn on the device in the reference's order): the torch env runs
  one run at a time. The goldens are the reference's own output (tests/golden/record_stable_audio_3.py) and the pack
  matches them at 75-82 dB SNR on MPS, not bit for bit (two implementations).
- **No Avoid (negative prompt).** The maker's inference guide: guidance and negative prompts have no effect on the
  post-trained checkpoints (base models only). The board's Avoid box is therefore not on this template.
- **No chunked decode.** The reference decodes latents of 128 or more (about 6 s past the headroom) in overlapping
  chunks to save VRAM; the pack decodes whole (1.1 GB above the components at 30 s). Takes over about 6 s may differ
  slightly from the reference's at the chunk seams; the goldens are 2 s.
- **Levels are the model's**: no peak normalisation (the model card's example normalises; the sample run did not).
- **Gated repo**: the download needs the host's Hub token and the terms accepted on the Hub page (`gated`,
  `must_accept`; tests/test_stable_audio_3.py on the fake hub).
