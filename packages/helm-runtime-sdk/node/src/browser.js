// helmstudio's runtime SDK for a studio's page: the same generated client as
// the Node package, over fetch, with no token. The studio's server mounts the
// runtime SDK's proxy at /helm/, which adds the token (docs/decisions.md M6
// Q10), so a page never holds one.
//
//   <script type="module">
//     import { connect, themeBridge } from "/helm/sdk/v1/helm-runtime.js";
//     themeBridge();
//     window.helm = connect();
//   </script>
//
// Served by the daemon as /sdk/v1/helm-runtime.js. No Node built-ins.

import { API_VERSION, Client } from "./generated.js";
import { HelmError, Transport } from "./transport.js";
import { applyTheme, themeBridge } from "./theme.js";

export { API_VERSION, Client, HelmError, Transport, applyTheme, themeBridge };

// connect returns a client for the platform API behind the page's proxy.
export function connect({ base = "/helm/api/v1", fetch } = {}) {
  return new Client(new Transport(base, undefined, { fetch }));
}
