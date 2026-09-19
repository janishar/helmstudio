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
| `local_path` | A folder on this machine that already holds the weight. Links it instead of downloading. Development only: a registry manifest never names a folder on someone's machine. |

[Process groups](/docs/concepts/process-groups/) has a manifest that declares one.

## Managed: downloaded by helmstudio

A managed weight is downloaded into `<models>/<dest>`, in helmstudio's shared models directory, never into the studio's checkout. Downloads resume per file after an interruption. A repository already downloaded at the same revision is not downloaded again for a second studio: both use the one copy, and it cannot be deleted while either needs it. A gated repository that refuses the download asks for a Hugging Face token rather than failing the install.

## Linked: a directory you already have

Anyone who follows model releases already has the checkpoints. A weight can be **linked** to a directory instead of downloaded: helmstudio checks the directory is readable and holds the declared `files`, and links them rather than fetching them.

How the link is made depends on what the weight declares. One that lists `files` gets a real directory at `<models>/<dest>` holding **one symbolic link per file**, so a studio is handed exactly what its manifest names and never the rest of the folder. One that lists no files has `<models>/<dest>` become a symbolic link to the directory itself.

There are three ways to say so, and they are for different moments:

| Say it with | Where it lives | Use it when |
|---|---|---|
| `local_path` on the weight | the manifest | You are writing the manifest and the weight is already on your machine. Never in a manifest a registry carries. |
| `-link <weight>=<directory>` | the `helm dev` command line | You are developing and do not want to edit the manifest to do it. |

A fourth field shares the name and is a different thing: `local_path` at the **top level** of a manifest points at a studio checkout you already have, so install skips the clone. That is about the studio's code; the one in `weights` is about its model files. Both are development-only, and either makes a manifest Draft — see [publishing](/docs/publishing/).

A linked directory is **read-only** to helmstudio. It never writes into it, never completes a partial one, and never deletes it: removing a linked weight removes the link and nothing else. A linked directory that disappears — a drive unplugged, a folder moved — is recorded as missing, and a launch that needs it is refused with the path it expected.

## Under `helm dev`

`helm dev` downloads nothing. Link each weight a command uses with `-link <name>=<directory>`; a launch whose command uses a weight that is not linked is refused, naming it. See [develop in isolation](/docs/guides/develop-in-isolation/).

A studio with `selectable` weights also needs to be told which one to launch with. Installing makes that choice on the approval screen; `helm dev` has no approval screen, so `-select <name>` is where you make it. With nothing selected the launch is refused and the choices are named — an arbitrary one is never picked for you. The choice is kept in the checkout's own `.helm`, so it holds until you change it.
