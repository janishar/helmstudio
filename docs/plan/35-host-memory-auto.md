# helmstudio plan — Host memory budget: Automatic or Custom

[← Plan index](../README.md)

Status: **asked by the owner (2026-11-26: "use host setting, also show the option to select the aumatic mode")**.
Board drawn; contracts and code not changed yet.

## 39. Automatic budget from the Mac's GPU limit

### 39.1 Today

A host's budget is physical memory (VRAM on a CUDA host) minus `reserve_gb`, set by the Memory reserve slider in
System › Hosts (else `RUNNER_RESERVE_GB`: 12 on a Mac, 2 on the GPU host). The owner's 64 GB Mac shows a 51 GB
budget. macOS has its own GPU memory limit (`sudo sysctl iogpu.wired_limit_mb=61440`, 60 GB); the owner set it and
wants the budget to follow it.

### 39.2 The form

Boards: [Hosts](../mock/Hosts.dc.html), frames 1–4 below the page.

- Above the reserve, **Memory budget** with a two-option segmented choice: **Automatic | Custom**.
- Automatic, Mac with a GPU limit set: no slider; `Budget  60 GB · the Mac's GPU limit`.
- Automatic, no limit set (or a CUDA host): `Budget  52 GB · RAM minus 12 GB` (on CUDA, `VRAM minus 2 GB`).
- Custom: today's Memory reserve slider (0.5 GB steps) and Budget, unchanged.
- The info text: Automatic uses the macOS GPU memory limit when one is set, else RAM minus the default reserve; on
  an NVIDIA host, VRAM minus the default reserve. Custom is physical memory minus the reserve you set. Both are also
  checked against live memory pressure.
- The host card's legend reads `reserve auto` under Automatic, `reserve N GB` under Custom.

### 39.3 Contract change (proposal)

- `HostSettingsPut` and `HostSettings` gain `reserve_mode: auto | custom`, default `custom`, so nothing changes for
  existing hosts. `reserve_gb` stays required and is kept (used again on a switch back to Custom).
- The KV `host_settings` value and the runner's reported `policy` carry `reserve_mode`.
- The runner on macOS reads `sysctl -n iogpu.wired_limit_mb` (0 = unset) at start and on each settings change. In
  auto: budget = that limit when > 0, else device memory − `RUNNER_RESERVE_GB`. In custom: as today.
- The hosts key's `machine` gains `gpu_limit_gb` (null when unset, and on non-macOS hosts) so the form can show
  which rule applies.
- Studio's 422 `reserve_gb leaves no memory` check skips `reserve_gb` in auto.
- Steps: contract first (`openapi.yaml`, `registry.schema.json`, contracts/README.md's budget table), `make gen`,
  then runner, studio, web; tests for the auto budget on both paths and the 422 skip.

### 39.4 Open questions

- A GPU limit at or above physical RAM leaves nothing for macOS: cap auto at RAM − 4 GB, or trust the owner's setting?
- The limit resets on reboot unless set at boot; should the form say so when `gpu_limit_gb` is null on a Mac?
- Should the Automatic button be the default for new hosts later (today: Custom for all)?
