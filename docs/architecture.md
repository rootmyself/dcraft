# dcraft architecture and feature contract

Published by Ticket 01 (issue #2). Tickets 02–19 build on this document; do not
fork these types or recreate these rules in a feature or UI package. Shared
interface changes go to the integration owner as a change request (see the end
of this file).

Scope of this version: one working binary, one registered feature (`status`),
the common operation seam, target resolution, diagnostics, CLI/TUI assembly,
state layout, machine-readable results and tests. Lifecycle, logs/exec,
environments, generation, install, update, secrets, hooks, backup/restore and
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
        +-- internal/target          Compose target discovery (filesystem only)
        +-- internal/features/status reference feature
        +-- internal/features/all    feature registration files (one per feature)
        +-- internal/stack           Backend interface + Snapshot types
        +-- internal/compose         Preflight + Inspect adapter (Docker CLI)
        +-- internal/state           filesystem StateStore
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
- `internal/stack.Backend` is the only stack-reading boundary the status feature
  consumes. A future backend can implement it without changing CLI/TUI code; do
  not build that backend now.

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
| `Target core.TargetRequest` | path, explicit files, env files, profiles, project name, environment |
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
    ProjectName    string   // explicit -p only; "" = Compose default
    Environment    string   // always "" until ticket #4
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
- `ProjectName` is only the explicit `-p` value. Empty means Compose resolves the
  name from its project directory. Ticket #3 allocates reusable automatic names
  and sets `ProjectName` before the operation; ticket #4 fills `Environment`.
- `Target.ComposeArgs()` is the single source of Compose global inputs:
  `compose -f <file>... --env-file <file>... --profile <profile>... [-p <name>]`.
  It deliberately does **not** pass `--project-directory`: Compose keeps its own
  rule that the project directory is the first file's directory, so relative
  paths and the default `.env` behave exactly as a direct Compose invocation.
- Existing Compose files and Dockerfiles are never modified. Status and target
  resolution are read-only.

State identity is `ProjectDir + Environment`, not the Compose project name (the
name itself will be stored state). This keeps the same folder/environment tied
to one helper-managed stack.

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
(`path`, `file`, `env-file`, `profile`, `project-name`, `json`, `approve`,
`help`, `service`), reserved shorthands (`f`, `p`, `s`, `h`, `v`), duplicate
flags and invalid bool defaults.

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

3. Run `go test ./...`. No existing file is edited.

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

## 5. CLI and TUI assembly

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
- `r` re-runs the active feature; `q` and `ctrl+c` quit;
- normal quit is not an operation and does not touch services. Only read-only
  features exist in this ticket; later mutating features keep the same quit
  contract and must report in-flight stage/partial effects.

No feature action is selectable unless it is registered. This ticket registers
only `status`.

---

## 6. Result, progress and cancellation contract

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

## 7. Per-computer state

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
<state>/stacks/<folder>-<hash12>/
    history.jsonl     # append-only operation records, 0600
    state.json        # reserved for #3 name allocation / #7 last-good state
```

- The key is derived from the absolute project folder **and** the environment
  id: same folder names in different places and different environments never
  share state; the readable folder name stays in the key.
- Directories are created lazily with `0700`; files with `0600`.
- History records contain operation id, action, outcome, timestamps, stages and
  diagnostics. Never resolved secret values.
- Status reads and writes no state.
- History is written only for successful `RecordsHistory` features in this
  ticket; failed verifications are never recorded as good. Ticket #7 extends
  good-version records; ticket #3 owns name allocation and cross-process
  coordination. In-process appends are serialized; cross-process locking is not
  implemented here and must be published by #3.

---

## 8. External boundaries

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
  `core.Preflight` and `stack.Backend` (`Name`, `Inspect`).
- `internal/stack.Backend` keeps the read-only stack boundary separate from the
  UI. No Kubernetes backend or multi-backend registry exists now.
- Compose semantics (validation, merge, path resolution, profiles, ps JSON) are
  delegated to the Compose CLI; dcraft does not reimplement YAML.

---

## 9. Diagnostics catalog

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
| `verification_failed` | post-operation verification failed | error |
| `canceled` | operation interrupted | warning (exit 130 outcome) |
| `history_failed` | operation history could not be written | warning |
| `internal_error` | unexpected error/parse failure/panic | error |

Each diagnostic carries Korean `message`, optional raw `detail`, and Korean
`next_actions`. Machine consumers switch on `category`, never on message text.
Warnings do not block success and are rendered on both interfaces.

---

## 10. Verification

Tested seams:

- target resolution (temp folders, override order, explicit order, parent
  isolation, env files, profiles, project name);
- preflight classification with `execxtest.Runner` (all diagnostic categories,
  local Desktop socket vs remote refusal, no engine contact for remote);
- Compose inspection parsing (health, instances, connections, unconfigured
  services, no secret leakage, stable Compose arguments);
- engine seam with fake features (approval, stages, verification, history,
  cancellation, panic recovery, warnings);
- registry validation and CLI/TUI assembly from registries;
- state layout and permissions;
- process e2e with a fake `docker` shim: exit codes, JSON purity, non-interactive
  no-args, help, parent isolation, SIGINT→130;
- pty process tests: TUI enter+quit, no mutating Docker command;
- real Docker integration behind `DCRAFT_INTEGRATION=1`: isolated
  `dcraft-test-*` project, real healthchecks, connection info, compose file
  unchanged, containers unchanged after TUI quit, nested-folder block, cleanup
  with its own project only. No host-wide prune or other project changes.

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

## 11. Not implemented here (owned by later tickets)

- `up`, `update`, `stop`, `down`, `restart`, `logs`, `exec`, `backup`, `restore`,
  `rollback`; no registration exists for them and no menu action is shown.
- Named environments/aliases (`Target.Environment` is reserved; no flag yet;
  ticket #4 fills it and owns the required selection rule).
- Secrets, host scripts/hooks, generated Compose/Dockerfile files, Compose
  installation, previous-good records, Kubernetes, remote engines, resident
  daemon/web UI.
- Cross-process state locking/name allocation (ticket #3), good-version history
  (ticket #7/#11), TUI flag/approval collection for future features.

Residual risks in this ticket: Compose capability detection is based on running
the installed CLI (version/JSON probes), so a heavily patched Compose may pass
probes and fail later; diagnostic details are tool output and could contain
user-provided file content, though dcraft never fetches or prints secret values;
status parses a subset of Compose/ps output, so unknown future fields are ignored
by design; the TUI renders long status text without a scroll viewport.

---

## 12. Change request checklist

Before changing a shared type/interface, send the integration owner:

1. the exact new field/type/service and why a feature cannot express it locally;
2. affected tickets and whether existing behavior/JSON stays compatible;
3. tests proving CLI and TUI still share the seam and no safety rule moved into
   a UI;
4. documentation updates to this file.

Allowed without a request: new feature package, new registration file, feature
payload type under `data`, feature-local Korean strings and tests. Not allowed:
editing another feature, adding a feature branch in CLI/TUI, bypassing the engine
for Docker access, printing secrets, or silently changing an existing machine
value/exit code.
