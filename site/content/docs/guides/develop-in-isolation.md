# Develop in isolation

A studio can be built and run with no helmstudio installed, no daemon, no registry — and, where the model allows, without the checkpoint. `helm dev` is how, and it is not a separate imitation of helmstudio: it is the daemon's own supervisor, manifest parser and platform API, run for one studio from a local manifest.

@diagram helm-dev-and-the-daemon caption: What changes between the two is how many studios run and where their roots live — not the code a studio meets.

## What `helm dev` does

Run in the studio's checkout, it:

- reads `helmstudio.yaml`, or the manifest named by `-f`, and refuses one that does not validate;
- keeps the studio's data — its database, assets, logs and stage directories — in `.helm`, beside the manifest;
- serves the platform API on a free loopback port, or on `-addr`, and gives the studio a token for the capabilities its manifest declares;
- starts the studio's processes in dependency order, with `{port}`, `{data}` and `{models.<name>}` substituted, and waits on their health checks;
- prints each process's output, prefixed with its name, until Ctrl-C stops the group.

It runs no build step and downloads nothing: the checkout is yours, and so are the weights. If the last `helm dev` left the studio running, the next one takes it over rather than starting a second copy.

## Weights you already have

Link each weight a command uses to the directory that holds it. The directory is read, never written.

@sample isolation/dev-with-weights.sh not-run: it needs a studio's checkout and its weights

A launch whose command uses a weight that is not linked is refused, naming the weight.

## Python

A studio that declares `python` runs in your environment: the active one, or the one `-venv` names. `helm dev` checks it is the Python version the manifest declares, and never creates or changes an environment — without one, it refuses and says how to make one.

@sample isolation/dev-python.sh not-run: it needs uv, a studio's checkout and the network

A studio that runs `python3` without declaring `python`, like the [quickstart](/docs/quickstart/)'s, runs whichever `python3` is first on the `PATH`.

## Go, without `helm dev`

A Go studio with one process can import the embedded provider and run the platform API in its own process. See [providers](/docs/concepts/providers/).

## What `helm dev` does not do yet

The design describes more than is built. These are not:

- **`helm studio init`**, which is to scaffold a studio. Copy the [quickstart](/docs/quickstart/)'s example instead.
- **`helm dev --fixtures`**, which is to seed a gallery with sample media, so a page can be built before the model is downloaded.
- **`helm dev --fail`**, which is to inject quota rejections, conflicts and outages.
- **`helm test`**, the smoke harness, and **`helm doctor`**.
- **`helm adopt`**, which is to bring what a studio recorded under `helm dev` into helmstudio's library.
