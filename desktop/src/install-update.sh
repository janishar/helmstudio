#!/bin/sh
# The update's swap (src/update.js): waits for the app to exit, mounts the .dmg read-only, copies its helmstudio.app
# beside the running bundle (ditto), swaps the two, clears the quarantine flag on the new copy only, and opens the app
# again. On any failure the old app stays where it was and opens again.
#   install-update.sh <app pid> <dmg> <bundle path> [NAME=VALUE ...]   (each NAME=VALUE goes to open --env)
set -u
pid=$1 dmg=$2 app=$3
shift 3
n=$#
for e in "$@"; do set -- "$@" --env "$e"; done
shift "$n"

echo "$(date) update: waiting for $pid to exit"
while kill -0 "$pid" 2>/dev/null; do sleep 0.5; done

new="$app.new" old="$app.old" ok=0
mnt=$(mktemp -d "${TMPDIR:-/tmp}/helmstudio-update.XXXXXX")
if hdiutil attach -nobrowse -noautoopen -readonly -mountpoint "$mnt" "$dmg" >/dev/null; then
  rm -rf "$new"
  [ -d "$mnt/helmstudio.app" ] && ditto "$mnt/helmstudio.app" "$new" && ok=1
  hdiutil detach "$mnt" -quiet || hdiutil detach "$mnt" -force -quiet
fi
rmdir "$mnt" 2>/dev/null

if [ "$ok" = 1 ]; then
  xattr -dr com.apple.quarantine "$new" 2>/dev/null
  rm -rf "$old"
  if mv "$app" "$old" && mv "$new" "$app"; then
    rm -rf "$old" && rm -f "$dmg"
    echo "$(date) update: installed $app"
  else
    [ -d "$app" ] || mv "$old" "$app"
    rm -rf "$new"
    echo "$(date) update: the swap failed; the old app stays"
  fi
else
  rm -rf "$new"
  echo "$(date) update: could not copy the app out of $dmg; the old app stays"
fi
exec open -n "$@" "$app"
