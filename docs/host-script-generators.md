# Host-script generators

Published by ticket #13 (issue #14). This document is the shared contract for
generated `up`/`update` host scripts. The S3 generator (ticket #14 / issue #15,
`dcraft generate-s3-hook`) consumes the same scaffolding instead of duplicating
path safety or the write step; its own semantics are in §5.

## 1. Command and inputs

`dcraft generate-git-hook` is a flat feature with the `file_overwrite` save
approval. It has a TUI wizard and the same CLI inputs:

| input | meaning |
|---|---|
| `--repo` | Git URL or local path. A URL with embedded userinfo (a password, or any userinfo on http(s)) is refused; SSH usernames such as `git@host` are allowed. |
| `--ref` | branch, tag or full 40/64-hex commit SHA. `refs/heads/...` and `refs/tags/...` are accepted; a bare short name is resolved by the generated script at execution time. Short SHAs are refused at generation. |
| `--dest` | project-scoped destination folder for the checked-out source. |
| `--output` | project-scoped script path. Parents are created; the file is written `0755`. |
| `--stage` | `pre` (default) or `post`; both apply to `up` and `update`. |

Non-interactive runs never prompt. Missing or unsafe input is
`generator_input_invalid` (exit 2). The file write uses the independent
`file_overwrite` approval: automation must pass
`--approve file_overwrite`; the TUI wizard routes the save through the generic
plan + confirmation screen, which supplies it. `file_overwrite` is never reused as `script_trust`. The descriptor
approval is static, so a new file also requires it; this is stricter than a
confirmation-only new-file save and keeps one published safe path for creation
and replacement.

The shared preflight contract has no "none" requirement, so the generator
declares `docker_cli` (the smallest published set, like `hooks`). It never
invokes Docker, contacts a remote, or requires Git at generation time; the
generated script checks Git when it runs.

A wizard feature with an approval shows that approval on its preview screen and
then routes to the generic plan + confirmation screen; that confirmation
supplies the approval to the apply run. This is generic TUI routing, not a
generator-specific branch.

Generation:

- contacts no remote and runs no Git command;
- does not edit `.dcraft.yaml`, register a hook, execute a script or grant
  script trust;
- prints the exact next commands:
  `dcraft hooks --add --stage <pre|post> --script <path>` and
  `dcraft up --approve script_trust` (or `dcraft update`).

## 2. Path safety (shared)

`internal/generators.ResolveLayout` owns:

- absolute, cleaned `Destination`, `ScriptPath`, `DestinationRel`, `ScriptRel`
  and the script-to-project relative path;
- rejection of empty/control-character paths, `.git` components, project-root
  destinations, a script inside the destination, the script's own directory as
  the destination, existing script symlinks/directories and non-directory
  destinations;
- symlink-aware containment: existing ancestors are resolved before deciding
  whether a path is inside the project;
- rejection of every destination or script path outside the project folder;
  there is no override flag.

`internal/generators.WriteScript` publishes atomically: a new file uses a
no-replace hard link, an existing regular file uses a same-folder rename, and
symlinks/non-regular files are refused. New and replaced files are `0755`.

`generators.InputError` carries `Field`, `Message`, `Detail`; a feature maps it
to `generator_input_invalid`. A write failure maps to `generator_failed`
(exit 1). Both categories are additive v1 diagnostics.

## 3. Generated Git script semantics

The script starts with `#!/usr/bin/env bash` and `set -euo pipefail`, checks
`git` before doing anything, validates the ref, and refuses unsafe destinations.
It contains Korean comments for inputs, execution location, effects,
prerequisites, failure behavior and how to edit; commands stay English.

Safe default:

- Not a Git repository → `git clone --depth 1 --no-checkout` then fetch and
  checkout the requested ref.
- A repository whose `origin` equals the generated repository →
  `git fetch --depth 1 origin <full ref>` then `git checkout --detach <commit>`.
  The last applied commit is recorded in `.git/dcraft-git-hook.head`; if HEAD
  moved away from it (a local commit or checkout), the script refuses. The
  previous HEAD is kept under `refs/dcraft/previous`.
