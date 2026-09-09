<div align="center">

# CloudPath

**A plugin-driven IoT control plane** for connecting devices through edge agents,
with real-time visibility, remote control, tenant isolation, and an embedded WebUI.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![CI](https://github.com/DeliciousBuding/cloud-path/actions/workflows/ci.yml/badge.svg)](https://github.com/DeliciousBuding/cloud-path/actions)

</div>

---

## What it is

CloudPath separates the control plane from device-specific code:

- **Control plane**: `cloudpath-server` owns desired state, tenants, RBAC, audit, commands, and
  the embedded WebUI.
- **Edge plane**: `cloudpath-edge` owns observed state, device supervision, offline buffering,
  and the local Driver Plugin Host.
- **Device semantics live in plugins**: Core (`internal/*`) does not know a specific board or
  business domain. A new device is a Driver plugin, not a Core change.
- **Realtime path**: with account login, Edge state and events flow to Server and then to the browser
  over WebSocket. REST serves history and management operations; tenant-token sessions are REST-only.
- **Single binary**: the React WebUI is embedded into the Go server; SQLite uses the pure-Go
  `modernc.org/sqlite` driver.

```text
Experience Plane
  WebUI: overview / devices / gateways / run log / apps & plugins / settings / administration
        |
        | REST + WebSocket (/ws)
        v
Control Plane: cloudpath-server
  desired state / tenant + RBAC / audit / retention / plugin catalog / command settlement
        |
        | WebSocket (/ws/edge): state + event up, command down
        v
Edge Plane: cloudpath-edge
  observed state / device supervision / offline event buffer / Driver Plugin Host
        |
        | serial or local bus
        v
devices (reference Driver: stcb; or external Driver plugins)
```

## Plugin types

| Type | Default host | Responsibility | Not responsible for |
|---|---|---|---|
| **Driver** | Edge | discovery, connection, protocol parsing, capability mapping, device actions | business flow or tenant UI |
| **Application** | Server | business objects, bindings, rules, jobs, domain APIs | direct serial access or Core database access |
| **Connector** | Edge or Server (runtime target-state) | MQTT, webhook, external platforms, notifications, data egress | defining the core device model |

Driver Protocol v1 and Application Protocol v1 are implemented. Connector has a manifest
declaration but no runtime. UI contributions are not an executable plugin type; the
current WebUI renders Descriptor/Capability schemas for device views, capability actions, and
command forms. Arbitrary page schemas and third-party JavaScript remain target-state work.

Current plugin repositories:

- [cloud-path-driver-stcb](https://github.com/DeliciousBuding/cloud-path-driver-stcb)
- [cloud-path-app-scheduled-compartment](https://github.com/DeliciousBuding/cloud-path-app-scheduled-compartment)
- [cloud-path-app-button-indicator](https://github.com/DeliciousBuding/cloud-path-app-button-indicator)
- [cloud-path-app-environment-guard](https://github.com/DeliciousBuding/cloud-path-app-environment-guard)

Application repositories are the source of truth for application code and releases. Core
examples and split/scaffold tooling are reference or historical bootstrap material; do not use
them to overwrite an independently maintained application.

**Identity-chain boundary:** command and event routing currently assumes globally unique
`entity_id`. `(device_key, entity_id)` is not yet threaded through binding and routing, so reuse
of an `entity_id` across multiple boards of the same model can make entity binding or event
routing ambiguous. Until that interface and multi-board hardware evidence exist, use the
single-board boundary or require the Driver to keep `entity_id` globally unique.

## Quick start (local)

Prerequisites: **Go 1.26+**, **Node 20+**, **pnpm** (version from
[webui/package.json](webui/package.json)), optional [task](https://taskfile.dev/).

```bash
git clone https://github.com/DeliciousBuding/cloud-path.git
cd cloud-path
task setup
task build
```

Start the server:

```bash
./bin/cloudpath-server
# default: 127.0.0.1:8080, data/cloudpath.db, embedded WebUI
curl -fsS http://127.0.0.1:8080/healthz
```

Local mode is L0 by default: reads are open and writes are accepted only from loopback. To use
account mode, create the first administrator from the same machine:

```bash
curl -fsS -X POST http://127.0.0.1:8080/api/auth/setup \
  -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<strong-password>"}'
```

Start an Edge with the built-in no-hardware demo:

```bash
cp edge.example.yaml edge.yaml
./bin/cloudpath-edge
```

For a real serial device, install and enable its Driver Plugin, enable `plugin_host` in
`edge.yaml`, and configure the device's `port` and adapter. A missing port keeps the device
offline while Edge retries with backoff.

Open <http://127.0.0.1:8080>. Account login uses the session cookie and the realtime `/ws`
channel. Tenant-token login is REST-only because browsers cannot attach custom headers to
WebSocket connections; the UI reports that boundary explicitly.

## Current status and boundaries

The current codebase includes Server, Edge, plugin CLI, embedded WebUI, account/RBAC/tenant
isolation, device supervision and offline buffering, external Driver Plugin Host, Application
Runtime, SQLite persistence, Registry CLI, and release/deployment assets. See
[docs/architecture.md](docs/architecture.md) for the full current-versus-target list.

Not current capabilities: Connector runtime and notification delivery (`SendNotification` fails
closed with `not_implemented`), Transform/WASM, multi-Server scaling, distributed quotas,
centralized KMS/Vault, MQTT/Modbus gateways, remote OTA, time-series analytics, and arbitrary
third-party React bundles in the main WebUI.

Real-hardware verification is separate from implementation. Protocol tests, mocked plugins, CI,
or a merged source change do not prove a new multi-board hardware path. Evidence must include
the real board log, command acknowledgement, and device event.

## Docs

- [README.md](README.md) - Chinese product and operations guide
- [docs/design.md](docs/design.md) - technical design
- [webui/DESIGN.md](webui/DESIGN.md) - WebUI presentation and interaction design
- [docs/architecture.md](docs/architecture.md) - architecture and current-versus-target boundary
- [docs/protocol.md](docs/protocol.md) - WebSocket protocol and DTOs
- [docs/api.md](docs/api.md) - HTTP API, authentication, RBAC, limits
- [docs/security.md](docs/security.md) - security and operations baseline
- [docs/deploy.md](docs/deploy.md) - local, container, and reverse-proxy deployment
- [docs/architecture/plugin-system.md](docs/architecture/plugin-system.md) - plugin runtime and protocols
- [docs/architecture/github-ecosystem.md](docs/architecture/github-ecosystem.md) - discovery and trust
- [docs/architecture/registry.md](docs/architecture/registry.md) - Registry and CLI
- [docs/architecture/how-to-build-driver.md](docs/architecture/how-to-build-driver.md) - Driver implementation guide
- [docs/architecture/capability-model.md](docs/architecture/capability-model.md) - Device/Entity/Capability model
- [docs/architecture/control-plane-sync.md](docs/architecture/control-plane-sync.md) - desired/observed synchronization
- [docs/architecture/tenant-security-policy.md](docs/architecture/tenant-security-policy.md) - tenant and secret boundaries
- [docs/architecture/repository-strategy.md](docs/architecture/repository-strategy.md) - repository and publication strategy
- [docs/architecture/adr/0001-capability-centered-plugins.md](docs/architecture/adr/0001-capability-centered-plugins.md) - capability-centered plugin decision
- [docs/architecture/adr/0002-github-plugin-discovery.md](docs/architecture/adr/0002-github-plugin-discovery.md) - discovery and trust decision
- [deploy/README.md](deploy/README.md) - public deployment SOP
- [deploy/edge/README.md](deploy/edge/README.md) - Edge distribution and configuration
- [deploy/compose/README.md](deploy/compose/README.md) - Docker Compose profiles
- [deploy/split/README.md](deploy/split/README.md) - application split and bootstrap reference
- [templates/go-plugin/README.md](templates/go-plugin/README.md) - new Driver/Application plugin templates

## License

MIT. See [LICENSE](LICENSE).
