# dcraft architecture and feature contract

Published by Ticket 01 (issue #2). Tickets 02–19 build on this document; do not
fork these types or recreate these rules in a feature or UI package. Shared
interface changes go to the integration owner as a change request (see the end
of this file).

Scope of this version: one working binary, six registered features (`status`,
`install-compose`, `up`, `stop`, `down`, `restart`), the common operation seam,
target resolution, named environments, automatic project-name allocation,
same-stack coordination, diagnostics, CLI/TUI assembly, state layout,
machine-readable results, the approval plan/confirm flow, and tests. Logs/exec,
generation, update, secrets, hooks, backup/restore and rollback are later
tickets.

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
        +-- internal/identity        read-only name/identity readout
        +-- internal/features/status reference read-only feature
        +-- internal/features/up     \
        +-- internal/features/stop    > thin lifecycle registration units
        +-- internal/features/down   /
        +-- internal/features/restart
        +-- internal/features/all    feature registration files (one per feature)
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
- `internal/lifecycle` knows `core`, `stack` and `state`; it never imports
  Cobra, Bubble Tea or `internal/compose` (the Compose adapter is passed as the
  published interfaces).
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
4. Read-only identity read — for `ReadOnly` features with no explicit `-p` and
   a configured `Engine.Identity`, the engine resolves the effective project
   name before `Invocation` (see §3.2). It adds no stage, never writes or locks,
   and on failure emits an `identity_unreadable` **warning** and falls back to
   Compose's default. Stack-changing features resolve and record their own
   allocation (they may write).
5. Feature `Run` — feature stages appear between preflight and verification. If
   a feature reports no stage, the engine adds a fallback `execute` stage.
6. `verify` — only if the feature also implements `core.Verifier`. Raw errors
   become `verification_failed`; no history is written.
7. `history` — only if `Descriptor.RecordsHistory`. Written after success. A
   history write failure is a `history_failed` **warning**; the operation stays
   successful because the stack change already happened. Never report a
   completed change as failed solely because history failed, and never record a
   failed verification as a good version.

Failure and interruption:

- Features return `*core.Error` (built with `core.Fail` / `core.FailCause`).
  Diagnostics carry a stable category, Korean message and Korean next actions.
- A raw Go error becomes `internal_error`.
- A panic inside a feature is recovered and becomes `internal_error`; the
  process must not crash the TUI.
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
engine reachability and permission. A remote endpoint is still refused whenever
`docker_engine` or `compose` is required. A feature that genuinely needs no
Docker capability is a future contract extension (an explicit "none" value);
an empty slice never means "check nothing". Descriptor requirements are
validated at registration.

Approval kinds (`core.ApprovalKind`), independent and non-substitutable:

| value | risk |
|---|---|
| `install` | installing a Compose distribution |
| `script_trust` | first execution or changed host script |
| `previous_version` | return to the previous good deployment |
| `data_restore` | replace data from a backup |
| `file_overwrite` | replace existing generated files |

CLI supplies them with repeated `--approve <kind>`; the TUI collects them in a
generic plan → confirm screen (see §6). Read-only features may not require an
approval (registry validation).

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
```

`Engine.Plan(ctx, req, sink) core.PlanReport` resolves the same target and
preflight requirements as `Run`, then calls the feature's `Plan`. It never
checks an approval, never calls `Run`, and never writes history. `Plan` must not
change anything. `PlanReport` carries feature, outcome, target, tools, stages,
diagnostics and the Korean plan text. Both paths share the same target and
preflight code, so a preview cannot silently use a different selection. Plan
text must state the concrete source, version, destination, permission need and
effect, and never contain secret values. `PlanReport.Result()` converts a failed
plan into the common result shape for both interfaces to render.

`core.Invocation` gives a feature: resolved `Target`, `Services`, `Args`,
`Options`, `Interactive`, `Preflight` report, `Reporter`, `Clock`, `State`.
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
Compose files, is never passed to Compose and is never modified by dcraft.
Unknown top-level keys are ignored so later tickets can add their own sections;
fields inside an environment are strict.

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
| `Flags []core.Flag` | feature-owned flags parsed by the shared CLI |

Registry validation rejects duplicate names, invalid names, missing Korean text,
read-only+approval combinations, a second primary, unknown preflight
requirements, reserved flag names (`path`, `file`, `env-file`, `profile`,
`project-name`, `env`, `json`, `approve`, `help`, `service`), reserved
shorthands (`f`, `p`, `s`, `h`, `v`), duplicate flags and invalid bool defaults.

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
`Options`, `Args`, `Services`. For `AcceptsArgs` features, options come before
positional args (the rest is passed through unchanged, e.g. `exec`). The TUI
currently invokes features with no options/args (defaults); a ticket that needs
interactive flag collection adds that UI, not a new engine path.

Contract changes (new shared service, new flag kind, new approval kind, new
result field) must be requested from the integration owner with the reason and
affected tickets. Do not copy the interface into a feature.

---

## 5. Stack lifecycle: up, stop, down, restart (ticket 03)

Four flat, stack-changing commands run through `core.Engine.Run`; CLI and TUI
share `lifecycle.Controller`. Nothing here builds, pulls, checks health, runs
hooks, backs up data or rolls back.

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
  (#16–#19) and rollback (#12). `state.AllocateName` serializes claims with a
  short bounded file lock and an atomic file replace.
- No host-wide prune; another project's containers, networks, volumes and
  images are never removed.

### TUI

- The menu lists every registered feature in Korean. Selecting a mutating
  feature first shows a confirmation screen with the requested target
  (path/files/profiles/project name), the whole-stack scope and the effect.
  `enter` runs the same engine path, `esc` cancels, `q` quits without running.
  The TUI provides whole-stack operations only; service selection is the CLI
  `-s/--service`.
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

The CLI never prompts. An approval-gated command without `--approve <kind>`
returns the engine's approval failure (exit 2) with Korean text describing the
effect; non-interactive and interactive terminals behave identically. When the
approval is supplied, human mode calls `Engine.Plan` and prints the plan
(source, version, destination, permission, integrity) to stderr before
`Engine.Run`, so nothing is downloaded before the user sees it. JSON mode does
not make a separate plan call; the result carries the same public metadata.

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
- a feature whose descriptor requires an approval and which implements
  `core.Planner` starts the read-only plan screen instead; that plan is the
  confirmation for the independent approval (flow below);
- `r` re-runs read-only features directly, re-plans approval features and asks
  for confirmation again for other mutating features. The selected environment
  is reused; `q` and `ctrl+c` quit outside an operation;
- normal quit is not an operation and does not touch services. During an
  in-flight mutating operation `q` explains the risk; `ctrl+c` requests safe
  cancellation and keeps the screen for the canceled result (see §5).

Approval flow (generic, engine-enforced):

1. Selecting a feature whose descriptor requires an approval and which
   implements `core.Planner` starts `Engine.Plan`, not `Engine.Run`.
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
registers `status` (primary), `install-compose`, `up`, `stop`, `down` and
`restart`.

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
- `data` is feature-specific and omitted on failure.
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
    state.json        # reserved for #7 last-good state
```

- The key is derived from the canonical absolute project folder **and** the
  environment id: same folder names in different places and different
  environments never share state; the readable folder name stays in the key.
  Symlinked spellings of an existing folder canonicalize to one identity.
- Directories are created lazily with `0700`; files with `0600`.
- History records contain operation id, action, outcome, timestamps, stages and
  diagnostics. Never resolved secret values.
- Status reads and writes no state.
- History is written only for successful `RecordsHistory` features; failed
  verifications are never recorded as good. Ticket #7 extends good-version
  records.
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
```

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
| `stack_busy` | another operation holds the same stack lock | error |
| `lifecycle_failed` | a Compose lifecycle call failed | error |
| `state_failed` | name/lock state could not be read or written | error |
| `verification_failed` | post-operation verification failed | error |
| `canceled` | operation interrupted | warning (exit 130 outcome) |
| `history_failed` | operation history could not be written | warning |
| `identity_unreadable` | stored/declared project identity could not be read; Compose default used | warning |
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
`environment_unknown`, `environment_input_conflict`, `project_name_conflict` and
`service_unknown`: those mean the caller must supply a different decision (an
approval, an environment, a free name, an existing service). dcraft never
prompts to resolve them; non-interactive runs fail with the explanation instead.
New categories are additive v1 values.

---

## 11. Verification

Tested seams:

- target resolution (temp folders, override order, explicit order, parent
  isolation, env files, profiles, project name);
- environment config parsing (missing file, empty file, alias order, relative
  paths, invalid/duplicate/missing fields), mandatory selection, no implicit
  choice, no-alias defaults, expansion order, explicit-input conflicts,
  per-environment state keys, explicit project-name sharing and static
  port/bind/external-resource overlap warnings;
- preflight classification with `execxtest.Runner` (all diagnostic categories,
  local Desktop socket vs remote refusal, no engine contact for remote);
- Compose inspection parsing (health, instances, connections, unconfigured
  services, no secret leakage, stable Compose arguments);
- engine seam with fake features (approval, stages, verification, history,
  cancellation, panic recovery, warnings, planned requirements, target
  optionality, plan preview without approval/run);
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
  Docker (not Compose);
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
- real Docker environments: two aliases with different files/profiles/env files
  run as separate stacks with separate declared project names and host ports,
  env-file values reach the containers, status reports each environment and
  never mixes their services, fixed-port/bind overlap is reported from static
  declarations before applying, and an unrelated canary stack is unchanged.

Commands:

```sh
gofmt -l .
go vet ./...
go build ./...
go test -count=1 ./...
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegration -v
DCRAFT_COMPOSE_REAL_INSTALL=1 go test ./test/e2e/... -run TestRealComposeInstall -v
```

Real-Docker tests skip without their guard variable. Unit tests never require
Docker or external network. Unrun platforms/paths are not claimed as verified.

---

## 12. Not implemented here (owned by later tickets)

- `update`, `logs`, `exec`, `backup`, `restore`, `rollback`; no registration
  exists for them and no menu action is shown.
- TUI alias creation/editing: aliases are created by editing `.dcraft.yaml` in
  this version (selection is provided by both interfaces).
- Secrets, host scripts/hooks, generated Compose/Dockerfile files and generator
  flows, previous-good records, Kubernetes, remote engines, resident daemon/web
  UI.
- Good-version history (ticket #7/#11), TUI flag collection for future features,
  service selection in the TUI (CLI `-s` only). The TUI plan/confirm component
  is generic but only `install` has a planner in this build.
- Docker itself (installation, repair, replacement), administrator escalation,
  Docker Desktop/WSL integration changes.

Residual risks: Compose capability detection is based on running the installed
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

---

## 13. Change request checklist

Before changing a shared type/interface, send the integration owner:

1. the exact new field/type/service and why a feature cannot express it locally;
2. affected tickets and whether existing behavior/JSON stays compatible;
3. tests proving CLI and TUI still share the seam and no safety rule moved into
   a UI;
4. documentation updates to this file.

Published by ticket #3 (consume, do not fork): `stack.Lifecycle`,
`stack.Identity`, `stack.ConfigInfo`, `stack.ProjectInfo`, `stack.OpResult`,
`deps.Deps.Lifecycle`, `lifecycle.Controller`/`Action`/`Payload`/`Report`/
`Present`, `state.AcquireStackLock`/`StackLock`/`LockHolder`/`BusyError`,
`state.AllocateName`/`NameRequest`/`NameAllocation`/`NameSource`/
`NameConflictError`/`LookupName`, `core.IdentityReader`/`Engine.Identity`,
`internal/identity.New`/`Reader.ReadProjectName`, the `names.json`/`.lock` state
layout, the lifecycle stage names and the new diagnostic categories/exit-code
mapping.

Allowed without a request: new feature package, new registration file, feature
payload type under `data`, feature-local Korean strings and tests. A lifecycle-like
feature consumes `deps.Deps.Lifecycle` (or requests a new narrow service); it does
not call Compose directly or copy the controller. Not allowed: editing another
feature, adding a feature branch in CLI/TUI, bypassing the engine for Docker
access, printing secrets, or silently changing an existing machine value/exit
code.
