#!/bin/bash
# Installs helm, helmstudio's command-line tool, from a GitHub Release:
#
#   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/janishar/helmstudio/main/installer/install.sh)"
#
# It downloads the archive for this machine and the release's SHA256SUMS over
# HTTPS, refuses an archive whose checksum does not match, checks that the
# archive was built by this repository's release workflow when the GitHub CLI
# is signed in, runs the binary once, and moves it into ~/.local/bin. It needs
# no sudo and edits no shell profile (docs/releasing.md, "Installing helm").
#
#   HELM_VERSION=<version>    a version, such as 1.0.0-rc.1, instead of the newest release
#   HELM_INSTALL_DIR=<dir>    where helm goes, instead of ~/.local/bin
#   HELM_RELEASES_URL=<url>   a mirror laid out as <url>/v<version>/<file>; provenance is not checked
#
# Nothing runs until the last line calls main, so a download cut short does nothing.

set -euo pipefail

REPO="janishar/helmstudio"
# The platforms .github/workflows/release-helm.yml builds; test/packaging holds them together.
PLATFORMS="darwin_arm64 linux_amd64 linux_arm64"
WORK=""

say() { printf 'helm install: %s\n' "$*"; }
abort() {
  printf 'helm install: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || abort "$1 is required and is not on PATH"
}

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    abort "shasum or sha256sum is required to check the download"
  fi
}

platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) abort "helm is not released for $os" ;;
  esac
  case "$arch" in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) abort "helm is not released for $arch" ;;
  esac
  # A shell running under Rosetta on Apple Silicon says x86_64.
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
    arch=arm64
  fi
  case " $PLATFORMS " in
    *" ${os}_${arch} "*) printf '%s_%s' "$os" "$arch" ;;
    *) abort "helm is released for ${PLATFORMS// /, }, and this machine is ${os}_${arch}" ;;
  esac
}

# Downloads $1 to $2: over HTTPS only, unless the user named a mirror.
fetch() {
  if [ -n "${HELM_RELEASES_URL:-}" ]; then
    curl -fsSL --retry 3 -o "$2" "$1" || abort "could not download $1"
  else
    curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$2" "$1" || abort "could not download $1"
  fi
}

# The newest release that is not a pre-release, or, while there is none, the newest pre-release.
newest() {
  local json tag
  json="$(curl --proto '=https' --tlsv1.2 -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null || true)"
  if [ -z "$json" ]; then
    json="$(curl --proto '=https' --tlsv1.2 -fsSL "https://api.github.com/repos/$REPO/releases?per_page=30")" ||
      abort "could not list helmstudio's releases; set HELM_VERSION to install a version"
  fi
  tag="$(printf '%s\n' "$json" | { grep -o '"tag_name": *"v[0-9][^"]*"' || true; } | head -n 1 | sed 's/.*"v\([^"]*\)"$/\1/')"
  [ -n "$tag" ] || abort "helmstudio has not released helm yet"
  printf '%s' "$tag"
}

main() {
  local plat version install_dir archive base expected actual bin existing found reported
  # A version is only digits, letters, dots and hyphens, so it cannot change a URL or a path.
  local semver='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
  need curl
  need tar
  need awk
  need mktemp

  plat="$(platform)"
  version="${HELM_VERSION:-}"
  version="${version#v}"
  if [ -z "$version" ]; then
    version="$(newest)"
  fi
  [[ "$version" =~ $semver ]] || abort "\"$version\" is not a version, such as 1.0.0-rc.1"

  install_dir="${HELM_INSTALL_DIR:-$HOME/.local/bin}"
  archive="helm_${version}_${plat}.tar.gz"
  base="${HELM_RELEASES_URL:-https://github.com/$REPO/releases/download}"
  base="${base%/}/v$version"

  # Anything already called helm there must be helmstudio's: Kubernetes' CLI is also called helm.
  if [ -e "$install_dir/helm" ]; then
    existing="$("$install_dir/helm" </dev/null 2>&1 || true)"
    case "$existing" in
      *"studio manifests"*) ;;
      *) abort "$install_dir/helm is another program called helm; set HELM_INSTALL_DIR to install helmstudio's somewhere else" ;;
    esac
  fi

  mkdir -p "$install_dir" || abort "could not create $install_dir"
  # Downloaded beside where it goes, so the final move is a rename.
  WORK="$(mktemp -d "$install_dir/.helm-install.XXXXXX")" || abort "could not write to $install_dir"
  trap 'if [ -n "$WORK" ]; then rm -rf "$WORK"; fi' EXIT

  say "downloading helm $version for $plat from $base"
  fetch "$base/$archive" "$WORK/$archive"
  fetch "$base/SHA256SUMS" "$WORK/SHA256SUMS"

  expected="$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$WORK/SHA256SUMS")"
  [ -n "$expected" ] || abort "SHA256SUMS has no checksum for $archive; nothing was installed"
  actual="$(sha256 "$WORK/$archive")"
  [ "$expected" = "$actual" ] || abort "$archive does not match its checksum in SHA256SUMS; nothing was installed"
  say "the archive matches SHA256SUMS"

  if [ -n "${HELM_RELEASES_URL:-}" ]; then
    say "provenance not checked: the archive came from HELM_RELEASES_URL"
  elif command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh attestation verify "$WORK/$archive" --repo "$REPO" >/dev/null 2>&1 ||
      abort "$archive has no attestation from $REPO's release workflow; nothing was installed"
    say "the archive was built by $REPO's release workflow"
  else
    say "provenance not checked: with the GitHub CLI signed in, gh attestation verify checks it"
  fi

  # Only the binary is unpacked, so nothing else in an archive lands anywhere.
  tar -xzf "$WORK/$archive" -C "$WORK" "helm_${version}_${plat}/helm" 2>/dev/null ||
    abort "$archive holds no helm_${version}_${plat}/helm; nothing was installed"
  bin="$WORK/helm_${version}_${plat}/helm"
  if [ ! -f "$bin" ] || [ ! -x "$bin" ]; then
    abort "$archive holds no helm; nothing was installed"
  fi
  # helm --version names the version a release was built as. A release from
  # before helm had --version is known by its usage, which names studio manifests.
  reported="$("$bin" --version </dev/null 2>/dev/null || true)"
  case "$reported" in
    "helm $version" | "helm $version "*) ;;
    "helm "*) abort "the helm in $archive says it is $(printf '%s\n' "$reported" | head -n 1), not helm $version; nothing was installed" ;;
    *)
      case "$("$bin" </dev/null 2>&1 || true)" in
        *"studio manifests"*) ;;
        *) abort "the helm in $archive does not run on this machine; nothing was installed" ;;
      esac
      ;;
  esac

  chmod 0755 "$bin"
  mv -f "$bin" "$install_dir/helm" || abort "could not move helm into $install_dir"
  say "installed helm $version at $install_dir/helm"

  case ":$PATH:" in
    *":$install_dir:"*)
      found="$(command -v helm 2>/dev/null || true)"
      if [ -n "$found" ] && [ "$found" != "$install_dir/helm" ]; then
        say "$found comes before it on PATH, so \`helm\` runs that one"
      fi
      ;;
    *)
      say "$install_dir is not on PATH; add this line to your shell's profile, such as ~/.zprofile:"
      printf '\n    export PATH="%s:$PATH"\n\n' "$install_dir"
      ;;
  esac
}

main "$@"
