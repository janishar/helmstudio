# How a studio fits together

A studio is code in three places: a host, the studio's own server, and the studio's page in a browser. helmstudio reaches each of them differently, and most questions about the SDKs come down to which of the three is asking.

## Three places, two servers

| Where | What runs there | Started by |
|---|---|---|
| **The host**: helmstudio, or `helm dev` | The platform API, the studio's data, and the files of the UI kit | You |
| **The studio's server** | The studio's own endpoints and work, and the runtime SDK's proxy at `/helm/` | The host, from the manifest's `processes` |
| **The studio's page** | The studio's HTML and JavaScript, helm-css and the components | The browser, from the studio's server |

The browser talks only to the studio's server, and the studio's server talks to the host. The page needs neither the host's address nor a token.

`helm dev` is helmstudio restricted to one studio. It serves the same API, the UI kit and the proxy's answers the same way, and keeps the studio's data in `.helm/` beside its manifest. What it leaves out is installing: it runs no build step and downloads nothing, so it runs the studio in your own Python environment, with weights you link. A studio that works under `helm dev` works in helmstudio unchanged; see [develop in isolation](/docs/guides/develop-in-isolation/).

When the host starts the studio's server, it says where everything is:

| Variable | What the server does with it |
|---|---|
| `HELM_API` and `HELM_TOKEN` | The runtime SDK's client calls the API with them. |
| `HELM_SDK_BASE` | The proxy fetches the UI kit from it, for the major the manifest pins in `sdk`. |
| `HELM_ACCENT_DARK` and `HELM_ACCENT_LIGHT` | The proxy writes `/helm/accent.css` from them: the studio's hue in each theme. |

This says which piece reads what. [Providers](/docs/concepts/providers/) has the full list of what a studio is given, and what each one holds.

[Providers](/docs/concepts/providers/) lists the rest.

## What one thing is called

The same parts go by several names across these pages, and two of them are not synonyms. This is which is which.

| You will meet | It means |
|---|---|
| **a provider** | Whatever serves the platform API. There are two: remote and embedded. See [providers](/docs/concepts/providers/). |
| **the host** | The provider that serves it over HTTP — helmstudio, or `helm dev`. Every host is a provider; the **embedded** provider is not a host, because it runs inside the studio's own process and answers no address. |
| **the daemon** | helmstudio's long-running process: the host, when the host is helmstudio rather than `helm dev`. |
| **the launcher** | The screens helmstudio serves for a person to click, not the API a studio calls. |
| **the platform API** | The endpoints themselves, whichever provider is serving them. |
| **the UI kit** | helm-css, the runtime SDK's browser build and the components, served together at `/helm/sdk/v1/`. |
| **the library** | Two things, by context: the studios this machine knows about, and the tree of what studios have made. [The manifest](/docs/concepts/manifest/) means the first; [record with provenance](/docs/guides/record-with-provenance/) means the second. |
| **a weight**, **a checkpoint** | The same thing: the model files a studio needs. `weights` is the manifest's field name. |

A package's directory name in this repository is not the name you install. `helm-css` and `helm-ui-sdk` are directories; `@helmstudio/css` and `@helmstudio/ui` are what npm carries.

## Where each piece comes from

| Piece | Used by | Comes from |
|---|---|---|
| **The runtime SDK**: a client for the API, and the proxy | The studio's server | The language's own registry, locked with the studio's other dependencies: `helm-runtime-sdk` from PyPI, `@helmstudio/runtime` from npm, or the Go module |
| **The UI kit**: helm-css, the runtime's browser client, and the components | The studio's page | The host, at `/helm/sdk/v1/`, through the studio's proxy |
| **The studio's data**: settings, sessions, records, assets, the gallery, jobs and the timeline | Both | The host's API: from the server directly, and from the page at `/helm/api/v1/` through the proxy |

The two halves of the SDK arrive differently because of how each is loaded:

- **A server imports from disk.** Python, Node and Go load a package from the studio's environment before they can reach anything, so the runtime SDK is an ordinary dependency: installed with the others, pinned in the lockfile, and the same under `helm dev`, under helmstudio and in a test.
- **A browser loads from a URL.** Whatever serves the page can hand it files as it opens, so the page needs no build step and no Node. It runs the kit of the host it is running in, which matches that host's API and the launcher's look, and it needs no internet.

