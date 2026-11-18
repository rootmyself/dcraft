# dcraft architecture and feature contract

Published by Ticket 01 (issue #2). Tickets 02–19 build on this document; do not
fork these types or recreate these rules in a feature or UI package. Shared
interface changes go to the integration owner as a change request (see the end
of this file).

Scope of this version: one working binary, fifteen registered features
(`status`, `install-compose`, `up`, `stop`, `down`, `restart`, `update`,
`secrets`, `hooks`, `logs`, `exec`, `backup`, `restore`, `rollback`,
`generate-git-hook`), the common operation seam, target resolution, named
environments, automatic project-name allocation, same-stack coordination,
runtime and build-time secret references, explicit host scripts with
independent script trust, the guided Git host-script generator,
service/instance selection, live terminal attach, versioned volume and declared
bind-folder backup, safe volume and bind-folder restore, AWS Secrets Manager
secret references, diagnostics, CLI/TUI assembly, state layout,
machine-readable results, the approval/plan preview flow, explicit update with
bounded readiness, the previous-good record, previous-good rollback, and tests.
The S3 generator is a later ticket.

---

## 1. Layers and dependency rule

```
cmd/dcraft                 process entry, signal context, TTY check, exit codes
  internal/app             wiring: deps, registry, engine
  internal/ui/cli          Cobra commands derived from the registry
  internal/ui/tui          Bubble Tea model derived from the registry
  internal/ui/view         shared Korean render helpers (no feature branches)
        |
        v
  internal/core            seam: Engine, Feature/Descriptor/Registry, Target,
                           Request/Invocation, Result/Stage/Outcome, Approval,
                           Diagnostic, Presenter/Verifier, StateStore, Clock
        |
        +-- internal/target          Compose target discovery + environment expansion
        +-- internal/environments    .dcraft.yaml aliases + static host-resource scan
        +-- internal/lifecycle       shared up/stop/down/restart controller
        +-- internal/update          explicit update: preview, build/pull/apply,
        |                            bounded readiness, previous-good record
        +-- internal/identity        read-only name/identity readout
        +-- internal/features/status reference read-only feature
        +-- internal/features/up     \
        +-- internal/features/stop    > thin lifecycle registration units
        +-- internal/features/down   /
        +-- internal/features/restart
        +-- internal/features/logs   live log stream (ticket #5)
        +-- internal/features/exec   attached command execution (ticket #5)
        +-- internal/features/all    feature registration files (one per feature)
        +-- internal/selector        service/instance selection (ticket #5)
        +-- internal/stack           Backend/read types + Lifecycle/Identity seams
        +-- internal/compose         Preflight + Inspect + Lifecycle adapter (Docker CLI)
        +-- internal/composeinstall  approved Compose plugin installation
        +-- internal/artifacts       HTTP download + atomic file-install boundary
        +-- internal/state           filesystem StateStore, name registry, stack lock
        +-- internal/execx           external command boundary
            internal/execx/execxtest fake Runner for tests
```

Dependency rules:

- UI packages never call Docker, Compose or the filesystem for stack state. They
  build a `core.Request`, call `core.Engine.Run`, and render the `core.Result`.
- Features never import Cobra or Bubble Tea. They own behavior, Korean text,
  stages and an optional human presenter.
- `internal/core` knows neither Docker nor Compose. It defines the boundaries.
- `internal/compose` knows `internal/stack` and `internal/execx`, not the UI.
- `internal/stack.Backend` is the read-only stack boundary the status feature
  consumes. `stack.Lifecycle` and `stack.Identity` are the narrow write and
  identity boundaries consumed by `internal/lifecycle`. A future backend can
  implement them without changing CLI/TUI code; do not build that backend now.
- `internal/lifecycle` and `internal/update` know `core`, `stack` and `state`;
  they never import Cobra, Bubble Tea or `internal/compose` (the Compose adapter
  is passed as the published interfaces). Update consumes `stack.Updater` and
  the same per-stack lock; it does not reuse the lifecycle controller's action
  entry point because update has its own stages.
- `internal/identity` implements the read-only `core.IdentityReader` from the
  same `stack.Identity` and `state.Store` boundaries. It never writes, locks or
  creates state.

---

## 2. Common operation seam

One code path is used by both interfaces:

```go
func (e *core.Engine) Run(ctx context.Context, req core.Request, sink core.Sink) core.Result
```

Construction (production, in `internal/app`):

```go
core.NewEngine(registry, target.New(), composeAdapter, stateStore, clock)
```

`core.Request`:

| field | meaning |
|---|---|
| `Feature string` | registered machine name / CLI command |
| `Target core.TargetRequest` | path, explicit files, env files, profiles, project name, environment alias |
| `Services []string` | explicit Compose service selection (empty = stack) |
| `Args []string` | positional arguments for features with `AcceptsArgs` |
| `Options map[string]string` | parsed feature-flag values |
| `Approvals []core.ApprovalKind` | explicit approvals supplied by the caller |
| `Interactive bool` | prompts are possible; never bypasses approval |
| `IO core.IO` | caller-provided live terminal streams for logs/exec; nil fields mean the process standard streams |
| `JSON bool` | the caller asked for one machine-readable result; a UI hint only, never a safety rule |

Engine sequence (stage names are stable machine values; titles are Korean):

1. `resolve_target` — `core.Resolver.Resolve`; target warnings become
   diagnostics. Fatal failure stops here. A feature marked
   `Descriptor.TargetOptional` resolves a valid folder without Compose files
   (target resolution still validates the folder and explicit `-f/--env-file`
   inputs).
2. Approval check — no stage; if `Descriptor.Approval` is not supplied in
   `Request.Approvals`, the result is a failure with category
   `approval_required`, `required_approval`, and exit code 2. **The engine
   decides this; a UI may only collect and pass the approval.** The install
   approval message states the effect at policy level (one official Compose
   plugin file in the user's Docker CLI plugin path; no Docker CLI/Engine/
   Desktop change, no administrator rights).
3. `preflight` — `core.Preflight.Check(ctx, requires)`; error diagnostics stop
   the feature. Warnings/info are kept. The requirements come from
   `Descriptor.Requires`; an empty declaration means the full default
   (`docker_cli`, `docker_engine`, `compose`) so every pre-existing feature
   keeps today's behavior. A feature that needs only a subset (Compose install
   needs `docker_cli`) declares it; a feature cannot bypass preflight by
   declaring an unknown value (registry validation rejects it).
4. Host hooks (only `Descriptor.Hooks == HookUpUpdate`, so only `up`/`update`):
   - The engine asks `core.HookService.Plan` for the registered scripts and
     their current sha256. A malformed registration, missing/unreadable file or
     non-executable file fails `hooks_pre` before the operation starts.
   - New or changed scripts need the independent `script_trust` approval. When
     the caller supplies `Request.ApprovedHooks` (the plan it showed the user),
     the current identities must match exactly; a change since that screen is
     `hook_content_changed` (exit 2) and nothing runs. Without a plan, explicit
     `--approve script_trust` covers the current identities; non-interactive
     without it fails `approval_required` (exit 2).
   - `hooks_pre` runs pre scripts sequentially with the project folder as cwd,
     inherited environment and documented `DCRAFT_*` variables. Each script is
     re-hashed immediately before execution; a mismatch is
     `hook_content_changed`. A failure blocks the feature; the main outcome did
     not start.
   - `revalidate_inputs` re-resolves the target after the pre scripts (a pre
     script may have changed Compose files, env files or `.dcraft.yaml`) and
     re-checks that the post-hook plan still matches the approved identities.
     A changed post script fails before the main operation starts.
5. Read-only identity read — for `ReadOnly` features with no explicit `-p` and
   a configured `Engine.Identity`, the engine resolves the effective project
   name before `Invocation` (see §3.2). It adds no stage, never writes or locks,
   and on failure emits an `identity_unreadable` **warning** and falls back to
   Compose's default. Stack-changing features resolve and record their own
   allocation (they may write).
6. Feature `Run` — feature stages appear between preflight/hooks and
   verification. If a feature reports no stage, the engine adds a fallback
   `execute` stage.
7. `verify` — only if the feature also implements `core.Verifier`. Raw errors
   become `verification_failed`; a panic is recovered and becomes
   `internal_error`; no history is written.
8. `hooks_post` — only for `HookUpUpdate`, after the feature's work and engine
   `verify` succeeded. Post scripts run in registration order. A failure is a
   `hook_post_failed` **warning** (or `hook_content_changed` for a changed
   derivative) and keeps the completed operation successful; remaining post
   scripts do not run. A canceled post hook is reported as canceled while
   stating the deployment already completed.
9. `history` — only if `Descriptor.RecordsHistory`. Written after success. A
   history write failure is a `history_failed` **warning**; the operation stays
   successful because the stack change already happened. Never report a
   completed change as failed solely because history failed, and never record a
   failed verification as a good version. The record snapshots completed stages
   only: the still-open `history` stage is not written into itself, so a
   persisted record never ends with a `started` stage.

Failure and interruption:

- Features return `*core.Error` (built with `core.Fail` / `core.FailCause`).
  Diagnostics carry a stable category, Korean message and Korean next actions.
- A raw Go error becomes `internal_error`.
- A panic inside a feature or inside `core.Verifier.Verify` is recovered and
  becomes `internal_error`; the process must not crash the TUI.
- `context.Canceled` / deadline / canceled context yields outcome `canceled`, a
  `canceled` diagnostic that states partial effects may remain, and exit 130.
- There is no hidden background handoff, no automatic rollback, no atomicity
  claim.

Preflight capability values (`core.Requirement`):

| value | meaning | default |
|---|---|---|
| `docker_cli` | `docker` is present and runnable | yes |
| `docker_engine` | a reachable local engine is required | yes |
| `compose` | an installed, supported Compose is required | yes |

`docker_cli` alone skips endpoint/engine/Compose checks; `compose` additionally
checks Compose presence/version/capability; `docker_engine` additionally checks
engine reachability and permission. `compose` and `docker_engine` implicitly
include `docker_cli`; normalization adds it, so a narrower declaration can never
disable the Docker lookup or the remote-endpoint refusal. A remote endpoint is
still refused whenever `docker_engine` or `compose` is required. A feature that
genuinely needs no Docker capability is a future contract extension (an explicit
"none" value); an empty slice never means "check nothing". Descriptor
requirements are validated at registration (unknown values are rejected).

Approval kinds (`core.ApprovalKind`), independent and non-substitutable:

| value | risk |
|---|---|
| `install` | installing a Compose distribution |
| `script_trust` | first execution or changed host script |
| `previous_version` | return to the previous good deployment |
| `data_restore` | replace data from a backup |
| `file_overwrite` | save a generated project file (new file or replacement); it is never script trust |

CLI supplies them with repeated `--approve <kind>`; the TUI collects them in a
generic plan → confirm screen (see §6). A wizard feature with a descriptor
approval shows that approval on its preview screen; the same generic screen is
the confirmation that supplies it to the apply run. Read-only features may not
require an approval (registry validation).

Optional feature capabilities:

```go
type core.Verifier interface {
    Verify(ctx context.Context, in core.Invocation, payload any) error
}
type core.Presenter interface {
    Present(res core.Result, width int) string
}
type core.Planner interface {
    Plan(ctx context.Context, in core.Invocation) (core.Plan, error)
}
// LiveFeature: stdout is user process data, not the result document.
type core.LiveFeature interface {
    LiveMode() core.LiveMode // "stream" (logs) or "attach" (exec)
}
// InputProvider: the TUI collects missing values; the feature owns meaning.
type core.InputProvider interface {
    Inputs(ctx context.Context, in core.Invocation) ([]core.InputField, error)
    WithInputs(req core.Request, values map[string]string) (core.Request, error)
}
// CanceledReporter: a feature-specific cancellation effect, normalized by the
// engine to the canceled/warning/exit-130 contract.
type core.CanceledReporter interface {
    CanceledDiagnostic() core.Diagnostic
}
```

`InputField` is UI metadata (`choice`/`text`, label, help, options, required);
it is not a safety policy. `WithInputs` maps collected values to request
fields, and the feature validates them again when it runs.

`Engine.Inputs(ctx, req, sink) core.InputsReport` resolves the same target and
preflight requirements as `Run`, then calls the feature's `InputProvider`
implementation. It never checks an approval, never runs the feature and never
writes history, so the TUI can show the service, instance and command before
attaching. The CLI never calls it and never prompts.

`Engine.Plan(ctx, req, sink) core.PlanReport` resolves the same target and
preflight requirements as `Run`, then calls the feature's `Plan`. It never
checks an approval, never calls `Run`, and never writes history. `Plan` must not
change anything. Any Planner is previewed by both interfaces; for an
approval-gated feature the plan is the approval confirmation, and for an
ordinary `update` (no approval) it is the target/planned-work preview before the
single deliberate run. `PlanReport` carries feature, outcome, target, tools, stages,
diagnostics and the Korean plan text. Both paths share the same target and
preflight code, so a preview cannot silently use a different selection. Plan
text must state the concrete source, version, destination, permission need and
effect, and never contain secret values. `PlanReport.Result()` converts a failed
plan into the common result shape for both interfaces to render.

`core.Invocation` gives a feature: resolved `Target`, `Services`, `Args`,
`Options`, `Interactive`, `Preflight` report, `IO`, `JSON`, `Reporter`,
`Clock`, `State`.
Use `in.Option(name)` and `in.BoolOption(name)`.

---

## 3. Target resolution

`core.TargetRequest` is caller input; `core.Target` is the resolved selection
shared by every operation:

```go
type Target struct {
    ProjectDir     string   // absolute target folder
    Files          []string // absolute Compose files, explicit order
    EnvFiles       []string // absolute explicit --env-file inputs
    Profiles       []string // selected Compose profiles
    ProjectName    string   // explicit -p only; "" = Compose default / ticket #3 allocation
    Environment    string   // selected alias; "" only when no aliases are registered
    DefaultEnvFile string   // informational .env loaded by Compose, if used
}
```

Rules (`internal/target.Resolver`):

- Automatic discovery is limited to `TargetRequest.Path` (default: current
  folder). It never checks a parent folder.
- Standard base files, in Compose's preference order: `compose.yaml`,
  `compose.yml`, `docker-compose.yml`, `docker-compose.yaml`. The first match is
  used; if several exist, Compose itself warns and uses the first, so dcraft
  emits a `multiple_configuration_files` **warning** instead of failing.
- The matching override is added automatically: `<base without ext>.override.<ext>`.
- Explicit `-f/--file` inputs replace default discovery, preserve order, and do
  not auto-add an override. Relative explicit paths are resolved against the
  process working directory, like `docker compose -f`.
- Explicit `--env-file` inputs preserve order and replace the default `.env`.
  Without explicit env files, if `.env` exists next to the first Compose file it
  is reported as `DefaultEnvFile` (informational only; it is not passed through).
- Profiles are passed as repeated global `--profile` flags.
- `ProjectName` is only the explicit `-p` value. Empty means the lifecycle
  controller allocates or reuses an automatic name (see §5) and pins it with
  `-p` on every Compose call. Ticket #4 fills `Environment`, which is part of the
  name and state key. Read-only features resolve the same identity through
  `core.IdentityReader` (see §3.2) without writing or locking state.
- `Target.ComposeArgs()` is the single source of Compose global inputs:
  `compose -f <file>... --env-file <file>... --profile <profile>... [-p <name>]`.
  It deliberately does **not** pass `--project-directory`: Compose keeps its own
  rule that the project directory is the first file's directory, so relative
  paths and the default `.env` behave exactly as a direct Compose invocation.
- Existing Compose files and Dockerfiles are never modified. Status and target
  resolution are read-only.
- `TargetRequest.AllowMissingConfiguration` is set by the engine from
  `Descriptor.TargetOptional`; callers never set it. It only turns "no Compose
  file in the folder" into a valid empty selection. The folder must still
  exist, and explicit `-f`/`--env-file` inputs are still validated as files.
  Because a target-optional feature does not operate on a Compose project, it
  also skips environment-alias resolution: registered aliases never make an
  alias mandatory, a broken `.dcraft.yaml` never blocks the feature, and plain
  folder/Compose defaults apply. An explicit `--env` has no effect there.

State identity is `ProjectDir + Environment`, not the Compose project name (the
name itself is stored state). This keeps the same folder/environment tied to
one helper-managed stack. The folder key is canonicalized with symlink
resolution when it exists, so the same target invoked as the working directory
or through a symlinked spelling (`/var` vs `/private/var` on macOS) shares one
stack identity. The environment id is the alias from `.dcraft.yaml` exactly as
written.

### 3.1 Named environments (ticket #4)

A named environment is a **selection input**, not a Compose replacement: it
bundles a Compose file list, explicit env files and profiles under an alias. It
adds no merge rules, no deployment model and no project-name source.

Config file: `<project folder>/.dcraft.yaml`. It is optional, lives next to the
Compose files and is never passed to Compose. dcraft never modifies user
Compose files or Dockerfiles. `.dcraft.yaml` is dcraft's own project config and
may be written only after an explicit user action (for example
`dcraft secrets --add` or the TUI save confirmation). It stores references
only, never resolved values, and is designed to be shareable with the team:
reading it reveals which services use which sources, but not the secrets
themselves. That is a metadata/privacy consequence, not a secret store.
Unknown top-level keys are preserved and comments are preserved where the YAML
library can attach them; the file is replaced atomically so an interrupted
write leaves the previous file intact. Fields inside an environment are
strict.

```yaml
environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
    env_files: [.env.dev]
    profiles: [dev, tools]
  prod:
    compose_files: [compose.yaml, compose.prod.yaml]
    env_files: [.env.prod]
    profiles: [prod]
```

Rules:

- Aliases are lowercase letters/digits/`-`/`_`, at most 32 characters, unique.
  Duplicate keys/fields/list entries, unknown fields, non-list values, empty
  values and aliases with no inputs at all are `environment_invalid` errors.
- Relative paths in the file are resolved against the project folder (not the
  process working directory). Referenced files are checked for existence only
  when that alias is selected, so one unfinished alias does not block others.
- `compose_files` may be empty: standard Compose discovery applies inside the
  project folder. File order, explicit env-file order and profiles are passed to
  Compose unchanged. When no explicit env files are configured, Compose still
  loads `.env` next to the first file and `DefaultEnvFile` reports it.
