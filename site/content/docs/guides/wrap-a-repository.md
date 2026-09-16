# Wrap a repository

Most model repositories worth running will never ship a `helmstudio.yaml`. You do not need their author's permission to write one: a manifest is a description of a repository, and anyone can write it. This guide writes one for an image model served by a Python server.

## Read the repository first

Everything a manifest says is something you would otherwise do by hand from the README: which operating system and chip it supports, which tools it needs, which Python version, how it is built, which checkpoint it downloads, and the command that starts it. Collect those first. A manifest written blind is a manifest that breaks at install.

## The manifest

@sample wrap/helmstudio.yaml

The repository it names is an illustration, and does not exist. Section by section:

- **Who it is.** `id` is a stable slug, and the file you save locally is named for it. `license` is the repository's own licence.
- **Where it comes from.** `repo` is anything `git clone` accepts. `ref` pins a tag or a commit: it is resolved to a commit before anything is shown or built, so a branch that moves after you approved it does not change what runs. `submodules: true` clones them too.
- **What it needs.** `requires` is the operating systems, architectures and tools it runs on, and the memory and disk it needs. `peak_ram_gb` is what the model occupies once loaded — an estimate is better than nothing, because it is what the arithmetic for switching between heavy studios uses.
- **What it is built on.** `runtime` says the framework and the backends, because PyTorch on `mps` and PyTorch on `cuda` are the same framework and very different claims.
- **Python.** `python.version` makes helmstudio create an environment with `uv`, pinned to that version, before any build step. Environments are never shared between studios.
- **How it is built.** Each `build` step runs in order, and must never prompt: there is no one to answer. A step that fails stops the install, and retrying resumes at the first step that has not succeeded.
- **Its weights.** Each weight is downloaded, or linked to a directory you already have. See [weights](/docs/concepts/weights/).
- **How it runs.** One process here, marked `heavy` because it holds the model. Its command takes its port from `{port}` and its checkpoint from `{models.base}`, and its health check has a budget large enough for the model to load. See [process groups](/docs/concepts/process-groups/).

## Validate it

@sample wrap/validate.sh

`helm validate` checks the schema, then the rules a schema cannot express: every `depends_on` names a process, there is no cycle, exactly one process is `main` and at most one is `heavy`, every placeholder resolves, and no `cwd`, weight or smoke test path escapes its root. `-criteria` then scores the manifest:

@criteria wrap/helmstudio.yaml

Seven criteria can be decided from a manifest alone, and this one passes five. It declares no `test.profile`, and it pins the repository but not the runtime SDK's major. The other eight need a smoke harness, the studio's source or its stylesheets, so they are not scored, and they say why. See [publishing](/docs/publishing/).

## Try it

Clone the repository, put the manifest at its root, and run it under `helm dev` with the checkpoint you already have. See [develop in isolation](/docs/guides/develop-in-isolation/).

## In helmstudio

helmstudio's launcher can add the same manifest: import the file, or write it in the editor, which is a form over the schema beside the YAML, validating and scoring the criteria as you type. It saves the manifest locally, where it overrides any registry entry with the same `id`. There is no release yet, so today the launcher runs from a source build of `cmd/helmstudio`.

## Share it

The file you wrote is the contribution. The repository's author can commit it as `helmstudio.yaml`, and the registry can point at the repository. Until the author ships one, a registry entry can carry the manifest inline. See [publishing](/docs/publishing/).
