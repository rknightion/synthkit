repo: rknightion/synthkit
branch: main
path: internal/control/ui

## Last sync

date: 2026-08-31T11:10:00Z

### Updated in this project

- Recreated the control UI's nine views and shell chrome from `internal/control/ui/src`, then restyled them onto m7kni Design System v2 (petrol accent, flat surfaces, light default).
- Ten screen files, each with a light and a dark 1440x900 frame.
- `implementation-spec.md`: full replacement for `src/theme/tokens.css`, Phosphor icon mapping, per-component restyle notes, gradient removal inventory, dark deltas, view-to-screen map, assumptions.
- Phosphor regular SVGs vendored into `icons/` with an inline sprite loader.

## Screen map

| Screen | Built from |
|---|---|
| 01 Shell and nav rail | src/shell/Rail.tsx, Nav.tsx, Posture.tsx, Status.tsx, ThemeToggle.tsx, App.tsx, src/theme/tokens.css, index.html |
| 02 Overview | src/views/Overview.tsx, src/utils/fmt.ts, src/utils/config.ts |
| 03 Blueprint detail | src/views/Blueprint.tsx, src/api/types.ts |
| 04 Incidents | src/views/Incidents.tsx |
| 05 X-ray | src/views/Xray.tsx |
| 06 Blueprint management | src/views/BpManage.tsx, src/shell/ConfirmDialog.tsx, src/shell/ActionError.tsx |
| 07 Command palette | src/shell/Search.tsx, src/shell/searchIndex.ts |
| 08 System states | src/views/Overview.tsx (loading/empty/error), src/shell/ActionError.tsx, src/shell/Status.tsx |
| 09 Global controls | src/views/Global.tsx |
| 10 Blueprint schema | src/views/Schema.tsx |
| Health, Config (mapped, not drawn) | src/views/Health.tsx, src/views/Config.tsx |
