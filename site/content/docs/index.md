# Documentation

helmstudio runs **studios**: repositories that each wrap an open-weight model — video, image or speech — behind a web page of their own. It installs a studio from a manifest, fetches its weights, runs its processes, keeps one heavy model in memory at a time, and gives every studio the same storage, gallery and timeline, so what one makes is there for the next.

These pages are for someone writing a studio, or wrapping a repository someone else wrote. If you only want to **run** studios, the [repository's README](https://github.com/janishar/helmstudio#choose-your-path) is the shorter road; if you want to **work on helmstudio itself**, start with [CONTRIBUTING](https://github.com/janishar/helmstudio/blob/main/CONTRIBUTING.md).

@diagram what-runs-where caption: Everything here runs on your own Mac. A model only ever runs inside a studio, and the only outbound calls are the ones you ask for.

## Where to start

| If you want to | Read |
|---|---|
| Get something running in a few minutes | [Quickstart](/docs/quickstart/), after [installing `helm`](/docs/install-helm/) |
| Understand what runs where | [How a studio fits together](/docs/concepts/how-a-studio-fits-together/) |
| Describe a studio to helmstudio | [The manifest](/docs/concepts/manifest/) |
| Wrap a repository whose author wrote no manifest | [Wrap a repository](/docs/guides/wrap-a-repository/) |
| Look something up | [The reference](/docs/reference/api/) — every operation, manifest field and command, generated from the contract and the code |

## What exists today

`helm` is released on GitHub, the runtime SDK on PyPI, npm and the Go module proxy, and `@helmstudio/css` and `@helmstudio/ui` on npm, so writing a studio needs no clone of this repository. helmstudio itself — the launcher and its daemon — ships as an unsigned Mac app, and also runs from a clone.

These are **not built**, and a page that would otherwise use one says so:

- `helm studio init`, `helm test`, `helm doctor` and `helm adopt` — four commands the design describes;
- `helm dev --fixtures` and `--fail`;
- the launcher's own Gallery and Timeline screens.

## Three packages, one direction

A studio takes as much of helmstudio as it wants. **None of it is required**: a studio that uses no package at all still installs, launches and runs.

| Package | Gives a studio | Depends on |
|---|---|---|
| `helm-css` | Tokens, and classes for layout and components, in both themes. No JavaScript. | Nothing. |
| `helm-runtime-sdk` | A client for the platform API — settings, sessions, records, assets, the gallery, jobs, the timeline and events — in Go, Python and JavaScript, generated from one contract. | The API contract. |
| `helm-ui-sdk` | `helm-gallery`, `helm-player`, `helm-terminal` and `helm-timeline`, as custom elements. | `helm-css`, and the browser build of the runtime SDK. |

Only the runtime SDK knows the wire format. A component is given a client and calls its methods, and helm-css assumes no component's markup, so nothing points back up the chain.

What no package will ever contain: prompt builders, parameter panels, model pickers and scheduler controls. Those are where studios differ, and where their authors' judgement lives.
