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

.PHONY: gate fmt vet vet-linux boundaries deps test validate build clean

gate: fmt vet vet-linux boundaries deps test validate
	@echo "gate: green"

fmt:
	@if [ ! -f go.mod ]; then echo "fmt: no go.mod yet, skipping"; \
	else out=$$(gofmt -l . ); \
	  if [ -n "$$out" ]; then echo "fmt: not formatted:"; echo "$$out"; exit 1; fi; \
	  echo "fmt: clean"; fi

vet:
	@if [ ! -f go.mod ]; then echo "vet: no go.mod yet, skipping"; \
	else $(GO) vet ./... && echo "vet: clean"; fi

# Type-checks the tree as Linux sees it, so the Linux implementation behind
# each platform seam cannot silently stop compiling. Not a test run.
vet-linux:
	@GOOS=linux GOARCH=arm64 $(GO) vet ./... && echo "vet-linux: clean"

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

# Every module path added or changed on a require or replace line of go.mod,
# and any change to the go or toolchain line, must be named — path and version
# as whole tokens, in one line — in a line added to docs/decisions.md over the
# same range.
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
changed=$(comm -13 <(git show "$base:go.mod" 2>/dev/null | mods | sort -u) <(mods < go.mod | sort -u))
if [ -z "$changed" ]; then echo "deps: go.mod unchanged since ${base:0:7}"; exit 0; fi
added=$(git diff "$base" -- docs/decisions.md | grep '^+' | grep -v '^+++')
esc() { printf '%s' "$1" | sed 's/[][\.*^$+?(){}|]/\\&/g'; }
tok='[^A-Za-z0-9._/@+~-]'
missing=""
while read -r name version; do
  n=$(esc "$name"); v=$(esc "$version")
  grep -E "(^|$tok)$n($tok|\$)" <<<"$added" | grep -qE "(^|$tok)$v($tok|\$)" || missing="$missing
  $name $version"
done <<<"$changed"
if [ -n "$missing" ]; then
  echo "deps: go.mod changed since ${base:0:7} without a docs/decisions.md line naming each of:$missing"
  exit 1
fi
echo "deps: every go.mod change since ${base:0:7} is recorded in docs/decisions.md"
endef
export DEPS_SCRIPT := $(value deps_script)

DEPS_BASE ?=
deps:
	@bash -c "$$DEPS_SCRIPT" deps "$(DEPS_BASE)"

test:
	@if [ ! -f go.mod ]; then echo "test: no go.mod yet, skipping"; \
	else $(GO) test ./...; fi

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

clean:
	rm -rf bin
