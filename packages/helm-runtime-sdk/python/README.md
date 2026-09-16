# helm-runtime-sdk

helmstudio's runtime SDK for Python: the client a studio uses to reach the
platform API — keep its files in the library, record what it makes in the
gallery with the parameters and inputs that made it, keep sessions and
settings, hand clips to the timeline — and the same-origin proxy that lets its
page use the API and helm-css without holding a token.

    pip install --pre helm-runtime-sdk

It uses the standard library only, and needs Python 3.9 or newer.

A studio built with it runs under helmstudio, or on its own under `helm dev`,
which set `HELM_API` and `HELM_TOKEN` for it:

    from helm_runtime_sdk import from_env

    helm = from_env()
    asset = helm.assets.adopt({"path": path, "kind": "image"})
    helm.gallery.add({"kind": "image", "asset_id": asset["id"], "params": {"prompt": prompt}})

The proxy is `helm_runtime_sdk.proxy`.

The documentation — the quickstart, the guides, and every call in Python, Go
and JavaScript — is at https://helmstudio.in/docs/.
