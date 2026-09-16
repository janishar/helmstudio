# Capabilities

A studio asks for the platform services it uses under `capabilities`, and gets a token that allows those and nothing else. A studio that asks for none gets no token at all, and still installs, launches and runs.

A person deciding whether to run a stranger's code is not helped by the word `gallery.read_all`. So the approval screen never shows a capability's name. It shows the sentence below, and the two that reach beyond the studio's own work are shown as warnings:

@capabilities

The screen lists them in this order, whatever order the manifest declares them in, so two studios asking for the same things read the same way. A capability this version of helmstudio does not recognise is shown too, named as itself, with a warning.

## Asking for the minimum

Ask for what the studio calls, and nothing more. A call the token does not allow is refused with `403` and the error `capability_required`, naming the capability — under `helm dev` exactly as under helmstudio, because both issue the same token. Every operation in the [API reference](/docs/reference/api/) names the capability it needs.

Whether a studio asks for more than it uses is criterion 9. It needs the studio's source to check, so nothing scores it yet.
