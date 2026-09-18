# Weights

A studio declares the model files it needs under `weights`, and its commands refer to them by name, as `{models.<name>}`. How the files arrived — downloaded by helmstudio, or already on the machine — is invisible to the studio: either way the placeholder becomes a real path to a directory.

## Where the files are

Everything helmstudio keeps is under `~/.helmstudio`, and weights are one root of the five.

@diagram where-files-live caption: The expensive things are never copied in. A weight you already downloaded and a checkout you already have are reached where they are, by a symlink pointing out of the tree.

## Declaring a weight

| Field | Says |
|---|---|
| `name` | The placeholder key: `{models.<name>}`. |
| `repo` | The Hugging Face repository to download from. |
| `revision` | The branch, tag or commit to download. `main` when it is left out. |
| `dest` | The directory name under the shared models directory. |
| `size_gb` | How large it is, so free disk is checked before a byte moves. |
| `files` | Which paths to download — and which paths must be present when a directory is linked instead. |
| `selectable` | One of a set the user chooses between, bound to `{models.selected}`. |
| `optional` | A weight install does not wait for. A launch whose command uses it before it is fetched is refused. |

[Process groups](/docs/concepts/process-groups/) has a manifest that declares one.

## Managed: downloaded by helmstudio

A managed weight is downloaded into `<models>/<dest>`, in helmstudio's shared models directory, never into the studio's checkout. Downloads resume per file after an interruption. A repository already downloaded at the same revision is not downloaded again for a second studio: both use the one copy, and it cannot be deleted while either needs it. A gated repository that refuses the download asks for a Hugging Face token rather than failing the install.

## Linked: a directory you already have

Anyone who follows model releases already has the checkpoints. A weight can be **linked** to a directory instead of downloaded: helmstudio checks the directory is readable and holds the declared `files`, and makes `<models>/<dest>` a symbolic link to it.

A linked directory is **read-only** to helmstudio. It never writes into it, never completes a partial one, and never deletes it: removing a linked weight removes the link and nothing else. A linked directory that disappears — a drive unplugged, a folder moved — is recorded as missing, and a launch that needs it is refused with the path it expected.

## Under `helm dev`

`helm dev` downloads nothing. Link each weight a command uses with `-link <name>=<directory>`; a launch whose command uses a weight that is not linked is refused, naming it. See [develop in isolation](/docs/guides/develop-in-isolation/).
