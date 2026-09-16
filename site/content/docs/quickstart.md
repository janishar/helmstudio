# Quickstart

From a clone of helmstudio to a studio of your own, running under `helm dev` and recording its first output.

> There is no release yet, and none of the packages is on PyPI or npm, so this builds `helm` from source and installs the runtime SDK from the clone.

You need Go 1.27.1 or newer, Python 3.9 or newer, `git` and `curl`, and a network connection to clone, build and install. Nothing here needs a GPU, a model or a Hugging Face account.

## 1. Clone helmstudio

@sample quickstart/01-clone.sh not-run: it needs the network; the gate runs every other step in the checkout it tests

## 2. Build `helm`

`helm` validates manifests, and runs a studio with no daemon. Go downloads the modules helmstudio depends on the first time it builds.

@sample quickstart/02-build-helm.sh

## 3. Make a Python environment

A studio runs in its author's environment. `helm dev` never makes one, or changes one.

@sample quickstart/03-environment.sh

## 4. Install the runtime SDK

The Python client uses the standard library and nothing else. Installing it fetches pip's build tools.

@sample quickstart/04-install-sdk.sh not-run: it needs the network; the gate puts the same directory on the environment's path instead

## 5. Copy the example studio

The example is about the smallest studio that does something: it makes an image from a prompt, and records it.

@sample quickstart/05-copy-example.sh

Its manifest says what it is, what it asks helmstudio for, and how to run it:

@sample hello-studio/helmstudio.yaml

Its code is a web server that uses the runtime SDK twice: to adopt the file it made, and to record a gallery item with the parameters that made it.

@sample hello-studio/studio.py

## 6. Run it

`helm dev` keeps the studio's data in `.helm`, beside the manifest. It serves the platform API on a free local port, gives the studio a token for the two capabilities its manifest asks for, starts its process, and waits for its health check.

@sample quickstart/06-run.sh

It prints the API's address and where the data is, then the studio's output, each line prefixed with its process's name — first the command it started, then what the studio itself printed:

@sample quickstart/06-output.txt

The studio asked for port 8765. If something else has it, `helm dev` gives the studio another, and the line above says which — use that one below. Leave it running, and use a second terminal for the rest. Ctrl-C stops it.

## 7. Make something

@sample quickstart/07-make.sh

The answer names the gallery item and the asset it holds.

## 8. See it recorded

@sample quickstart/08-items.sh

The item comes back with the prompt and the seed that made it. That is provenance: wherever the item is shown later, what made it is shown with it.

## 9. Check it against the criteria

@sample quickstart/09-validate.sh

`helm validate` checks a manifest against the schema and the rules the schema cannot express. With `-criteria` it also scores the fifteen criteria a published studio is held to, as far as a manifest can answer them. The example fails two: it declares no test profile, because the harness that would run one is not built, and it pins no `ref`, because it has no repository yet. [Publishing](/docs/publishing/) explains both.

## Next

- [The manifest](/docs/concepts/manifest/), and the [manifest reference](/docs/reference/manifest/).
- [Record with provenance](/docs/guides/record-with-provenance/), for outputs made from other outputs.
- [Develop in isolation](/docs/guides/develop-in-isolation/), for a studio with weights and a Python version of its own.
