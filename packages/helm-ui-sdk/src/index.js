// helm-ui-sdk: the prebuilt components (docs/design/04-packages.md §5).
//
//   <link rel="stylesheet" href="/helm/sdk/v1/helm.css">
//   <script type="module">
//     import { connect, themeBridge } from "/helm/sdk/v1/helm-runtime.js";
//     import "/helm/sdk/v1/helm-ui.js";
//     themeBridge();
//     window.helm = connect();
//   </script>
//   <helm-gallery scope="self" kind="video"></helm-gallery>
//
// Importing this module registers <helm-terminal>, <helm-gallery> and
// <helm-player>. They are custom elements: no React, no framework, no build
// step. helm-timeline ships with M8b.
//
// Nothing here imports the runtime SDK. A component is handed a client — by
// `el.client = …` or by `window.helm` — and calls methods on it. That is the
// whole of its dependency, which is why the arrow in §11 never reverses.

export { HelmElement, bytes, clock, define, el, kindOf, message, timecode } from "./base.js";
export { HelmTerminal, progress, spans, strip } from "./terminal.js";
export { HelmGallery, facts, origin } from "./gallery.js";
export { HelmPlayer, NEEDS_FFMPEG } from "./player.js";

/** Version is helm-ui-sdk's own; it moves independently of css and runtime (04 §9). */
export const VERSION = "1.0.0";