- If at least one alias is registered, **every operation requires an explicit
  selection**. CLI: `--env <alias>`; missing/unknown/conflicting input exits 2
  with Korean guidance listing the aliases and no prompt. TUI: a selection
  screen before the primary feature; nothing is preselected, first/prod/last is
  never chosen implicitly, and `q` quits without running anything.
- With no aliases, `Target.Environment` stays empty and standard Compose
  defaults apply. `--env` with no registered aliases is an error, never a
  fallback.
- `--env` cannot be combined with `-f`, `--env-file` or `--profile`: the alias
  is the single source of those inputs, so there is no second merge language.
- The resolved `core.Target` carries `Environment=alias` plus the expanded
  `Files`, `EnvFiles` and `Profiles`. `Target.ComposeArgs()` is unchanged.
  Later features must consume this target and must not re-read `.dcraft.yaml`,
  re-branch on the environment, or re-derive Compose inputs.
- Helper state and history are keyed by `ProjectDir + Environment`, so dev and
  prod aliases never share dcraft-managed state even when their Compose project
  names or host resources coincide. Explicit Compose project names (`-p` or the
  top-level `name:`) are always respected; dcraft never renames them.
- Visible limitations are surfaced, never hidden: every selected alias emits an
  `environment_isolation_limits` info diagnostic. Static declarations across
  aliases of the same folder are compared for duplicate published host ports,
  identical absolute/`~` bind sources, and equally named external volumes or
  networks (`environment_overlap` warning). If the effective explicit project
  name (`-p`, else the last declared top-level `name:` in the file list) is
  shared by several aliases, `project_name_shared` warns before any apply; the
  lifecycle controller still stops such an apply with `project_name_conflict`
  because two environments must not share one Compose project (§5).
- The scan is deliberately incomplete and does not claim isolation: it does not
  interpolate variables, evaluate merge keys/`extends`/`!reset`, expand
  relative binds, resolve symlinks, compare IPv6 bracket port syntax, or follow
  what external scripts/users do. The Compose CLI remains the authority for
  merge, validation, paths and profiles.
- Alias creation in this version is direct editing of `.dcraft.yaml`. There is
  no TUI alias editor and no generated config file; adding one is a later
  ticket, not a hidden write from this one.

### 3.2 Read-only project identity (ticket #3 follow-up)

The same effective project name is used by query and change:

- Stack-changing features (`up`, `stop`, `down`, `restart`) resolve the name
  through `internal/lifecycle` (explicit `-p`, then a declared Compose name,
  then a recorded allocation, then the smallest free suffix) and record it.
- Read-only features (`status`, later `logs`/`exec`) get the name from
  `core.Engine.Identity` (`core.IdentityReader`, implemented by
  `internal/identity`) **after preflight and before the feature runs**. The
  priority is identical: explicit `-p`, declared Compose name, recorded
  allocation. With no claim and no declaration the name is left empty and
  Compose resolves its folder default exactly as before.
- The read is strictly read-only: `state.LookupName` reads
  `<state>/names.json` only. It never creates the state root or stack
  directories, never takes `names.lock` or the per-stack `.lock`, and never
  writes. Any failure becomes an `identity_unreadable` warning and the feature
  continues with the Compose default; it is never recorded as a successful
  identity.
- The engine sets `Target.ProjectName` and `Result.Target.ProjectName` to the
  resolved name before the feature runs, so `status --json`, the TUI status and
  future read-only features identify the same stack as `up` without a
  feature-specific branch. No result-schema field was added.
- Rule: read-only features may read identity state but must never write or lock
  it. Every feature that addresses a stack — `status`, `up`, `stop`, `down`,
  `restart` and later `logs`/`exec`/`backup` — uses this one identity and must
  not derive a separate project name.

---

## 4. Feature registration unit

A feature is one flat command:

```go
type core.Feature interface {
    Descriptor() core.Descriptor
    Run(ctx context.Context, in core.Invocation) (any, error)
}
```

`core.Descriptor` fields:

| field | purpose |
|---|---|
| `Name` | English command/machine name, `^[a-z][a-z0-9-]*$`, globally unique |
| `Title`, `Summary`, `Usage` | Korean user-facing text |
| `ReadOnly` | no stack change; cannot require approval |
| `Approval` | required `core.ApprovalKind` |
| `RecordsHistory` | stack-changing feature whose success is written to history |
| `Primary` | TUI opening screen; exactly one registered feature |
| `SupportsServices` | adds common `--service/-s` flag |
| `Requires` | preflight capabilities this feature needs; empty = full default |
| `TargetOptional` | feature does not operate on a Compose project; a folder without Compose files is valid |
| `AcceptsArgs` | allows positional args; flags then stop at the first arg |
| `Wizard` | TUI entry uses the generic guided flow (`core.Wizard`) instead of the generic confirmation screen; the CLI path is unchanged |
| `Flags []core.Flag` | feature-owned flags parsed by the shared CLI |
| `Hooks` | registered host-script scope; `HookUpUpdate` only on `up`/`update`, rejected on read-only features |

Registry validation rejects duplicate names, invalid names, missing Korean text,
read-only+approval combinations, read-only+history combinations,
read-only+hooks combinations, a mutating primary feature without an independent
approval (the TUI would otherwise run it as soon as it opens), a second
primary, unknown preflight requirements, reserved flag names (`path`, `file`,
`env-file`, `profile`, `project-name`, `env`, `json`, `approve`, `help`,
`service`), reserved shorthands (`f`, `p`, `s`, `h`, `v`), duplicate flags and
invalid bool defaults.

### Adding a feature (the only steps)

1. Create `internal/features/<name>/` with the `Feature` implementation, its
   Korean messages in a local `messages.go`, and its tests. Consume only
   `core`, `stack`, and the services in `deps.Deps`. Report stages with
   `core.RunStage` / `core.RunStageValue`. Return `core.Fail(...)` diagnostics,
   never ad-hoc user-facing errors.
2. Create exactly one **new** file `internal/features/all/<name>.go`:

```go
package all

func init() {
    register(func(reg *core.Registry, d *deps.Deps) {
        reg.MustRegister(foo.New(d.Compose))
    })
}
```

3. Run `go test ./...`. No existing file is edited. A lifecycle-like feature
   calls `deps.Deps.Lifecycle` with its `lifecycle.Action`; it does not add a
   second write path. A read-only feature gets the effective project identity
   from the engine automatically; it never reads or writes the name map itself.

The CLI command, Korean help entry, JSON wrapper, exit codes, TUI menu entry and
stage display are derived automatically. Registration files execute in filename
order; that is the CLI/TUI order.

Rendering: a feature may implement `core.Presenter` for human text; JSON always
uses the generic `core.Result` wrapper. JSON payload types live in the feature
package under `Result.Data`; the status feature publishes
`status.Payload{Stack stack.Snapshot}` → `data.stack`.

Flags/args/services: declare them; the shared assembly parses them into
`Options`, `Args`, `Services`. `core.Flag{Name, Shorthand, Usage, Kind,
Default, Required}` supports `FlagString`, `FlagBool` and `FlagList`. A
`FlagList` is repeatable and is carried separately in
`Request.OptionLists`/`Invocation.OptionLists` (`Invocation.OptionList(name)`),
so values are never re-split by a UI. `Required` marks input a non-interactive
run cannot invent: the CLI passes what was given and the feature returns its
own Korean usage diagnostic (exit 2). For `AcceptsArgs` features, options come
before positional args (the rest is passed through unchanged, e.g. `exec`).
Interactive collection uses the published `core.Wizard` flow (§14): the feature
supplies steps and an effect preview, the TUI only collects answers and runs the
same engine path with `wizard=apply`. A feature must not add a TUI branch or a
second engine path for input.

Contract changes (new shared service, new flag kind, new approval kind, new
result field) must be requested from the integration owner with the reason and
affected tickets. Do not copy the interface into a feature.

---

## 5. Stack lifecycle: up, stop, down, restart (ticket 03)

Four flat, stack-changing commands run through `core.Engine.Run`; CLI and TUI
share `lifecycle.Controller`. Nothing here builds, pulls, checks health, backs
up data or rolls back; `up` participates in the engine-owned hook seam (§7.2),
but the lifecycle controller itself runs no scripts.

| command | scope | Compose call | effect |
|---|---|---|---|
| `up` | whole stack; `-s/--service` optional | `... up --detach [services...]` | starts/resumes; applies the current Compose configuration; short interruption possible |
| `stop` | whole stack; `-s/--service` optional | `... stop [services...]` | stops containers; containers and data remain; `up` resumes |
| `down` | project-wide only | `... down` | removes containers and the default network; never `-v` or prune; named volumes survive |
| `restart` | whole stack; `-s/--service` optional | `... restart [--no-deps] [services...]` | restarts containers; never re-reads configuration or images; `up` applies configuration |

Compose global inputs always precede the subcommand (`-f`, `--env-file`,
`--profile`, `-p`), so profiles work on every lifecycle command. Explicit
service names are Compose services, never container instances. No atomicity,
zero-downtime or automatic-rollback guarantee is implied.

Published services:

- `stack.Lifecycle` (`Up`/`Stop`/`Down`/`Restart`) implemented by
  `internal/compose`; `deps.Deps.Lifecycle` exposes the shared
  `*lifecycle.Controller`. Update/pull/build/health remain ticket #8.
- `stack.Identity` (`ConfigInfo`/`DerivedProjectName`/`Projects`) is the narrow
  read boundary used only for name allocation; it returns names and service
  keys, never environment values.
- `lifecycle.Controller.Run(ctx, in, action)` returns `lifecycle.Payload`, which
  JSON consumers read as `data.lifecycle`.
- `state.Store.AllocateName` and `state.AcquireStackLock` are reusable by later
  tickets (see §8).

Stages (stable snake_case; Korean titles): `resolve_project`, `allocate_name`,
`coordinate`, `<action>` (`up`/`stop`/`down`/`restart`), `observe`. On failure
the result lists the completed stages, the failed stage, the observable current
state in the diagnostic message and Korean recovery actions. Cancellation stays
`canceled` (exit 130) and states that partial effects may remain.

Payload (`data.lifecycle`): `action`, `project_name`,
`project_name_source` (`explicit|configured|allocated|existing`), `scope`
(`stack|services`), `services`, `volumes_removed` (always false),
`config_applied` (true only for `up`), `state` (observed snapshot, omitted when
observation failed) and `notices` (Korean notices such as a rename).
`Result.Target.ProjectName` remains the explicit `-p` input; the effective name
is in the payload and in every Compose call.

### Automatic names and local ownership

- The effective name comes from Compose's resolved config name. If it differs
  from Compose's own folder-derived default (a project `name:` or
  `COMPOSE_PROJECT_NAME`), it is honored as declared. A declared name owned by
  another folder or an unmanaged existing project stops with
  `project_name_conflict` before any change; dcraft never renames or adopts the
  other stack.
- Otherwise the base is Compose's derived name for the project folder (the first
  file's directory) plus the environment id (currently empty). The smallest free
  suffix is used: `hello-world`, `hello-world-1`, … No path hash is displayed.
- Claims live in `<state>/names.json`, keyed by absolute project folder +
  environment. A recorded name is reused on rerun, after stop and after down,
  even when containers are gone. An existing Compose project whose config file
  belongs to the same folder is reused (`existing`) instead of duplicated, so a
  stack created by plain Compose or after state loss is identified.
- Changing a recorded automatic name to a declared name updates the claim and
  emits a Korean notice; the old project is left untouched and is no longer
  managed. Dropping `-p` later reuses the recorded name instead of silently
  creating a second stack; declaring a different name is the explicit way to
  change it. A declared name that equals Compose's derived default is
  indistinguishable from an automatic name (Compose exposes no provenance flag)
  and is treated as automatic.
- Declared names are detected against Compose's true folder default. The probe
  uses an empty env file and drops an exported `COMPOSE_PROJECT_NAME`, so a
  name in `.env`, an exported `COMPOSE_PROJECT_NAME` and a project `name:` are
  all recognized as declarations instead of defaults.
- `status` and later read-only features use the read-only identity read from
  §3.2. They identify the recorded name (for example `hello-world-1`), never
  write or lock state, and fall back to the Compose default when no claim or
  declaration exists. Explicit `-p` and declared Compose names still win over
  the recorded allocation for every interface.

### Same-stack coordination

- One per-stack exclusive lock (`<state>/stacks/<key>/.lock`, `flock`, holder
  JSON with action/PID/start time) is held across the Compose call and the
  observation. A conflicting operation fails immediately with `stack_busy`
  (exit 1) and a Korean message naming the holder; it does not wait forever.
  Different folders and environments use different locks.
