<div align="center">

# CloudPath · Cloud Path

**A cloud-native, plugin-driven IoT control plane** that connects any device to the cloud in a
**center-control + edge-agent** edge-cloud synergy architecture: on-board visualization and remote control,
device-agnostic, edge-autonomous — a new device is just a Driver plugin.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![CI](https://github.com/DeliciousBuding/cloud-path/actions/workflows/ci.yml/badge.svg)](https://github.com/DeliciousBuding/cloud-path/actions)

</div>

---

## What it is

CloudPath turns **plug in a device → see it in the cloud → control it remotely** into an
**out-of-the-box cloud-native infrastructure**, not a host program for one dev board.

- **Cloud-native · edge-cloud synergy**: the central control plane (Server) is the **authority for desired state /
  tenant / audit**; the edge agent (Edge) is the **authority for observed state** and keeps the last successfully
  applied snapshot. Edge is autonomous: keeps running offline, and on reconnect applies only the final snapshot
  without replaying intermediate side effects.
- **Device-agnostic · plugin-driven**: core (`internal/*`) knows no concrete hardware; a new device = one Driver plugin.
- **Distributed Hub-Spoke**: multiple edge nodes + one control plane, a natural center-edge topology, horizontally scalable.
- **Device identity** = `(tenant_id, edge_id, device_id)`; the wire key is `<edge_id>/<device_id>`.
- **Real-time end-to-end**: edge → server → browser all over WebSocket; REST only serves history and management.
- **Single binary · zero CGO**: WebUI `go:embed`-ed into server; SQLite via `modernc.org/sqlite`; cross-compiles to Linux/arm64 with no toolchain.

```text
        ┌──────────────────────────────────────────────────────────┐
        │  Experience Plane                                        │
        │  WebUI (overview/devices/events/edges/system/admin) · Schema rendering │
        └───────────────▲──────────────────────────────────────────┘
                        │ REST + WebSocket (/ws)
        ┌───────────────┴──────────────────────────────────────────┐
        │  Control Plane — cloudpath-server (single binary + SQLite)│
        │  desired authority · tenant/RBAC · tokens · audit · rate-limit · retention │
        │  plugin catalog/instance desired state · command dispatch & ack settlement │
        └───────────────▲──────────────────────────────────────────┘
                        │ WebSocket (/ws/edge): state/event up, command down
        ┌───────────────┴──────────────────────────────────────────┐
        │  Edge Plane — cloudpath-edge (one per host/site)          │
        │  observed authority · device supervision & backoff restart · offline event buffer │
        │  Driver Host (external plugin process) · local secret resolution │
        └───────────────▲──────────────────────────────────────────┘
                        │ serial / local bus
                  devices (reference Driver: stcb; or external Driver plugins)
```

## Plugin types & runtime

| Type | Default host | Responsible for | Not responsible for |
|---|---|---|---|
| **Driver** | Edge | device discovery, connect, protocol parse, capability mapping, device actions | business flow, tenant UI |
| **Application** | Server | business objects, bindings, rules, tasks, domain APIs | direct serial access or Core DB |
| **Connector** | Edge or Server | MQTT / Webhook / external platforms / notifications / data egress | defining the core device model |

UI contributions are not a separate executable plugin type: plugins submit declarative navigation,
forms, and page Schema via a Manifest.

Current plugin flavors: `examples/scheduled-compartment` (process-based reference Application) ·
`templates/go-plugin` (official Go plugin template). The STC-B reference Driver lives in the separate
`cloud-path-driver-stcb` repo and is installed via GitHub discover/install. See `docs/plugin-system.md`.

## Quick start (local)

Prereqs: **Go 1.26+**, **Node 20+** (CI uses 24), **pnpm**, optional [task](https://taskfile.dev/).

```bash
git clone https://github.com/DeliciousBuding/cloud-path.git
cd cloud-path
task setup && task build       # → bin/cloudpath-server + bin/cloudpath-edge
./bin/cloudpath-server         # default 127.0.0.1:8080, DB data/cloudpath.db, WebUI embedded
curl -fsS http://127.0.0.1:8080/healthz
```

Then set up an admin account (`POST /api/auth/setup`), install the STC-B Driver plugin
(`cloudpath plugin install github.com/DeliciousBuding/cloud-path-driver-stcb`), and run an edge
(`cp edge.example.yaml edge.yaml; ./bin/cloudpath-edge`) with `adapter: stcb` for real devices.

## Docs & architecture

- `docs/design.md` — technical SSOT
- `docs/protocol.md` — message envelope / DTO contracts
- `docs/plugin-system.md` — plugin system (Developer)
- `docs/architecture/*` — capability model, control-plane sync, tenant security policy
- `docs/api.md` · `docs/deploy.md` · `docs/security.md`

## License

MIT (see `LICENSE`).
