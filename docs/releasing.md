# Releasing the packages

A studio author installs helmstudio's packages from their registries, without
this repository (`docs/design/04-packages.md` §8). This is how they get there.

Publishing is the maintainer's: a version on a registry cannot be taken back,
and every step below that reaches a registry is done by a person, or by a
workflow a person started by pushing a tag. No token is stored in the
repository or its secrets.

## What is published, and where

| Package | Registry | Tag that publishes it | A studio installs it with |
|---|---|---|---|
| `helm-runtime-sdk` for Go | the Go module proxy | `packages/helm-runtime-sdk/go/v<version>` | `go get github.com/janishar/helmstudio/packages/helm-runtime-sdk/go@v<version>` |
| its embedded provider | the Go module proxy | `packages/helm-runtime-sdk/go/embedded/v<version>`, with `v<version>` on the root module, which it imports | `go get github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded@v<version>` |
| `helm-runtime-sdk` for Python | PyPI | `packages/helm-runtime-sdk/python/v<version>` | `pip install helm-runtime-sdk` |
| `@helmstudio/runtime` | npm | `packages/helm-runtime-sdk/node/v<version>` | `npm install @helmstudio/runtime` |
| `@helmstudio/ui` | npm | `packages/helm-ui-sdk/v<version>` | `npm install @helmstudio/ui` |
| `@helmstudio/css` | npm | `packages/helm-css/v<version>` | `npm install @helmstudio/css` |
| the `helm` CLI | GitHub Releases | `v<version>`, the root module's tag | a download: see *Installing helm* below |

A pre-release installs with `pip install --pre`, `npm install <package>@next`,
or its exact version. The `helm` CLI is released with the root module, at the
runtime SDK's version, because it is built from the root module.

**Versions** (04 §9). The runtime SDK's three languages are generated from one
contract and move together, at one version; helm-ui-sdk and helm-css each have
their own. The three Go modules — the root, the runtime SDK and its embedded
provider — are tagged together at the runtime SDK's version, and every `go.mod`
in the repository requires that version (docs/decisions.md, M4 second review
#1). A version is written in the manifest of each package: `pyproject.toml` in
PEP 440's spelling (`1.0.0rc1`), `package.json` in semver's (`1.0.0-rc.1`), and
the `Version` constants in `packages/helm-ui-sdk` and `packages/helm-css`. The
test in `test/packaging` fails when any of them disagree.

## Once, before the first release

**PyPI.** Signed in to pypi.org, open *Your account → Publishing* and add a
pending publisher for GitHub:

- PyPI project name: `helm-runtime-sdk`
- owner: `janishar`
- repository: `helmstudio`
- workflow: `publish-python.yml`
- environment: `pypi`

A pending publisher does not reserve the name; the first publish does. In the
repository's *Settings → Environments*, the `pypi` environment is created by
the first run, and can be given a required reviewer there.

**npm.** Create the organisation `helmstudio` on npmjs.com; it is free for
public packages. npm sets a trusted publisher only on a package that already
exists, so the first version of each of the three packages is published by
hand, from the tagged commit:

    npm login
    cd packages/helm-runtime-sdk/node && npm publish --access public --tag next
    cd ../../helm-ui-sdk && npm publish --access public --tag next
    cd ../helm-css && npm publish --access public --tag next

Then, for each package on npmjs.com, *Settings → Trusted publishing → GitHub
Actions*: owner `janishar`, repository `helmstudio`, workflow
`publish-npm.yml`. From the next version on, the workflow publishes, and npm
attaches provenance.

## Each release

1. On `main`, set the versions: every file named under *Versions* above for
   the packages being released. helm-css writes its version into `helm.css`
   and `tokens.json`, so run `make css` after changing it. Then `make gate`,
   which fails on any version left behind.
2. Tag that commit and push the tags. For the runtime SDK, helm-ui-sdk and
   helm-css all at `1.0.0-rc.1`:

       git tag v1.0.0-rc.1
       git tag packages/helm-runtime-sdk/go/v1.0.0-rc.1
       git tag packages/helm-runtime-sdk/go/embedded/v1.0.0-rc.1
       git tag packages/helm-runtime-sdk/python/v1.0.0-rc.1
       git tag packages/helm-runtime-sdk/node/v1.0.0-rc.1
       git tag packages/helm-ui-sdk/v1.0.0-rc.1
       git tag packages/helm-css/v1.0.0-rc.1
       git push origin v1.0.0-rc.1 packages/helm-runtime-sdk/go/v1.0.0-rc.1 packages/helm-runtime-sdk/go/embedded/v1.0.0-rc.1 packages/helm-runtime-sdk/python/v1.0.0-rc.1 packages/helm-runtime-sdk/node/v1.0.0-rc.1 packages/helm-ui-sdk/v1.0.0-rc.1 packages/helm-css/v1.0.0-rc.1

   Name the tags rather than pushing `--tags`, which sends every local tag.

3. Watch the workflows in the Actions tab:
   - `publish-python` checks the tag against `pyproject.toml`, builds, checks
     and imports the wheel, and publishes;
   - `publish-npm` checks the tag against `package.json` and publishes;
   - `verify-go-tags` fetches each Go module through the proxy and builds a
     studio against the embedded provider;
   - `release-helm` builds `helm` for macOS on Apple Silicon and for Linux on
     x86-64 and ARM64, runs each binary on its own platform, and attaches the
     archives, their `SHA256SUMS` and their build provenance to the GitHub
     Release for `v<version>`, marked a pre-release when the version is one.

   A version already on its registry is skipped, and a release that exists has
   its archives replaced, so a tag pushed again changes nothing.
4. When the packages are on their registries and `helm` is on the release,
   change the documentation that builds or installs from the clone — the
   quickstart's steps 2 and 4, and the theming guide's note on
   `@helmstudio/runtime` — so it installs from them instead. Not before: a page
   must not name an install that fails.

## Installing helm

A studio author needs `helm dev` and `helm validate`, without cloning this
repository. `go install github.com/janishar/helmstudio/cmd/helm@<tag>` does not
work — Go refuses to install from a module whose `go.mod` has a `replace`
directive, and the root module's does — so each release carries prebuilt
binaries: `helm_<version>_darwin_arm64.tar.gz`,
`helm_<version>_linux_amd64.tar.gz` and `helm_<version>_linux_arm64.tar.gz`,
each holding `helm` and its licence.

On a Mac with Apple Silicon, for `1.0.0-rc.1`:

    curl -fsSLO https://github.com/janishar/helmstudio/releases/download/v1.0.0-rc.1/helm_1.0.0-rc.1_darwin_arm64.tar.gz
    curl -fsSLO https://github.com/janishar/helmstudio/releases/download/v1.0.0-rc.1/SHA256SUMS
    grep ' helm_1.0.0-rc.1_darwin_arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
    tar -xzf helm_1.0.0-rc.1_darwin_arm64.tar.gz

Then put `helm_1.0.0-rc.1_darwin_arm64/helm` in a directory on your `PATH`.
`gh attestation verify helm_1.0.0-rc.1_darwin_arm64.tar.gz --repo
janishar/helmstudio` checks that the archive was built by this repository's
workflow.

The binaries are not signed until the Mac app's release signs and notarises
them (M9). Download them with `curl`, as above: macOS quarantines a file a
browser downloads, and refuses to run an unsigned binary that is quarantined.
Kubernetes' CLI is also called `helm`; the one first on `PATH` is the one that
runs.
