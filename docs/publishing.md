# Publishing safely

How helmstudio's packages reach their registries without a stored secret, the
setup each registry needs once, and what keeps anyone else from publishing.
`docs/releasing.md` is the procedure a release follows; it relies on all of
this being in place.

## How a tag becomes a published version

No token that can publish is kept anywhere: not in the repository, not in its
secrets, not on a maintainer's machine. A maintainer pushes a tag, a workflow
checks the tag against the version the package declares, and the registry
accepts the upload because GitHub vouches, with a short-lived OpenID Connect
token, that it came from that workflow in this repository. That is trusted
publishing.

| Tag | Workflow | Publishes to | Trusted because |
|---|---|---|---|
| `packages/helm-runtime-sdk/python/v<version>` | `publish-python.yml` | PyPI | the project's trusted publisher names this repository, the workflow and the `pypi` environment |
| `packages/helm-runtime-sdk/node/v<version>`, `packages/helm-ui-sdk/v<version>`, `packages/helm-css/v<version>` | `publish-npm.yml` | npm | each package's trusted publisher names this repository, the workflow and the `npm` environment |
| `v<version>` | `release-helm.yml` | GitHub Releases | the job's own `GITHUB_TOKEN`, which can write only in that job |
| `v<version>`, `packages/helm-runtime-sdk/go/v<version>`, `packages/helm-runtime-sdk/go/embedded/v<version>` | `verify-go-tags.yml` | the Go module proxy | nothing to trust: the tag is the release, and the checksum database records it the first time the proxy fetches it |

The consequence, which npm prints when a trusted publisher is added: **anyone
with write access to the repository can publish.** Everything below narrows who
can push a release tag, puts a person with a second factor in front of every
registry publish, and says what to do when something goes wrong.

## Accounts

- **Two-factor authentication** on every GitHub, PyPI and npm account that can
  push a release tag, approve a publish or change a package, with a security
  key or passkey where the site offers one. PyPI requires it of every account,
  and npm refuses a publish from an account without it. Keep each site's
  recovery codes offline.
- **No registry tokens.** Trusted publishing replaces them. Never create a
  token that bypasses two-factor authentication, and never put a token in the
  repository's secrets or variables.
- **Log out of npm when the task is done.** `npm login` writes a token to
  `~/.npmrc`, and `npm logout` revokes it.
- **Write access is publishing access.** Give it only to people who may
  publish.

## The repository

In the repository's settings on GitHub:

- **Release tags.** *Rulesets → New ruleset → New tag ruleset*: active,
  targeting `v*` and `packages/**/*`, with *Restrict creations*, *Restrict
  updates* and *Restrict deletions*, and the Repository admin role on the
  bypass list. Only an admin can then push a release tag, and nobody else can
  move or delete one. The pattern is `packages/**/*`, not `packages/**`: a
  ruleset matches with `File::FNM_PATHNAME`, so `**` at the end of a pattern
  stops at the next `/` and matches no package's tag. The ruleset's page says
  how many existing tags it applies to; check it counts every release tag.
- **The default branch.** The scripts in `installer/` are served from `main`,
  so whoever can change `main` decides what the one-line install and uninstall
  run. *Rulesets →
  New ruleset → New branch ruleset*: active, targeting the default branch, with
  *Restrict deletions*, *Block force pushes* and *Require a pull request before
  merging*, and the Repository admin role on the bypass list.
- **Environments.** *Environments*, one for each place the workflows publish
  to, each with a required reviewer and *Deployment branches and tags* limited
  to the refs that publish there:
  - `pypi`: tags `packages/helm-runtime-sdk/python/v*`
  - `npm`: tags `packages/helm-runtime-sdk/node/v*`, `packages/helm-ui-sdk/v*`
    and `packages/helm-css/v*`
  - `github-pages`, for the site: the `main` branch

  A job in a protected environment waits on its run's page until a reviewer
  approves it. Leave *Prevent self-review* off while one person maintains the
  repository, or nobody can approve.
- **Actions.** *Actions → General*: workflow permissions *Read repository
  contents and packages permissions*, and GitHub Actions may not create or
  approve pull requests.

In the account's own settings, *Pages → Add a domain* verifies `helmstudio.in`
with a TXT record, so no other account can serve a site at the domain if this
repository's Pages settings ever lose it.

## PyPI

Once, before the project exists: signed in to pypi.org, open *Your account →
Publishing* and add a pending publisher for GitHub:

- PyPI project name: `helm-runtime-sdk`
- owner: `janishar`
- repository: `helmstudio`
- workflow: `publish-python.yml`
- environment: `pypi`

