# helmstudio-next plan — Out of scope and risks

[← Plan index](../README.md)

## 12. Deliberately not in v1

- Tensor IPC or shared memory between runners.
- Sharing between users (galleries, pipelines, memory are private), quotas
  and billing, public sign-up.
- A desktop app shell and a one-click installer; the product is a web app
  behind Kong.
- Running services without Docker on a Mac.
- The packed mmap no-copy format (interface reserved, section 6.3).
- Fine-grained sampler nodes (scheduler, guider, noise as separate nodes).
- Conditionals, loops or scripting inside a workflow, and nested workflows.
  Branching and capped loops exist only in pipelines (section 16.2).
- Remote models switched on by default; they are opt-in per pipeline (section 16.4).
- A scheduler inside the app (use `helm run` from cron or launchd).
- An image upscale template; until a pack ships one, a batch cannot upscale a folder.
- Per-workflow or per-user memory policies (section 6.2).
- Third-party node sandboxing or a marketplace.
- Collaboration, a community feed, credits, or cloud services other than the
  opt-in remote models and agents of section 16.4.

## 13. Risks and open items

1. **CUDA coverage is per model.** LTX is MLX-only until a torch pack is
   written from the upstream reference; on CUDA, v1 runs Qwen Image only, and
   Qwen's CUDA path is untested today.
2. **Adapter LoRA on MLX quantized layers is unbuilt and unmeasured**, as is
   adapter cost on int8 torch layers. Bake is the fallback for both.
3. **LTX's low-RAM block streaming** rebinds weights per block; adapters and
   the residency manager's idea of "loaded" must both follow it.
4. **Memory formulas start as guesses.** Until measured history accumulates,
   admission will be conservative or wrong for unusual sizes.
5. **The timeline port** is larger than it looks; it is scheduled after the
   engine milestones (section 11).
6. **Torch pins**: FLUX.3 needs torch 2.15 and diffusers git-main; MiniMax pins
   torch < 2.15. Workers are keyed by env so this costs a second env, not a redesign.
7. **Reproducibility against the old studios** depends on keeping their seed
   rules (CPU generator, input-pixel seed mixing for edits, per-stage offsets).
8. **LTX-2.5 files on disk** use the split-file layout and some components may
   still need downloading before M4.
9. **LangGraph as the pipeline engine** adds a fast-moving dependency to
   pipelines_service and a second store (the `pipelines` database) beside the
   batch record in `studio`. The rules in section
   16.3 (two-phase nodes, state as ids, caps in state, pinned versions) are what
   keep it safe; they must be tested against crashes before M1 is called done.
10. **Agents make runs less predictable.** Suggest mode, recorded decisions and
    replay (sections 16.2, 16.7) are the guard; remote models stay opt-in.
11. **Many moving parts.** Eight services, Postgres, NATS and an object store
    is far more to run than one process. The contract suite, fake mode and the
    reference's conventions are what keep it manageable; M0 exists to prove the
    whole loop before any model work.
12. **Native runners beside a compose stack** on a Mac means two ways of
    starting things and published ports for NATS and the object store. The runner's
    supervision and reconnect behaviour need testing early (M1).
13. **Stored progress is polled.** The reference relays its outbox every
    250 ms and polls the event log each second; live subjects cover step
    progress, but state changes still arrive up to a second late.
14. **Multi-user on one GPU** makes waiting visible. The dispatcher's fairness
    is simple round-robin; quotas are out of scope.
