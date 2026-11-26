# helmstudio plan — Host memory budget: Automatic or Custom

[← Plan index](../README.md)

Status: **built on branch alpha.5** (owner, 2026-11-26: "use host setting, also show the option to select the aumatic
mode", then "start woking on the items"). API 1.75; As built below.

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
- Automatic, no limit set (or a CUDA host): `Budget  52 GB · RAM minus 12 GB` (on CUDA, `VRAM minus 2 GB`); on a Mac
  `· no GPU limit set` follows (owner).
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

### 39.4 Owner's answers (2026-11-26)

- A GPU limit above RAM − 4 GB counts as RAM − 4 GB (macOS keeps 4 GB): auto = min(limit, RAM − 4).
- A Mac with no limit found says so briefly on the Budget line: `52 GB · RAM minus 12 GB · no GPU limit set`.
- The default stays Custom for every host; nothing changes by itself.

### 39.5 As built

- Contract 1.75 (`4be21c56`): `ReserveMode` `auto | custom` on `HostSettingsPut` (optional: absent = unchanged, a new
  row custom), `HostSettings` (required) and `HostAdmin.policy` (optional: absent from older runners = custom);
  `Host.machine.gpu_limit_gb` (null when unset); registry `settings.reserve_mode` (optional), `policy.reserve_mode`,
  `machine.gpu_limit_gb`; contracts/README.md "Automatic budget".
- Runner (`7a4202d5`): `core.residency.gpu_limit_gb()` runs `sysctl -n iogpu.wired_limit_mb` (macOS only; 0 =
  unset) at start and on each settings apply; `Settings.budget()` does the rule; the hosts key reports both.
- Studio (`7a475e0d`): migration 00053 adds `host_settings.reserve_mode` (default custom). The 422 reserve check moved
  inside the locked row update so it uses the effective mode (a PUT without the field on an auto host skips it too).
  The KV value carries `reserve_mode` only for auto, so a runner older than 1.75 (whose schema refuses unknown
  fields) keeps taking custom settings.
- Web (`5bbb4e9b`): `components/admin/hosts.tsx` as frames 1–4; the Budget line's words come from `lib/hosts.ts`
  `autoBudget` (limit and the capped case are known from `machine`; without a limit the default reserve is read off
  the budget the runner reports, so for a few seconds after a switch it can show the old figure).
- Contract tests (`dab63541`): `test_hosts_admin.py::test_reserve_mode_auto` on the fake host.
- Not done: the CLI (`helm admin hosts settings`) sends no `reserve_mode` and so keeps the stored one; the info text
  does not say the limit resets on reboot (not asked).
