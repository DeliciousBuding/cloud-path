# Device twin (WebUI-only)

This directory adds a read-only 3D digital twin to the device detail page without
changing the Driver, plugin manifest, API, or Core contracts.

- `device-twin.ts` resolves the current adapter to an explicit frontend model and
  maps descriptor observations / raw state into renderer state.
- `DeviceTwinPanel.tsx` places the model in the right column of the controls
  and overview tab layouts. The overview starts with a one-column card; expanding
  it spans two columns and pushes following cards to the next flex row. It renders
  nothing for adapters without a registered frontend model.
- `StcbBoardTwin.tsx` lazy-loads the STC-B renderer and exposes only orbit/zoom
  canvas interaction. The viewer's own side console is not included. The scene
  background follows the app theme: white in light mode, studio-dark in dark mode.
- `vendor/stcb/` is the isolated copy of the public STC-B viewer renderer. The
  i18n/design-token gates exclude this directory because it owns upstream
  presentation constants and device-specific copy.

Current STC-B mapping:

| UI state | Source |
|---|---|
| powered | `DeviceView.online` |
| LED mask | `led-bank.mask`, then raw fallback |
| display mode/page | `display.mode` / `display.page`, then raw fallback |
| display glyphs | explicit `display.digits` when reported; otherwise the clock page is derived from `clock.time` and the device timestamp |

The renderer must never invent glyphs or turn a missing observation into a
successful device state. Adapters without a frontend model fail closed.
