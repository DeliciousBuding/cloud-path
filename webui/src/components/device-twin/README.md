# Device twin (WebUI-only)

This directory adds a read-only 3D digital twin to the device detail page without
changing the Driver, plugin manifest, API, or Core contracts.

- `device-twin.ts` resolves the current adapter to an explicit frontend model and
  maps descriptor observations / raw state into renderer state.
- `DeviceTwinPanel.tsx` places the model in the right column of the controls
  and overview tab layouts. The overview starts with a one-column card; expanding
  it spans two columns and pushes following cards to the next flex row. It renders
  nothing for adapters without a registered frontend model.
- `StcbBoardTwin.tsx` lazy-loads the STC-B renderer and caches one WebGL runtime
  per device. Switching between the controls and overview tabs only re-parents
  the existing canvas; it does not rebuild the scene. The viewer exposes only
  orbit/zoom canvas interaction. Its side console is not included, and the scene
  background follows the app theme: white in light mode, studio-dark in dark mode.
- `activity.ts` carries short-lived UI feedback from live device events and
  command lifecycle updates. `device-twin.ts` maps the event entity or command
  to model parts such as K1, the buzzer, the display, or the LED bank, then the
  renderer pulses a localized outline and light on those parts. It never mutates
  hardware-derived state or claims a physical event that the device did not report.
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
