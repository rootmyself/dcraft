# dcraft architecture and feature contract

Published by Ticket 01 (issue #2). Tickets 02–19 build on this document; do not
fork these types or recreate these rules in a feature or UI package. Shared
interface changes go to the integration owner as a change request (see the end
of this file).

Scope of this version: one working binary, five registered features (`status`,
`up`, `stop`, `down`, `restart`), the common operation seam, target resolution,
named environments, automatic project-name allocation, same-stack coordination,
diagnostics, CLI/TUI assembly, state layout, machine-readable results and tests.
Logs/exec, generation, install, update, secrets, hooks, backup/restore and
rollback are later tickets.

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
        +-- internal/features/status reference read-only feature
        +-- internal/features/up     \
        +-- internal/features/stop    > thin lifecycle registration units
        +-- internal/features/down   /
        +-- internal/features/restart
        +-- internal/features/all    feature registration files (one per feature)
        +-- internal/stack           Backend/read types + Lifecycle/Identity seams
        +-- internal/compose         Preflight + Inspect + Lifecycle adapter (Docker CLI)
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
   diagnostics. Fatal failure stops here.
2. Approval check — no stage; if `Descriptor.Approval` is not supplied in
   `Request.Approvals`, the result is a failure with category
   `approval_required`, `required_approval`, and exit code 2. **The engine
   decides this; a UI may only collect and pass the approval.**
3. `preflight` — `core.Preflight.Check`; error diagnostics stop the feature.
   Warnings/info are kept.
4. Feature `Run` — feature stages appear between preflight and verification. If
   a feature reports no stage, the engine adds a fallback `execute` stage.
5. `verify` — only if the feature also implements `core.Verifier`. Raw errors
   become `verification_failed`; no history is written.
6. `history` — only if `Descriptor.RecordsHistory`. Written after success. A
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

Preflight is always the full local check in this version: Docker CLI presence,
endpoint locality, Compose presence/version, engine reachability/permission,
and Compose capability probes. A feature that genuinely must work without
Docker is a contract change, not a feature-local bypass.

Approval kinds (`core.ApprovalKind`), independent and non-substitutable:

| value | risk |
|---|---|
| `install` | installing a Compose distribution |
| `script_trust` | first execution or changed host script |
| `previous_version` | return to the previous good deployment |
| `data_restore` | replace data from a backup |
| `file_overwrite` | replace existing generated files |

CLI supplies them with repeated `--approve <kind>`; the TUI will collect them in
the ticket that owns that approval flow. Read-only features may not require an
approval (registry validation).

Optional feature capabilities:

```go
type core.Verifier interface {
    Verify(ctx context.Context, in core.Invocation, payload any) error
}
type core.Presenter interface {
    Present(res core.Result, width int) string
}
```

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
  name and state key. The read-only status feature keeps letting Compose resolve
  the folder default and writes no state.
- `Target.ComposeArgs()` is the single source of Compose global inputs:
  `compose -f <file>... --env-file <file>... --profile <profile>... [-p <name>]`.
  It deliberately does **not** pass `--project-directory`: Compose keeps its own
  rule that the project directory is the first file's directory, so relative
  paths and the default `.env` behave exactly as a direct Compose invocation.
- Existing Compose files and Dockerfiles are never modified. Status and target
  resolution are read-only.

State identity is `ProjectDir + Environment`, not the Compose project name (the
name itself is stored state). This keeps the same folder/environment tied to
one helper-managed stack. The environment id is the alias from `.dcraft.yaml`
exactly as written.

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
| `AcceptsArgs` | allows positional args; flags then stop at the first arg |
| `Flags []core.Flag` | feature-owned flags parsed by the shared CLI |

Registry validation rejects duplicate names, invalid names, missing Korean text,
read-only+approval combinations, a second primary, reserved flag names
(`path`, `file`, `env-file`, `profile`, `project-name`, `env`, `json`,
`approve`, `help`, `service`), reserved shorthands (`f`, `p`, `s`, `h`, `v`),
duplicate flags and invalid bool defaults.

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
   second write path.

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
- `status` stays read-only and writes no state; it still lets Compose resolve
  the folder default. For a suffixed folder (`hello-world-1`) status shows the
  default project until integration wires the published name lookup into
  status. No lifecycle operation relies on status for identity.

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
- selecting a mutating feature shows the small confirmation screen from §5
  (requested target and environment, whole-stack scope, effect); `esc` returns
  to the menu;
- `r` re-runs read-only features directly and asks for confirmation again for
  mutating features. The selected environment is reused; `q` and `ctrl+c` quit
  outside an operation;
- normal quit is not an operation and does not touch services. During an
  in-flight mutating operation `q` explains the risk; `ctrl+c` requests safe
  cancellation and keeps the screen for the canceled result (see §5).

No feature action is selectable unless it is registered. This ticket registers
`status`, `up`, `stop`, `down` and `restart`.

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

Secret boundary: status never requests resolved environment, labels or commands.
It parses only names, images, build presence, healthcheck presence, ports and
runtime state. Compose/ps JSON is decoded into narrow structs; extra fields are
dropped immediately. Diagnostic `detail` is truncated tool output; no dcraft
feature may add fetched secret values to it.

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

- The key is derived from the absolute project folder **and** the environment
  id: same folder names in different places and different environments never
  share state; the readable folder name stays in the key.
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
| `internal_error` | unexpected error/parse failure/panic | error |

Each diagnostic carries Korean `message`, optional raw `detail`, and Korean
`next_actions`. Machine consumers switch on `category`, never on message text.
Warnings do not block success and are rendered on both interfaces.

`Result.ExitCode` returns 2 for `approval_required`, `project_name_conflict` and
`service_unknown`: those mean the caller must supply a different decision (an
approval, a free name, an existing service). dcraft never prompts to resolve
them; non-interactive runs fail with the explanation instead. `approval_required`
keeps its existing behavior; the two new categories are additive v1 values.

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
  cancellation, panic recovery, warnings);
