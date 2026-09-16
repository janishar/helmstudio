# Publishing

A studio can reach people three ways, from least ceremony to most: a manifest in someone's own helmstudio, a `helmstudio.yaml` in the studio's repository that anyone can add by URL, and an entry in the registry that ships with helmstudio.

## The criteria

Fifteen criteria describe a studio fit to publish. `helm validate -criteria`, the launcher's editor and its approval screen all score them from one table, so they cannot disagree. Seven can be decided from a manifest alone, and the score counts only those: "5 of 7 checkable pass" rather than "5 of 15", because nothing was checked for the rest. Each of the other eight says what it would need.

Scored against the manifest in [wrap a repository](/docs/guides/wrap-a-repository/):

@criteria wrap/helmstudio.yaml

**Required** criteria are the ones the design holds a registry entry to. No criterion stops someone installing a studio on their own machine: a failure is shown, so they know what they are deciding.

To check your own studio before you share it:

@sample wrap/validate.sh

@sample theming/lint.sh

## Levels

A studio's level is a label on its card, derived from what has been checked. It is never declared in a file, and it never stops anyone running a studio on their own machine.

| Level | Means | Reachable today |
|---|---|---|
| Draft | A manifest with `local_path`: a directory already on this machine, which nobody could have reviewed. | Yes |
| Unverified | Every other manifest: valid, from a repository, not checked by a harness. | Yes |
| Verified | Every required criterion passes, and the smoke harness ran from a clean state. | No — there is no smoke harness yet |
| Registry | In helmstudio's registry, verified in CI on every release. | No — for the same reason |

So every studio today, the four helmstudio launches with included, is Draft or Unverified, and says so.

## A registry pull request

The registry is the `studios/` directory of helmstudio's repository, one file per studio, named for its id. An entry is a pointer:

@sample publishing/tern-studio.yaml

The repository and ref are what a reviewer reads, and the commit the ref resolves to is what installs, so the manifest that runs is the manifest that was reviewed. An entry for a repository that does not ship a `helmstudio.yaml` carries the manifest inline, under `manifest`, and any `id`, `repo` or `ref` it sets must match the pointer's. When the repository starts shipping one, the pull request that moves `ref` removes the inline copy.

A pull request adds or changes one file. helmstudio's gate validates every entry in `studios/`, inline manifests included. Running each studio's smoke test on a clean machine is part of the design, and is not built.

An update moves `ref`. An entry never gains a field saying it was checked: there is none to set.
