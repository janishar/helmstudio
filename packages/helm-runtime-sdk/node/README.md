# @helmstudio/runtime

helmstudio's runtime SDK for JavaScript: the client a studio uses to reach the
platform API — keep its files in the library, record what it makes in the
gallery with the parameters and inputs that made it, keep sessions and
settings, hand clips to the timeline — for Node and for the browser, and the
same-origin proxy a studio's server mounts at `/helm/` so its page never holds
a token.

    npm install @helmstudio/runtime@next

- `@helmstudio/runtime`: the Node client. `fromEnv()` reads the `HELM_API` and
  `HELM_TOKEN` that helmstudio, or `helm dev`, set for a studio.
- `@helmstudio/runtime/proxy`: `createProxy()`, for a Node server.
- `@helmstudio/runtime/browser`: `connect()` and `themeBridge()`, for the page.
  helmstudio also serves this build to a studio's page at
  `/helm/sdk/v1/helm-runtime.js`, through the proxy, so a page with no build
  step needs no install.

The documentation — the quickstart, the guides, and every call in JavaScript,
Python and Go — is at https://helmstudio.in/docs/.
