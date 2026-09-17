# Quickstart

From an empty directory to a studio of your own, running under `helm dev` and recording its first output.

You need macOS on Apple Silicon, or Linux on x86-64 or ARM64, with Python 3.9 or newer, `curl`, and a network connection to install. Nothing here needs a clone of helmstudio, Go, a GPU, a model or a Hugging Face account.

## 1. Install `helm`

`helm` validates manifests, and runs a studio with no daemon. The installer downloads the newest release for this machine, checks it against the release's checksums, runs it once, and puts it in `~/.local/bin`.

@sample quickstart/01-install-helm.sh not-run: it needs the network; the gate builds helm from the checkout it tests and puts it on the path instead

If `~/.local/bin` is not on your `PATH`, the installer prints the line to add. [Installing helm](https://github.com/janishar/helmstudio/blob/main/docs/releasing.md#installing-helm) covers installing a particular version, upgrading and uninstalling.

## 2. Make a directory and a Python environment

A studio runs in its author's environment. `helm dev` never makes one, or changes one.

@sample quickstart/02-environment.sh

## 3. Install the runtime SDK

The Python client comes from PyPI, and uses the standard library and nothing else.

@sample quickstart/03-install-sdk.sh not-run: it needs the network; the gate puts the same package from the checkout it tests on the environment's path instead

## 4. Get the example studio

The example is about the smallest studio that does something: it makes an image from a prompt, and records it. Download its two files into the directory:

@sample quickstart/04-get-example.sh not-run: it needs the network; the gate copies the same two files from the checkout it tests instead

Its manifest says what it is, what it asks helmstudio for, and how to run it:

@sample hello-studio/helmstudio.yaml

Its code is a web server that uses the runtime SDK twice: to adopt the file it made, and to record a gallery item with the parameters that made it.

@sample hello-studio/studio.py

## 5. Run it

`helm dev` keeps the studio's data in `.helm`, beside the manifest. It serves the platform API on a free local port, gives the studio a token for the two capabilities its manifest asks for, starts its process, and waits for its health check.

@sample quickstart/05-run.sh

It prints the API's address and where the data is, then the studio's output, each line prefixed with its process's name — first the command it started, then what the studio itself printed:

@sample quickstart/05-output.txt

The studio asked for port 8765. If something else has it, `helm dev` gives the studio another, and the line above says which — use that one below. Leave it running, and use a second terminal for the rest. Ctrl-C stops it.

## 6. Make something

@sample quickstart/06-make.sh

The answer names the gallery item and the asset it holds.

## 7. See it recorded

@sample quickstart/07-items.sh

The item comes back with the prompt and the seed that made it. That is provenance: wherever the item is shown later, what made it is shown with it.

## 8. Check it against the criteria

@sample quickstart/08-validate.sh

`helm validate` checks a manifest against the schema and the rules the schema cannot express. With `-criteria` it also scores the fifteen criteria a published studio is held to, as far as a manifest can answer them. The example fails two: it declares no test profile, because the harness that would run one is not built, and it pins no `ref`, because it has no repository yet. [Publishing](/docs/publishing/) explains both.

## Next

- [How a studio fits together](/docs/concepts/how-a-studio-fits-together/): what runs where, and where each part of the SDK comes from.
- [The manifest](/docs/concepts/manifest/), and the [manifest reference](/docs/reference/manifest/).
- [Record with provenance](/docs/guides/record-with-provenance/), for outputs made from other outputs.
- [Develop in isolation](/docs/guides/develop-in-isolation/), for a studio with weights and a Python version of its own.
