# Theme with helm-css

helm-css is the design system helmstudio's own screens are built with: tokens for colour, space, type and motion in a dark and a light theme, and classes for layout and components, all prefixed `helm-`. A studio that wears it looks like it belongs, follows the theme the person chose in the launcher, and keeps its own colour.

This guide builds a small studio, lantern studio, whose page does all three. It asks for no capabilities: theming needs no token.

@sample theming/helmstudio.yaml

## The server mounts the proxy

A page reaches helmstudio through its own server, at `/helm/`, never directly. The runtime SDK's proxy answers three things there: helm-css and the other SDK files at `/helm/sdk/v1/`, the studio's colour at `/helm/accent.css`, and the platform API at `/helm/api/v1/`, with the studio's token added on the way. The page never holds the token, and a launcher operation — install, launch, stop — is never forwarded.

@sample theming/server.py

The Go and JavaScript runtime SDKs ship the same proxy, and the conformance suite holds all three to the same behaviour. The same server in Go:

@sample theming/go/main.go

And in JavaScript, with `@helmstudio/runtime` installed from npm:

@sample theming/node/server.mjs

## The page links helm-css and the studio's hue

@sample theming/index.html

`themeBridge()` follows the launcher's theme stream, and sets `data-theme` on the page as the person switches between System, Light and Dark. With no stream, as when the page is opened on its own, the page follows the operating system.

Behind `/helm/sdk/v1/`, the proxy serves the major the studio pins in `sdk`. helmstudio refuses to launch a studio that pins a major it does not serve, so an update never moves a studio across one.

## The studio's own stylesheet uses tokens

@sample theming/studio.css

Every colour is a token, so the stylesheet is right in both themes without a line for either. `--helm-studio-accent` is the studio's `hue` for whichever theme is showing, and `--helm-on-studio-accent` is the text colour that contrasts with it. A studio with no `hue` is given one from a fixed set, chosen by its id, so it is the same at every launch.

In the launcher, a studio's hue is only ever a stripe, a dot and its clips on the timeline. On the studio's own page it is the studio's to use, for its main action as here.

## Lint it

@sample theming/lint.sh

`helm validate -theme` reads a studio's stylesheets and flags colour literals and font families other than IBM Plex, skipping a vendored copy of helm-css under `vendor/helm/`. It is advisory, and `-strict` makes a finding fail, as a registry check would. Give `-strict` before `-theme`: after the directory, it is read as a file name.

It does not check colours set from JavaScript, or contrast as rendered. Theme conformance is criterion 12, and scoring it needs the studio's stylesheets and a rendered page, so nothing scores it yet.