- registry validation and CLI/TUI assembly from registries;
- state layout and permissions, automatic-name allocation (first/suffix/reuse/
  declared/conflict/external ownership/environment/concurrency) and the per-stack
  lock (busy holder, different stacks, goroutine exclusivity, cancellation);
- lifecycle command construction (global flags before the subcommand, `up
  --detach` + services, `stop` + services, project-wide `down` without `-v`,
  `restart --no-deps` for explicit services), config/project parsing and
  `lifecycle_failed`/`stack_busy`/`project_name_conflict`/`service_unknown`
  diagnostics with the fake runner;
- process e2e with a fake `docker` shim: exit codes, JSON purity, non-interactive
  no-args, help, parent isolation, SIGINT→130, missing `--env` exit 2 with the
  alias list, `--env` in `status`, `target.environment`, alias inputs in the
  Compose argument order, no prompt when non-interactive, repeated up/name
  reuse, same-name folders, explicit and configured name conflicts, unknown
  service, no secret values in output/history, TUI menu + confirmation +
  quit-without-run;
- pty process tests: TUI enter+quit, environment selection screen and keyboard
  choice, quit-from-selection runs nothing, no mutating Docker command,
  mutating confirmation before running, up through the same engine;
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
  from `-p`/`name:`/`.env`/exported `COMPOSE_PROJECT_NAME`, another stack's
  container IDs/data unchanged, TUI quit leaving services running, cleanup with
  its own projects only.
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
go test ./...
DCRAFT_INTEGRATION=1 go test ./test/e2e/... -run TestIntegration -v
```

Real-Docker tests skip without `DCRAFT_INTEGRATION=1`. Unit tests never require
Docker. Unrun platforms/paths are not claimed as verified.

---

## 12. Not implemented here (owned by later tickets)

- `update`, `logs`, `exec`, `backup`, `restore`, `rollback`; no registration
  exists for them and no menu action is shown.
- TUI alias creation/editing: aliases are created by editing `.dcraft.yaml` in
  this version (selection is provided by both interfaces).
- Secrets, host scripts/hooks, generated Compose/Dockerfile files, Compose
  installation, previous-good records, Kubernetes, remote engines, resident
  daemon/web UI.
- Good-version history (ticket #7/#11), TUI flag/approval collection for future
  features, service selection in the TUI (CLI `-s` only).

Residual risks (tickets #3 and #4): Compose capability detection is based on
running the installed CLI (version/JSON probes), so a heavily patched Compose may
pass probes and fail later; diagnostic details are tool output and could contain
user-provided file content, though dcraft never fetches or prints secret values;
status parses a subset of Compose/ps output, so unknown future fields are ignored
by design; the TUI renders long status text without a scroll viewport.
Environment alias files are hand-edited (no TUI editor); the host-resource
conflict scan is static and best-effort (no interpolation/merge evaluation), so
undetected sharing is possible and complete isolation is never claimed.
Environment aliases and lifecycle names are separate: aliases partition state,
files and profiles; the lifecycle controller allocates or honors the Compose
project name per folder+environment.
Lifecycle residual risks: status does not read the allocated name (see §5), so a
suffixed folder's status shows the folder-default project until integration
wires the lookup; a declared name equal to Compose's derived default is treated
as automatic; a declared rename of a recorded target leaves the old project
unmanaged by design; per-stack coordination fails fast instead of queuing, so an
automation caller must retry; `flock` coordination is verified on macOS
(Linux/WSL2 use the same syscall but were not run on this machine); a declared
project name shared by several environment aliases produces the ticket-04
`project_name_shared` warning but still stops at apply time with
`project_name_conflict`, because two environments must not operate on one
Compose project.

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
`NameConflictError`, the `names.json`/`.lock` state layout, the lifecycle stage
names and the new diagnostic categories/exit-code mapping.

Allowed without a request: new feature package, new registration file, feature
payload type under `data`, feature-local Korean strings and tests. A lifecycle-like
feature consumes `deps.Deps.Lifecycle` (or requests a new narrow service); it does
not call Compose directly or copy the controller. Not allowed: editing another
feature, adding a feature branch in CLI/TUI, bypassing the engine for Docker
access, printing secrets, or silently changing an existing machine value/exit
code.
