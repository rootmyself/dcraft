# Host-script generators

Published by ticket #13 (issue #14). This document is the shared contract for
generated `up`/`update` host scripts. The S3 generator (ticket #14 / issue #15)
consumes the same scaffolding instead of duplicating path safety or the write
step.

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
| `--allow-outside` | explicit confirmation for paths outside the project folder. It never permits the project root as `--dest`. |

Non-interactive runs never prompt. Missing or unsafe input is
`generator_input_invalid` (exit 2). The file write uses the independent
`file_overwrite` approval: automation must pass
`--approve file_overwrite`; the TUI wizard preview is the confirmation that
supplies it. `file_overwrite` is never reused as `script_trust`.

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
- `Options{AllowOutside: true}` only when the caller explicitly confirmed the
  outside path.

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

## 4. Contract for the S3 generator (ticket #15)

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

## 5. Verification

- Unit: path traversal/root/symlink cases; branch/tag/SHA rendering; annotated
  tag peel; `bash -n`; no destructive commands or credentials; approval and
  overwrite preservation; wizard preview without writes; JSON purity.
- Real Git (no network): local bare repository with branch/tag/full SHA;
  update; dirty destination refusal; foreign origin and ambiguity refusal.
- Real Docker integration (`DCRAFT_INTEGRATION=1`): generate → register with
  `dcraft hooks --add` → untrusted `up` blocked (exit 2) → trusted `up` runs
  the pre-hook, builds the Compose image from the synced source, and a dirty
  destination blocks the next `up` without changing the stack.
