#!/bin/sh
# Updates helmstudio to the newest release, or the one given: downloads and checks that release's pack, puts it in
# place of this folder and restarts (install.sh, HELMSTUDIO_FETCH). The settings and secrets in ~/.helmstudio
# (HELMSTUDIO_HOME) and the data in the Docker volumes stay.
#   ./update.sh               the newest release
#   ./update.sh 0.1.0-alpha.5
set -eu
d=$(cd "$(dirname "$0")" && pwd)
HELMSTUDIO_FETCH=1 HELMSTUDIO_DIR=${HELMSTUDIO_DIR:-$d} exec sh "$d/install.sh" "$@"
