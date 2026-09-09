# Scheduled Compartment

> **Core reference / historical bootstrap only.** The maintained application source is
> [cloud-path-app-scheduled-compartment](https://github.com/DeliciousBuding/cloud-path-app-scheduled-compartment).
> Make application fixes and upgrades there; do not regenerate it from this example or a scaffold.
> The build, configuration and test instructions below describe only this Core reference snapshot,
> not the independently maintained application.

`cloud-path-app-scheduled-compartment` is a **device-agnostic** reference
Application plugin for CloudPath. It manages a set of compartments against a
daily schedule: when a schedule window starts it emits a reminder, treats a
key press on a bound compartment entity as the user confirming that
compartment, records a completed or missed outcome per window, and keeps
everything idempotent.

The application does **not** depend on any Driver ID, port or vendor field. It
is expressed purely in terms of standard Capability requirements and stable
`entity_id` bindings, so the same application can be deployed against any set
of entities that expose those capabilities. Business meaning ("key press =
medication taken") lives entirely in the application; Drivers only report
generic hardware facts.

Only the following domain terms are used: **schedule**, **window**,
**compartment**, **opened**, **completed**, **missed** and **reminder**. No
industry-specific semantics are hard-coded.

## Dependencies

- **Go 1.26.3 or newer** (the checkout's `go.mod` declares `go 1.26.3`).
- **Core `>=0.2.8 <0.3.0`.** The application uses the existing durable
  `schedule_job` / `cancel_job` effects for restart-safe miss checks.
- **Only the public CloudPath SDK and protocol schema.** The code imports
  these packages from `github.com/DeliciousBuding/cloud-path` and nothing else
  from the Core repository:

  | Import path | Purpose |
  |---|---|
  | `sdk/go/cloudpath/v1/application` | Application Protocol v1 types and RPC |
  | `sdk/go/cloudpath/v1/status` | Status codes |
  | `sdk/go/pluginmain` | Host-injected launch identity and handshake |
  | `sdk/go/rpc` | RPC server |
  | `sdk/go/transport` | Host-provided transport |

  There are **no imports from `internal/`** of the Core repository. Re-check
  the boundary from this directory with:

  ```bash
  grep -R "cloud-path/internal" --include="*.go" .
  # expected: no output
  ```

- The manifest requests **no hardware, network, filesystem or secret
  permissions**.

## Required Capabilities

| Requirement ID | Capability | Cardinality | Purpose |
|---|---|---|---|
| `reminder-output` | `cloudpath.dev/capability/buzzer@1` | one | Emit scheduled reminders |
| `compartments` | `cloudpath.dev/capability/key@1` | one-or-more, minimum 3 | Represent and monitor compartments |
| `local-display` | `cloudpath.dev/capability/display-text@1` | zero-or-one | Optional local status text |

The same declarations live in `requirements.yaml` (human review) and
`plugin.yaml` (machine manifest). `Describe` returns the equivalent
`ApplicationDescriptor`, so the runtime and the manifest cannot drift; the
`manifest_test.go` suite enforces that all three copies stay identical.

## Instance Configuration

The instance config is a bounded JSON object. It is validated on
`ConfigureInstance`; a missing field, a duplicate compartment, an unknown
window compartment or an invalid time is rejected with a non-OK status.

```json
{
  "timezone": "Asia/Shanghai",
  "compartments": [
    {"id": "c1", "name": "Compartment 1"},
    {"id": "c2", "name": "Compartment 2"},
    {"id": "c3", "name": "Compartment 3"}
  ],
  "schedule": [
    {"id": "w-morning", "compartment": "c1", "start": "08:00", "end": "08:30"}
  ],
  "reminder": {"freq": 1, "duration": 1}
}
```

Field rules:

- `timezone`: required, a valid IANA name (for example `Asia/Shanghai`).
- `compartments`: required, at least one, each with a unique non-empty `id`.
- `schedule`: required, at least one window. Each window has a unique non-empty
  `id`, a `compartment` that references a configured compartment, and `start` /
  `end` in 24-hour `HH:MM`. `end` must be strictly after `start`.
- `reminder`: optional buzzer policy with `freq` and `duration` step levels
  (0-9 each). Omitted fields default to the quiet minimum (`freq: 1`,
  `duration: 1`); out-of-range values are rejected.

The application owns the window state machine. Core runs `window-check` every
minute; the app derives today's concrete `start` / `end` timestamps from the
instance config and timezone and never asks Core to parse compartment schedules
or synthesize window ticks. A fresh open also arms the occurrence-qualified
durable task `window-miss-<window>@<date>`. Core dispatches durable tasks with
`JobID == ScheduleID`, so the app handles that prefix separately from the
automatic `window-check` job.

## Binding

An application instance is bound to Capabilities rather than to a device or
Driver. `ValidateBinding` enforces the declared cardinalities and rejects any
requirement id that this application does not declare (which structurally rules
out Driver coupling).

| Requirement ID | Candidate Entity |
|---|---|
| `reminder-output` | one buzzer-capable Entity |
| `compartments[0..n]` | key-capable Entities, at least 3 |
| `local-display` | an optional display-text-capable Entity |

Bindings persist stable `entity_id` values. Reconnects, Edge restarts and
Driver restarts must not change a binding. A valid binding is stored so that
the reminder is routed to the bound buzzer entity and key press events are
mapped back to their compartment.

## Behavior

The runtime is a process-based `ApplicationService` (Application Protocol v1):

1. **Window start** — when the automatic `window-check` job observes the
   configured start time, the app opens the window and emits an
   `UpsertDomainRecord` (`window`, `record_id=<window>@<date>`,
   `state=opened`), a `RequestCommand` to the bound buzzer entity (action
   `buzzer` with the configured freq/duration steps and an
   occurrence-qualified idempotency key), and a durable `ScheduleTask` named
   `window-miss-<window>@<date>`.
2. **Completion** — on a key `press` event for the compartment while its
   window is open, the app marks the occurrence `completed` and cancels its
   durable miss task. The occurrence is reconstructed from the event timestamp
   and config, so a plugin restart does not lose a confirmation that still
   falls inside the window.
3. **Missed** — the durable miss task observes the configured end time without
   a completion, records `state=missed`, cancels itself and emits a
   notification. Completion cancellation is the durable fact that prevents a
   restart after completion from producing a false miss.
4. **Idempotency** — repeated automatic jobs, repeated durable dispatches,
   duplicate key presses and cross-day occurrences have distinct
   occurrence-qualified record/command/task identities; same-occurrence repeats
   do not emit duplicate effects. A first `window-check` observation already
   past the end stays quiet instead of fabricating a miss: the app has no
   plugin-to-Core read API to distinguish a restart after completion from a
   late start. A late mid-window observation (more than one minute after start)
   records the occurrence but suppresses the stale reminder and does not arm a
   second miss task. A restart inside the first minute after start remains
   ambiguous and may re-arm the miss task. The Application Protocol has no
   plugin-to-Core state read, so this case cannot be distinguished from a fresh
   open.

Effects are limited to the Core-approved closed set: `UpsertDomainRecord`,
`DeleteDomainRecord`, `RequestCommand`, `ScheduleTask`,
`CancelScheduledTask` and `SendNotification`. The app never produces arbitrary
SQL, shell commands, file/network effects or global credential requests.

The `HandleRequest` subroute is read-only and returns a bounded JSON summary of
the instance config and window state.

## Build

From a standalone checkout of this repository:

```bash
# Application library, tests and entrypoint command
go build ./...
go vet ./...

# Build just the entrypoint binary
go build -o cloud-path-app-scheduled-compartment ./cmd/cloud-path-app-scheduled-compartment
```

The entrypoint binary is `cloud-path-app-scheduled-compartment`, matching the
`entrypoint` field in `plugin.yaml`.

Inside the CloudPath monorepo the same code lives under
`examples/scheduled-compartment`; prefix the package patterns with that
directory, for example `go build ./examples/scheduled-compartment/...`.

## Test

```bash
go test ./... -count=1
go test ./... -count=20   # idempotency / flake soak
```

The suite covers the descriptor requirements, config/binding validation, the
window reminder effect, occurrence-qualified records/commands/tasks,
key-press-driven completion with and without warm state, durable missed-window
recording, restart/no-false-miss behavior, duplicate-event idempotency,
cross-day identity, rejection of driver coupling, invalid config, graceful
shutdown, and manifest identity / requirements drift.

Inside the monorepo, the repository-level gates additionally cover the whole
tree:

```bash
go vet ./...
python scripts/fmtcheck.py
# Full binary → Host E2E (monorepo-only harness)
go test ./testing/plugin-harness -run TestScheduledCompartmentBinaryHostE2E -count=1
```

## Run as an Application Plugin

`cmd/cloud-path-app-scheduled-compartment` is an install-style, process-based
Application plugin. It is launched by the CloudPath Plugin Host, never with
manual flags: the Host injects the launch identity and a loopback endpoint
through `CLOUDPATH_*` environment variables (the contract is defined by the
public `pluginmain` package), the process prints the single `CP1` handshake
line, dials back and serves Application Protocol v1 over that authenticated
transport. Started outside a Host it exits immediately with a missing
environment error.

To attach it to a Host:

1. Build the entrypoint binary (see Build).
2. Publish `plugin.yaml` and the entrypoint binary as a release asset together
   with its published sha256 digest.
3. Install and enable the instance:

   ```bash
   cloudpath plugin install <repository-url-or-id> --digest sha256:<hex> --yes
   cloudpath plugin enable io.github.deliciousbuding.cloud-path-app-scheduled-compartment
   ```

4. Start the Host, which supervises the plugin process and serves the
   protocol:

   ```bash
   cloudpath plugin host
   ```

The runtime then delivers the instance configuration through
`ConfigureInstance` and entity bindings through `ValidateBinding` (see
Instance Configuration and Binding).

## Disable and remove

```bash
cloudpath plugin disable io.github.deliciousbuding.cloud-path-app-scheduled-compartment
cloudpath plugin remove  io.github.deliciousbuding.cloud-path-app-scheduled-compartment
```

`remove` keeps the instance data; add `--purge` to delete it as well.

## Repository Layout

| Path | Purpose |
|---|---|
| `plugin.yaml` | Machine manifest (id, version, protocol, entrypoint, requirements, contributions) |
| `requirements.yaml` | Human-readable requirement mirror |
| `config.go` | Bounded instance config schema and validation |
| `service.go` | `ApplicationService` implementation and state machine |
| `service_test.go` | Conformance/behaviour tests over the real wire |
| `manifest_test.go` | Machine identity lock + manifest/requirements/descriptor drift tests |
| `cmd/cloud-path-app-scheduled-compartment/` | Executable entrypoint |

## Status

Implemented as a runnable reference application. It is a process-based plugin
that depends only on the public CloudPath SDK and schema.