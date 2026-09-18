# Providers

A studio reaches the platform API through a **provider**. There are two, and they serve one contract: an OpenAPI document that generates the Go, Python and JavaScript clients, so an operation cannot exist in one language and not another.

## Remote: over HTTP

helmstudio's daemon serves the API on the machine's loopback address, and so does `helm dev`. Either one starts the studio's processes with the address and a token in the environment:

| Variable | Holds |
|---|---|
| `HELM_API` | The API's address. |
| `HELM_TOKEN` | A token for the capabilities the manifest declares. Absent for a studio that declares none. |
| `HELM_STUDIO_ID` | The studio's id. |
| `HELM_STAGE_DIR` | A directory for this run's outputs, which the studio adopts from. |
| `HELM_SDK_BASE` | Where the UI kit is served, for the major the manifest pins in `sdk`. The proxy fetches it from there. |
| `HELM_ACCENT_DARK`, `HELM_ACCENT_LIGHT` | The studio's hue in each theme. The proxy writes `/helm/accent.css` from them. |
| `HELM_THEME` | `system`, `light` or `dark`: the launcher's theme when the studio started. It changes afterwards over the theme stream, not by restarting. |

That is every variable a studio is given. A studio run standalone is given none of them, which is how the clients tell the two apart.

`from_env()` in Python, `fromEnv()` in JavaScript and `helm.FromEnv()` in Go read them and return a client. The Python and JavaScript clients are remote only: without `HELM_API` they refuse, naming helmstudio and `helm dev`, so a Python or JavaScript studio developed on its own runs under `helm dev`.

A studio's page never holds the token. Its server mounts the runtime SDK's proxy at `/helm/`, which adds the token to what the page sends — see [theming](/docs/guides/theming/), which uses it.

## Embedded: in the studio's own process

In Go, a studio can import the embedded provider and run the platform API in its own process, with no daemon and no `helm dev`. It keeps its data in `.helm`, in the directory the studio runs from, unless `HELM_DIR` names another:

- import `github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded` for its side effect, and `helm.FromEnv()` uses it whenever `HELM_API` is not set;
- it is a module of its own, so a studio that only ever runs under helmstudio never pulls SQLite into its build;
- it reads the studio's capabilities from its manifest and enforces them as the daemon does;
- only one process may open a `.helm` at a time, so a studio with more than one process runs under `helm dev` instead.

Run in a directory that holds the studio's `helmstudio.yaml`, this records an output with neither helmstudio nor `helm dev` running:

@sample providers/embedded/main.go

Where a studio may adopt from comes from `/me`, so the same code works under every provider. There is no embedded provider in Python or JavaScript.

## The same answers

A provider that answered differently would make a studio's tests worthless. The conformance suite in `test/conformance` runs the same cases against the daemon over HTTP and against the embedded provider in process.

What is deliberately different is presentation. `POST /timeline/{id}:open` asks for a window to show a sequence in, and today every provider answers `501`, because the launcher's own timeline screen is not built yet. The sequence is still made, edited and exported, and a studio hides its Open button. See [the timeline](/docs/guides/timeline/).

## Not built yet

`helm adopt`, which is to bring what a studio recorded under `helm dev` or the embedded provider into helmstudio's library, is not built. Neither are `helm dev --fixtures`, for a gallery of sample media, and `helm dev --fail`, for injected errors.
