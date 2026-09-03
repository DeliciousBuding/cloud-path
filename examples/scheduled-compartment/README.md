# Scheduled Compartment

Scheduled Compartment is a CloudPath reference application. It manages several
compartments according to a schedule and records open/close and completion
events.

The application is device-agnostic: it declares Capability requirements and does
not depend on any Driver ID. Different deployments can map the same application
to different physical or virtual entities.

## Required Capabilities

| Requirement ID | Capability | Cardinality | Purpose |
|---|---|---|---|
| `reminder-output` | `cloudpath.dev/capability/alarm@1` | one | Emit scheduled reminders |
| `compartments` | `cloudpath.dev/capability/contact@1` | one-or-more, minimum 3 | Represent and monitor compartments |
| `local-display` | `cloudpath.dev/capability/display-text@1` | zero-or-one | Show local status text |

The same declarations are in `requirements.yaml` for human review; `plugin.yaml`
is the machine-readable manifest.

## Binding

An application instance is bound to Capabilities rather than to a device or
Driver. During installation, CloudPath presents a binding wizard:

| Requirement ID | Candidate Entity |
|---|---|
| `reminder-output` | an alarm-capable Entity |
| `compartments[0..n]` | contact-capable Entities, at least 3 |
| `local-display` | an optional display-text-capable Entity |

Bindings persist stable `entity_id` values. Reconnects, Edge restarts, and
Driver restarts must not change a binding.

## Status

This is a skeleton for the future
`cloud-path-app-scheduled-compartment` repository. The manifest and requirements
are in place; the executable entrypoint is a placeholder and will be implemented
in A6.