- Any tracked or untracked change in the destination → refuse before changing
  anything. No `reset --hard`, forced checkout, folder deletion, arbitrary
  path overwrite or tool installation.
- A different origin, a non-empty non-repository folder, or a folder inside
  another repository is refused; dcraft never repoints or adopts it.
- branch: `refs/heads/<name>`; tag: `refs/tags/<name>` then peel with
  `FETCH_HEAD^{commit}`; commit: full SHA fetch. A bare short name is resolved
  by `git ls-remote --heads/--tags`; a branch and tag with the same name is
  ambiguous and fails instead of guessing.
- Authentication uses the user's existing credential helper/SSH agent. The
  script embeds no credentials. A full-SHA fetch depends on the server's
  policy (`uploadpack.allowReachableSHA1InWant` etc.).

Execution location: hooks run with the project folder as cwd, but the script
also derives `PROJECT_DIR` from its own location (or embeds it when the script
was explicitly placed outside the project), so manual execution works too.

## 4. Contract consumed by the S3 generator

Reuse, do not fork:

- `internal/generators.ResolveLayout` for the S3 destination and script path;
- `internal/generators.WriteScript`, `ScriptMode` and `InputError`;
- the same feature shape: flat command, `TargetOptional`, `Wizard`, required
  CLI flags, `core.Planner` preview, `core.Wizard` preview, stages in Korean,
  `data.<feature>` JSON payload, `ApprovalFileOverwrite` for saving;
- the same separation: generation approval ≠ first-run `script_trust`;
  no auto-registration, no auto-run, no generation-time network/tool install.

The S3 generator owns its own validation (bucket/prefix, sync vs tar.gz),
preview and Korean script comments. It must not embed AWS credentials or
require an AWS account at generation time; it checks `aws` in the generated
script, uses existing credentials, and avoids `--delete`/destructive sync by
default. Tar extraction must validate a fresh destination and reject path
escape/dangerous links as verified in the generator notes.

## 5. Generated S3 script semantics

`dcraft generate-s3-hook` is the second flat generator feature. It reuses
`ResolveLayout`, `WriteScript`, `ScriptMode`, `InputError`, the
`file_overwrite` save approval and the generic planner/wizard/preview flow from
§2; it adds no deployment backend, credential storage, auto-registration or
automatic trust.

| input | meaning |
|---|---|
| `--source` | `s3://bucket/prefix` for `sync`, or one concrete `s3://bucket/path/artifact.tar.gz` for `tar`. A bucket-only URL is a valid whole-bucket sync. Userinfo, query strings/pre-signed URLs, wildcards, empty/`.`/`..` path components and a `.tar.gz` object in `sync` mode are refused. |
| `--mode` | `sync` or `tar`, required. It must match the object shape: `tar` needs one `.tar.gz` object, `sync` needs a prefix. |
| `--dest` | project-scoped destination folder; the project root, the script's own folder, the script path itself, any path containing the script and every path outside the project are refused. |
| `--output` | project-scoped script path, written atomically with mode `0755`. |
| `--aws-profile` | optional existing AWS CLI profile name (not a secret). It is embedded only when explicitly supplied. |

Generation contacts no S3 endpoint and runs no AWS CLI command. Non-interactive
runs never prompt; missing or unsafe input is `generator_input_invalid` (exit
2), a publish failure is `generator_failed` (exit 1), and a canceled run is
exit 130. Saving uses the independent `file_overwrite` approval (a new file
also needs it, as in §1); it is never `script_trust`. The success payload is
`data.generator` with `action=generate-s3-hook`, paths, source, mode, sha256,
`registered=false`, `trust_required=true` and the exact next commands:

```
dcraft hooks --add --stage pre --script <path>
dcraft up --approve script_trust
```

Generated script rules:

- `#!/usr/bin/env bash` and `set -euo pipefail`; Korean comments cover inputs,
  execution location, changed files, effects, prerequisites, failure behavior
  and how to edit. No `eval`, no interpolation of uncontrolled input, and no
  credentials or credential exports.
- Checks `aws` before any destination work; `tar` mode also checks `tar` and
  `gzip`. It never installs tools. Existing AWS CLI configuration (environment,
  `~/.aws`, SSO, role) is used; `aws configure` is not run and AWS CLI error
  text is disclosed as the user's own CLI output.
- Resolves the project folder from its own location (or embeds it when placed
  outside), and refuses a destination outside the project, the project root,
  the script's folder or a destination containing symlinks.
- `sync`: `aws s3 sync <src> <dest> --only-show-errors --no-progress`
  (optionally with `--profile`). `--delete` is never generated; files that
  exist only in the destination stay.
- `tar`: downloads to a private temporary file, verifies gzip integrity
  (`gzip -t`), lists names and verbose metadata, and refuses absolute paths,
  `..` components, hard links and unsupported member types before extraction.
  It extracts only into a freshly created empty temporary folder, rescans the
  extracted tree for symlinks that are absolute or resolve outside, and refuses
  a destination that contains any symlink before copying. It copies the staged
  tree into the destination without deleting files that exist only there, and
  removes the temporary folder on exit. `-P` and extraction directly into the
  destination are never generated.
- A pre-hook failure blocks `up`/`update`. Download/validation failures leave
  the destination untouched; a copy or sync failure reports that partial files
  may remain. dcraft does not roll back either case.

## 6. Verification

- Unit: path traversal/root/symlink cases; branch/tag/SHA rendering; annotated
  tag peel; `bash -n`; no destructive commands or credentials; approval and
  overwrite preservation; wizard preview without writes; JSON purity.
- Real Git (no network): local bare repository with branch/tag/full SHA;
  update; dirty destination refusal; foreign origin and ambiguity refusal.
- Real Docker integration (`DCRAFT_INTEGRATION=1`): generate → register with
  `dcraft hooks --add` → untrusted `up` blocked (exit 2) → trusted `up` runs
  the pre-hook, builds the Compose image from the synced source, and a dirty
  destination blocks the next `up` without changing the stack.

S3 generator (ticket #14 / issue #15):

- Unit: S3 URL shape and credentials/query refusal; `sync`/`tar` pairing;
  destination outside/root/script-path refusal; profile validation; `bash -n`;
  no `--delete`, credentials, tool installation or `eval`; approval and
  overwrite preservation; wizard preview writes nothing; JSON purity.
- Real process with a PATH `aws` shim (no real S3, no credentials): sync copies
  a local fixture tree, stale destination files remain and the shim log has no
  `--delete`; tar serves a valid `.tar.gz` (verify, extract, copy, internal
  links preserved), a truncated archive fails before any destination change,
  and hostile archives (absolute path, `..`, escaping symlinks, escaping hard
  links) are rejected without writing outside; a destination or destination
  file that is a symlink cannot be used to escape; temporary folders are
  removed.
- Real Docker integration (`DCRAFT_INTEGRATION=1`, PATH `aws` shim, no real
  S3): generate → register → untrusted `up` blocked (exit 2, no destination and
  no containers) → trusted `up` runs the pre-hook, builds the Compose image
  from the synced folder, keeps a stale local file and reports success.
- Linux GNU tar (`DCRAFT_INTEGRATION=1`, Debian bookworm, bash + GNU tar 1.34):
  the generated tar script is executed in a container with the `aws` shim; a
  valid archive with an internal symlink is copied, while escaping symlink and
  hardlink archives are rejected with the destination unchanged and temporary
  folders removed.
- Not verified in this ticket: real AWS S3/credentials or an AWS CLI other than
  the local shim; BusyBox tar (the script requires bash, which is not present
  in the Alpine/BusyBox images used for the earlier tool probes); WSL2.
