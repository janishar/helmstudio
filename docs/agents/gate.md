# The gate

`make gate` is everything that must pass before a commit lands. It is built in
M1 and grows as the contracts it checks come into existence.

| Check | From |
|---|---|
| `gofmt -l` clean | M1 |
| `go vet ./...` | M1 |
| dependency diff — `go.mod` changed without a decision-log entry fails | M1 |
| `go test ./...` on macOS **and** Linux | M1 |
| `helm validate studios/*.yaml` | M1 (the command lands in M0) |
| generated-client drift — regenerate, fail on a diff | M4 |
| conformance suite × both providers | M4 |
| visual regression, both themes | M6 |
| export goldens × the media fixtures | M8 |
| the site builds, its links lead somewhere, and every sample on it runs | M10 |
| the published packages agree: versions, where each comes from, what npm packs, a workflow per package; `helm` builds for every released platform | packaging |

**Visual regression (`make visual`).**
- **What it runs.** An installed Chrome, driven over its DevTools pipe by Go's standard library (`internal/chrome`). `HELM_CHROME` names the binary. A missing browser fails the gate unless `HELM_ALLOW_MISSING_BROWSER` is set.
- **The goldens.** `test/visual/golden/*.png` are compared exactly, pixel for pixel. They are tied to the Chrome major in `test/visual/golden/CHROME_MAJOR` and to the operating system in `OS_MAJOR` (`macOS 27`), which the browser itself reports; a different one fails and asks for a deliberate `make golden` and a look at the images. Both are checked before any screenshot is taken.
- **What is pinned** (M6b): helm-css's tokens, components and layout; the three `helm-ui-sdk` components; and each of the launcher's screens at 1280, 1000 and 380 px, drawn from the canned data in `test/visual/fixtures/fake.js` against the real modules the daemon serves. Time is frozen in the fixtures, because every screen states an elapsed time.
- **What is not pinned, and why** (M6b): a decoded raster image. A thumbnail in the page changes how Chrome composites the panel around it, by one unit in one channel at the rounded corners and differently from run to run, so `helm-gallery`'s goldens carry no thumbnail. The thumbnail path, and the component behaviour a picture cannot show, are held by `TestComponentsBehave`, which reads the DOM.
- **Theme end to end.** The same target runs the test that the launcher's theme reaches a running studio's page.
- **Contrast and token names** are checked by `go test` in `packages/helm-css`, against 03 §2a and §2b.
- **The dependency arrow** is checked by `go test` in `packages/helm-ui-sdk`: no component names the wire, constructs a client or imports anything outside the package; the runtime SDK names no component; helm-css styles no component's element; every token a component uses exists; and the component stylesheets hold no colour literal.

The Linux run is deliberate. macOS has a case-insensitive filesystem, which
hides a class of path bug that a Linux run surfaces on the first try.

**The export goldens (`go test ./test/media`).**
- **What it runs.** The timeline's export pipeline against the fixtures in `test/media/fixtures`, which are committed bytes made once by `test/media/make-fixtures.sh`. `HELM_FFMPEG` names the binary; a missing ffmpeg fails the gate unless `HELM_ALLOW_MISSING_FFMPEG` is set.
- **What is compared.** A copy is compared with its sources frame for frame, because nothing re-encoded it. A conform is compared with a golden of what the filter graph produced *before* any encoder saw it (`test/media/golden/*.framemd5`), since videotoolbox's bytes move with the operating system; the encoded file is then checked by probing it, never by its bytes. Its sound is checked by where each clip's tone lands, which is how a copied stream's priming would be caught.
- **The goldens** are tied to the ffmpeg major and the architecture in `test/media/golden/FFMPEG_MAJOR`; a different one fails and asks for a deliberate `make golden-media`.

