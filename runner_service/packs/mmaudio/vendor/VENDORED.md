# Vendored: MMAudio

`mmaudio/` here is a subset of the package `mmaudio/` of https://github.com/hkchengrex/MMAudio, commit `974010a`
(the clone at `~/GenAI/model-samples/src/MMAudio`, the code the sample run in `~/GenAI/model-samples/mmaudio` used),
for the torch runtime (plan 38). 42 files: the network (`model/networks.py` and what it imports), the Euler flow
matching, the sequence lengths, the 44k VAE (`ext/autoencoder/vae*.py`, `edm2_utils.py`), BigVGAN v2
(`ext/bigvgan_v2/`, its torch activations only) and the Synchformer (`ext/synchformer/` with its
`divided_224_16x4.yaml`), found by walking their imports. Nothing of gradio, hydra, tensorboard, training, data or
the 16k BigVGAN is copied.

- Licence: MIT, `LICENSE` next to this file (Copyright (c) 2024 Sony Research Inc.); BigVGAN v2 MIT
  (`mmaudio/ext/bigvgan_v2/LICENSE`, its sources' licences in `incl_licenses/`); Synchformer MIT
  (`mmaudio/ext/synchformer/LICENSE`). The weights are not covered: MMAudio's are CC BY-NC 4.0, DFN5B's Apple's
  research licence, BigVGAN's MIT (`../licences/mmaudio-weights.md`, accepted before a download).
- Copied unchanged but for three files, each change marked `helmstudio (VENDORED.md)` in the file, so the runner's
  torch env needs no new dependency (PyAV, librosa, omegaconf and torchdiffeq are not in it):
  - `model/flow_matching.py`: `from torchdiffeq import odeint` moved into the `adaptive` branch (the pack runs
    `euler`). Upstream sha256 `fb91770584229d6b76000dd63a319d67d624cace27ad739fb1eb5e70a856b276`.
  - `ext/synchformer/motionformer.py`: `from omegaconf import OmegaConf` replaced by a small class of the same name
    that loads the YAML with PyYAML and gives attribute access (all this file uses). Upstream sha256
    `e7c5cb68dff0f95ae4343f29705044266895ac9f2ae25f79720d2a15a677e132`.
  - `ext/autoencoder/__init__.py`: no longer imports `autoencoder.py` (AutoEncoderModule, not copied: it pulls in
    the 16k BigVGAN and omegaconf); the pack builds the VAE and the vocoder itself. Upstream sha256
    `429abde414b7f5bf799f666b378340695af33136682831404b8f7ce3e080f042`.
- Not copied, re-written in `../torch.py` instead: `eval_utils.generate` (at batch 1), `eval_utils.load_video` with
  `data/av_utils.read_frames` (frames from ffmpeg in place of PyAV, picked and resized the same way), and the parts of
  `model/utils/features_utils.py` the pack uses (`patch_clip`, the CLIP, Synchformer and text encodes; the module
  imports librosa and AutoEncoderModule). The goldens (`tests/test_mmaudio_golden.py`) show the port gives the
  upstream WAV byte for byte, from a text and from a video.
- To refresh: copy the same files from a newer commit (re-walk the imports, re-apply the three changes), re-record
  `tests/golden/record_mmaudio.py` from the new clone, run `tests/test_mmaudio_golden.py`, update this note.

| file | sha256 (as vendored) |
|---|---|
| `LICENSE` | `157b6af8c1bc4eac646abdf17473934a85e8815ade3a346cd83a58a171586a73` |
| `mmaudio/__init__.py` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `mmaudio/ext/__init__.py` | `01ba4719c80b6fe911b091a7c05124b64eeece964e09c058ef8f9805daca546b` |
| `mmaudio/ext/autoencoder/__init__.py` | `0ca4e51880acc4fa1f9430bba61148833a0634191363ef089795f546ab9a71ed` |
| `mmaudio/ext/autoencoder/edm2_utils.py` | `838e77d4252a4771e50b204c60bf44048a5c4d37a6b694b348dc8ab6f7a5f048` |
| `mmaudio/ext/autoencoder/vae.py` | `ebc9d03d388e9cbbf815dfbaf53b4f11ba5e2a402729a3d1fbf87bd313fc2231` |
| `mmaudio/ext/autoencoder/vae_modules.py` | `a993a05a14f95bdfc86a35f0a398d715dba90bb0d5858bad2f84780c1a20fc80` |
| `mmaudio/ext/bigvgan_v2/LICENSE` | `5c7f573db5f807a9adc2a755c4901e203ea067f73c7a20fa6b703da7e77d7b35` |
| `mmaudio/ext/bigvgan_v2/__init__.py` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `mmaudio/ext/bigvgan_v2/activations.py` | `ff2562e116399bca730929aeb07e029a61321660a4f07c3db0b8f9853c70470f` |
| `mmaudio/ext/bigvgan_v2/alias_free_activation/torch/__init__.py` | `2e3138e1052e377ba2e51ea59c7d5d255a519559001757af21dbec4cb9c22471` |
| `mmaudio/ext/bigvgan_v2/alias_free_activation/torch/act.py` | `47ef38c3ca52461033a8859f4bb894bad9fa2fefafc0bc226b24e4638d8e6da3` |
| `mmaudio/ext/bigvgan_v2/alias_free_activation/torch/filter.py` | `acf2257276e617dd3161e53abc0a1582e586a3d2d618a43235c7f83818b4e179` |
| `mmaudio/ext/bigvgan_v2/alias_free_activation/torch/resample.py` | `6e540908934e2f5df7c9189449165b0e2fb1e79db567ea883692e4246a32de3b` |
| `mmaudio/ext/bigvgan_v2/bigvgan.py` | `8b147803f8184c2cf6eedace27867d46d20b23fce31556da19c99d849a2e32e4` |
| `mmaudio/ext/bigvgan_v2/env.py` | `54ae665797fbb20ed3fc9b856be688f6b8a903b0bed8ff7edabff81dd9cdcd33` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_1` | `5eec017ce442989e01a46cd9cd6aeb13cd0aa3b3943f681577bda00475afaa23` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_2` | `7441474b0fe03def0efc41a6340b4818359453c7cb32423c647839f8799e68df` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_3` | `7339fa418c9ad3e8e12e74ad0fd26a9cc4be8703f9c110728a992b193be85cb2` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_4` | `9a2fbb788a9d584a2a0766e4bb790c8368911143472d257952581455313cf2c6` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_5` | `b6234401b9831a86505fbeecad803a0b2fe17b06997f614d5325c6d0e1a6fa7f` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_6` | `3fb4aef5b76dc0ccb1f57499d75c2fe039df5c9793778ede172f5ac7a4045681` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_7` | `8f36ba0c214d4f487ffd7ee0b8528da9b67b459bef2d115de9cd001316589f46` |
| `mmaudio/ext/bigvgan_v2/incl_licenses/LICENSE_8` | `ff474afe15f36bab44711fc45c1a77314a4b9d8c96fa2d333f3fefe719fd76f9` |
| `mmaudio/ext/bigvgan_v2/utils.py` | `cfd3d2e280120d48f5c87f3d81c1d8cee070da1f8f219c752abb88066b7be120` |
| `mmaudio/ext/rotary_embeddings.py` | `66d2bc397b8d85ec77700dcbcdc62fd9079c4c595e513dc1817b83169147efca` |
| `mmaudio/ext/synchformer/LICENSE` | `768c302e92c9c1b9c829b73f83101edbe312ca6ed3abc2e8cc9d2eb3e4b185b2` |
| `mmaudio/ext/synchformer/__init__.py` | `3e0a5e12b4e51fd8ba3f7e180e88a8c995f3fc99136a43621747f2cca04f8e62` |
| `mmaudio/ext/synchformer/divided_224_16x4.yaml` | `80461bd19c1f7bc1213445eba48aa69554e4b0230c57d3acc80625d2e2519dce` |
| `mmaudio/ext/synchformer/motionformer.py` | `ea5f8f372a902a79b28ec6d229ba930ee8c6caa128a0c477801b4f8721c0923d` |
| `mmaudio/ext/synchformer/synchformer.py` | `d0a162d9e1ee4e215631dae93afa997867004d80b57472e6195aca6a0e6aa370` |
| `mmaudio/ext/synchformer/utils.py` | `7f5aa03e995ee57bd29c42968b9f8863fc098af56c9bf5b4ff71803d8ce21266` |
| `mmaudio/ext/synchformer/video_model_builder.py` | `eff9225dd341572bc7089a0c127c48d2f80bcf88cfe56f442d71057f0be0988b` |
| `mmaudio/ext/synchformer/vit_helper.py` | `cbfcea6b2b74b89ea0ad2e7f657e54a942f7c8167f3324d7d707dac810a83573` |
| `mmaudio/model/__init__.py` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `mmaudio/model/embeddings.py` | `258cf556c7a13cd19dd51756372dcea000125245e2c1edef5d7b615e0b527add` |
| `mmaudio/model/flow_matching.py` | `7cb1e540e4ef907726b6dd0ed28239a7af6d343a91c944a92152d374d5e61e74` |
| `mmaudio/model/low_level.py` | `7f6720816dc544754c4bcb4ac044b2e8827f1478a3d59f48dd5e68adcea84746` |
| `mmaudio/model/networks.py` | `e4c44a2c3d493b5ded26d687360f08572be797203c201437c8f5dffbd12a9ba5` |
| `mmaudio/model/sequence_config.py` | `7618bb604a5500f7f3d2242dceeb68993224e37e9f75d66c542ae44e5bf161a3` |
| `mmaudio/model/transformer_layers.py` | `2fe6d1931dee3a463dd016bc4dabe0764dff6833f381028f109bf7878449f987` |
| `mmaudio/model/utils/__init__.py` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `mmaudio/model/utils/distributions.py` | `4781b5981d4268ea99b21a667c9cab4f29a79173f30b7485a141dea32767b9f0` |