A pending publisher does not reserve the name; the first publish does, and
turns the pending publisher into the project's trusted publisher, managed from
then on under the project's *Publishing* settings. The workflow uploads each
file with a provenance attestation that names the repository, the workflow and
the environment that published it.

## npm

**The organisation.** `@helmstudio` is an organisation on npmjs.com, free for
public packages. In its settings, require two-factor authentication of every
member.

**A new package.** npm adds a trusted publisher only to a package that exists,
so a package's first version is published by hand, and then handed to the
workflow:

1. Check out the release tag, with nothing uncommitted:

       git switch --detach <tag>
       git status --short

   The second command prints nothing.
2. Publish it:

       npm login
       npm publish ./<package directory> --access public --tag next

   npm asks for the second factor. A package's first version is tagged
   `latest` as well, whatever `--tag` says, because every package has one.
3. Trust the workflow, in its environment:

       npm trust github @helmstudio/<name> --file publish-npm.yml --repo janishar/helmstudio --env npm --allow-publish

   `--allow-publish` is required: the workflow publishes with `npm publish`,
   and npm has created trusted publishers that allow only `npm stage publish`
   since 3 September 2026 unless told otherwise. `--env npm` means a job
   outside the protected environment cannot publish, even from this workflow.
   `npm trust list @helmstudio/<name>` shows what a package trusts.
4. On the package's page, *Settings → Publishing access → Require two-factor
   authentication and disallow tokens*. Trusted publishing keeps working; a
   token, stolen or forgotten, cannot publish.
5. `npm logout`.

From the next version on, the workflow publishes and npm attaches provenance.
A version published by hand has none; `1.0.0-rc.1` of all three packages was
published by hand.

## The workflows

Every workflow that publishes follows the same rules, and a change to one is
reviewed against them:

- **Permissions job by job.** A workflow reads. `id-token: write` is given only
  to a job that publishes or signs, and `contents: write` only to the job that
  writes the GitHub Release.
- **No credentials left behind.** `actions/checkout` runs with
  `persist-credentials: false`, so the job's token is not in `.git/config` for
  the steps after it.
- **No cache in what is published.** A job that builds a package or a binary
  restores no cache, which another run could have written.
- **No expressions in scripts.** A value reaches a shell through `env`, never
  through `${{ }}` inside `run`, so no tag name or file can become a command.
- **Only GitHub's actions and PyPA's publish action.** Adding any other action
  to a workflow that publishes is a security review, and pins the action to a
  full commit SHA.
- **A check before every publish.** The tag must name the version the package
  declares, and a version already on its registry is skipped.

## Never

- Push `--tags`. It sends every local tag, and a stray one publishes.
- Push more than three tags in one `git push`. GitHub starts no workflow for
  any of them.
- Move, delete or push again a tag whose version has reached its registry, or
  a Go module tag the proxy has fetched. That version is fixed: release the
  next one.
- Publish by hand from anything but a clean checkout of the release tag.
- Approve a publish you did not start.

## Checking a release

- **PyPI.** Each file's provenance, shown on its page on pypi.org or at
  `https://pypi.org/integrity/helm-runtime-sdk/<version>/<file name>/provenance`,
  names the repository, the workflow and the environment that published it.
- **npm.** `npm audit signatures`, in a project that installed the packages,
  checks the registry's signatures, and the provenance of every version that
  has it.
- **helm.** `SHA256SUMS`, and `gh attestation verify`, as *Installing helm* in
  `docs/releasing.md` shows.
- **Go.** `go` checks every module it downloads against the checksum database.

## When something goes wrong

- **A tag started no workflow**, because it was pushed with more than three
  others. If its version is not on its registry, delete the tag on GitHub and
  push it again on its own:

      git push origin :refs/tags/<tag>
      git push origin <tag>

  Never do this to a Go module tag the proxy has already fetched.
- **A publish failed.** Fix what the failed step names, such as a trusted
  publisher or an environment, then *Re-run failed jobs* on the run's page. A
  tag that names the wrong version is not re-run: nothing was published, so
  delete it, set the version, and tag again.
- **A bad version is out.** It cannot be replaced, only superseded. Yank it on
  PyPI, under the project's *Manage → Releases*; run `npm deprecate
  @helmstudio/<name>@<version> "<why>"`; `retract` it in the next version's
  `go.mod`; say so in the GitHub Release's notes; then release a fixed version.
- **An account or a token may be compromised.** Revoke its sessions and tokens
  and reset its second factor; read its security history on each site; remove
  the trusted publishers and add them again; yank or deprecate anything that no
  maintainer published; and open a draft security advisory under the
  repository's *Security* tab.
