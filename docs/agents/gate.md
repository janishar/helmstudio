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

**Visual regression (`make visual`).**
- **What it runs.** An installed Chrome, driven over its DevTools pipe by Go's standard library (`internal/chrome`). `HELM_CHROME` names the binary. A missing browser fails the gate unless `HELM_ALLOW_MISSING_BROWSER` is set.
- **The goldens.** `test/visual/golden/*.png` are compared exactly, pixel for pixel. They are tied to the Chrome major in `test/visual/golden/CHROME_MAJOR` and to the operating system in `OS_MAJOR` (`macOS 27`), which the browser itself reports; a different one fails and asks for a deliberate `make golden` and a look at the images. Both are checked before any screenshot is taken.
- **Theme end to end.** The same target runs the test that the launcher's theme reaches a running studio's page.
- **Contrast and token names** are checked by `go test` in `packages/helm-css`, against 03 §2a and §2b.

The Linux run is deliberate. macOS has a case-insensitive filesystem, which
hides a class of path bug that a Linux run surfaces on the first try.

**The export goldens (`go test ./test/media`).**
- **What it runs.** The timeline's export pipeline against the fixtures in `test/media/fixtures`, which are committed bytes made once by `test/media/make-fixtures.sh`. `HELM_FFMPEG` names the binary; a missing ffmpeg fails the gate unless `HELM_ALLOW_MISSING_FFMPEG` is set.
- **What is compared.** A copy is compared with its sources frame for frame, because nothing re-encoded it. A conform is compared with a golden of what the filter graph produced *before* any encoder saw it (`test/media/golden/*.framemd5`), since videotoolbox's bytes move with the operating system; the encoded file is then checked by probing it, never by its bytes. Its sound is checked by where each clip's tone lands, which is how a copied stream's priming would be caught.
- **The goldens** are tied to the ffmpeg major and the architecture in `test/media/golden/FFMPEG_MAJOR`; a different one fails and asks for a deliberate `make golden-media`.

**Currently not run.** As of M1, by the human's direction, `go test` runs on
the host only: the Linux and Windows test legs are out of the gate and no CI
workflow exists. `make vet-linux` type-checks the tree as Linux and runs
nothing. See `docs/decisions.md`, "2026-09-15 · M1 foundation". Until that
entry is superseded, the path bugs described above are not caught.

## What the gate cannot tell you

Nothing that touches a model. Metal builds, real weights, unified-memory
behaviour, ffmpeg on videotoolbox, Keychain, code signing and notarisation are
all checked by hand on an Apple Silicon Mac.

Every milestone lists its machine-bound checks. They queue for an integration
window rather than blocking the milestone — but they are never reported as
passing because the gate was green. An agent that writes "verified" about a
Metal path has told you nothing except that it did not understand the
boundary.