`helm` and helmstudio each carry the UI kit compiled into their binary: the same files the npm packages publish. Neither downloads anything to serve them.

## What the proxy answers

@diagram how-a-page-reaches-the-api caption: The page holds no token and never learns the host's address. Everything it needs arrives from its own origin, and only the studio's server side ever sees `HELM_TOKEN`.

The studio's server mounts the proxy at `/helm/`: `Proxy.from_env()` in Python, `createProxy()` in JavaScript and `helm.Proxy(helm.ProxyFromEnv())` in Go. [Theming](/docs/guides/theming/) mounts it in all three.

| The page asks for | The proxy |
|---|---|
| `/helm/api/v1/…` | Forwards it to the host's API, adding the studio's token. A launcher operation, such as install, launch or stop, is never forwarded. |
| `/helm/api/v1/theme/events` | Forwards the host's theme stream, with no token. |
| `/helm/sdk/v1/…` | Forwards it to `HELM_SDK_BASE`, with no token, for the UI kit's files. |
| `/helm/accent.css` | Writes it from the studio's hue, forwarding nothing. |

## How a page gets its styles and components

1. **The page links and imports from `/helm/sdk/v1/`.** `helm.css` is the whole of helm-css: tokens, base, layout and components. `helm-runtime.js` is the runtime's browser client, and `helm-ui.js` is the components. [Theming](/docs/guides/theming/) shows such a page.
2. **The host answers from its binary.** `helm-runtime.js` and `helm-ui.js` are two short files that re-export the packages' own entry points, and the browser fetches those, and every file they import, through the proxy the same way.
3. **Importing `helm-ui.js` defines the elements**: `helm-gallery`, `helm-player`, `helm-terminal` and `helm-timeline`.
4. **A component finds its client** in its own `client` property, or in `window.helm`, which a page sets once to what `connect()` returns. That client calls `/helm/api/v1/` and holds no token.
5. **A component's own CSS is inside its JavaScript**, in its shadow root, and takes its colours, spacing and type from helm-css's variables. Variables reach inside a shadow root, so a component matches the page it is on.
6. **`themeBridge()` follows the launcher's theme** and sets `data-theme` on the page. The tokens and the accent change with it, and so does every component, without a reload.

A page that wants only the tokens links `helm-tokens.css` in place of `helm.css`. helm-css declares the IBM Plex faces in its base layer, so such a page declares them itself, with their files at `/helm/sdk/v1/fonts/`.

## With a bundler, or a Node server

npm carries `@helmstudio/runtime`, `@helmstudio/css` and `@helmstudio/ui` for studios with a toolchain, and for CI.

- **A Node server** installs `@helmstudio/runtime` for `fromEnv()` and `createProxy()`. It is to a Node server what `helm-runtime-sdk` from PyPI is to a Python one.
- **A page built with a bundler** runs whatever its bundle contains. A kit bundled from npm is that copy, under `helm dev` and inside helmstudio alike, and updating helmstudio does not change it. To run the host's kit, leave it out of the bundle and load it from `/helm/sdk/v1/`, as above.

## Questions people ask

**Does the page call the host's API directly?**
No. It calls `/helm/api/v1/` on its own server, and the proxy forwards the call with the token. The page never needs the host's address, which under `helm dev` is a free port chosen at start.

**Does `helm` download the CSS and JavaScript from npm?**
No. They are compiled into `helm` and helmstudio when those are built, from the same files npm publishes.

**If the host serves the JavaScript, why does a Python server need a package from PyPI?**
The page's JavaScript is fetched by URL each time the page opens. Python imports from the studio's environment before it can reach a host, so the runtime SDK is a dependency like any other, pinned in the studio's lockfile.

**Can a page load the kit from a CDN?**
Nothing in helmstudio does. A page that did would need the internet to draw, would send requests to a third party, which the manifest's `network` has to name, and would run that copy rather than the host's.

**Why does `from_env()` refuse when I start the server myself?**
Without `HELM_API`, the Python and JavaScript clients refuse and name helmstudio and `helm dev`. Run the studio under `helm dev`, which starts the server with everything above.

**Where is the studio's data?**
With the host: in `.helm/` beside the manifest under `helm dev`, and in `~/.helmstudio` under helmstudio. A studio that holds capabilities keeps its data there, not in files of its own.
