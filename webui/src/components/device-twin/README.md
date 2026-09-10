# Device twin (WebUI-only)

This directory adds a read-only 3D digital twin to the device detail page without
changing the Driver, plugin manifest, API, or Core contracts.

- `device-twin.ts` resolves the current adapter to an explicit frontend model and
  maps descriptor observations / raw state into renderer state.
- `DeviceTwinPanel.tsx` places the model between the device header and tabs. It
  renders nothing for adapters without a registered frontend model.
- `StcbBoardTwin.tsx` lazy-loads the STC-B renderer and exposes only orbit/zoom
  canvas interaction. The viewer's own side console is not included.
- `vendor/stcb/` is the isolated copy of the public STC-B viewer renderer. The
  i18n/design-token gates exclude this directory because it owns upstream
  presentation constants and device-specific copy.

Current STC-B mapping:

| UI state | Source |
|---|---|
| powered | `DeviceView.online` |
| LED mask | `led-bank.mask`, then raw fallback |
| display mode/page | `display.mode` / `display.page`, then raw fallback |
| display glyphs | only when a future `display.digits` observation exists; otherwise blank |

The renderer must never invent glyphs or turn a missing observation into a
successful device state. Adapters without a frontend model fail closed.
