# The gate. Everything that must pass before a commit lands.
# See docs/agents/gate.md. Checks are added as the contracts they check appear:
# M1 adds the dependency diff and the platform boundaries, M4 generated-client
# drift and conformance, M6 visual regression.
#
# Tests run on the host only. The Linux and Windows test legs are deliberately
# out of the gate for now (docs/decisions.md, M1 foundation); `vet-linux` only
# proves the Linux side of internal/platform still compiles.
#
# A target whose contract does not exist yet says so and passes. A target whose
# contract exists and fails, fails.

SHELL := /bin/bash
GO ?= go
HELM ?= ./bin/helm

.PHONY: gate fmt vet vet-linux boundaries deps test validate build clean generate drift sdk conformance visual golden css site site-test app app-run app-check

# The Go modules besides the root: the runtime SDK (stdlib only), its embedded
# provider, the conformance suite (docs/decisions.md M4 Q2, Q25), and the site's
# generator, a module of its own so the daemon never links goldmark (M10 Q7).
MODULES := packages/helm-runtime-sdk/go packages/helm-runtime-sdk/go/embedded test/conformance site

# Every file api/gen writes. A hand edit to any of them fails drift.
GENERATED := packages/helm-runtime-sdk/go/zz_types.go packages/helm-runtime-sdk/go/zz_client.go \
	internal/api/studioapi/zz_server.go \
	packages/helm-runtime-sdk/python/helm_runtime_sdk/_generated.py \
	packages/helm-runtime-sdk/node/src/generated.js \
	web/launcher.js

gate: fmt vet vet-linux boundaries deps drift test sdk conformance app-check site site-test visual validate
	@echo "gate: green"

fmt:
	@if [ ! -f go.mod ]; then echo "fmt: no go.mod yet, skipping"; \
	else out=$$(gofmt -l . ); \
	  if [ -n "$$out" ]; then echo "fmt: not formatted:"; echo "$$out"; exit 1; fi; \
	  echo "fmt: clean"; fi

vet:
	@if [ ! -f go.mod ]; then echo "vet: no go.mod yet, skipping"; \
	else $(GO) vet ./... && for m in $(MODULES); do (cd $$m && $(GO) vet ./...) || exit 1; done && echo "vet: clean"; fi

# Type-checks the tree as Linux sees it, so the Linux implementation behind
# each platform seam cannot silently stop compiling. Not a test run.
vet-linux:
	@GOOS=linux GOARCH=arm64 $(GO) vet ./... && for m in $(MODULES); do (cd $$m && GOOS=linux GOARCH=arm64 $(GO) vet ./...) || exit 1; done && echo "vet-linux: clean"

