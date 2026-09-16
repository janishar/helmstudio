# @helmstudio/ui

helmstudio's prebuilt components, as custom elements: `helm-terminal`,
`helm-gallery`, `helm-player` and `helm-timeline`. A component is given a
runtime client and calls its methods; it never builds a URL or holds a token.

    npm install @helmstudio/ui@next

helmstudio also serves these components to a studio's page at
`/helm/sdk/v1/helm-ui.js`, through the runtime SDK's proxy, so a page with no
build step needs no install. They are themed by helm-css's tokens, which reach
inside each component.

The documentation is at https://helmstudio.in/docs/.
