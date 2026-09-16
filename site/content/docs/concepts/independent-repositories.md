# Independent repositories

A studio is its own repository, and stays one. helmstudio never vendors, forks, patches or imports a studio's code: it clones the repository, runs the build steps its manifest declares, and supervises the processes it names. Someone who clones a studio directly and ignores helmstudio entirely still has a studio that works.

## Why

- **A studio ships on its own schedule.** Its manifest sits beside its code, so a new build step or a second process reaches people with the studio's next commit, not with helmstudio's next release.
- **There is one copy of the truth.** The registry holds a pointer — the repository and the reviewed ref — rather than a copy of the manifest, and two copies of a manifest drift. A manifest carried inline, for a repository that does not ship one yet, is removed by the pull request that moves the ref to a commit that does.
- **Adding a studio is a data change.** Supporting a studio never means changing helmstudio's code. If a studio cannot be described by the manifest, the schema is what is wrong.
- **Nothing is required.** A studio that uses none of the three packages still installs, launches and runs. The packages save an author work; they are never the price of admission.

## What the launch studios show

The four studios helmstudio launches with are separate repositories with separate stacks: a native Metal engine in C behind a Go server, the same shape for images, an MLX model behind a Python server, and PyTorch behind FastAPI. None of them had a front-end toolchain, and none of them had to adopt one. Their [annotated manifests](/docs/manifests/h3-studio/) are what describes them to helmstudio.

## The same studio, with and without helmstudio

A studio built against the runtime SDK talks to a provider, not to helmstudio: under `helm dev` while it is being written, under the daemon once it is installed. The manifest is the same file in both places, and so are the calls. See [providers](/docs/concepts/providers/).
