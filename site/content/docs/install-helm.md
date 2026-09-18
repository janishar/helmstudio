# Install helm

`helm` is the one binary a studio author needs. It validates a manifest, scores it against the criteria a published studio is held to, lints a stylesheet against the theme, and runs a studio under `helm dev` with no daemon at all. Installing it needs no clone of this repository.

`go install` does not work for it. Go refuses to install from a module whose `go.mod` carries a `replace` directive, and this one's does, so each release ships prebuilt binaries instead: macOS on Apple Silicon, and Linux on x86-64 and ARM64.

## With the installer

@sample quickstart/01-install-helm.sh not-run: it needs the network; the gate builds helm from the checkout it tests and puts it on the path instead

It downloads the archive for your machine and the release's `SHA256SUMS` over HTTPS, and **refuses an archive whose checksum does not match**. When the GitHub CLI is signed in, it also refuses one without this repository's build provenance. It runs the binary once and refuses one whose `helm --version` names a different version, then moves it into `~/.local/bin`.

It needs no `sudo` and edits no shell profile. When `~/.local/bin` is not on your `PATH`, it prints the line to add. It installs the newest release, or the newest pre-release while there are only pre-releases.

Read a script before handing it to a shell. This one is [`installer/install.sh`](https://github.com/janishar/helmstudio/blob/main/installer/install.sh).

### A particular version, or somewhere else

@sample install/a-particular-version.sh not-run: it needs the network; the gate runs the same installer against a release it serves, in test/packaging

Three variables change what it does:

| Variable | Does |
|---|---|
| `HELM_VERSION` | Installs that version rather than the newest. |
| `HELM_INSTALL_DIR` | Installs somewhere other than `~/.local/bin`. |
| `HELM_RELEASES_URL` | Downloads from a mirror laid out as the release is, without the provenance check. |

## Which helm you have

@sample install/which-helm.sh

It names the version and the commit it was built from. A `helm` built from a clone says `dev`.

**Kubernetes' CLI is also called `helm`**, and the one first on your `PATH` is the one that runs. The installer will replace an older helmstudio `helm`, but it never replaces another program called `helm`: it stops and tells you to set `HELM_INSTALL_DIR` instead. The uninstaller leaves it alone for the same reason.

## Upgrade

@sample install/upgrade.sh not-run: it needs the network; the gate checks the same release ruleset in test/packaging

`helm upgrade` replaces the running `helm` with the newest release, making the same checks the installer makes, and says so when there is nothing newer. It never installs an older release unless `--version` names one. A `helm` built from a clone is refused, and so is one reached through a link.

It reaches the network only when you run it. **Nothing checks for a new release on its own.**

## Uninstall

@sample install/uninstall.sh not-run: it needs the network; the gate runs the same uninstaller in test/packaging

It removes `helm` from `~/.local/bin`, or from `HELM_INSTALL_DIR`, with whatever an interrupted install left beside it, and only when that `helm` is helmstudio's. It removes nothing else — not `~/.helmstudio`, and not the `.helm` a studio keeps under `helm dev` — and it says so when there is nothing to remove.

## By hand

@sample install/by-hand.sh not-run: it needs the network; it is the installer's work written out, and the gate runs the installer itself in test/packaging

Then put `helm` from the unpacked directory somewhere on your `PATH`. The last line checks that the archive was built by this repository's workflow.

**The binaries are not signed.** Download them with `curl`, as above: macOS quarantines a file a browser downloads, and refuses to run an unsigned binary that is quarantined.

## Next

- [Quickstart](/docs/quickstart/) — a studio of your own, running, in a few minutes.
- [Develop with helm dev](/docs/guides/develop-in-isolation/) — what it does, and what it does not.