**The site (`make site`, `make site-test`).**
- **What it builds.** `site/`'s generator, a module of its own, writes the documentation and the site into `site/out`, which is not committed: Markdown pages from `site/content`, the API reference from `api/openapi.yaml`, the manifest reference from `schema/manifest.json`, the CLI reference from `helm`'s own usage, and the four annotated manifests from `studios/*.yaml`. The output is emptied first, so a page for something the contract no longer has cannot survive a build.
- **What fails it.** A link or `#fragment` that leads nowhere, checked in the written HTML under both `/` and a project-pages base; a documented client call the generated Go, Python or JavaScript client does not have; a studio file whose digest is not the one its annotations were written against, a field with no note, or a note for a field that is not there; a sample file on no page; and a code block written inline in a page.
- **The samples.** Every code sample is a file under `site/samples`, and `site/gen/samples_test.go` names the test that runs each one. The quickstart runs as written, step by step, in a workspace linked to the checkout, with `helm dev` serving the example studio on port 8765 — the port the page names, so the test fails when it is taken. The Python samples run inside a studio under `helm dev`; the theming studio runs with its Python, Go and JavaScript servers; the Go embedded provider sample records with no daemon; the manifests validate. A step that needs the network — the clone, and `pip install` — is not run, and the page says so beside it, and a test fails when a sample is neither run nor marked.
- **What it needs.** `python3`, `node` and `curl`, or `HELM_ALLOW_MISSING_CLIENTS`; `ffmpeg` for the timeline sample, or `HELM_ALLOW_MISSING_FFMPEG`.
- **The goldens.** The landing page and the quickstart, in both themes at 1280, 1000 and 380 px, are pinned by `TestTheSiteMatchesItsGoldens` in `test/visual`, with the same Chrome and operating-system pins as helm-css's. An edit to either page changes them: `make golden`, and look at the images.
- **The dependency diff** reads every `go.mod` in the repository, so the site module's dependencies are recorded like the root's.

**The packages (`go test ./test/packaging`, part of `make test`).** A release is a tag on a commit that passed the gate (`docs/releasing.md`), so what a registry will be handed is checked before a tag exists: the runtime SDK at one version in Python, JavaScript and every `go.mod`; the version constants in helm-ui-sdk and helm-css against their `package.json`; each npm package's repository, public access and licence; what `npm pack --dry-run` would publish against what each package's `exports` and `style` name; helm-ui-sdk's peer ranges against its siblings' versions; a publishing workflow tag for every package; and `helm` cross-compiled, with cgo off and the release's own `-ldflags`, which stamp the version `helm --version` names on this machine, for every platform `release-helm.yml` builds, which are also the platforms it runs them on and the archives `docs/releasing.md` names; and `installer/install.sh`, run against a release the test serves: it installs an archive that matches `SHA256SUMS` and replaces an older helmstudio `helm`, refuses a mismatched checksum, a `HELM_VERSION` that is not a version and another program called `helm`, and accepts exactly the platforms `release-helm.yml` builds; and `installer/uninstaller.sh`, which removes helmstudio's `helm` and an interrupted install's leftovers, leaves another `helm` and a link alone, and leaves a directory as it was before an install. It needs `npm`, `bash`, `curl` and `tar`, or `HELM_ALLOW_MISSING_CLIENTS`. The workflows themselves run only on a pushed tag, where the gate does not reach.

**Currently not run.** As of M1, by the human's direction, `go test` runs on
the host only: the Linux and Windows test legs are out of the gate and no CI
workflow runs it. The repository's one workflow, `.github/workflows/site.yml`,
publishes the site and checks nothing else. `make vet-linux` type-checks the
tree as Linux and runs nothing. See `docs/decisions.md`, "2026-09-15 · M1
foundation". Until that entry is superseded, the path bugs described above are
not caught.

## What the gate cannot tell you

Nothing that touches a model. Metal builds, real weights, unified-memory
behaviour, ffmpeg on videotoolbox, Keychain, code signing and notarisation are
all checked by hand on an Apple Silicon Mac.

Every milestone lists its machine-bound checks. They queue for an integration
window rather than blocking the milestone — but they are never reported as
passing because the gate was green. An agent that writes "verified" about a
Metal path has told you nothing except that it did not understand the
boundary.
