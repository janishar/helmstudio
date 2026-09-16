# The manifest

A studio is described by one file, `helmstudio.yaml`: what it is, what it needs from the machine, how to build it, which weights to fetch, and how to run it. The [manifest reference](/docs/reference/manifest/) lists every field, generated from the schema `helm validate` checks against.

## It lives in the studio's repository

The manifest sits at the root of the studio's own repository, beside the code it describes, so a studio can add a build step or a process without waiting for a helmstudio release. The registry does not keep copies: an entry is a **pointer** — an id, a repository and a pinned ref — and it carries a manifest inline only for a repository that does not ship one yet. See [independent repositories](/docs/concepts/independent-repositories/).

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

## Levels are earned, never declared

A studio's level is derived from what has been checked; no field sets it. Today only two are reachable: **Draft**, for a manifest with `local_path`, and **Unverified**, for every other. Verified and Registry need a smoke harness that does not exist yet. See [publishing](/docs/publishing/).

## Annotated examples

The four launch studios' manifests, line by line: [h3](/docs/manifests/h3-studio/), [ltx](/docs/manifests/ltx-studio/), [iris](/docs/manifests/iris-studio/) and [AuK](/docs/manifests/auk-studio/).