# Only internal/platform may make an operating-system decision or find the home
# directory; every other path comes from its directories helper. This is a text
# match over tracked and untracked Go files: it catches the known spellings of
# OS branching and home lookups, not every way to compute a path.
define boundaries_script
files=$(git ls-files -co --exclude-standard -- '*.go' | grep -v '^internal/platform/')
fail=0
os='aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|nacl|netbsd|openbsd|plan9|solaris|wasip1|windows|zos'
arch='386|amd64|arm|arm64|loong64|mips|mipsle|mips64|mips64le|ppc64|ppc64le|riscv64|s390x|wasm'
report() { if [ -n "$2" ]; then echo "boundaries: $1 outside internal/platform:"; echo "$2"; fail=1; fi; }
if [ -n "$files" ]; then
  report "an OS-suffixed file name" "$(grep -E "_($os)(_($arch))?(_test)?\.go$" <<<"$files")"
  report "an OS build constraint" "$(grep -nE "^//(go:build| \+build).*(^|[^A-Za-z0-9_])($os|unix)([^A-Za-z0-9_]|$)" $files)"
  report "runtime.GOOS" "$(grep -nE 'runtime\.GOOS' $files)"
  report "a home or OS directory lookup" "$(grep -nE 'os\.User(Home|Cache|Config)Dir|user\.Current\(|(Getenv|LookupEnv)\("(HOME|USERPROFILE|XDG_)|\$\{?HOME([^A-Za-z0-9_]|$)|"~/' $files)"
fi
[ $fail -eq 0 ] && echo "boundaries: clean"
endef
export BOUNDARIES_SCRIPT := $(value boundaries_script)

boundaries:
	@bash -c "$$BOUNDARIES_SCRIPT"

# Every module path added or changed on a require or replace line of a go.mod,
# and any change to a go or toolchain line, must be named — path and version as
# whole tokens, in one line — in a line added to docs/decisions.md over the
# same range. Every go.mod in the repository is read, tracked or not yet, so a
# module added beside the root is held to the rule from its first line
# (M10 task 9).
#
# The range starts where this branch left main (DEPS_BASE overrides it), and
# uncommitted changes count. It is a branch check: on main itself the base is
# HEAD, so a dependency committed straight to main is never compared against
# anything and passes.
define deps_script
base="$1"
if [ -z "$base" ]; then base=$(git merge-base HEAD main 2>/dev/null || git merge-base HEAD origin/main 2>/dev/null); fi
if [ -z "$base" ]; then echo "deps: no base to compare go.mod against; set DEPS_BASE=<commit>"; exit 1; fi
# One "name version" line per dependency fact. A replace is named by its
# module path and its target.
mods() {
  awk '
    function rep(i) { for (; i <= NF; i++) if ($i == "=>") return $(i+1); return "" }
    /^go[ \t]/        { print "go", $2; next }
    /^toolchain[ \t]/ { print "toolchain", $2; next }
    /^(require|replace)[ \t]*\(/ { blk = $1; next }
    blk != "" && /^\)/ { blk = ""; next }
    blk != "" && NF && $1 !~ /^\/\// { print $1, (blk == "replace" ? rep(1) : $2); next }
    /^require[ \t]+[^( \t]/ { print $2, $3; next }
    /^replace[ \t]+[^( \t]/ { print $2, rep(2); next }'
}
changed=""
while read -r f; do
  [ -f "$f" ] || continue
  c=$(comm -13 <(git show "$base:$f" 2>/dev/null | mods | sort -u) <(mods < "$f" | sort -u))
  if [ -n "$c" ]; then changed="$changed$(sed "s|^|$f |" <<<"$c")"$'\n'; fi
done < <(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)
if [ -z "$changed" ]; then echo "deps: no go.mod changed since ${base:0:7}"; exit 0; fi
added=$(git diff "$base" -- docs/decisions.md | grep '^+' | grep -v '^+++')
esc() { printf '%s' "$1" | sed 's/[][\.*^$+?(){}|]/\\&/g'; }
tok='[^A-Za-z0-9._/@+~-]'
missing=""
while read -r file name version; do
  [ -n "$file" ] || continue
  n=$(esc "$name"); v=$(esc "$version")
  grep -E "(^|$tok)$n($tok|\$)" <<<"$added" | grep -qE "(^|$tok)$v($tok|\$)" || missing="$missing
  $file: $name $version"
done <<<"$changed"
if [ -n "$missing" ]; then
  echo "deps: a go.mod changed since ${base:0:7} without a docs/decisions.md line naming each of:$missing"
  exit 1
fi
echo "deps: every go.mod change since ${base:0:7} is recorded in docs/decisions.md"
endef
export DEPS_SCRIPT := $(value deps_script)

DEPS_BASE ?=
deps:
	@bash -c "$$DEPS_SCRIPT" deps "$(DEPS_BASE)"

# The documentation and the site, into site/out (docs/decisions.md M10 Q8).
# The output is not committed; .github/workflows/site.yml builds it again where
# it is published.
site: build
	@cd site && $(GO) run ./cmd/site -helm ../bin/helm

# The site module's own tests: every internal link leads somewhere, every
# sample on every page runs or the page says why not — the quickstart as
# written, under helm dev — the annotations are in step with the studio files,
# the reference is the contract, and the site's stylesheet passes the theme
# lint. A missing python3, node or curl fails unless HELM_ALLOW_MISSING_CLIENTS
# is set; a missing ffmpeg, unless HELM_ALLOW_MISSING_FFMPEG is.
site-test:
	@cd site && $(GO) test -count=1 ./...

test:
	@if [ ! -f go.mod ]; then echo "test: no go.mod yet, skipping"; \
	else $(GO) test $$($(GO) list ./... | grep -v '/test/visual$$'); fi

# Visual regression of helm-css in both themes, and the theme reaching a
# running studio's page, in a real Chrome driven over its DevTools pipe
# (docs/decisions.md M6 Q16). A missing browser fails unless
# HELM_ALLOW_MISSING_BROWSER is set; HELM_CHROME names one.
visual:
	@$(GO) test -count=1 ./test/visual/

# Regenerate the visual goldens and record the Chrome major and the operating
# system they were made with. Review the images before committing them.
golden:
	@HELM_UPDATE_GOLDEN=1 $(GO) test -count=1 -run 'TestHelmCSSMatchesItsGoldensInBothThemes|TestTheSiteMatchesItsGoldens' ./test/visual/ && echo "golden: written to test/visual/golden; review them"

# Regenerate the export goldens and record the ffmpeg major and architecture
# they were made with (docs/decisions.md M8 Q15). These hash what the filter
# graph produced, so a change here is a change in what an export renders: look
# at the diff before committing it.
golden-media:
	@HELM_UPDATE_GOLDEN=1 $(GO) test -count=1 ./test/media/ && echo "golden-media: written to test/media/golden; review them"

# Build helm.css, helm.min.css and tokens.json from helm-css's four layers.
css:
	@$(GO) run ./packages/helm-css/cmd/build && echo "css: built"

# Regenerate the clients and the studio-api router from api/openapi.yaml.
generate:
	@$(GO) run ./api/gen && echo "generate: written"

# Generate into a scratch tree and compare with what is checked in, so a hand
# edit, or a contract change nobody regenerated, fails whether or not the files
# are committed yet.
drift:
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	$(GO) run ./api/gen -root "$$tmp" || exit 1; \
	fail=0; for f in $(GENERATED); do \
	  if ! cmp -s "$$tmp/$$f" "$$f"; then echo "drift: $$f differs from what api/gen generates from api/openapi.yaml; run make generate"; fail=1; fi; \
	done; [ $$fail -eq 0 ] && echo "drift: generated clients match api/openapi.yaml"

# The runtime SDK and embedded provider modules' own tests.
sdk:
	@for m in packages/helm-runtime-sdk/go packages/helm-runtime-sdk/go/embedded; do (cd $$m && $(GO) test ./...) || exit 1; done

# One suite against the daemon over HTTP and the embedded provider in process,
# plus the Python and Node clients' smoke tests (docs/decisions.md M4 Q25).
conformance:
	@cd test/conformance && $(GO) test -count=1 ./...

build:
	@if [ ! -f go.mod ]; then echo "build: no go.mod yet, skipping"; \
	else $(GO) build -o bin/helm ./cmd/helm && $(GO) build -o bin/helmstudio ./cmd/helmstudio; fi

# Every manifest in the registry validates, every time. A schema change that
# broke a real studio is caught here and nowhere else. Depends on build so a
# stale bin/helm can never validate with old code.
validate: build
	@shopt -s nullglob; set -- studios/*.yaml; \
	if [ ! -x $(HELM) ]; then echo "validate: helm not built yet, skipping"; \
	elif [ $$# -eq 0 ]; then echo "validate: no manifests yet, skipping"; \
	else $(HELM) validate "$$@"; fi

# The Mac app (docs/design/01-prd.md §12, docs/decisions.md 2026-09-18). The
# bundle is assembled here rather than by Xcode: everything that goes into it
# is visible in one place, the build needs only the Command Line Tools, and
# there is no project file to review. The shell is `app/`; the daemon it
# bundles is the same binary `make build` produces, and the registry beside it
# is what R72 ships with the shell and the daemon together.
#
# What this cannot do is sign for anyone else: R71 wants a Developer ID
# signature and notarisation, and this ad-hoc signs so the app runs on the
# machine that built it. A release signs with a real identity.
APP_VERSION ?= 0.0.0
APP_BUNDLE := bin/helmstudio.app

# The shell compiles. In the gate rather than `app` itself: a Swift error is a
# regression anyone can cause, and catching it costs a debug build rather than
# a release build, a bundle and a signature. Skipped where it cannot run, like
# every other machine-bound check here.
app-check:
	@if [ "$$(uname -s)" != "Darwin" ]; then echo "app-check: the Mac app builds on macOS only, skipping"; exit 0; fi
	@if ! command -v swift >/dev/null; then echo "app-check: no swift toolchain, skipping"; exit 0; fi
	@out=$$(cd app && swift build 2>&1) || { echo "$$out"; exit 1; }; \
	echo "app-check: the shell compiles"

app:
	@if [ "$$(uname -s)" != "Darwin" ]; then echo "app: the Mac app builds on macOS only, skipping"; exit 0; fi
	@if ! command -v swift >/dev/null; then echo "app: no swift toolchain, skipping"; exit 0; fi
	@rm -rf $(APP_BUNDLE)
	@mkdir -p $(APP_BUNDLE)/Contents/MacOS $(APP_BUNDLE)/Contents/Resources
	@$(GO) build -trimpath -ldflags="-s -w -X main.version=$(APP_VERSION)" \
		-o $(APP_BUNDLE)/Contents/MacOS/helmstudio-daemon ./cmd/helmstudio
	@out=$$(cd app && swift build -c release --disable-sandbox 2>&1) || { echo "$$out"; exit 1; }
	@cp app/.build/release/helmstudio $(APP_BUNDLE)/Contents/MacOS/helmstudio
	@sed 's/__VERSION__/$(APP_VERSION)/g' app/Resources/Info.plist > $(APP_BUNDLE)/Contents/Info.plist
	@printf 'APPL????' > $(APP_BUNDLE)/Contents/PkgInfo
	@mkdir -p $(APP_BUNDLE)/Contents/Resources/studios
	@shopt -s nullglob; set -- studios/*.yaml; \
	if [ $$# -gt 0 ]; then cp "$$@" $(APP_BUNDLE)/Contents/Resources/studios/; fi
	@out=$$(codesign --force --sign - --timestamp=none $(APP_BUNDLE)/Contents/MacOS/helmstudio-daemon 2>&1) || { echo "$$out"; exit 1; }
	@out=$$(codesign --force --sign - --timestamp=none $(APP_BUNDLE) 2>&1) || { echo "$$out"; exit 1; }
	@echo "app: $(APP_BUNDLE) — ad-hoc signed, not notarised (R71 needs a Developer ID)"

# Run the app that `make app` built, from the terminal, so its stderr is
# visible. `open` would detach it and swallow that.
app-run: app
	@$(APP_BUNDLE)/Contents/MacOS/helmstudio

clean:
	rm -rf bin app/.build