- `state.AcquireStackLock(ctx, target, state.LockHolder)` and
  `(*state.StackLock).Release()` are published for update (#8), backup/restore
  (#16–#19) and rollback (#12). `state.Locker` is the narrow published
  interface (`AcquireStackLock`); `deps.Deps.Locks` exposes it so a feature
  never type-asserts the concrete `*state.Store`. `state.AllocateName`
  serializes claims with a short bounded file lock and an atomic file replace.
- No host-wide prune; another project's containers, networks, volumes and
  images are never removed.

### TUI

- The menu lists every registered feature in Korean. Selecting a mutating
  feature first shows a confirmation screen with the requested target
  (path/files/profiles/project name), the whole-stack scope and the effect.
  `enter` runs the same engine path, `esc` cancels, `q` quits without running.
  Lifecycle screens still operate on the whole stack; service selection for
  those mutations stays the CLI `-s/--service`. The ticket-05 logs/exec screens
  select a service/instance through the published selector and a generic
  `core.InputProvider` form, without a feature-specific branch in the TUI.
- During an in-flight mutating operation, `q` explains that the operation is
  running and that quitting can leave partial effects (it does not orphan the
  Compose process). `ctrl+c` requests safe cancellation: the per-run context is
  canceled, the external Compose process is stopped, and the screen stays open
  to show the canceled result with partial-effect guidance; a later `q` quits.
  Read-only loading keeps the ticket-01 quit behavior. Normal quit after
  completion touches no service.

---

## 6. CLI and TUI assembly

### CLI (`internal/ui/cli`)

`cli.New(cli.Options)` builds a root command plus one command per registered
feature. It contains no feature-specific branch.

Persistent flags on every command:

| flag | meaning |
|---|---|
| `--json` | emit one machine-readable JSON result on stdout |
| `--path` | target folder (default: current folder) |
| `-f, --file` | explicit Compose files in order (repeatable) |
| `--env-file` | explicit env files (repeatable) |
| `--profile` | enabled Compose profiles (repeatable) |
| `-p, --project-name` | explicit Compose project name |
| `--env` | named environment alias; required when `.dcraft.yaml` registers any |
| `--approve` | explicit approval kind (repeatable) |

Feature-owned flags and `--service/-s` are added from the descriptor. Help and
usage templates are Korean; command/flag names and machine values stay English.

Without a command:

- interactive terminal → `Home` opens the TUI;
- non-interactive (stdin/stdout not both TTYs) → Korean explanation, list of
  registered commands, exit 2, no TUI, no prompt;
- `--json` without a command → usage error, exit 2.

Human output: successful feature `Presenter` text goes to stdout; warnings and
failure diagnostics go to stderr; JSON mode writes only JSON to stdout and
nothing to stderr for operation outcomes. Usage errors remain human text.

Live features follow the same rule. The CLI passes the command streams as
`core.IO`; a `LiveStream` feature (logs) is refused in JSON mode with a Korean
usage error and exit 2, because its stdout is user data that cannot become one
result object. A `LiveAttach` feature (exec) may run in JSON mode by capturing
the command and returning it under `data.exec`; it never attaches a TTY then.
`logs --json` therefore fails before any Docker call.

The CLI never prompts. An approval-gated command without `--approve <kind>`
returns the engine's approval failure (exit 2) with Korean text describing the
effect; non-interactive and interactive terminals behave identically. For any
feature that implements `core.Planner`, human mode calls `Engine.Plan` and
prints the plan to stderr before `Engine.Run`: `install-compose` shows the
source, version, destination, permission and integrity, and ordinary `update`
shows the target and planned work (then runs without another approval). A failed
plan is rendered as the common failure and nothing is run. JSON mode does not
make a separate plan call; the result carries the same public metadata.

### TUI (`internal/ui/tui`)

`tui.NewModel(ctx, Options)` is pure construction; `tui.Run` starts Bubble Tea.
It derives everything from the registry:

- the primary feature runs immediately;
- the body is the feature presenter on success or `view.Failure` on
  failure/canceled, including warnings and stage list;
- the stage sink shows the current stage while loading;
- if more than one feature is registered, a Korean menu lists all features;
  `↑/↓` select, `enter` runs the selected feature through the same engine;
- if the folder registers environment aliases and no `--env` was given, the TUI
  first lists them through `core.Engine.Environments` and requires an explicit
  `↑/↓` + `enter` selection. Nothing is preselected; `q` quits without running
  a feature. An explicit `--env` skips the screen. A broken alias file is not
  hidden: running the feature surfaces the same `resolve_target` failure.
- selecting a mutating feature that has no independent approval shows the small
  confirmation screen (requested target and environment, whole-stack scope,
  effect); `esc` returns to the menu;
- a feature that implements `core.Planner` starts the read-only plan screen
  instead of the generic confirmation. For an approval-gated feature the plan is
  the approval confirmation (flow below); for an ordinary `update` the same plan
  is the target/planned-work preview and one `enter` runs it without an extra
  approval kind;
- a feature with `Descriptor.Wizard` starts the generic guided flow (`steps`
  run, keyboard answers, effect preview, `apply` run). `backup` uses it to select
  a mixed volume/bind selection and an output directory and to show
  affected/running services before anything is stopped. The source step is the
  published generic `Multi` step; the TUI adds no safety rule; the feature
  rechecks selection, paths and consumers on the apply run;
- `r` re-runs read-only features directly, re-plans every Planner and asks for
  confirmation again for other mutating features. The selected environment is
  reused; `q` and `ctrl+c` quit outside an operation;
- a live feature opens a live screen instead of the loading text. Logs stop
  following with `q`/`esc`/`ctrl+c`; an attached command forwards keys and
  detaches with `ctrl+]`. Both return to the management view, keep the last
  output lines visible, and never stop a service;
- a feature implementing `core.InputProvider` first opens the generic form
  (`phaseForm`) fed by `Engine.Inputs`; `esc` runs nothing and the exact
  service/instance/command values are shown before the feature runs;
- normal quit is not an operation and does not touch services. During an
  in-flight mutating operation `q` explains the risk; `ctrl+c` requests safe
  cancellation and keeps the screen for the canceled result (see §5).

Approval flow (generic, engine-enforced):

1. Selecting a feature that implements `core.Planner` starts `Engine.Plan`, not
   `Engine.Run`. If the descriptor also requires an approval, the plan screen
   shows that approval kind.
2. The read-only plan is rendered with the approval kind; `enter` approves and
   passes only that kind in the request; `esc`/`n` declines and runs nothing,
   showing a canceled result that states no change was made.
3. After approval the same engine runs the feature. The TUI adds no safety rule;
   it only collects and passes the approval. A feature that requires an approval
   but has no planner is not silently approved; the engine returns the normal
   approval failure.
4. The confirm screen is not feature-specific, so `script_trust`,
   `previous_version` and `data_restore` will reuse it without a TUI branch.

No feature action is selectable unless it is registered. The current build
registers `status` (primary), `install-compose`, `up`, `stop`, `down`,
`restart`, `update`, `secrets`, `hooks`, `logs`, `exec`, `backup`, `restore`,
`rollback` and `generate-git-hook`.

---

## 7. Result, progress and cancellation contract

`core.Result` is the only operation outcome. JSON schema version is `1`
(`core.SchemaVersion`); changes within v1 are additive, breaking changes bump it.

```json
{
  "schema_version": 1,
  "operation_id": "op_...",
  "action": "status",
  "outcome": "success",
  "target": {
    "project_dir": "/abs/project",
    "files": ["/abs/project/compose.yaml"],
    "env_files": [],
    "profiles": [],
    "project_name": "hello-world",
    "environment": "",
    "default_env_file": "/abs/project/.env"
  },
  "tools": {
    "docker_version": "29.10.0",
    "compose_version": "5.7.0",
    "engine_version": "29.9.3",
    "endpoint": "unix:///...",
    "endpoint_kind": "unix"
  },
  "started_at": "RFC3339",
  "finished_at": "RFC3339",
  "stages": [
    {"name": "resolve_target", "title": "대상 확인", "status": "succeeded", "started_at": "...", "finished_at": "..."}
  ],
  "diagnostics": [],
  "data": {"stack": {"backend": "compose", "project_name": "hello-world", "services": [], "summary": {}}}
}
```

- `action`, `target`, `stages`, `outcome` are separate from `diagnostics`.
- `required_approval` appears on approval failures.
- `data` is feature-specific and omitted on failure and on cancellation.
- `stages` and `diagnostics` are always arrays (`[]`, never `null`).
- Progress is not embedded in the final JSON. The `core.Sink` receives live
  `Stage` events; only human UIs consume them. `--json` stdout contains exactly
  one JSON object.

Outcome values: `success`, `failure`, `canceled`.
Stage status values: `started`, `succeeded`, `failed`, `canceled`, `skipped`.

Exit codes (`core.Result.ExitCode`):

| code | condition |
|---|---|
| 0 | success |
| 1 | operation/environment failure |
| 2 | usage error or missing required input/approval |
| 130 | canceled/interrupted |

`environment_required`, `environment_unknown` and `environment_input_conflict`
are missing/invalid required input and map to exit 2; `environment_invalid`
config/file failures map to exit 1.

Rules for later features:

- Report at least one named stage with a Korean title; use stable snake_case
  stage names.
- Return typed `core.Error` diagnostics with Korean message/actions and English
  machine category.
- Put machine payload under `data`; never print secrets. Do not write progress
  to stdout in JSON mode.
- Treat cancellation and partial effects honestly; do not claim success for
  partial work and do not claim automatic rollback.
- Add new diagnostic categories instead of renaming existing values.
- If a feature needs result fields beyond `data`, request a schema addition.

Status payload types (`internal/stack`):

- `Snapshot{backend, project_name, project_dir, environment, services, summary}`
- `Service{name, configured, image, build, healthcheck_configured, state,
  health, app_verified, instances, connections}`
- `Instance{name, state, health, image, exit_code}`
- `Connection{source: runtime|config, host_ip, published_port, target_port,
  protocol}`
- `Summary{services, instances, running, healthy, unverified, unhealthy,
  stopped, not_created}`

Health semantics: `health=none` + `healthcheck_configured=false` means “running,
app unverified”, never verified. `healthy` plus `app_verified=true` requires a
configured healthcheck and every instance running and healthy. `starting` and
`unhealthy` are never success. Stopped/not-created services are shown without
claiming app health.

Compose install payload (`internal/composeinstall` → `data.install`):

```json
{
  "changed": true,
  "already_available": false,
  "version": "v5.8.0",
  "source": "https://github.com/docker/compose/releases/download/v5.8.0/docker-compose-darwin-aarch64",
  "destination": "/Users/me/.docker/cli-plugins/docker-compose",
  "checksum_verified": true,
  "size_bytes": 32440418,
  "recheck_output": "Docker Compose version v5.8.0"
}
```

Stable stages for `install-compose`: `plan` (설치 계획), `download` (내려받기),
`verify` (무결성 확인), `install` (설치), `recheck` (기능 재확인). On success
`changed` is false when Compose was already usable or the target already
existed in a usable path; the feature never overwrites a file. Failure diagnostics
state what was and was not changed; cancellation text explains that the target
is either absent or complete because the plugin is published atomically.

Secret boundary: status never requests resolved environment, labels or commands.
It parses only names, images, build presence, healthcheck presence, ports and
runtime state. Compose/ps JSON is decoded into narrow structs; extra fields are
dropped immediately. Diagnostic `detail` is truncated tool output; no dcraft
feature may add fetched secret values to it. The install payload contains only
public release metadata and a filesystem path.

---

## 7.1 Compose installation contract (ticket 06 / issue #7)

Feature: flat command `dcraft install-compose`, approval `install`,
`Requires: [docker_cli]`, `TargetOptional: true`. It never installs or repairs
Docker itself, never escalates privileges, and never changes Docker
Desktop/WSL integration.

Destination resolution (first match):

1. `DCRAFT_COMPOSE_PLUGIN_DIR` — absolute directory, documented test/explicit
   override; the binary is `<dir>/docker-compose`.
2. `$DOCKER_CONFIG/cli-plugins/docker-compose` when `DOCKER_CONFIG` is set.
3. `~/.docker/cli-plugins/docker-compose`.

The target must be a user-writable Docker CLI plugin path. A relative override,
an unknown home directory, or an unsupported platform is refused with
`compose_install_manual` and the exact manual commands; no risky attempt is
made. dcraft never writes system directories or application bundles.

Source and integrity:

- The latest stable official release is resolved at install time from
  `https://api.github.com/repos/docker/compose/releases/latest` (draft and
  prerelease ignored, major ≥ 2). `DCRAFT_COMPOSE_RELEASE_API` is a documented
  test/enterprise override; the resolved plan always shows the full source URL.
- Platform mapping: darwin/linux × amd64/arm64 → `docker-compose-<os>-<arch>`
  with Docker's `x86_64`/`aarch64` names. Other platforms get manual steps.
- Integrity requires the published `checksums.txt` asset. If the release does
  not publish it, has no entry for the asset, or the per-asset `digest` conflicts
  with `checksums.txt`, dcraft refuses (`compose_integrity_unavailable`) instead
  of installing unverified bytes. The published asset size detects truncation.
- The downloaded file must be a native executable for this platform
  (Mach-O `MH_EXECUTE` for darwin, 64-bit ELF `ET_EXEC`/`ET_DYN` for linux) and
  its CPU/machine must match `runtime.GOARCH`; wrong format is
  `compose_artifact_invalid`, wrong architecture is `compose_arch_mismatch`.
- Version numbers are never hardcoded as "latest"; the tag comes from the
  resolved release.

Install and verification:

- The feature never overwrites an existing plugin path (including a symlink)
  and never falls back to a standalone `docker-compose`. If Compose already
  works, it reports `already_available` with `changed=false`.
- `internal/artifacts.OSInstaller` copies into a temporary file in the
  destination directory, applies mode 0755, and publishes with one atomic
  no-replace operation; a concurrent creation yields `ErrExists`. Partial
  plugin files cannot appear.
- After installation the feature runs `docker compose version` through the same
  `execx.Runner` and only reports success when it succeeds. A recheck failure is
  `compose_recheck_failed` and states that the file may already be installed.
- Cancellation is reported with an install-specific `canceled` diagnostic: the
  target is either absent or complete; temporary download files are removed.

Engine and dependency boundary: `deps.Deps` exposes `Getenv`,
`Downloader artifacts.Downloader` and `Installer artifacts.Installer`. The
downloader only performs HTTP GET/download; the installer only checks existence
and performs atomic placement. Release resolution, integrity, platform and
Korean diagnostics stay in `internal/composeinstall`.

---

## 7.2 Explicit update contract (ticket 07 / issue #8)

Feature: flat `dcraft update [--no-cache] [--build-pull] [--no-pull]
[--force-recreate] [--timeout <seconds>] [-s/--service ...]`, no approval,
`RecordsHistory: true`, full default preflight. It runs only on an explicit
command or a deliberate TUI action. There is no file watcher, new-image
detector, periodic refresh, remote source download or hidden background run.

Stages (stable machine names; Korean titles in the UI):

| stage | title | effect |
|---|---|---|
| `resolve_project` | 프로젝트 이름 확인 | same name/service validation as every stack change |
| `allocate_name` | 프로젝트 이름 배정 | `state.AllocateName`; recorded name reused |
| `coordinate` | 같은 스택 작업 조정 | `state.AcquireStackLock`; held to the end |
| `plan` | 갱신 계획 | read-only model + container baseline; `--no-pull` image check |
| `build` | 로컬 이미지 빌드 | cached `compose build`; skipped when no build service |
| `pull` | 이미지 취득 | `compose pull` for image-only services; skipped for `--no-pull` |
| `apply` | 변경 적용 | `compose up --detach [--force-recreate] --pull missing|never`; runtime secret delivery prepared and committed through the same boundary as `up` |
| `verify` | 실행 상태 확인 | bounded readback; failure marks this stage failed |
| `record` | 정상 버전 기록 | retention tags + `state.json`; failure is a warning |

Ordinary behavior: local build services build with the normal cache; image-only
services pull their configured tag/digest. Unchanged services are not
force-recreated; real Docker verifies identical container IDs and `Created` on a
repeat update. `config --hash` is **not** image identity and is never used to
claim "unchanged"; Compose itself compares the resolved image/config during
`up`, so a moved tag recreates the service. Runtime secret references are
resolved by `apply` exactly as by `up` (current environment values in the
Compose subprocess, file delivery rewritten from its source), so an update with
`--force-recreate` delivers the current values; update never stores them. `up --wait` is never used as proof:
it returns 0 for crash-looping `restart: always` containers, for a
no-healthcheck container that dies right after, and for a re-up of an exited
container.

Four independent explicit choices (never combined behind one flag; their cost is
printed in the Korean preview):

- `--no-cache`: `compose build --no-cache` for local build services only. It
  re-executes every build step and takes longer; it is not a base-image refresh.
- `--build-pull`: `compose build --pull`. It refreshes base images of local
  builds; it does not pull an image-only service's configured reference.
- `--no-pull`: skip the image refresh. dcraft preflights each image-only
  reference with `docker image inspect` **before** build/apply and fails with
  `image_missing` if it is absent; apply uses `--pull never`. The default
  refreshes only image-only services, never local build services.
- `--force-recreate`: `compose up --force-recreate`; without it only
  Compose-changed containers are recreated. It increases the short interruption.

`--timeout` is an integer 1–3600 seconds and bounds only readiness observation;
invalid values are `invalid_option` (exit 2). It is not passed to Compose.

Readiness: after apply, update reads the engine itself (`docker compose ps -a
-q` then `docker inspect` for the exact image ID, state, health, exit code and
`RestartCount`) in a bounded poll/settle loop. Per service the published
classification is `healthy`, `running_unverified`, `unhealthy`, `exited`,
`restarting`, `not_started`, `not_created` or `timeout`. Only `healthy` and
`running_unverified` may be part of a completed update; `verified` is true only
when every service passed a configured healthcheck. `running_unverified` is
reported as "실행 중 · 앱 확인 안 됨 (healthcheck 없음)" and is never called
healthy. `RestartCount` is compared with the pre-apply baseline (a new container
must stay at 0) and the settle re-read catches a process that exits or restarts
just after the first success. `unhealthy`, `exited`, `restarting`,
`not_started`, `not_created` and `timeout` stop the operation with the observed
state, the failed stage and recovery actions; there is no automatic rollback.

Preview: update implements `core.Planner`. `Engine.Plan` is read-only: no name
allocation, no lock, no build/pull and no state write. The text states the
target, per-service planned work, the chosen option costs and explicitly that it
is a step plan, not an exact future-state diff (images, builds and hooks can
change conditions). The CLI prints it to stderr before running in human mode;
the TUI reuses the generic plan → confirm screen and one `enter` runs the
ordinary update without a second approval. JSON mode carries `plan.text` and the
list of build/pull services in the result.

Payload `data.update`: `project_name`, `project_name_source`, `scope`
(`stack|services`), `services`, `options` (`no_cache`, `build_pull`,
`image_pull`, `force_recreate`, `timeout_seconds`), `plan` (`text`,
`services`, `build_services`, `pull_services`), `built`, `pulled`, `applied`,
`verified`, `readiness` (per-service classification above), `record_status`
(`recorded|failed|skipped`), `good_version` (when recorded), `record_detail`
(when recording failed) and Korean `notices`. Target, stage list and outcome stay
in the common result envelope. On failure `data` is omitted; the failed stage,
the observed `service=status` summary and the per-instance detail are in the
diagnostics.

### Previous-good deployment record (published for rollback, consumed by #12)

Exactly one record per stack **and** environment lives at
`<state>/stacks/<folder>-<hash12>/state.json` (format version 1). Types in
`internal/state`:

```go
type GoodVersion struct {
    Version     int
    RecordedAt  time.Time
    OperationID string
    Target      GoodTarget
    Services    []GoodService
}
type GoodTarget struct {
    ProjectDir, ProjectName, Environment, DefaultEnvFile string
    Files, EnvFiles, Profiles []string // original order
    SecretRefs []SecretReference      // references only, never values
}
type GoodService struct {
    Service, ImageID, ImageRef, RetentionTag string
    Build, Healthcheck bool
}
```

- `ImageID` is the exact full `sha256:...` ID of the running container
  (`docker inspect .Image`). It is never the movable tag and never
  `com.docker.compose.image` (that label is the per-platform manifest digest,
  not the image ID).
- `RetentionTag` is a dcraft-owned tag
  `dcraft-good/<project>-<service>:<first16-of-image-id>`. Update creates it with
  `docker tag` and verifies `docker image inspect <tag> --format '{{.Id}}'`
  resolves to the recorded ID. It is unique per image, so it never moves.
- Written **only after** `verify` succeeded, while holding the per-stack lock;
  failed, unhealthy or canceled updates leave the previous file byte-identical
  (verified with real Docker). It replaces atomically (temp + rename,
  `0600`).
- It stores references only: file list in order, explicit env files, profiles,
  effective project name, environment and secret references. It never stores
  resolved secret values or container environments. Every `GoodTarget.SecretRefs`
  entry carries the service, phase, delivery, source kind, source name/ref,
  delivery target and (for AWS) the optional field, region and profile; update
  populates it from the current `.dcraft.yaml` only
  after `verify` succeeded and while holding the per-stack lock. Rollback (#12)
  re-resolves these references; it never reads a stored value.
- `(*state.Store).LoadGoodVersion(target)` returns
  `(GoodVersion, bool, error)`; `SaveGoodVersion(target, rec)` is the atomic
  writer. A first update on a previously unmanaged stack records the state it
  verified; no earlier history is invented. `OperationID` is reserved for a
  future engine-invocation id and is empty in this build; consumers must not
  treat it as required.
- dcraft never deletes retention tags/images and never prunes. #12 (rollback)
  must preflight the recorded ID with the local engine, never rely on the
  movable tag alone, and never replace a record outside the same lock.

### Same-stack coordination

`update` takes `state.AcquireStackLock` after name allocation and holds it across
`plan`, `build`, `pull`, `apply`, `verify` and `record`; the release is deferred
and therefore happens on success, failure and cancellation. A concurrent `up`,
`update`, rollback or backup on the same stack fails immediately with
`stack_busy` (exit 1) and the holder in the detail; a different folder or
environment is unaffected. There is no queue/wait policy in this version.
**Rollback (#12) and backup/restore (#16–#19) MUST take the same lock for their
read-record/change/write sequences**; a record replacement outside it can
interleave with a failed update and destroy the escape route.

### Host hooks and script trust (ticket #12 / issue #13)

Hooks are an engine-owned capability, not feature code. `up` and `update`
declare `Descriptor.Hooks = core.HookUpUpdate`; no other feature declares a
scope, so status/logs/exec/stop/restart/down/backup/restore/rollback never run
scripts. There is no filename convention and no automatic discovery.

Published `core` contract (consume, do not fork):

```go
type HookScope string // HookNone (""), HookUpUpdate ("up_update")
type HookStage string // HookStagePre ("pre"), HookStagePost ("post")
type HookEntry struct { Path string; Stage HookStage; SHA256 string; Trusted bool; Changed bool }
type HookPlan struct { Entries []HookEntry; RequiresApproval bool }
type HookRequest struct { Action, OperationID string; Target Target; Entry HookEntry }
type HookExecution struct { ExitCode int; Output string }
type HookService interface {
    Plan(ctx context.Context, target Target) (HookPlan, error)
    Approve(ctx context.Context, target Target, plan HookPlan, at time.Time) error
    Execute(ctx context.Context, req HookRequest) (HookExecution, error)
}
```

`Engine.Hooks` is wired by `internal/app`; `deps.Deps.Hooks` exposes the same
`*hooks.Service` to features. `Request.ApprovedHooks` carries the plan a UI
showed the user; it binds `script_trust` to those exact path/stage/sha256
identities. `PlanReport.RequiredApprovals` and `PlanReport.Hooks` let both
interfaces collect the independent approval generically.

Project registration lives in `.dcraft.yaml` (other sections and comments are
preserved; writes are atomic; no values or secrets):

```yaml
hooks:
  - path: scripts/pre-deploy.sh   # project-relative or absolute local path
    stage: pre                    # pre | post
  - path: scripts/post-deploy.sh
    stage: post
```

Both stages apply to `up` and `update`; `pre` runs before the feature, `post`
after its verification. Manual scripts and generated scripts (#14/#15) use this
one record through `hooks.Add`/`hooks.Remove`/`hooks.Load`. Registration does
not grant trust. `Add`/`Remove` serialize the read-modify-write with a bounded
(`5s`) `flock` on `.dcraft.lock` in the project folder, so concurrent
registrations cannot lose an update; the lock file is never deleted (removing a
flock inode would let two writers lock different inodes). A busy lock is
`hook_invalid` with retry guidance. Operator surfaces:

- `dcraft hooks` / `dcraft hooks --list` — show registrations, effective path,
  sha256, trust state and problems.
- `dcraft hooks --add --stage pre --script scripts/pre-deploy.sh`
- `dcraft hooks --remove --stage pre --script scripts/pre-deploy.sh`
- TUI wizard (`core.Wizard`): action → stage → script, with a pure preview.

Machine-local trust is stored per stack and environment at
`<state>/stacks/<folder>-<hash12>/script-trust.json` (0600, atomic replace,
format version 1). It is never written into the project. Each entry records the
canonical absolute script path, stage, full lowercase content sha256 and
approval time. The key is the canonical target identity (state directory) plus
path and stage; a content change or stage change needs a new approval. A
corrupt/unreadable/newer record is `state_failed` rather than a silent reuse.

Execution rules:

- cwd = project folder; the user's inherited environment plus
  `DCRAFT_HOOK_STAGE`, `DCRAFT_HOOK_PATH`, `DCRAFT_HOOK_SHA256`,
  `DCRAFT_ACTION`, `DCRAFT_OPERATION_ID`, `DCRAFT_PROJECT_DIR`,
  `DCRAFT_PROJECT_NAME` (may be empty before name allocation) and
  `DCRAFT_ENVIRONMENT`.
- Direct execution only, with the user's privileges. A missing executable bit,
  a directory or a start/format error is `hook_invalid` with guidance; dcraft
  never guesses an interpreter. No sudo, no sandbox claim, no automatic undo.
- Immediately before every execution the file is re-hashed. A mismatch is
  `hook_content_changed` (exit 2) and nothing is executed. This narrows but
  cannot eliminate TOCTOU (an in-place edit or symlink retarget after the final
  hash can still affect what the interpreter reads).
- Cancellation kills the hook's process group and reports the stage; partial
  external effects may remain. There is no hidden background execution.
- Output is captured, known environment-sourced secret values from
  `.dcraft.yaml` are replaced with `[REDACTED]`, and output is carried as
  `hook_output` info diagnostics (stderr/progress for humans). It never goes
  directly to JSON stdout.
- Pre failure is `hook_failed` (exit 1) and the main operation did not start.
  Post failure is a `hook_post_failed` warning and the completed operation
  stays successful. Hooks never run on rollback and do not restore data or
  external systems.

Stable stage names: `hooks_pre` (사전 스크립트), `revalidate_inputs` (입력
재확인), `hooks_post` (사후 스크립트). Exit codes remain 0/1/2/130.

### Previous-good rollback contract (ticket 11 / issue #12)

Feature: flat `dcraft rollback [--timeout <seconds>]`, approval
`previous_version`, `RecordsHistory: true`, full default preflight. It returns
to the **single** previous verified-good deployment recorded by update
(`state.json`, §7.2). There is no multi-version history, picker or automatic
rollback.

Stages (stable machine names; Korean titles): `load_record` (기록 확인),
`precheck` (변경 전 확인), `apply` (이전 이미지 적용), `verify` (실행 상태 확인),
`report` (결과 정리). The per-stack lock is acquired at the start of
`load_record` and held through the record read, apply and verification, so a
rollback cannot interleave with a failed update or a backup. The record is read
under the lock and remains byte-identical.

Precheck (all before any stack change, all reported with distinct categories):

- `rollback_unavailable`: no record for this stack and environment — first run,
  never-updated or pre-existing external stack. dcraft never invents a version.
- `rollback_record_invalid`: record format/folder/environment mismatch,
  duplicate service, non-immutable image identity (not a full `sha256:` ID) or
  malformed retention tag, or a recorded service missing from the recorded
  Compose configuration.
- `rollback_input_missing`: recorded project folder, Compose file or env file is
  gone.
- `rollback_image_missing`: the recorded image ID or its dcraft-owned retention
  tag is absent, or the tag now resolves to another ID. A newer image is never
  substituted.
- `rollback_secret_unavailable`: a recorded secret reference or a current
  `.dcraft.yaml` reference for the recorded service closure cannot be resolved
  (missing/empty env or file source, unavailable AWS source, unknown source).
  Values are read for availability only and never stored or printed. In a
  recorded `SecretReference`, `name` is the source reference (env var name,
  local path or AWS secret id), `field` the optional AWS field, `region`/
  `profile` the optional AWS CLI selectors, and `service`/`phase`/`delivery`/
  `source` the saved connection metadata. AWS references, including
  build-phase ones, are re-resolved through the same secret runtime, which
  fetches and discards the value. Both `phase: runtime` and `phase: build`
  references are availability-checked; rollback never rebuilds, so build
  values are not delivered by this operation. Current runtime delivery comes
  from the project's saved references through the published secret runtime.

`apply` uses `internal/compose.ApplyRollback` (published as `stack.Rollbacker`):
it writes a temporary dcraft-managed override that pins every recorded service
to its exact image ID, prepares the project's current runtime secret delivery
for those services, and runs `compose up --detach --pull never --no-build
<recorded services>`. Compose therefore cannot pull or build; a missing image
fails instead of being replaced. Only the recorded services are selected, so a
service added after the record is left untouched and reported in a notice. The
recorded file order, env files, profiles, project name and environment are used
instead of the current discovery; an explicit `-p` that conflicts with the
record is `invalid_option` (exit 2) and runs nothing. File *contents* are not
snapshotted, so editing a recorded Compose file changes the configuration that
rollback applies; the record reproduces the execution selection, not file bytes.

`verify` reuses update's published readiness classification (`stack.Readiness`:
`healthy`, `running_unverified`, `unhealthy`, `exited`, `restarting`,
`not_started`, `not_created`, `timeout`) with the same bounded poll/settle and
restart-count baseline. `unhealthy`, `exited`, `restarting`, `not_started`,
`not_created` and `timeout` fail with `readiness_failed`. Independently, every
observed instance's image ID must equal the recorded ID; a mismatch fails with
`rollback_verify_failed`. `running_unverified` is never called healthy.

Payload `data.rollback`: `project_name`, `environment`, `scope`, `services`,
`recorded_at`, `images` (`service`/`image_id`/`retention_tag`), `applied`,
`verified` (true only when every service passed a configured healthcheck),
`image_ids_verified`, `already_at_version`, `readiness`, `record_status`
(always `unchanged`), `data_restored` (always false), `hooks_run` (always false)
and Korean `notices`. On failure `data` is omitted; the failed stage, observed
`service=status` summary and per-instance image state are in the diagnostics.

Record policy: rollback **keeps the record unchanged**. There is exactly one
previous version, so a repeated rollback re-applies the same recorded images
(Compose leaves unchanged containers alone), reports `already_at_version` and
states that no older version exists. The rolled-back deployment is not written
back as a new "previous". Failed or canceled rollbacks never write the record or
history.

Honesty: rollback restores images and the recorded execution selection only. It
does not restore DB/volume data, external systems or script side effects, does
not run deploy hooks, and never deletes retention images/tags or prunes. It
never writes the project `.dcraft.yaml`: newer hook or secret registrations are
read as the current configuration, not rolled back to a recorded file. The
descriptor declares no hook scope, so the engine runs no pre/post host scripts
for rollback even when the project registers them.

---

## 7.3 Logs and exec (ticket #5)

Two read-only live features share one published selector and one streaming
boundary. Neither stops a service, and leaving a screen only detaches.

### Service and instance selection (`internal/selector`)

Published for later screens; do not create a second selection model.

- `Services(snap)` lists configured services with their observed instances in
  a stable order. Instances of services outside the active configuration are
  not selectable.
- `Select(snap, requested)` validates explicit service names (empty = every
  configured service). Unknown names are `service_unknown` (exit 2) with the
  available list; explicit order is preserved and repeats are dropped.
- `ChooseInstance(service, index)` applies the zero-based `-i/--index`. A
  stopped instance remains loggable; an out-of-range index is
  `instance_unknown` (exit 2) with the instance list.
- `LogTargets(snap, requested, index)` is the logs selection. `--index`
  requires exactly one explicit service; without it, a scaled service streams
  every instance with its Compose log prefix.
- `OneRunning(snap, requested, index)` is the exec selection. Exactly one
  service and one running instance are required. A scaled service without an
  index fails with `selection_required` (exit 2) and lists the instances; a
  stopped service or a stopped explicit instance is `instance_stopped`
  (exit 1). The first instance is never chosen implicitly.
- Indexes are zero-based everywhere in dcraft; the Compose adapter maps them
  to Compose's one-based `--index`. Using a sorted instance list and the
  default Compose naming means index N corresponds to `...-N+1`.

### `logs`

- Flags: `-s/--service` (repeatable; empty = whole stack), `-i/--index`
  (single service), `--no-follow` (follow is the default), `--tail`, `--since`,
  `--timestamps`.
- Color is requested only when the caller's stdout is a terminal. Logs never
  allocate a pseudo-terminal and never change terminal mode.
- Ctrl+C (or leaving the TUI live screen) cancels the follow. The result is
  `canceled` (exit 130), the `stream` stage says the stream was detached, and
  the service keeps running.
- Stages: `select_instance` (인스턴스 선택), `stream` (로그 스트림).
- `logs --json` is refused with a Korean usage error and exit 2: a live stream
  cannot be one result and must never be interleaved with the final JSON
  object.

### `exec`

- Requires one service and a non-empty argv. `AcceptsArgs` keeps options before
  positional arguments and the shared `--` terminator; `-s/--service`,
  `-i/--index`, `-T/--no-tty`.
- The argv is passed to `docker compose exec` unchanged after the service.
  dcraft never invokes `sh -c` and never guesses a shell. The image must
  contain the command; a missing command is `exec_failed` (exit 1) with Korean
  guidance, not a silent shell fallback.
- A pseudo-terminal is allocated only when the caller can relay an interactive
  terminal and `-T` is absent. `-T`, piped input, and JSON mode never allocate
  one.
- A non-zero remote status is a dcraft failure (exit 1); the tool's status is
  carried in the diagnostic's additive `exit_code` field so a remote exit 2 or
  130 can never be confused with dcraft's usage (2) or cancellation (130).
  Success payload `data.exec` contains `service`, `instance`, `index`,
  `command`, `tty`, `exit_code` and, in JSON mode, captured `stdout`/`stderr`.
- `exec --json` forces non-interactive capture and never writes live bytes to
  stdout. On failure the schema rule still omits `data`; the remote status is
  on the diagnostic.
- Stages: `select_instance`, `attach` (명령 실행). Cancellation restores the
  terminal and states that the service keeps running.
- Terminal ownership lives in `internal/execx.RunStream`: raw mode is applied
  only to a real terminal stdin and restored with `defer` on success, failure
  and cancellation. dcraft's own exit codes stay 0/1/2/130.

### TUI

- A `core.LiveFeature` opens a live screen. Logs stop with `q`/`esc`/`ctrl+c`;
  an attached command forwards keys and detaches with `ctrl+]` (or exits by
  itself). The last output lines remain visible in the management view.
- A `core.InputProvider` feature opens a generic form (choice and text fields)
  that shows the resolved values before running; `esc` runs nothing, and the
  feature validates the values again through `WithInputs`.
- The TUI never passes JSON mode, never uses the CLI's standard streams for a
  live feature, and normal quit touches no service.

---

## 8. Per-computer state

`internal/state.Root(getenv)`:

- `DCRAFT_STATE_DIR` when set (absolute or made absolute);
- otherwise `<os.UserConfigDir()>/dcraft`.

`internal/state.Store` implements `core.StateStore`:

```go
type StateStore interface {
    StackDir(target core.Target) (string, error)
    AppendOperation(target core.Target, rec core.OperationRecord) error
}
```

Layout:

```
<state>/names.json                  # project-name claims, 0600, atomic replace
<state>/names.lock                 # short-lived allocation lock, 0600
<state>/stacks/<folder>-<hash12>/
    history.jsonl     # append-only operation records, 0600
    .lock             # per-stack change lock + holder JSON, 0600
    state.json        # previous-good deployment record, 0600 (see §7.2)
    script-trust.json # machine-local hook approval, 0600 (see §7.2)
```

- The key is derived from the canonical absolute project folder **and** the
  environment id: same folder names in different places and different
  environments never share state; the readable folder name stays in the key.
  Symlinked spellings of an existing folder canonicalize to one identity.
- Directories are created lazily with `0700`; files with `0600`.
- History records contain operation id, action, outcome, timestamps, completed
  stages and diagnostics. The record-writing `history` stage itself, and any
  other still-open stage, is not persisted; a record never ends with a
  `started` stage. Never resolved secret values.
- `script-trust.json` is the machine-local script trust record. It is keyed by
  the same canonical folder+environment state directory and stores only the
  canonical script path, stage, sha256 and approval time. It is never a
  project-shared file and never contains values.
- Status reads and writes no state.
- History is written only for successful `RecordsHistory` features; failed
  verifications are never recorded as good.
- `state.json` is the single previous-good deployment record consumed by
  rollback (#12): full image IDs, dcraft-owned retention tags and reproducible
  file/env-file/profile/project/environment/secret-reference data. It is written
  only by a verified-good update under the per-stack lock; failed, unhealthy or
  canceled updates never touch it. See §7.2 for the exact types and rules.
- Name claims are keyed by the same folder + environment identity. They hold the
  effective Compose project name, its source and the owning folder; the
  automatic-name mapping is reused on rerun, after stop and after down.
- `state.LookupName` is the read-only claim read used by read-only features. It
  opens `names.json` only: no directory creation, no lock, no write. A missing
  file means "no claim"; a corrupt file is reported to the caller, which turns
  it into an `identity_unreadable` warning and a Compose-default fallback.
- `names.json` updates take `names.lock` with a short bounded wait, verify the
  claim, write a temp file and rename it atomically. The per-stack change lock
  never waits: a conflicting operation returns `*state.BusyError` immediately.
- Cross-process locking uses `flock` on macOS/Linux (WSL2). Other platforms keep
  building but return an explicit "unsupported" error instead of pretending to
  coordinate.

---

## 9. External boundaries

```go
type execx.Runner interface {
    LookPath(file string) (string, error)
    Run(ctx context.Context, cmd execx.Command) (execx.Result, error)
}

// Optional live capability required by logs/exec.
type execx.Streamer interface {
    RunStream(ctx context.Context, cmd execx.Command, stream execx.Stream) (execx.Result, error)
}
type execx.Stream struct {
    Stdin, Stdout, Stderr io...   // caller streams
    TTY                   bool    // allocate a child pty and set raw mode
    Width, Height         int
}
```

`RunStream` streams without buffering the live output; `Result` keeps only a
bounded tail for diagnostics. With `TTY` it allocates a pseudo-terminal for the
child and forwards bytes; only a real terminal stdin is put in raw mode, and the
previous mode is restored on success, failure and cancellation. A non-zero exit
is a `Result`; start failures and cancellation are errors.

- `execx.Command{Name, Args, Dir, Env, Stdin}`, `execx.Result{Stdout, Stderr,
  ExitCode}`. Non-zero exits are results, not Go errors; start failures and
  cancellation are errors.
- Production: `execx.NewOSRunner()`.
- Tests: `internal/execx/execxtest.Runner` records every call and returns
  scripted results without Docker or a shell.
- `internal/compose.Compose` is the only Compose adapter. It implements
  `core.Preflight`, `stack.Backend` (`Name`, `Inspect`), `stack.Lifecycle` and
  `stack.Identity`.
- `internal/artifacts` is the only artifact boundary: `Downloader` (HTTP GET and
  download to a file) and `Installer` (existence check and atomic no-replace
  placement). Features own release resolution, checksums, platform validation
  and Korean errors; this package performs no policy.
- `internal/stack.Backend` keeps the read-only stack boundary separate from the
  UI. No Kubernetes backend or multi-backend registry exists now.
- `stack.Streamer` (`Logs`, `Exec`) is the narrow live-output boundary used by
  the logs/exec features and implemented by `internal/compose`. Its requests
  carry a zero-based instance index and the exact argv; the adapter owns the
  Compose argument mapping, never a shell.
- Compose semantics (validation, merge, path resolution, profiles, ps JSON) are
  delegated to the Compose CLI; dcraft does not reimplement YAML.

---

## 10. Diagnostics catalog

Order: target resolution, then preflight. A fatal category stops later work.
A remote endpoint is refused before any `docker info`/Compose contact. Docker
Desktop's VM-backed unix socket is **local**, not remote.

| category | meaning | blocking |
|---|---|---|
| `docker_missing` | Docker CLI not found/executable | error |
| `engine_unreachable` | engine not running or connection failed | error |
| `engine_permission` | permission denied on the Docker socket | error |
| `remote_engine` | tcp/ssh/http(s) endpoint selected | error |
| `engine_endpoint_unknown` | endpoint/context could not be determined or is unsupported | error |
| `compose_missing` | `docker compose` unavailable | error |
| `compose_unsupported` | Compose lacks a required feature/version | error |
| `compose_release_unavailable` | official release metadata could not be resolved | error |
| `compose_install_manual` | install is not safely automatable here; manual steps given | error |
| `compose_install_exists` | target plugin path already exists; never overwritten | error |
| `compose_download_failed` | download/transport failure or truncated asset | error |
| `compose_integrity_unavailable` | source publishes no usable checksum metadata | error |
| `compose_integrity_mismatch` | downloaded SHA-256 does not match the published value | error |
| `compose_artifact_invalid` | artifact is not a native executable for this OS | error |
| `compose_arch_mismatch` | artifact architecture does not match this computer | error |
| `compose_install_permission` | destination is not writable by the user | error |
| `compose_install_failed` | other install/placement failure | error |
| `compose_recheck_failed` | installed, but `docker compose version` still fails | error |
| `no_configuration` | no Compose file in the target folder | error |
| `invalid_configuration` | Compose file cannot be parsed/validated | error |
| `target_invalid` | path/file/env input is missing or not a file/folder | error |
| `multiple_configuration_files` | several base files; first used | warning |
| `approval_required` | missing independent approval | error (exit 2) |
| `environment_required` | aliases exist and no alias was selected | error (exit 2) |
| `environment_unknown` | selected alias is not registered (or none exist) | error (exit 2) |
| `environment_input_conflict` | `--env` combined with `-f`/`--env-file`/`--profile` | error (exit 2) |
| `environment_invalid` | `.dcraft.yaml` syntax, fields, duplicates or referenced files are invalid | error |
| `project_name_shared` | several aliases resolve to the same explicit project name | warning (apply still stops at `project_name_conflict`) |
| `environment_overlap` | aliases declare the same host port/bind/external resource | warning |
| `environment_isolation_limits` | environment isolation never covers host resources | info |
| `project_name_conflict` | declared/recorded project name belongs to another stack (folder or environment) | error (exit 2) |
| `service_unknown` | explicit `-s/--service` is not in the Compose configuration | error (exit 2) |
| `selection_required` | a service/instance must be chosen explicitly; ambiguous scaled service | error (exit 2) |
| `instance_unknown` | explicit `-i/--index` is outside the instance list | error (exit 2) |
| `instance_stopped` | the selected service/instance is not running | error (exit 1) |
| `logs_failed` | the Compose log stream failed | error (exit 1) |
| `exec_failed` | the command could not start or exited non-zero | error (exit 1) |
| `stack_busy` | another operation holds the same stack lock | error |
| `lifecycle_failed` | a Compose lifecycle call failed | error |
| `update_failed` | a build, pull or detached apply failed; the stage and observed state are reported | error |
| `readiness_failed` | post-update state was unhealthy, exited, restarting, not started/created or timed out | error |
| `image_missing` | `--no-pull` found a required local image absent; nothing changed | error |
| `invalid_option` | an explicit feature option is invalid (for example `--timeout`) or conflicts with the recorded rollback target | error (exit 2) |
| `rollback_unavailable` | no previous-good record for this stack and environment; nothing inferred | error |
| `rollback_record_invalid` | stored record is malformed, belongs elsewhere or no longer matches the recorded configuration | error |
| `rollback_input_missing` | a recorded Compose/env file or folder is missing; nothing changed | error |
| `rollback_image_missing` | a recorded image ID or retention tag is missing or moved; no substitution | error |
| `rollback_secret_unavailable` | a recorded/current secret reference cannot be re-resolved; nothing changed | error |
| `rollback_failed` | the recorded images could not be applied; stage and observed state are reported | error |
| `rollback_verify_failed` | after apply, a running image ID differs from the record | error |
| `state_failed` | name/lock state could not be read or written | error |
| `verification_failed` | post-operation verification failed | error |
| `canceled` | operation interrupted | warning (exit 130 outcome) |
| `history_failed` | operation history could not be written | warning |
| `identity_unreadable` | stored/declared project identity could not be read; Compose default used | warning |
| `secret_invalid` | `.dcraft.yaml` secrets syntax, fields, duplicates or unsupported values are invalid | error |
| `secret_input_invalid` | explicit secret reference input is missing or invalid | error (exit 2) |
| `secret_unsupported` | another published-but-unimplemented secret shape is requested | error (exit 2) |
| `secret_source_unavailable` | referenced environment/file source is missing, unreadable or empty | error |
| `secret_apply_failed` | delivery preparation/materialization/override failed; a partial rotation may remain | error |
| `secret_aws_cli_missing` | the AWS CLI is not installed or not on PATH | error |
| `secret_aws_credentials_missing` | the AWS CLI cannot locate credentials/profile material | error |
| `secret_aws_region_missing` | no AWS region was supplied by the reference or AWS configuration | error |
| `secret_aws_access_denied` | IAM/KMS denied `secretsmanager:GetSecretValue` | error |
| `secret_aws_secret_not_found` | the secret name/ARN does not resolve in the selected region | error |
| `secret_aws_field_invalid` | the requested dot path is not a scalar in the JSON SecretString | error |
| `secret_aws_network` | the AWS endpoint was unreachable | error |
| `secret_aws_value_invalid` | SecretString is absent/binary-only, empty, whitespace-only or malformed | error |
| `secret_aws_failed` | another AWS service/runtime failure without a value echo | error |
| `backup_output_required` | no explicit backup output directory was given | error (exit 2) |
| `backup_volume_required` | no data source (named volume or declared bind folder) was selected | error (exit 2) |
| `backup_volume_unknown` | selected volume key/name is not part of the stack, or a bind folder was passed through `--volume` | error (exit 2) |
| `backup_volume_unsupported` | anonymous volume or another unarchivable volume selection | error |
| `backup_bind_unknown` | `--bind` value is not a declared bind folder or a unique mount target | error (exit 2) |
| `backup_bind_invalid` | a declared bind folder is missing, not a directory, unreadable, or contains an unsupported name/socket | error |
| `backup_bind_unsafe` | the bind source is a filesystem root or the output is inside the source | error |
| `backup_output_invalid` | output is not a writable directory or the artifact would escape it | error |
| `backup_consumer_unsafe` | another stack, unmanaged container or paused service consumes the selected volume or bind folder | error |
| `backup_selection_changed` | the selected source identity changed between discovery and stop | error |
| `backup_failed` | stop, archive helper, filesystem write, publish or completion-marker failure | error |
| `backup_integrity_failed` | archive truncation, unsafe entry or manifest mismatch | error |
| `backup_state_restore_failed` | stopped services could not be started again after the copy | error |
| `hook_invalid` | hook registration/config problem: unknown stage, unreadable `.dcraft.yaml`, busy write lock | error |
| `hook_input_invalid` | explicit `dcraft hooks` input is missing or invalid | error (exit 2) |
| `hook_failed` | a pre hook failed; the main operation did not start | error (exit 1) |
| `hook_content_changed` | approved content identity no longer matches; nothing was executed | error (exit 2) |
| `hook_post_failed` | a post hook failed after the main operation completed (result stays successful) | warning |
| `hook_output` | redacted hook output carried to human surfaces | info |
| `generator_input_invalid` | generated-script input is missing or unsafe: empty value, traversal, project-root destination, script inside the destination, or embedded credentials | error (exit 2) |
| `generator_failed` | the generated script file could not be published; nothing else changed | error |
| `internal_error` | unexpected error/parse failure/panic | error |

Each diagnostic carries Korean `message`, optional raw `detail`, and Korean
`next_actions`. Machine consumers switch on `category`, never on message text.
Warnings do not block success and are rendered on both interfaces.

Only `compose_missing` adds the `dcraft install-compose` next action. Docker
missing, engine unreachable/permission, remote endpoint, Compose unsupported and
older Compose keep their own Korean actions and never suggest a Compose install.
Compose presence is checked independently of the engine; if a Docker CLI reports
the missing plugin as an unknown top-level flag for `compose version --short`,
the full `compose version` command confirms the classification.

`Result.ExitCode` returns 2 for `approval_required`, `environment_required`,
`environment_unknown`, `environment_input_conflict`, `project_name_conflict`,
`service_unknown`, `invalid_option`, `secret_input_invalid`,
`secret_unsupported`, `selection_required`, `instance_unknown`,
`backup_output_required`, `backup_volume_required`,
`backup_volume_unknown`, `backup_bind_unknown`, `hook_input_invalid`,
`generator_input_invalid` and
`hook_content_changed`: those mean the caller must supply a different decision
(an approval, an environment, a free name, an existing service, a valid option,
a valid secret reference, a service/instance selection, or a backup
volume/bind/output). dcraft never prompts to resolve them; non-interactive runs
fail with the explanation instead. `instance_stopped`, `logs_failed` and
`exec_failed` are operation failures (exit 1). New categories are additive v1
values.

A failed update records the failed stage in `stages` (for example `verify`) and
puts the observed `service=status` summary plus per-instance state in the
diagnostic. A record-write failure after a verified update is not an operation
failure: the stage is failed, the payload says `record_status=failed`, and the
previous record is untouched.

---

## 10.1 Data backup and backup format v1 (ticket #16)

Feature: flat command `dcraft backup`; no approval (it exports data, it never
replaces it), `RecordsHistory: true`, `Wizard: true`, and three feature-owned
flags: `--output/-o <dir>` (`FlagString`, required), repeatable `--volume <key>`
(`FlagList`) and repeatable `--bind <path-or-mount-target>` (`FlagList`). At
least one volume or bind is required. The CLI never invents a destination;
a missing value is a `backup_output_required`/`backup_volume_required` failure
(exit 2) with Korean guidance and no service stop. The TUI uses the published
`core.Wizard` flow, not a feature-specific screen: the first read-only run
(`Options["wizard"]="steps"`) lists named volumes and asks for the output
directory, `WizardPreview` shows the selected volume, affected and running
services and the effect, and only `enter` runs the same engine path with
`wizard=apply`. A canceled wizard writes nothing.

Stable stages: `output` (저장 위치 확인), `coordinate` (스택 잠금),
`resolve_project` (프로젝트 확인), `discover` (데이터 선택),
`recheck_selection` (선택 재확인), `check_paths` (경로 안전 확인),
`inspect_consumers` (소비자 확인),
`stop_consumers` (서비스 중지), `archive` (볼륨/폴더 복사),
`verify_archive` (무결성 확인), `publish` (백업 공개),
`complete` (완료 표시), `restore_state` (서비스 상태 복구).

### Selection and consumers

- The selected stack is the same identity as `status`/lifecycle: explicit `-p`,
  a declared Compose name, then the recorded allocation; a read-only lookup is
  used and the resolved name is pinned with `-p` on every Compose call. The
  feature never allocates a name and never changes the stack's identity.
- Volume identity comes from `docker compose config --format json`:
  `volumes.<key>.name` (custom `name:` included), `external: true` as declared.
  Engine queries always use the actual engine name, never the Compose key.
  Anonymous volumes (a `volume` mount with no `source`) are reported by
  `data.Sources` but are explicitly rejected by this command. Declared bind
  mounts are supported through `--bind` (§10.3); anonymous volumes and other
  unarchivable selections remain rejected. Restore is #18/#19.
- Consumers are read with `docker ps -a --filter volume=<actual engine name>`
  (`-a` is mandatory: a stopped container still references the volume). A
  consumer is accepted only when its Compose project label equals the selected
  project and its service is one of the configured services that mount the
  volume. Any other container (another stack, an unmanaged container, an orphan
  of this project) is a refusal: `backup_consumer_unsafe`, no service is
  stopped, and the refusal names the containers, projects and states. dcraft
  never stops another stack and never claims consistency it cannot provide.
- A `paused` affected service is also refused (`backup_consumer_unsafe`): it
  cannot be stopped and restored to its prior state safely. `running` and
  `restarting` services are recorded as the prior running set and are the only
  services stopped. Services already `exited`/`created` are never started.
- Immediately before stopping, discovery and selection are repeated
  (`recheck_selection`). A changed volume key, engine name, service set, mount
  set or external flag is `backup_selection_changed` (exit 1) and nothing is
  stopped or archived. This is the binding between a plan/wizard preview and the
  actual operation; the preview is advisory, this recheck is authoritative.

### Coordination and verification

- The per-stack change lock is taken before the name read, consumer check and
  every stop/copy/restart. It is held across the entire stop → copy → verify →
  publish → restart sequence. A busy stack is `stack_busy` and nothing is
  stopped. `deps.Deps.Locks` is the published `state.Locker` capability; no
  feature type-asserts the concrete store.
- The archive helper has an explicit lifetime owned by dcraft, not by the
  killed CLI child: a unique name is chosen first, `docker create --name` makes
  the container, `docker start -a` runs it, and every exit path (success,
  error, cancellation, deadline) runs `docker rm -f <name>` in a bounded retry
  loop with a background context and confirms with `docker inspect` that the
  container is gone. After an interrupted create it keeps watching for a short
  grace window, because the daemon can complete a create whose CLI client was
  killed. `--rm` is not relied on. The stack lock is held until that
  confirmation, and services are restored only after it; a leaked helper can
  never become a later "outside consumer" of the volume.
- Post-copy verification happens inside this lock. The feature must not use
  `core.Verifier`: the engine releases the lock before `Verifier.Verify` and
  writes history afterwards. Future data features (#17–#19) follow the same
  rule: verify and restore state inside their own lock; history is only an
  audit append.
- Tool output is passed through `data.Backend.Redact` (the shared
  `secretref.Runtime.RedactText` helper) before it becomes a diagnostic or
  history record. It is best-effort redaction of values referenced by the
  project's saved secret references; dcraft never prints file contents. Backup
  itself fetches no secret value and never scans the volume for secrets.

### Backup format v1 (published for #17/#18/#19)

One backup is a directory:

```text
<output>/<name>/manifest.json    versioned identity, completion marker, integrity
<output>/<name>/data.tar.gz      tar+gzip, member paths relative to the data root
```

`<name>` is `dcraft-<project>-<kind>-<key>-<UTC timestamp>` with unsafe
characters replaced and a numeric suffix on collision; it is generated by
`backup.ArtifactName` and proven to stay inside the user-chosen output directory
(`backup.ArtifactPath`/`UniqueArtifactPath`). `manifest.json` v1:

```json
{
  "schema": "dcraft.backup",
  "format_version": 1,
  "complete": true,
  "created_at": "RFC3339",
  "tool": {"version": "...", "commit": "...", "go_version": "...", "os": "...", "arch": "..."},
  "source": {
    "kind": "volume",
    "project": "...", "environment": "", "project_dir": "/abs/project",
    "volume_key": "data", "volume_name": "project_data",
    "host_path": "", "mount_targets": ["/data"],
    "external": false, "anonymous": false, "services": ["app"]
  },
  "stack": {
    "project_name": "...",
    "services": ["app"],
    "running_before": ["app"],
    "stopped_for_backup": ["app"]
  },
  "data": {"file": "data.tar.gz", "format": "tar.gz", "size_bytes": 123, "sha256": "64-hex"},
  "entries": [
    {"path": "./file.txt", "type": "file", "mode": "0644", "uid": 0, "gid": 0,
     "size_bytes": 5, "sha256": "64-hex"},
    {"path": "./dir", "type": "dir", "mode": "0755"},
    {"path": "./link", "type": "symlink", "link_target": "file.txt"},
    {"path": "./hard", "type": "hardlink", "link_target": "./file.txt"}
  ],
  "totals": {"entries": 4, "files": 1, "dirs": 1, "symlinks": 1, "hardlinks": 1, "other": 0, "bytes": 5},
  "sensitive": true,
  "ownership_note": "Korean limitation text"
}
```

- `source.kind` is `volume` today; ticket #17 publishes `bind` with
  `host_path` set (and `volume_*` empty). #18/#19 must not invent a second
  manifest or a second data-file name.
- Per-entry `sha256` covers regular-file content only. It excludes ownership,
  mode, mtime and hardlink identity; those are separate fields. `mode` is the
  tar permission bits as octal text. `uid`/`gid` record container ownership so
  a root volume restore can check it. Symlinks and hardlinks carry
  `link_target`; FIFOs/devices are type `other` and are not materialized by host
  verification.
- `stack.running_before` and `stack.stopped_for_backup` are audit metadata, not
  a consistency promise. `sensitive` is always true: the selected volume may
  contain secrets; dcraft does not scan, encrypt or classify it.
- The completion marker is `complete`. An artifact is built and verified under
  `<name>.partial`; `manifest.json` is first written with `complete: false` and
  synced. The archive is decompressed, parsed and extracted into a fresh empty
  temporary directory; every entry is compared with the manifest. Only then is
  the directory renamed to `<name>`, the directory entry fsynced, and
  `manifest.json` atomically rewritten with `complete: true`, followed by a
  final `Verify`. If that last write fails, the artifact is demoted back to
  `.partial` when possible; otherwise the `complete: false` marker keeps it out
  of every valid listing. A kill between the rename and the completion marker
  therefore leaves an invalid directory, never a "complete" marker on partial
  data. A `.partial` directory is never a valid backup.
- Integrity entry points: `backup.Verify(dir)` requires a supported schema and
  format version, `complete: true`, the exact archive hash/size, and a full
  extract-and-compare of every entry and total. `backup.InspectArchive(path)`
  reads the whole gzip/tar stream into a fresh directory and rejects truncated
  gzip/tar (a truncated tgz can still list every name: listing alone is never
  integrity), unsafe names (`..`, absolute, NUL, backslash), duplicate entries
  and unsupported tar types. `backup.ReadManifest` only parses;
  `backup.List(root)` returns only complete, supported manifests and skips
  `.partial`/dot-prefixed directories.
- Extraction safety is part of the contract: a destination must be a fresh,
  empty directory. No tar flag reliably prevents a pre-existing destination
  symlink from being followed (GNU/BusyBox tar write outside with exit 0;
  bsdtar refuses). Empty the destination first and never extract into a live
  volume or folder. `backup.Verify` always creates its own fresh temporary
  directory and never touches a restore destination.
- Ownership/permission limits: the volume archive is created by a root helper
  container reading `:ro` (with the create/start/remove lifecycle above), so it
  stores exact uid/gid/modes/hardlinks/symlinks.
  Host-side extraction cannot fully restore container ownership or setuid;
  volume restore must use a root helper (#18). Recorded modes and uids are
  integrity metadata, not a host restore guarantee.

### Published interfaces (consume, do not fork)

- `internal/data`: `Backend` (`ProjectName`, `Sources`, `ServiceRuntime`,
  `VolumeConsumers(ctx, target, volumeName)`,
  `BindConsumers(ctx, target, hostPath)`, `StopServices`, `StartServices`,
  `ArchiveVolume`, `HelperImage`, `Redact`), `DataSource` (`Kind`, `Key`, `Name`,
  `Declared`, `DeclaredPaths`, `MountTargets`, `External`, `Anonymous`, `ReadOnly`, `Services`),
  `Kind` (`volume`/`bind`), `ServiceRuntime`, `ContainerConsumer`, `OpResult`,
  `VolumeHelperImage` and `ArchiveDataFile`. `internal/compose` is the only
  implementation; tests substitute a fake.
- `internal/backup`: `Schema`, `FormatVersion`, `ManifestFile`, `DataFile`,
  `DataFormatTarGz`, `PartialSuffix`, `Manifest`, `Source`, `StackState`,
  `ArchiveInfo`, `Entry`, `Totals`, `Tool`, `InspectArchive`, `Verify`,
  `ReadManifest`, `WriteManifest`, `List`, `Artifact`, `Kind`,
  `SelectVolumeSources`, `SelectableVolumes`, `SelectSources`,
  `SelectableSources`, `NormalizeBindSource`, `CheckBindSources`,
  `ResolveForContainment`, `ArchiveHostDir`, `ParseWizardSources`, `SourceFrom`,
  `ArtifactName`, `ArtifactPath`, `UniqueArtifactPath`, `Discovery`,
  `ConsumerReport`, `Discover`, `CheckConsumers`.
- `secretref.Runtime.RedactText(target, text)` is the shared redaction helper.
- The feature-owned archive image is `alpine:3.20` by default and can be
  overridden with `DCRAFT_BACKUP_IMAGE` (the image is shown in the plan/wizard
  and notices). Docker may pull it if it is absent; no other network access is
  performed by backup.

### Output, retention and failure behavior

- The output directory is explicit. `--output` is required in the CLI; the TUI
  wizard shows `./dcraft-backups` as an editable default. Relative paths resolve
  against the process working directory (not `--path`). Before any service is
  stopped, the directory is created if missing, resolved with symlinks, checked
  to be a directory and probed with a temporary file; a missing/invalid output
  is `backup_output_required`/`backup_output_invalid` and nothing is stopped.
- One invocation may select several volumes; each gets its own v1 artifact.
  The stop set is the union of their configured services. A later volume's
  failure keeps earlier published artifacts (and names them), plus the partial
  path of the failing one; dcraft never deletes valid backups to make a run look
  atomic.
- There is no automatic deletion, retention policy, scheduling, remote upload
  or backup hook. Two runs produce two artifacts. Output shows the location,
  size, completion/integrity state and a Korean sensitive-data notice; images,
  logs, whole environments and secret files are not collected.
- Failure diagnostics state the completed/failed stage, the observable service
  state, the partial-output location and whether cleanup succeeded. Only
  services this operation stopped are started again; a restore failure is
  `backup_state_restore_failed` and the artifact paths are included. Cancellation
  attempts the same restoration with a fresh bounded context and reports
  `canceled` (exit 130); it never claims the archive is valid.
- New categories (additive v1): `backup_output_required`,
  `backup_volume_required`, `backup_volume_unknown` (exit 2);
  `backup_volume_unsupported`, `backup_output_invalid`,
  `backup_consumer_unsafe`, `backup_failed`, `backup_integrity_failed`,
  `backup_selection_changed`, `backup_state_restore_failed` (exit 1).

---

## 10.2 Data restore and the safe-replace contract (tickets #18/#19)

Feature: flat command `dcraft restore --backup <artifact-dir> [--volume <key> |
--bind <path-or-mount-target>] [--safety-output <dir>]
[--allow-source-mismatch] [--allow-path-mismatch]`, `Approval: data_restore`,
`RecordsHistory: true`, `Wizard: true`, no hooks. A non-interactive run without
`--approve data_restore` exits 2 before any service or data change. The same
command and pipeline restore named volumes (#18) and stack-declared bind
folders (#19); the artifact kind decides which target flag is valid.

Stable stages: `validate_artifact` (백업 검증), `coordinate` (스택 잠금),
`resolve_project` (프로젝트 확인), `select_target` (복원 대상 확인),
`preview` (복원 계획), `inspect_consumers` (소비자 확인),
`safety_check` (안전 백업 공간 확인), `stop_consumers` (서비스 중지),
`safety_backup` (안전 백업), `replace` (데이터 교체), `verify` (복원 검증),
`restore_state` (서비스 상태 복구).

### Validation before any overwrite

`restore.ValidateArtifact` resolves the directory and calls `backup.Verify`
(schema, format version, `complete`, gzip/tar exit and per-entry hashes). It
then requires a stable source identity (volume project/key/name, or bind
project/host path), rejects duplicate normalized destination paths, and
rejects an entry below a symlink (files created through a link). Unsafe names,
absolute paths, `..`, hardlink targets outside the archive and dangerous links
are refused. Corrupt, truncated, incomplete or unsupported archives never stop
a service. The data hash is checked again immediately before `replace`, so a
file changed after the preview aborts with `restore_integrity_failed` and the
original data untouched.

Bind artifacts add host-representation checks before any stop: the target must
be an existing, writable directory that is not a filesystem root, the project
folder or one of its parents; the artifact and safety folders must not contain
or be contained by the target. `extract` refuses FIFO/device entries and
invalid link/hardlink shapes explicitly instead of silently skipping them.

### Mapping, approval and preview

The target is the same stack identity as `status`/lifecycle (explicit `-p`,
declared Compose name, then recorded allocation, normalised with `-p`). By
default the artifact project, environment and volume identity (or bind host
path) must match the resolved destination. `--allow-source-mismatch` permits a
documented project/environment mapping; bind path mapping has its own
`--allow-path-mismatch` flag so accepting a renamed stack never silently
redirects a folder. Both report warnings. `--volume` names an explicit target
key (omitting it uses the artifact's recorded key). `--bind` names a declared
bind target by path or mount target; without it the archived path must equal a
current declared spelling. An unknown explicit volume/bind is
`restore_volume_unknown`/`restore_bind_unknown` (exit 2); a recorded target
missing from the stack or a path mismatch without the path flag is
`restore_target_mismatch` (exit 1). The volume destination must exist and the
bind target must pass the host checks above (`restore_target_invalid`). The
preview shows project, artifact, backup content totals, target key→name or host
folder, current content summary, affected/running/stopped services, the
safety-backup path, overwrite impact, path mapping warnings and the brief
interruption. The TUI wizard collects the artifact (complete volume and bind
artifacts found in the current folder or `./dcraft-backups`, or a typed path),
one target from the current stack's volumes and declared bind folders, and the
safety folder, then shows `WizardPreview`. The wizard preview is not an
approval: an approval-gated wizard routes through the same generic plan +
confirmation screen, and the engine skips the approval only for the read-only
`wizard=steps` collection run.

### Safety backup and replacement

The feature holds the per-stack change lock across stop → safety → replace →
verify → state restore. Consumers are checked with the actual engine name
(including stopped containers); another stack, an unmanaged container or a
paused affected service aborts without stopping anything. Free space for the
safety archive is checked on the host filesystem that holds the safety folder;
an unknown size is a warning, a shortfall is `restore_safety_failed` before any
stop. The safety backup is written to `<safety>/<artifact>.partial`, inspected,
marked complete, verified with `backup.Verify`, renamed and verified again. A
failed or incomplete safety backup aborts with the original data untouched and
the partial location reported.

Replacement empties the volume with the root helper `find /data -mindepth 1
-delete` (never `rm -rf /data/*`, which misses dotfiles), extracts
`data.tar.gz` with the root helper (`tar -C /data -xpzf`) and verifies the
restored tree against the manifest: file hashes, type, uid, gid, mode, symlink
targets, hardlink inode equality and an exact entry count (a merge with stale
files fails). The destination is emptied before extraction because GNU/BusyBox
tar follow pre-existing destination symlinks. Only previously running services
this operation stopped are started again; already stopped services stay
stopped. `special files` (FIFO/devices), xattrs/ACLs and platform ownership
limits are reported, never claimed as preserved.

A bind destination uses the same stop → safety → replace → verify sequence:
the current folder is archived host-side as a verified v1 `kind=bind` safety
artifact, emptied with the host-side equivalent of `find <target> -mindepth 1
-delete` (no glob, no following links, never the folder root) and extracted
host-side through `os.Root`, so neither a pre-existing nor an archive-provided
symlink can redirect a write outside the target. Verification compares hashes,
entry types, ordinary permission bits, symlink targets, hardlink inode identity
and the exact entry count, then applies the archived folder mode. Normal modes,
mtimes, symlinks and hardlinks are preserved; uid/gid, xattrs/ACLs and special
mode bits are reported as host-dependent limits, not claimed as volume-equivalent
fidelity. FIFO/device entries are refused before replacement.

### Honest failure and cancellation

`ExecutionReport.DataState` is one of `untouched`, `uncertain_partial` or
`replaced_verified`. Failures before `replace` restart only the services this
operation stopped and state that the original data was unchanged. A failure or
cancellation during empty/extract/verify never restarts services, reports the
stage, the remaining data state, the safety-backup location and a recovery
action (restore the safety artifact or inspect the volume). A failure after
verification keeps `replaced_verified`. Cancellation uses exit 130 and the
feature's `CanceledReporter` text. No project hooks run.

### Published interfaces (consume, do not fork)

- `internal/restore`: `ValidateArtifact`, `Artifact`, `CheckArtifactData`,
  `Destination` (`Kind`, `Key`, `Name`, `Services`, `MountTargets`,
  `ManifestSource`, `Match`, `Outside`, `Prepare`, `UnsafeConsumers`, `Usage`,
  `SafetyBackup`, `Empty`, `Extract`, `Verify`), `Executor.Execute`,
  `ExecutionRequest` (including `Locked` and `ExplicitPath`), `ExecutionReport`
  (`host_path`, `path_mapping`), `DataState`,
  `CreateSafetyBackup`, `MatchSource`, `ValidateLocation`, `WriteVerifyScript`,
  `SpaceChecker`, `DiskSpace`. #19 implements `Destination` for a bind folder
  and reuses the executor; it must not fork the v1 manifest or the safety
  sequence.
- `internal/data`: `VolumeRestorer` (`VolumeExists`, `VolumeUsage`,
  `EmptyVolume`, `ExtractVolume`, `VerifyVolume`) and `VolumeRestoreBackend`
  (`Backend` + `VolumeRestorer`). `internal/compose` is the only production
  implementation.
- `internal/backup`: `BuildArtifact` (complete/verify an already-written
  `data.tar.gz` as a safety artifact) and `CompareInspect` (manifest vs
  inspected archive), alongside the v1 format and `Verify`.
- New categories (additive v1): `restore_backup_required`,
  `restore_volume_unknown`, `restore_bind_unknown` (exit 2);
  `restore_artifact_invalid`, `restore_integrity_failed`,
  `restore_target_mismatch`, `restore_target_unsupported`,
  `restore_target_invalid`, `restore_consumer_unsafe`,
  `restore_safety_failed`, `restore_failed`, `restore_verification_failed`,
  `restore_state_restore_failed` (exit 1). `stack_busy` is shared.

## 10.3 Bind-folder backup (ticket #17)

`dcraft backup` also accepts repeatable `--bind <path-or-mount-target>` next to
`--volume`. The source list is still only the current stack's
`docker compose config --format json` bind mounts; dcraft never adds an
arbitrary host folder. The TUI wizard lists named volumes first and bind
folders second in one generic multi-select step (`core.WizardStep.Multi`, space
toggles, enter confirms; enter on an untouched list selects the highlighted
item). The apply run uses the same option lists as the CLI.

Path rules:

- `data.DataSource.Declared` keeps the absolute path as Compose declared it;
  `DeclaredPaths` carries every distinct spelling that resolves to the same
  folder (merged into one selectable source); `Name` is the symlink-resolved
  path (`EvalSymlinks`) used for display, containment checks, archiving and
  `manifest.source.host_path`. Relative and `~` inputs are accepted only as
  spellings of a declared source; they resolve against the first Compose file's
  directory, like Compose.
- `CheckBindSources` requires an existing readable directory, refuses a
  filesystem root, and refuses an output directory inside a selected source
  (including after the output is created and resolved). The check runs before
  any service stop. Volume-only backups keep their previous behavior.
- Consumer detection mirrors #16: `docker ps -a --filter volume=<path>` is
  tried with the host spelling and the Docker-visible `/host_mnt` spelling,
  stopped containers included, and the union is compared with the selected
  project/service. Any outside consumer refuses with
  `backup_consumer_unsafe`; dcraft never stops another stack. The wizard labels
  show the same shared-use probe before selection.
- The archive is host-side tar (`backup.ArchiveHostDir`), not a container
  helper. It stores `./`-relative names, dotfiles, modes (including
  setuid/setgid/sticky), uid/gid, symlinks and hardlinks; FIFOs/devices are
  recorded as `other` and not materialized by host verification. Sockets and
  backslash names are refused; a file whose size changes while it is read fails
  explicitly. Host tar is a live read, not a filesystem snapshot: an in-place
  write that keeps the same size cannot be detected. The destination is opened
  with `O_EXCL` inside a fresh `.partial` directory, so a pre-existing
  destination symlink is never followed. Verification still uses
  `backup.Verify` and a fresh temporary extraction directory.
- `manifest.source.kind` is `bind` with `host_path`; `volume_key` and
  `volume_name` are empty. `ownership_note` states that xattrs/ACLs are not
  archived and ownership restoration limits exist. The report includes
  `msgBindOwnershipNote` and the sensitive-data notice.
- New additive categories: `backup_bind_unknown` (exit 2);
  `backup_bind_invalid` and `backup_bind_unsafe` (exit 1). New stage:
  `check_paths` (경로 안전 확인). No retention, scheduling, remote upload,
  hooks, auto-collection of secret files, or automatic deletion is added.

---

## 11. Verification

Tested seams:

- target resolution (temp folders, override order, explicit order, parent
  isolation, env files, profiles, project name); target-optional resolution
  ignores registered environment aliases and broken `.dcraft.yaml` catalogs
  while the same folder still requires an alias for stack features;
- environment config parsing (missing file, empty file, alias order, relative
  paths, invalid/duplicate/missing fields), mandatory selection, no implicit
  choice, no-alias defaults, expansion order, explicit-input conflicts,
  per-environment state keys, explicit project-name sharing and static
  port/bind/external-resource overlap warnings;
- preflight classification with `execxtest.Runner` (all diagnostic categories,
  local Desktop socket vs remote refusal, no engine contact for remote);
  `compose`-only and `docker_engine`-only declarations still look up Docker,
  refuse a remote endpoint, and check exactly their own capability, while
  `docker_cli` alone stays minimal;
- compose inspection parsing (health, instances, connections, unconfigured
  services, no secret leakage, stable Compose arguments);
- engine seam with fake features (approval, stages, verification, a panicking
  verifier mapped to `internal_error`, data omitted on failure and
  cancellation, history stage excluded from its own record, cancellation,
  panic recovery, warnings, planned requirements, target
  optionality, plan preview without approval/run);
- backup format v1 (entry hashes for files/dirs/symlinks/hardlinks, truncated
  gzip rejected even when all names list, unsafe/absolute/duplicate names
  rejected, complete marker required, unsupported version, per-entry tampering,
  `List` ignoring `.partial`/incomplete output, artifact-name containment,
  explicit writable output directory with a removed write probe);
- backup controller with a fake data backend (actual engine-name selection,
  named/custom/external/anonymous/bind mapping, `-a` consumer detection and
  other-stack refusal without stopping, paused refusal, only-running stop and
  only-stopped restart, lock busy before stop, output precheck before stop,
  manifest running/stopped audit data, truncated/read-failure cleanup with no
  published artifact, cancellation restoring services, selection re-resolution
  and `backup_selection_changed`, two backups retained, multiple volumes,
  sensitive notice, redacted tool output with a sentinel, wizard steps/preview);
- backup CLI/TUI through the same engine: missing output/volume exit 2, unknown
  volume, success JSON (`data.backup`), stop→archive→start order, read-only
  source mount, actual volume name in the consumer filter, no environment value
  in output, other-stack refusal, stack busy, helper failure cleanup/restart,
  repeated backups, pty wizard selection/preview/apply; a real-Docker
  interruption regression asserts no `dcraft-backup-*` container survives, no
  interrupted artifact is listed, prior services are restored and the
  immediately following backup of the same volume succeeds;
- bind-folder backup through the same engine: Compose config relative/`~`/
  symlink normalization, arbitrary-path rejection, output-inside-source and
  root refusal before any stop, shared-consumer detection with the host and
  `/host_mnt` spellings, mixed volume+bind artifacts (`kind=bind`,
  `host_path`), host tar content fidelity (dotfiles, modes, uid/gid, symlinks,
  hardlinks, FIFOs), destination-symlink refusal, unreadable folder, changed
  names, cancellation after stop restoring services with no valid artifact,
  and the TUI multi-select/preview/apply path;
- safe restore through the shared executor with fake and real bind
  destinations: declared-target resolution, path-mismatch refusal and explicit
  mapping warnings, undeclared/project-root/artifact-inside-target/safety-inside
  target refusal before stop, safety-backup failure with untouched data,
  replace-not-merge with stale files, extraction and verification failures
  reported as `uncertain_partial` without restarting, cancellation preserving a
  verified safety backup, only previously running services restarted,
  special-file rejection, host tar fidelity (modes, mtimes, symlinks,
  hardlinks, uid/gid/xattr limits), other-stack consumer refusal and the
  TUI artifact+target selection including bind folders;
- registry validation and CLI/TUI assembly from registries;
- state layout and permissions, automatic-name allocation (first/suffix/reuse/
  declared/conflict/external ownership/environment/concurrency), the read-only
  `LookupName` (no creation, no lock, no rewrite, symlink-canonical identity,
  corrupt registry) and the per-stack lock (busy holder, different stacks,
  goroutine exclusivity, cancellation);
- read-only identity resolution (explicit/declared/allocated priority, no
  Compose calls for `-p`, no state creation, no rewrite) and the engine seam
  (read-only features get the name without a new stage; mutating features do
  not; read failure is a warning with Compose fallback);
- lifecycle command construction (global flags before the subcommand, `up
  --detach` + services, `stop` + services, project-wide `down` without `-v`,
  `restart --no-deps` for explicit services), config/project parsing and
  `lifecycle_failed`/`stack_busy`/`project_name_conflict`/`service_unknown`
  diagnostics with the fake runner;
- update unit/fake-runner: every readiness classification (healthy,
  running_unverified, unhealthy, exited, restarting/crash-loop, not_started,
  not_created, timeout), settle re-read after an early exit/restart, bounded
  timeout messages, the four independent flag semantics, `--no-pull` image
  preflight before mutation, service selection, invalid `--timeout` exit 2,
  preview content/limits, stage order, lock release on success/failure/cancel,
  `stack_busy` on the same stack with another stack unaffected, record
  replacement only on success, record-tag failure as a warning with the old
  record intact, retention-tag naming and no delete/prune path, and no secret
  values in payload or record;
- Compose update adapter with the fake runner: narrow model parsing (no
  environment values), exact image ID/health/restart-count parsing, build/pull/
  apply argument construction, no `--wait`, and missing-image vs engine-failure
  classification;
- Compose install feature with a fake downloader/installer: plan text, existing
  plugin refusal, already-available no-op, checksum mismatch, truncated
  download, non-executable artifact, wrong architecture, permission failure,
  recheck failure, cancellation cleanup and stage order;
- artifacts boundary: atomic placement, no overwrite (file and symlink), temp
  cleanup, HTTP status/truncation handling;
- release resolution against captured real GitHub response shapes for every
  supported platform, missing/conflicting checksum metadata, unsupported
  platforms and broken API responses (no external network in default tests);
- process e2e with a fake `docker` shim: exit codes, JSON purity, non-interactive
  no-args, help, parent isolation, SIGINT→130, missing `--env` exit 2 with the
  alias list, `--env` in `status`, `target.environment`, alias inputs in the
  Compose argument order, no prompt when non-interactive, repeated up/name
  reuse, same-name folders, explicit and configured name conflicts, unknown
  service, no secret values in output/history, TUI menu + confirmation +
  quit-without-run, status in a fresh state dir creates no files, status uses
  the recorded/declared/explicit name and rewrites nothing, install approval
  exit 2, blocked release source clean failure, missing Docker CLI reported as
  Docker (not Compose), update pre-run preview on stderr then success, update
  JSON purity/stage order/readiness values/no secret values, update invalid
  timeout exit 2, `--no-pull` missing image fails before any build/pull/apply,
  unhealthy update keeps the old record, cancellation exit 130 with no record,
  update service selection;
- process status check against the real Docker CLI with a `DOCKER_CONFIG`
  without cli-plugins (install offered only for a genuinely missing Compose);
- pty process tests: TUI enter+quit, environment selection screen and keyboard
  choice, quit-from-selection runs nothing, no mutating Docker command,
  mutating confirmation before running, up through the same engine, install plan
  shown, decline changes nothing, approval installs and re-checks;
- guarded real install (`DCRAFT_COMPOSE_REAL_INSTALL=1`): official download,
  published checksum, temp plugin directory, independent `docker compose
  version`, real home Compose untouched;
- real Docker integration behind `DCRAFT_INTEGRATION=1`: isolated
  `dcraft-test-*` project, real healthchecks, connection info, compose file
  unchanged, containers unchanged after TUI quit, nested-folder block, cleanup
  with its own project only. No host-wide prune or other project changes.
- real Docker lifecycle: up→status running, stop→exited, resume with the same
  container, restart not applying changed configuration, down keeping
  named-volume data, repeated up without duplicate project, two same-name
  folders (`hello-world`, `hello-world-1`), explicit-name collision refused
  with exit 2, an existing externally created project reused, concurrent up
  (one winner, one `stack_busy`), concurrent name allocation, declared names
  from `-p`/`name:`/`.env`/exported `COMPOSE_PROJECT_NAME`, suffixed-folder
  status reporting `hello-world-1` and the same container IDs with unchanged
  state files, another stack's container IDs/data unchanged, TUI quit leaving
  services running, cleanup with its own projects only.
- runtime secret references: config parse/validation/persistence (no values),
  unknown service, duplicate target, unsupported build phase, AWS shape
  validation (name/ARN, field path, region, profile, credentials-like fields),
  idempotent re-add, add/remove/list CLI and JSON, non-interactive exit 2,
  only needed services resolved (including depends_on), missing/empty/
  unreadable sources failing before Compose, redaction of captured output,
  per-stack materialized file mode 0600 and lifetime, crash-safe rewrite on
  every apply (no value cache), removal and `down` cleanup, and the TUI
  wizard preview/decline/save flow with keyboard navigation;
- real Docker runtime secrets: environment-variable value visible inside the
  container, file secret readable at `/run/secrets/<target>` with mode 0600,
  `docker restart` and no-change `up` surviving, force-recreate, reference
  removal/down cleanup, a missing source leaving no orphan file, a known
  sentinel never appearing in JSON/human output/state/diagnostics, and the
  pre-existing Compose file hash unchanged;
- real Docker environments: two aliases with different files/profiles/env files
  run as separate stacks with separate declared project names and host ports,
  env-file values reach the containers, status reports each environment and
  never mixes their services, fixed-port/bind overlap is reported from static
  declarations before applying, and an unrelated canary stack is unchanged;
- service/instance selection: default all services, explicit order, unknown
  service, 0-based indexes, stopped instance loggable, exec ambiguity/stops,
  and the exit-code split (selection = 2, operation = 1);
- live execution units: logs flag mapping, no pty and no color without a TTY,
  cancellation detached without service commands, JSON refusal; exec exact
  argv with no shell, TTY only with a terminal and no `-T`, JSON capture,
  remote non-zero mapping with `exit_code`, missing command and stopped service;
- execx streaming: piped and pty modes, input forwarding, output capture and
  context cancellation, and terminal restoration after success and failure;
- TUI live screens: logs streaming, quit to the management view, attach key
  forwarding and `ctrl+]` detach, generic input form, no run before confirm;
- real Docker logs/exec: a scaled `web=2` stack with distinct instances, logs
  follow streaming a new line and ctrl+c leaving both containers running,
  exec reaching the right instance in both indexes, literal argv (no shell),
  missing command, stopped service, non-zero remote exit, and quitting the TUI
  logs screen leaving the stack running, with cleanup limited to its project.
- real Docker update: repeat update keeps container IDs and `Created`; a normal
  update keeps the build cache while `--no-cache` re-executes the build and
  changes the image identity; a source change rebuilds/recreates only that
  service; a tag moved in a temporary local registry (host-network `registry:2`,
  removed afterwards) recreates only the image-only service and records the new
  exact image ID; runtime env/file secrets reach a force-recreated container
  with their current values, the record stores references (service, source kind,
  ref, target) without values, and `down` still removes the materialized file
  and managed override; a healthcheck pass is
  success/healthy and a no-healthcheck service is `running_unverified` (never
  verified); unhealthy, crash-loop (`restart: always`), early exit and timeout
  all fail with the observed `service=status` and leave the previous record
  byte-identical; cancellation mid-apply exits 130 with the record intact; the
  record has image IDs/retention tags/file/env/profile/name/environment data and
  no resolved secret value; a retention tag still resolves to the recorded
  image after a forced recreation and after `down`; update holds the per-stack
  lock and a concurrent `up` fails `stack_busy` while a canary stack's container
  IDs and volume data are unchanged; cleanup removes only this test's
  projects/images/registry.
- rollback unit/fake: no record, malformed/non-immutable record, missing
  Compose/env file, missing image ID, missing or moved retention tag, missing
  env/file/AWS secret source, explicit project-name conflict, stack busy, image
  ID mismatch after apply, unhealthy/exited/restarting readiness failures,
  cancellation, idempotent repeat, recorded execution configuration over
  current discovery, extra configured services untouched, no build/pull/tag/
  prune path, payload honesty fields and no record change on failure/cancel.
- real Docker rollback: a verified v1 update plus a failed v2 update (record
  stays v1); rollback restores the recorded image ID and execution
  configuration (env marker and baked image content), leaves named-volume data
  unchanged, keeps the record byte-identical, reports `already_at_version` on
  a repeat and leaves a canary stack unchanged; a missing retention tag, a
  missing image ID and a missing recorded secret source all abort before the
  stack changes; cancellation mid-apply exits 130 with the record intact; a
  fresh stack with no record fails cleanly without creating containers.

Commands:

```sh
gofmt -l .
go vet ./...
go build ./...
go test -count=1 ./...
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegration -v
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegrationHook -v
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegrationVolumeBackup -v
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegrationBindBackup -v
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegrationVolumeRestore -v
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegrationBindRestore -v
DCRAFT_COMPOSE_REAL_INSTALL=1 go test ./test/e2e/... -run TestRealComposeInstall -v
```

Real-Docker tests skip without their guard variable. Unit tests never require
Docker or external network. Unrun platforms/paths are not claimed as verified.

---

## 12. Not implemented here (owned by later tickets)

- TUI alias creation/editing: aliases are created by editing `.dcraft.yaml` in
  this version (selection is provided by both interfaces).
- AWS Secrets Manager sources are implemented by ticket #10 below; generated
  Compose/Dockerfile files and the S3 generator flow, Kubernetes, remote
  engines, resident daemon/web UI are not implemented. Runtime environment/file
  references are implemented by ticket #9 below; Compose installation by
  ticket #6; update readiness and the previous-good record by ticket #7; host
  hooks and script trust by ticket #12; the guided Git host-script generator by
  ticket #13 (see `docs/host-script-generators.md`); named-volume backup by
  ticket #16 and bind-folder backup by ticket #17; build-time delivery by
  ticket #10 and previous-good rollback by ticket #11.
- Good-version history beyond the single record (rollback has no multi-version
  picker). The TUI plan/confirm component is generic; `install-compose`,
  `update`, `backup`, `restore`, `rollback` and `generate-git-hook` are
  planners in this build, and `secrets`/`backup`/`restore`/`generate-git-hook`
  use the generic wizard. An ordinary update shows its plan
  and requires one `enter` to run, not an approval; rollback shows the same plan
  screen and collects only `previous_version`. `up` also goes through the
  generic plan because it declares the hook scope; with no hooks the plan text
  is empty and only the ordinary confirmation remains. TUI service/instance
  selection exists for the ticket-05 live screens through `core.InputProvider`;
  lifecycle mutations still use the CLI `-s/--service`.
- Docker itself (installation, repair, replacement), administrator escalation,
  Docker Desktop/WSL integration changes.

Residual risks: Exec and logs run through the Docker CLI with the user's
privileges; a command can change application data, and dcraft does not sandbox
it, restore files it changes, or claim otherwise. Live output is user data:
dcraft never fetches secret values, but an application can print its own
secrets, and dcraft does not censor that. Logs/exec errors report a bounded tail
of tool output (which may include application output) as diagnostic detail.
`--index` maps a zero-based sorted instance list to Compose's one-based index;
a service with a custom `container_name` cannot be scaled, so the mapping holds
for the scaled (default-named) case only. The child pseudo-terminal size is
captured when the command starts; later terminal resizes are not forwarded. TUI
live output keeps a bounded line window and drops lines if rendering falls
behind. TUI exec forwards printable and common navigation keys, not every
terminal-specific escape sequence. Compose capability detection is based on running the installed
CLI (version/JSON probes), so a heavily patched Compose may pass probes and fail
later; diagnostic details are tool output and could contain user-provided file
content, though dcraft never fetches or prints secret values; status parses a
subset of Compose/ps output, so unknown future fields are ignored by design; the
TUI renders long status text without a scroll viewport. Environment alias files
are hand-edited (no TUI editor); the host-resource conflict scan is static and
best-effort (no interpolation/merge evaluation), so undetected sharing is
possible and complete isolation is never claimed. Environment aliases and
lifecycle names are separate: aliases partition state, files and profiles; the
lifecycle controller allocates or honors the Compose project name per
folder+environment. Lifecycle residual risks: an unreadable `names.json` makes
read-only features fall back to the Compose default with an
`identity_unreadable` warning instead of failing; a declared name equal to
Compose's derived default is treated as automatic; a declared rename of a
recorded target leaves the old project unmanaged by design; per-stack
coordination fails fast instead of queuing, so an automation caller must retry;
`flock` coordination is verified on macOS (Linux/WSL2 use the same syscall but
were not run on this machine); a declared project name shared by several
environment aliases produces the ticket-04 `project_name_shared` warning but
still stops at apply time with `project_name_conflict`, because two environments
must not operate on one Compose project. Install residual risks: the plan and
the install run resolve the release separately, so a release published between
confirmation and download could change the version; integrity and the recheck
still apply to the version actually installed. The release API is rate-limited
for unauthenticated use; a rate-limited response is a clean
`compose_release_unavailable` with manual guidance.

Update residual risks: readiness is a bounded poll with a settle window, so a
process that dies after the window can still be reported as running (the app is
then `running_unverified` at best when it has no healthcheck); only configured
healthchecks are used. A record-write failure after a verified update is not an
operation failure (consistent with history), so it surfaces as
`record_status=failed` plus a Korean notice rather than a `Diagnostic`; the
previous record remains intact. A service-scoped update (`-s`) verifies only the
selected services; Compose may still start dependencies, and their health is not
part of this operation's readiness result. Retention tags accumulate one per
service and image ID; dcraft never deletes them and has no retention policy. Only one
previous-good deployment is kept and it is not a history. Name allocation in
`internal/update` follows the published lifecycle rules through
`stack.Identity`/`state.AllocateName`, but the small resolution sequence is
currently duplicated; extracting a shared allocator is a change request to the
integration owner. `--no-pull` still lets Compose attempt a pull if an image is
absent between the preflight and apply (the window is small and the preflight
fails first in normal use). Real-Docker verification ran on macOS arm64 with
Docker Desktop; the registry scenario uses Docker host networking, so a machine
without that support skips that part instead of claiming it passed.

Backup residual risks: the archive helper image (`alpine:3.20`, override
`DCRAFT_BACKUP_IMAGE`) may be pulled on first use (a hidden but announced
network action); anonymous volumes are deliberately rejected; bind folders are
copied host-side with the running user's host permissions and the documented
xattr/ACL/ownership limits; a host file that changes mid-copy fails the
backup instead of being silently truncated;
`data.Backend.ArchiveVolume` output and any Compose failure text are redacted by
the feature, but redaction is best-effort against values referenced by
`.dcraft.yaml` and cannot remove values the helper or Compose invent/echo from
elsewhere; the root helper records container ownership but host-side extraction
cannot restore it; special files (FIFOs/devices) are recorded but not
materialized during host verification; a crash between the final directory
rename and the completion-marker rewrite leaves an invalid final directory with
`complete: false` (listed nowhere, safe to delete, but visible); a plan/wizard
preview is not cryptographically bound to the run, so the pre-stop
`recheck_selection` is the authoritative check; concurrent backups to the same
output directory are serialized per stack, not per directory, so different
stacks can write different names concurrently; no retention/scheduling/remote
copy exists by design. Real disk-full was not run; the disk-full path is tested
with a fake helper writer and by classification of helper output. Helper
cleanup retries for up to 20 seconds; only a daemon that refuses removal for
that whole window can leave a `dcraft-backup-*` container, and that run fails
with `backup_failed` naming the helper instead of silently continuing.

Host-hook residual risks: scripts are arbitrary user code and may leave partial
external effects; dcraft neither sandboxes nor undoes them. Content hashing
narrows but cannot eliminate TOCTOU: an in-place edit or symlink retarget after
the final hash can change what the interpreter reads, and a script may source or
execute other files whose contents are not part of the digest. Only
environment-sourced runtime secret values known from `.dcraft.yaml` are redacted
from hook output; values fetched or printed from other sources are not covered.
Hooks run before the feature takes the per-stack lock, so two concurrent
same-stack invocations can both run pre hooks; trust-record writes are atomic
but last-writer-wins, and losing an approval only forces re-approval. Project
registration writes are serialized with a bounded `.dcraft.lock` flock on
Unix; other platforms keep building but cannot serialize concurrent writers.
The project-name variable is empty in a pre hook on a first `up` because name
allocation happens inside the feature. Generated-script features must not treat
registration or plan display as trust.

Rollback residual risks: the record stores the file *list*, not file bytes, so
editing a recorded Compose file changes the configuration a rollback applies;
rollback cannot reproduce a version whose Compose files were changed or deleted.
Secret values are re-resolved from their current external sources, so old values
are not guaranteed and a source removed after the record aborts the rollback.
AWS references are re-resolved through the AWS CLI and the operator's existing
credential chain; a missing CLI/credential/region/network or denied/removed
secret aborts before any change. The readiness
classification is currently a second implementation of update's rules against
the same `stack.Readiness` values (§13 change request). Cancellation inside
apply leaves Compose's partial stop/recreate effects; the apply stage detail
reports the observed state and the engine adds the generic canceled diagnostic.
Rollback applies only recorded services; a service added after the record is
not reverted or removed and is reported in a notice. `flock` coordination and
real-Docker verification ran on macOS arm64 with Docker Desktop only.

---

## 13. Change request checklist

Before changing a shared type/interface, send the integration owner:

1. the exact new field/type/service and why a feature cannot express it locally;
2. affected tickets and whether existing behavior/JSON stays compatible;
3. tests proving CLI and TUI still share the seam and no safety rule moved into
   a UI;
4. documentation updates to this file.

Published by ticket #16 (consume, do not fork): `internal/data` `Backend`,
`DataSource`, `Kind`, `ServiceRuntime`, `ContainerConsumer`, `OpResult`,
`VolumeHelperImage`, `ArchiveDataFile`; `internal/backup` `Schema`,
`FormatVersion`, `ManifestFile`, `DataFile`, `DataFormatTarGz`, `PartialSuffix`,
`Manifest`, `Source`, `StackState`, `ArchiveInfo`, `Entry`, `Totals`, `Tool`,
`InspectArchive`, `Verify`, `ReadManifest`, `WriteManifest`, `List`, `Artifact`,
`SelectVolumeSources`, `SelectableVolumes`, `SourceFrom`, `ArtifactName`,
`ArtifactPath`, `UniqueArtifactPath`, `Discovery`, `ConsumerReport`;
`state.Locker` and `deps.Deps.Locks`; `core.FlagList`,
`Request.OptionLists`, `Invocation.OptionLists`/`OptionList`; the backup
diagnostic categories and the archive-verification-inside-the-lock rule.
`secretref.Runtime.RedactText` is the shared redaction entry point.

Published by ticket #17 (consume, do not fork): `data.Backend.BindConsumers`,
`data.DataSource.Declared` (bind path as Compose declared it; `Name` is the
symlink-resolved path), `core.WizardStep.Multi`, `core.WizardStep` multi-select
answer convention (newline-joined option values), `backup.Kind`,
`backup.SelectSources`, `backup.SelectableSources`,
`backup.NormalizeBindSource`, `backup.CheckBindSources`,
`backup.ResolveForContainment`, `backup.ArchiveHostDir`,
`backup.ParseWizardSources`, the `check_paths` stage and the
`backup_bind_unknown`/`backup_bind_invalid`/`backup_bind_unsafe` categories.
Bind consumers are queried with both the host spelling and the Docker-visible
`/host_mnt` spelling.

Published by ticket #5 (consume, do not fork): `internal/selector` types and
functions (`Service`, `Instance`, `Selection`, `Services`, `Select`,
`ChooseInstance`, `LogTargets`, `OneRunning`), `stack.Streamer` +
`stack.LogsRequest`/`ExecRequest`/`ExecResult`/`CommandError`,
`execx.Streamer` + `execx.Stream`, `core.IO` + `Request.IO`/`Invocation.IO` +
`Request.JSON`/`Invocation.JSON`, `core.LiveFeature`/`LiveMode`
(stream/attach), `core.InputProvider` + `InputField`/`InputKind`/`InputOption` +
`Engine.Inputs`/`InputsReport`, `core.CanceledReporter`,
`Diagnostic.ExitCode`, the `select_instance`/`stream`/`attach`/`inputs` stage
names, the `logs`/`exec` payloads under `data.logs`/`data.exec`, and the new
diagnostic categories/exit mapping.

Published by ticket #9 (consume, do not fork): `internal/secretref` types
(`Phase`, `Delivery`, `SourceType`, `Reference`, `Catalog`, `Runtime`,
`Session`, `Mode`), the `.dcraft.yaml` `secrets:` list schema, the
`core.WizardSpec`/`core.Wizard`/`Descriptor.Wizard` guided-flow contract, the
`stack.ConfigInfo.Dependencies` map, `stack.OpResult.Notices`, and the
`secret_invalid`/`secret_input_invalid`/`secret_unsupported`/
`secret_source_unavailable`/`secret_apply_failed` diagnostic categories.

Published by ticket #12 (consume, do not fork): `Descriptor.Hooks`/
`core.HookScope`/`core.HookStage`, `core.HookEntry`/`HookPlan`/`HookRequest`/
`HookExecution`/`HookService`/`Request.ApprovedHooks`, `PlanReport.Hooks`/
`RequiredApprovals`, `internal/hooks` (`Registration`, `Stage`, `Catalog`,
`Load`/`Add`/`Remove`/`ResolvePath`/`ContentSHA256`/`Describe`, `New`,
`TrustVersion`), `deps.Deps.Hooks`, the `.dcraft.yaml` `hooks:` list schema, the
machine-local `script-trust.json` format, the `dcraft hooks` flags and the
`hook_invalid`/`hook_input_invalid`/`hook_failed`/`hook_content_changed`/
`hook_post_failed`/`hook_output` categories and stage names `hooks_pre`/
`revalidate_inputs`/`hooks_post`. Generated-script features must register
through `hooks.Add` and must still request script trust separately from their
generation/save approval.

Published by ticket #3 (consume, do not fork): `stack.Lifecycle`,
`stack.Identity`, `stack.ConfigInfo`, `stack.ProjectInfo`, `stack.OpResult`,
`deps.Deps.Lifecycle`, `lifecycle.Controller`/`Action`/`Payload`/`Report`/
`Present`, `state.AcquireStackLock`/`StackLock`/`LockHolder`/`BusyError`,
`state.AllocateName`/`NameRequest`/`NameAllocation`/`NameSource`/
`NameConflictError`/`LookupName`, `core.IdentityReader`/`Engine.Identity`,
`internal/identity.New`/`Reader.ReadProjectName`, the `names.json`/`.lock` state
layout, the lifecycle stage names and the new diagnostic categories/exit-code
mapping.

Published by ticket #7 (consume, do not fork): `stack.Updater` (`UpdateModel`,
`Containers`, `Build`, `Pull`, `Apply`, `ImageID`, `TagImage`), `stack.UpdateModel`,
`stack.ServiceDefinition`, `stack.BuildOptions`, `stack.ApplyOptions`,
`stack.Container`, `stack.ImageMissingError`, `stack.Readiness`/
`stack.ServiceReadiness`/`stack.ReadinessInstance`, `state.GoodVersion`/
`GoodTarget`/`GoodService`/`SecretReference`/`GoodVersionPath`/
`LoadGoodVersion`/`SaveGoodVersion`, `update.Controller`/`Options`/`Plan`/
`Payload`/`Report`, update stage names and the `update_failed`/
`readiness_failed`/`image_missing`/`invalid_option` categories. `#12` consumes
the record and must take the same per-stack lock; `#9`/`#10` may populate
`GoodTarget.SecretRefs` additively, never with values.

Published by ticket #11 (consume, do not fork): `stack.Rollbacker`
(`ApplyRollback`), `stack.RollbackPin`, the rollback stage names, the
`rollback_unavailable`/`rollback_record_invalid`/`rollback_input_missing`/
`rollback_image_missing`/`rollback_secret_unavailable`/`rollback_failed`/
`rollback_verify_failed` categories and `data.rollback`. The record schema is
unchanged; rollback only reads it. Readiness classification is currently
implemented in both `internal/update` and `internal/rollback` against the same
published `stack.Readiness` values; extracting one shared bounded-readiness
helper is a change request for the integration owner.

Ticket #7 change request to the integration owner: the CLI/TUI preview path now
calls `Engine.Plan` for every `core.Planner` in human/TUI mode, not only for
approval-gated features. This is required for the ordinary update preview; the
engine's approval policy is unchanged, `install-compose` behavior is unchanged,
and JSON mode still makes no separate plan call. Ticket #13 needs the hook
capability described in §7.2 (a new `core` descriptor field/capability and a
hook trust service in `deps.Deps`).

Allowed without a request: new feature package, new registration file, feature
payload type under `data`, feature-local Korean strings and tests. A lifecycle-like
feature consumes `deps.Deps.Lifecycle` (or requests a new narrow service); it does
not call Compose directly or copy the controller. Not allowed: editing another
feature, adding a feature branch in CLI/TUI, bypassing the engine for Docker
access, printing secrets, or silently changing an existing machine value/exit
code.


---

## 14. Runtime secret references and delivery (tickets #9/#10)

Runtime secrets are references, not a secret store. Environment-variable,
local-file and AWS Secrets Manager sources share one reference contract;
build-time delivery extends the same contract in 14.6 and the AWS resolver is
specified in 14.7.

### 14.1 Project config

`<project folder>/.dcraft.yaml` gains an optional list:

```yaml
secrets:
  - service: web               # Compose service
    phase: runtime             # runtime (build has its own delivery; see 14.6)
    delivery: environment      # environment | file
    source: env                # env | file | aws
    ref: DB_PASSWORD           # env var name, file path, or AWS secret name/ARN
    target: DB_PASSWORD        # optional for env/file; required for aws
    field: database.password   # aws only: dot path into a JSON SecretString
    region: ap-northeast-2     # aws only: optional CLI region
    profile: ops-admin         # aws only: optional CLI profile
```

- Required: `service`, `phase`, `delivery`, `source`, `ref`. `target` defaults
  to the env var name (environment delivery) or the source file's base name
  without the last extension (file delivery). AWS references must set an
  explicit `target`, because a secret name/ARN is not a valid delivery name.
- Validation rejects unknown services, duplicate target per service+delivery,
  unsupported/unknown phase, delivery and source values, unsafe env names,
  empty refs and unsafe target names. AWS validation checks the name/ARN shape,
  the dot path, region and profile syntax, and refuses `field`/`region`/
  `profile` on non-AWS sources. Credential-like YAML fields
  (`aws_access_key_id`, `aws_secret_access_key`, `session_token`, `value`, …)
  are rejected; dcraft stores no credentials.
- `delivery: environment` with `phase: build` is an input error (exit 2).
- Re-adding an identical reference is idempotent. Removing one regenerates the
  managed override without it; a file-delivery materialization is kept until a
  safe cleanup point (next successful `up` or `down`) so restart still works.
- The file is written only after explicit confirmation, atomically. Existing
  `environments:` and unknown top-level keys survive; comments survive where
  the YAML library can attach them. Resolved values are never written.
- The file is shareable project metadata: it reveals sources and targets, not
  values. `--add` stores the reference without reading the source; existence
  and emptiness are checked when applying. A service that already declares the
  same `environment:` key or `/run/secrets` target can conflict with the
  generated override; dcraft does not rewrite the user's Compose file to
  resolve it.

### 14.2 Effective Compose inputs

Existing Compose files and Dockerfiles are never modified. When runtime
references exist, dcraft writes a managed override at
`<state>/stacks/<stack>/compose.secrets.yaml` (0600) and appends it with `-f`
after the user's Compose files. The first user file still determines Compose's
project directory. The override contains only references:

```yaml
services:
  web:
    environment:
      DB_PASSWORD: "${DCRAFT_SECRET_1}"
  worker:
    secrets:
      - source: dcraft_secret_1
        target: worker_token
secrets:
  dcraft_secret_1:
    file: /abs/state/stacks/<stack>/secrets/web__worker_token__<hash>.secret
```

Environment values are passed only in the Compose subprocess environment and
are never written to disk. File values are materialized under the per-stack
state directory with mode 0600. Basic use without references has no override
and no changed Compose inputs. Rollback history must store the user file list
and `.dcraft.yaml` references; the override is regenerated from them, never
copied as a value-bearing artifact.

### 14.3 Lifetime, cleanup and crash safety

File secrets are live bind mounts. Materialized files live as long as the
stack may restart. Explicit removal (`dcraft secrets --remove`) removes the
reference from the override but keeps the file until a safe point: a successful
`dcraft up` that can recreate the service without it, or a successful `dcraft
down`. A failed teardown keeps them. This is deliberate: deleting the file
immediately would break `docker restart` of a container that still binds it.
The next operation rewrites every file-delivery materialization from its
current source (in place, never atomic rename); dcraft stores no fetched value
and cannot compare old values, so it never silently reuses a value it cannot
verify. Environment delivery is fetched before every create/recreate; restart
does not need it because Docker already holds the value. Empty, missing,
unreadable or non-regular sources fail before Compose changes services. If a
prepare fails after an existing materialized file was overwritten, dcraft says
that a partial rotation may remain; it never stores the old value to restore.

`DCRAFT_STATE_DIR` inside the project folder is refused for file delivery so
materialized values are never left in the project folder.

### 14.4 Disclosures, migration and redaction

- Environment-variable delivery is visible to Docker-authorized users and
  `docker compose config` prints service `environment:` values. dcraft never
  prints resolved environments.
- File delivery requires the application to read `/run/secrets/<target>`;
  dcraft does not assume a universal `*_FILE` convention or guarantee app
  support.
- Existing inline sensitive values in Compose or `.env` are not detected or
  removed. `status` and `secrets` output include Korean guidance to move them
  to references; Docker's own storage/query boundary is not controlled.
- Fetched values are redacted from all captured tool output, diagnostics,
  errors and human/JSON results, including a trailing-newline variant. Values
  are held only for the current operation. `Runtime.RedactText(target, text)`
  is the shared best-effort helper for diagnostics outside a prepared session
  (backup helper output); it reads referenced env/file sources and never
  returns or persists a value. It deliberately does not call AWS outside a
  delivery need: a diagnostic path must not trigger a secret fetch. dcraft
  does not control values the app, Dockerfile or user scripts print
  themselves.

### 14.5 Interfaces for tickets #10/#11

New sources and build delivery must reuse `secretref.Reference`
(`phase`, `source`, `ref`, `target`, `field`, `region`, `profile`),
`secretref.Validate`, `Catalog`, `Runtime.Prepare`, and the managed-override
pattern instead of adding a second config format or a secret store. Build
delivery adds BuildKit mount declarations to the managed inputs; AWS adds a
source resolver with the same non-empty and redaction rules. Rollback
re-resolves references; it never keeps old values. The AWS checker is a narrow
`CheckAWS(ctx, Reference)` capability on the same runtime.

### 14.6 Build-time delivery (ticket #10)

Build-phase references extend the same config with a fixed delivery:

```yaml
secrets:
  - service: web
    phase: build            # build
    delivery: file          # the BuildKit secret mount; environment is rejected
    source: env             # env | file | aws
    ref: BUILD_TOKEN        # env var name, file path, or AWS secret name/ARN
    target: BUILD_TOKEN     # BuildKit secret id; required for aws
```

- `phase: build` with `delivery: environment` is an input error (exit 2): build
  values never become environment variables or ARG/ENV values. `service` must
  have a Compose `build:` section; a non-build service is `build_service_invalid`
  (exit 2) before anything is saved or built.
- Existing Compose files and Dockerfiles are never modified. dcraft writes
  `<state>/stacks/<stack>/compose.build-secrets.yaml` (0600) and appends it with
  `-f` only for build operations. It declares `build.secrets` for the selected
  services and the top-level file secret that points at the private
  materialization:

  ```yaml
  services:
    web:
      build:
        secrets:
          - source: dcraft_build_secret_1
            target: BUILD_TOKEN
  secrets:
    dcraft_build_secret_1:
      file: /abs/state/stacks/<stack>/build-secrets/web__build_token__<hash>.secret
  ```

  The user's first Compose file still determines the project directory. The
  generated file is reported as `build_override_file` by `dcraft secrets`.
- Environment, file and AWS sources are materialized as 0600 regular files
  under `<state>/stacks/<stack>/build-secrets/`. File-source copies live for a
  rebuild and are pruned at a safe point (a successful build of that selection
  or `dcraft down`). Environment-source and AWS copies are written only for the
  Compose build command and removed as soon as it exits, successful or not,
  because both can be re-resolved. A source is resolved before any file or
  override is written, so a missing/empty/denied source fails before the build
  and leaves no partial preparation. The state directory inside the project
  folder is refused.
- AWS build references use the same CLI lookup as runtime references and are
  materialized only as file sources for the BuildKit mount. The generated
  override remains file-only; no consumer receives an AWS-specific branch.
- Verified BuildKit behavior is surfaced, not hidden: changing a build secret
  **does not invalidate the build cache**. The ordinary build keeps the cache;
  `dcraft update --no-cache` is the explicit fresh-build choice. dcraft never
  stores or compares previous values, so it cannot warn by detecting a value
  change. `dcraft update`'s plan and report, and `dcraft secrets`, state this
  limitation in Korean.
- Compose can build implicitly during `dcraft up` when an image is missing.
  When the selected services have build-phase references, `dcraft up` passes
  `--no-build` and points to `dcraft update`, so Compose can never run a build
  without the BuildKit secrets.
- Before a build, dcraft checks the builder: `DOCKER_BUILDKIT=0` or a missing
  `docker buildx` fails with an explanation. It never falls back to an ARG,
  environment value, or command-line value.
- The Dockerfile must consume the mount. dcraft reads the resolved build context
  and Dockerfile(s) (including `dockerfile_inline`) and, when
  `RUN --mount=type=secret,id=<target>` is absent, reports a Korean warning with
  the file path and a concrete example. It never edits the Dockerfile. If the
  Dockerfile cannot be read or its path is unknown, the warning says so.
- Captured tool output, diagnostics and JSON are redacted against fetched build
  values. dcraft cannot prevent a Dockerfile from printing the value or copying
  it into the image; that is disclosed. The value is not placed in image
  config/history by dcraft, and no value is written to state/history.
- The previous-good record stores the build (and runtime) references in
  `target.secret_refs` (`service`, `phase`, `delivery`, `source`, `name`) so a
  rollback can precheck source availability. It stores no value.

### 14.7 AWS Secrets Manager sources (ticket #10)

The AWS source reuses the operator's existing AWS authentication. dcraft runs
the installed `aws` CLI through the same narrow `execx.Runner` boundary as
Docker, so the credential chain (environment, shared config/credentials, SSO,
instance role) stays entirely outside dcraft. dcraft never reads, stores,
writes or prints credential material. External prerequisite: a working AWS CLI
v2 and whatever authentication the operator already uses. dcraft does not
install or configure the CLI.

The lookup command is exact and has no shell interpolation or value argument:

```sh
aws secretsmanager get-secret-value \
  --secret-id <name-or-ARN> \
  --query SecretString \
  --output json \
  --no-cli-pager \
  [--region <region>] [--profile <profile>]
```

- `--output json` is deliberate: text mode appends a newline and prints `None`
  for an absent/binary-only value, which is ambiguous with the literal string
  `None`. The JSON string is decoded exactly; a legitimate trailing newline in
  the secret is preserved and the delivered value is not trimmed.
- `--region` and `--profile` are optional non-secret selectors saved with the
  reference for reproducibility. The CLI flag is `--aws-profile` because the
  shared CLI already reserves `--profile` for Compose profiles; the YAML field
  is `profile`.
- `field` is a dot path into a JSON SecretString. Each segment is a map key or
  an array index; the selected leaf must be a string, number or boolean.
  Objects/arrays are refused instead of serialized implicitly. Keys containing
  `.` are not addressable in this version.
- A reference that has no usable `SecretString` (binary-only/absent), an empty
  or whitespace-only value, a NUL byte, a malformed JSON document or a missing
  path is a source error. dcraft never substitutes an empty value.
- Only references actually needed for the operation are fetched: the phase and
  service selection is applied before any lookup. Within one operation the
  fetcher memoizes by secret id + region + profile, so two fields of the same
  secret cost one CLI call. The cache dies with the operation; `dcraft secrets`
  add/list never fetch, `up` fetches runtime references, `restart` re-fetches
  only live file-delivery values, and `update` fetches build references.
- Error categories separate a missing CLI, missing credentials/profile,
  missing region, access denied (including KMS), unknown secret, invalid field,
  network failure, an unusable value and a generic service failure. Each has a
  Korean message and concrete next action; the diagnostic detail contains only
  the CLI exit status and an AWS error code, never raw stderr, a value or an
  account identity. Invalid reference syntax remains an input error (exit 2);
  lookup failures are operation failures (exit 1).
- A fetched value reaches the existing `Session`/`BuildSession` delivery path.
  Runtime environment/file delivery and the BuildKit file mount are unchanged;
  no consumer branches on `aws`. Build delivery remains file/BuildKit-only.
- Fetched values are added to the session's redaction list. The previous-good
  record stores `source: aws`, the secret id/ARN, `field`, `region` and
  `profile` in `target.secret_refs`; rollback re-checks availability through
  `Runtime.CheckAWS` (fetch and discard) and never stores the value.

Verification used a controlled `aws` executable first on PATH to prove the
success, access-denied, unknown-secret, network, missing-CLI, missing-region,
absent/empty value, field selection, invocation-count and redaction paths,
including real Docker runtime/build delivery. No real AWS credentials or AWS
API calls were used; real-service behavior is documented from verified CLI
conventions but not claimed as exercised here.

### 14.8 Wizard runs and history

`Options["wizard"]="steps"` is a read-only preview. The engine does not write a
history record for a `RecordsHistory` feature's steps run; only the
`wizard=apply` run (and the ordinary CLI run) is an operation. Both runs still
use the same target resolution, preflight and feature `Run` path. A canceled
wizard leaves no history and no stack change.
