# Vendored STC-B renderer

This directory carries the device-specific 3D renderer for the STC-B reference
board. It is isolated from CloudPath's generic UI layer and is loaded lazily
only when the current device adapter is `stcb`.

Upstream: `https://github.com/DeliciousBuding/stcb-board-3d-viewer`
Vendored from: viewer `v0.1.0` (`3f0a0da`)

The copied files intentionally retain their renderer-owned colors and geometry
constants, so the design-token gate excludes this directory. CloudPath UI code
must not import vendor files directly outside `DeviceTwinPanel` / `StcbBoardTwin`.
