# Scheduled Compartment

`cloud-path-app-scheduled-compartment` is a **device-agnostic** reference
Application plugin for CloudPath. It manages several compartments according to a
schedule: when a schedule window starts it emits a reminder, observes contact
opened/closed events for each compartment, records a completed or missed outcome
per window, and keeps everything idempotent.

The application does **not** depend on any Driver ID, port or vendor field. It is
expressed purely in terms of standard Capability requirements and stable
`entity_id` bindings, so the same application can be deployed against any set of
entities that expose those capabilities.

Only the following domain terms are used: **schedule**, **window**,
**compartment**, **opened**, **completed**, **missed** and **reminder**. No
industry-specific semantics are hard-coded.

## Required Capabilities

| Requirement ID | Capability | Cardinality | Purpose |
|---|---|---|---|
| `reminder-output` | `cloudpath.dev/capability/alarm@1` | one | Emit scheduled reminders |
| `compartments` | `cloudpath.dev/capability/contact@1` | one-or-more, minimum 3 | Represent and monitor compartments |
| `local-display` | `cloudpath.dev/capability/display-text@1` | zero-or-one | Optional local status text |

The same declarations live in `requirements.yaml` (human review) and
`plugin.yaml` (machine manifest). `Describe` returns the equivalent
`ApplicationDescriptor`, so the runtime and the manifest cannot drift.

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
  ]
}
```

Field rules:

- `timezone`: required, a valid IANA name (for example `Asia/Shanghai`).
- `compartments`: required, at least one, each with a unique non-empty `id`.
- `schedule`: required, at least one window. Each window has a unique non-empty
  `id`, a `compartment` that references a configured compartment, and `start` /
  `end` in 24-hour `HH:MM`. `end` must be strictly after `start`.

`ScheduleTick` events carry the concrete runtime window (with RFC3339 `start` /
`end` timestamps). The app validates that the referenced compartment exists in
the instance config before starting the window.

## Binding

An application instance is bound to Capabilities rather than to a device or
Driver. `ValidateBinding` enforces the declared cardinalities and rejects any
requirement id that this application does not declare (which structurally rules
out Driver coupling).

| Requirement ID | Candidate Entity |
|---|---|
| `reminder-output` | one alarm-capable Entity |
| `compartments[0..n]` | contact-capable Entities, at least 3 |
| `local-display` | an optional display-text-capable Entity |

Bindings persist stable `entity_id` values. Reconnects, Edge restarts and
Driver restarts must not change a binding. A valid binding is stored so that
the reminder is routed to the bound alarm entity and contact events are mapped
back to their compartment.

## Behavior

The runtime is a process-based `ApplicationService` (Application Protocol v1):

1. **Window start** — on a `ScheduleTick`, the app opens a window, emits a
   `UpsertDomainRecord` (`window`, `state=opened`), a `RequestCommand` to the
   bound alarm entity, and a `ScheduleTask` so Core can later trigger the
   `window-check` job.
2. **Completion** — on a contact `opened`/`closed` event for the compartment
   while its window is open, the app marks the window `completed` and cancels
   the `window-check` task.
3. **Missed** — the `window-check` job scans open windows against the clock and
   records a `window` record with `state=missed`, cancels the task and emits a
   notification.
4. **Idempotency** — duplicate events (same sequence or same window id) and
   repeated jobs (same `IdempotencyKey`) do not emit duplicate effects.

Effects are limited to the Core-approved closed set: `UpsertDomainRecord`,
`DeleteDomainRecord`, `RequestCommand`, `ScheduleTask`,
`CancelScheduledTask` and `SendNotification`. The app never produces arbitrary
SQL, shell commands, file/network effects or global credential requests.

The `HandleRequest` subroute is read-only and returns a bounded JSON summary of
the instance config and window state.

## Build

These are the commands as they would be run from the root of the standalone
repository. Inside the CloudPath monorepo, the package lives at
`examples/scheduled-compartment` and the paths are prefixed accordingly.

```bash
# Application library + tests + entrypoint command
go build ./...
go vet ./...

# Build just the entrypoint binary
go build -o ./cloud-path-app-scheduled-compartment ./cmd/cloud-path-app-scheduled-compartment
```

The entrypoint binary is `cloud-path-app-scheduled-compartment`, matching the
`entrypoint` field in `plugin.yaml`.

## Test

```bash
go test ./... -count=1
go test ./... -count=20      # flake / idempotency soak
python scripts/fmtcheck.py   # gofmt gate
```

In the monorepo, run the same with the `./examples/scheduled-compartment/...`
path and `go test ./... -count=1` from the repository root.

The suite covers the descriptor requirements, config/binding validation, the
window reminder effect, contact-driven completion, missed-window recording,
duplicate-event idempotency, rejection of driver coupling, invalid config and
graceful shutdown.

## Run

The entrypoint is an install-style Application plugin. The A4 Plugin Host
injects the launch identity and loopback endpoint through the environment, and
the shared `sdk/go/pluginmain` helper emits the single handshake line, dials
the host and serves the Application Protocol v1 over that authenticated
transport:

```bash
go run ./cmd/cloud-path-app-scheduled-compartment
```

Run outside a host it fails fast on the missing `CLOUDPATH_*` environment; it
is meant to be launched by the Plugin Host or the process-host E2E tests
(`go test ./testing/plugin-harness -run TestScheduledCompartmentBinaryHostE2E`).

## Repository Layout

| Path | Purpose |
|---|---|
| `plugin.yaml` | Machine manifest (id, version, protocol, entrypoint, requirements) |
| `requirements.yaml` | Human-readable requirement mirror |
| `config.go` | Bounded instance config schema and validation |
| `service.go` | `ApplicationService` implementation and state machine |
| `service_test.go` | Conformance/behaviour tests over the real wire |
| `cmd/cloud-path-app-scheduled-compartment/` | Executable entrypoint |

## Status

Implemented as a runnable reference application. The application is a
process-based plugin; it does not depend on any `internal/*` package and it is
safe to incubate here before splitting out to
`cloud-path-app-scheduled-compartment`.


