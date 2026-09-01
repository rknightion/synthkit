---
id: SKT-0047
title: Restyle the control UI onto design system v2
status: Done
assignee:
  - '@codex'
created_date: '2026-08-31 12:11'
updated_date: '2026-09-01 20:42'
labels:
  - design-system
dependencies: []
ordinal: 138000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The complete v2 design is committed at design/control-ui-v2/: ten screen canvases (shell, overview, blueprint detail, incidents, x-ray, blueprint management, command palette, system states, global controls, blueprint schema), implementation-spec.md, an icons/ directory with the Phosphor SVGs used (name+weight table in the spec), and build/ reference JS for shell/tab/control behaviours. Read the spec in full before any code change; surface any assumption that looks wrong rather than building on it.

Scope: SolidJS + Vite stack STAYS (the UI is embedded in the Go binary via go:embed; @m7kni/ui is React and does not apply). The migration is the token layer, fonts, icons and patterns onto the existing hand-rolled components: the spec's replacement for src/theme/tokens.css is designed to be a mechanical swap. The deliberate indigo own-look (gradients, depth, dark default) is retired by decision, not oversight: petrol accent, hue-227 neutrals, Hanken Grotesk + JetBrains Mono, Phosphor icons, flat surfaces, LIGHT default honouring prefers-color-scheme with the existing toggle and persistence kept. The shell moves to the strict v2 app-shell arrangement (200px sidebar with brand/nav/counts, top bar with title/last-poll/primary action, telemetry status pinned to sidebar bottom) - the spec carries the old-rail-slot to new-location mapping so nothing is dropped. IA otherwise untouched: nine views, same purposes, Cmd-K palette and posture indicator kept.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 control UI renders on v2 tokens light and dark; indigo/gradient/depth styling fully removed; light is the default
- [x] #2 shell matches the v2 app-shell arrangement with every old rail element relocated per the spec mapping
- [x] #3 icons are Phosphor per the spec table; fonts are Hanken Grotesk + JetBrains Mono self-hosted
- [x] #4 all nine views render per their canvases or the view-to-screen map
- [x] #5 AA pairs from the spec hold in both themes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
- [x] #4 just check green (includes ui-check)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read the complete v2 design specification and canvases, restyle the existing SolidJS UI within its package, preserve all view behaviour and theme persistence, measure AA pairs in both themes, and run just ui-check.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented the v2 token, font, icon and shell system while preserving all nine routes, posture, command search, theme persistence and reset behavior. Browser-rendered acceptance covered every route in both themes; search and reset confirmation surfaces were exercised. Measured light/dark contrast ratios: fg 15.60/13.49, soft 9.87/9.16, muted 5.67/6.42, accent 5.73/6.89, warning 4.99/7.00, failure 6.03/5.66, foreground-on-accent 6.03/7.38. just check passed 22 UI test files and 179 tests, TypeScript, and the production build.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Done: the embedded SolidJS control UI now uses design system v2 in light and dark themes, with the specified shell, self-hosted fonts, vendored Phosphor icons, preserved behavior, nine-route rendering, and measured AA contrast. Verified in-browser and through the complete UI and repository gates.
<!-- SECTION:FINAL_SUMMARY:END -->
