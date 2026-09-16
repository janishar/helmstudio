# @helmstudio/css

helm-css, helmstudio's design system: tokens, base, layout and components, in a
dark and a light theme, as plain CSS with custom properties. Every class is
prefixed `helm-`, nothing is `!important`, and a studio overrides a rule by
writing one.

    npm install @helmstudio/css@next

`helm.css` is the four layers together, and `helm.min.css` the same, minified.
`tokens.json` holds the tokens for anything that is not CSS. The IBM Plex fonts
it names are in `fonts/`, under their own licence.

helmstudio also serves helm-css to a studio's page at `/helm/sdk/v1/helm.css`,
through the runtime SDK's proxy, so a page running under helmstudio needs no
install.

The theming guide is at https://helmstudio.in/docs/guides/theming/.
