#!/bin/bash
# Uninstalls helm, helmstudio's command-line tool, as installer/install.sh installed it:
#
#   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/janishar/helmstudio/main/installer/uninstaller.sh)"
#
# It removes helm from ~/.local/bin, and whatever an interrupted install left
# beside it, only when that helm is helmstudio's: another program called helm,
# such as Kubernetes' CLI, is left alone, and so is a link install.sh never
# makes. It removes nothing else: not helmstudio's data, not the .helm a studio
# keeps under helm dev, not a line in a shell profile (docs/releasing.md,
# "Installing helm").
#
#   HELM_INSTALL_DIR=<dir>    where helm was installed, instead of ~/.local/bin
#
# Nothing runs until the last line calls main, so a download cut short does nothing.

set -euo pipefail

say() { printf 'helm uninstall: %s\n' "$*"; }
abort() {
  printf 'helm uninstall: %s\n' "$*" >&2
  exit 1
}

# helmstudio's helm names studio manifests in its usage. It is run with no
# input, so nothing waits on a terminal.
is_helmstudio_helm() {
  case "$("$1" </dev/null 2>&1 || true)" in
    *"studio manifests"*) return 0 ;;
    *) return 1 ;;
  esac
}

main() {
  local install_dir helm leftover found removed=""
  install_dir="${HELM_INSTALL_DIR:-$HOME/.local/bin}"
  helm="$install_dir/helm"

  if [ -L "$helm" ]; then
    abort "$helm is a link, which install.sh never makes, so it was left alone"
  fi
  if [ -e "$helm" ]; then
    if [ ! -f "$helm" ] || ! is_helmstudio_helm "$helm"; then
      abort "$helm is another program called helm, so it was left alone"
    fi
    rm -f "$helm" || abort "could not remove $helm"
    say "removed $helm"
    removed=yes
  fi

  # install.sh downloads into a directory beside helm and removes it when it
  # ends; one killed before then leaves it behind.
  for leftover in "$install_dir"/.helm-install.??????; do
    [ -d "$leftover" ] || continue
    rm -rf "$leftover" || abort "could not remove $leftover"
    say "removed $leftover, left by an interrupted install"
  done

  if [ -z "$removed" ]; then
    say "helm is not installed in $install_dir; nothing to remove"
  fi
  found="$(command -v helm 2>/dev/null || true)"
  if [ -n "$found" ] && [ "$found" != "$helm" ] && is_helmstudio_helm "$found"; then
    say "helmstudio's helm is still on PATH at $found; to remove it, set HELM_INSTALL_DIR=$(dirname "$found")"
  fi
  say "helmstudio's data in ~/.helmstudio, and the .helm a studio keeps under helm dev, are not touched"
}

main "$@"
