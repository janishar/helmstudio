# The manifest

A studio is described by one file, `helmstudio.yaml`: what it is, what it needs from the machine, how to build it, which weights to fetch, and how to run it. The [manifest reference](/docs/reference/manifest/) lists every field, generated from the schema `helm validate` checks against.

## It lives in the studio's repository

The manifest sits at the root of the studio's own repository, beside the code it describes, so a studio can add a build step or a process without waiting for a helmstudio release. A registry entry is a **pointer** — an id, a repository and a pinned ref — and it carries a copy of that commit's manifest beside it. The copy is what the [annotated examples](/docs/manifests/h3-studio/) on this site read.

A studio stays its own repository. helmstudio never vendors, forks, patches or imports a studio's code: it clones the repository, runs the build steps the manifest declares, and supervises the processes it names. Someone who clones a studio directly and ignores helmstudio entirely still has a studio that works. Four things follow from that:

- **A studio ships on its own schedule.** A new build step or a second process reaches people with the studio's next commit, not with helmstudio's next release.
- **One copy is the one that runs.** A studio's own `helmstudio.yaml` is resolved before the registry's copy, so the registry's never installs anything once the repository ships one. That is what keeps the second copy from mattering: the pull request that moves a `ref` updates it to the manifest that commit ships, so the entry goes on describing what is actually there.
- **Adding a studio is a data change.** Supporting one never means changing helmstudio's code. If a studio cannot be described by the manifest, the schema is what is wrong.
- **Nothing is required.** A studio that uses none of the three packages still installs, launches and runs. The packages save an author work; they are never the price of admission.

The four launch studios are separate repositories with separate stacks: a native Metal engine in C behind a Go server, the same shape for images, an MLX model behind a Python server, and PyTorch behind FastAPI. None had a front-end toolchain, and none had to adopt one. A studio built against the runtime SDK talks to a [provider](/docs/concepts/providers/), not to helmstudio — under `helm dev` while it is being written, under the daemon once it is installed, and the manifest is the same file in both.

## Anyone can write one

Most repositories worth running will never ship a manifest, so writing one for a repository you did not write is an ordinary thing to do. See [wrap a repository](/docs/guides/wrap-a-repository/).

## Where helmstudio finds a studio's manifest

Every studio in the library is resolved from three sources, highest first:

1. **Local** — a manifest you wrote, imported or duplicated, kept in helmstudio's data directory as `studios/<id>.yaml`.
2. **The studio's repository** — `helmstudio.yaml` at the pinned ref, from its installed checkout, or from the copy cached when it was fetched.
3. **The registry** — a manifest carried inline by the pointer.

**The first source that has a file wins, whether or not that file is valid.** A local manifest with a typo in it does not quietly fall through to the registry's version of a studio you meant to override: the studio is listed as invalid, with the errors and a way to edit it. Reverting a local manifest moves it aside rather than deleting it, and the source beneath it applies again.

## What every manifest declares

`id`, `name`, `kinds`, `requires`, `runtime` and `processes` are required, and so is `repo`, unless `local_path` names a checkout already on the machine. The schema also describes `run`, a shorthand for a single process, but a manifest that uses it cannot validate today, because `processes` is required whether or not `run` is present. Use `processes`, even for one.

## Nothing runs until you have seen what will

Installing a studio runs someone else's code with your permissions, and so does launching one. So install and launch stop first at an **approval screen**, for a manifest from any source, including one you wrote. It shows every command, byte for byte and grouped by when it runs — build steps at install, each process's command and health check at every launch, the importer on first launch — with the capabilities the studio asks for written as sentences, and the network hosts it declares.

Approving records a digest of exactly what will execute. When a manifest changes any of that, the next install or launch asks again.

## How much of helmstudio to take

A manifest says which of the three packages a studio uses, and nothing obliges it to use any.

@diagram the-four-adoption-levels caption: A level is a choice, not a score. A studio with an unusual interface may sit at level 2 for ever, and that is a success rather than a gap.

`sdk` names the major versions a studio needs, and `capabilities` names what its token may reach — so a studio that declares no capabilities is asking helmstudio for nothing at all, and gets no token.

## Certification levels are earned, never declared

A studio's certification level is derived from what has been checked; no field sets it. Today only two are reachable: **Draft**, for a manifest with `local_path`, and **Unverified**, for every other. Verified and Registry need a smoke harness that does not exist yet. See [publishing](/docs/publishing/).

## Annotated examples

The four launch studios' manifests, line by line: [h3](/docs/manifests/h3-studio/), [ltx](/docs/manifests/ltx-studio/), [iris](/docs/manifests/iris-studio/) and [AuK](/docs/manifests/auk-studio/).
