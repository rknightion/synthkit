# synthkit control UI on m7kni Design System v2 - implementation spec

Target: `internal/control/ui/` (SolidJS + Vite, embedded via `go:embed`).
Source of truth for values: `packages/tokens/dist/tokens.css` in `m7kni/design-system`, vendored
into this project at `_ds/m7kni-design-system-v2-.../tokens/tokens.css`.
Gate: `just check` (ui-check leg). `just ui` builds the UI alone.

`@m7kni/ui` is React and does not apply here. The migration is the token layer, fonts, icons and
patterns onto the existing hand-rolled Solid components. Every component below already exists in the
repo; none is replaced by a library component.

---

## 1. Replacement for `src/theme/tokens.css`

The v2 token file is the only place colour is defined. The legacy token names stay as **aliases** so
the swap is mechanical: no view CSS has to change name-by-name in the same commit. Every alias
resolves to a v2 semantic token, so the dark theme needs no second alias block - `[data-theme="dark"]`
re-points the v2 tokens and the aliases follow.

Import order matters: v2 tokens first, then the alias block, then the shell CSS.

```css
/* src/theme/tokens.css - synthkit control plane on m7kni Design System v2.
   Identity: petrol accent, petrol-tinted neutrals, flat precise surfaces.
   Light is the default; dark is first class. No gradients, no glow, no depth. */

@import "@m7kni/tokens/dist/tokens.css";   /* :root (light) + [data-theme="dark"] */
@import "@m7kni/ui/styles/fonts.css";      /* Hanken Grotesk + JetBrains Mono, woff2, self-hosted */

/* ── legacy alias layer (delete once call sites are migrated) ─────────────── */
:root {
  /* surfaces */
  --bg:      var(--color-bg-canvas);
  --panel:   var(--color-bg-raised);
  --panel2:  var(--color-bg-surface);
  --hover:   var(--color-bg-hover);      /* was referenced by Xray/Health/Config, never defined */
  --selected: var(--color-bg-selected);  /* new: selected row / active nav */

  /* lines */
  --bd:      var(--color-border-default);
  --bd2:     var(--color-border-strong);

  /* ink */
  --tx:      var(--color-fg-default);
  --soft:    var(--color-fg-soft);       /* new: secondary copy, 12.5px body */
  --dim:     var(--color-fg-muted);
  --faint:   var(--color-fg-faint);      /* placeholder / disabled only, never readable copy */

  /* accent */
  --acc:     var(--color-bg-accent);
  --acc2:    var(--color-bg-accent-soft);
  --accbg:   var(--color-bg-accent-soft); /* was referenced by Blueprint.tsx, never defined */
  --accbd:   var(--color-bg-accent);
  --acc-ink: var(--color-fg-on-accent);   /* NEW and mandatory: replaces every hardcoded #fff */
  --acc-hov: var(--color-accent-hover);

  /* status: colour from tokens, tint and edge derived on the surface so they
     survive hover and selected rows (FOUNDATIONS.md, "rules learned the hard way") */
  --ok:      var(--color-status-ok);
  --okbg:    color-mix(in oklab, var(--color-status-ok) 10%, var(--color-bg-surface));
  --okbd:    color-mix(in oklab, var(--color-status-ok) 45%, var(--color-bg-surface));
  --warn:    var(--color-status-warn);
  --warnbg:  color-mix(in oklab, var(--color-status-warn) 10%, var(--color-bg-surface));
  --warnbd:  color-mix(in oklab, var(--color-status-warn) 45%, var(--color-bg-surface));
  --err:     var(--color-status-fail);
  --crit:    color-mix(in oklab, var(--color-status-fail) 10%, var(--color-bg-surface));
  --critbd:  color-mix(in oklab, var(--color-status-fail) 45%, var(--color-bg-surface));

  /* in-cell furniture: derive from ink, never a fixed grey */
  --track:   color-mix(in oklab, var(--color-fg-default) 14%, var(--color-bg-surface));

  /* type */
  --mono:    var(--font-family-mono);
  --sans:    var(--font-family-sans);

  /* retired: kept as inert aliases for one release so no rule breaks mid-migration.
     Delete these lines and their call sites in the same follow-up commit. */
  --acc-2:            var(--color-bg-accent);  /* gradient terminus - no gradients in v2 */
  --rail-grad:        var(--color-bg-surface);
  --rail-bd:          var(--color-border-default);
  --card-grad:        var(--color-bg-surface);
  --card-shadow:      none;
  --nav-active-grad:  var(--color-bg-selected);
  --accent-glow:      none;                    /* replaced by the focus outline, see 6 */
  --live-glow:        none;
  --accent-text:      var(--color-fg-default);
  --shadow:           transparent;
  --shadow2:          transparent;
  --overlay-bg:       color-mix(in oklab, var(--color-bg-inverse) 55%, transparent);
  --overlay-shadow:   0 12px 32px color-mix(in oklab, var(--color-bg-inverse) 30%, transparent);
}

/* ── base ────────────────────────────────────────────────────────────────── */
* { box-sizing: border-box; }
html, body { height: 100%; }
:root { color-scheme: light; }               /* FLIPPED: was dark */
[data-theme="dark"] { color-scheme: dark; }

body {
  margin: 0;
  background: var(--color-bg-canvas);
  color: var(--color-fg-default);
  font: var(--font-weight-regular) var(--font-size-md)/1.5 var(--font-family-sans);
  -webkit-font-smoothing: antialiased;
  font-variant-numeric: tabular-nums;
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation: none !important; transition: none !important; }
}
```

### 1.1 Every legacy token, its v2 value, both themes

Resolved values, for review and for the AA script. Light is the `:root` value; dark is what the same
alias resolves to under `[data-theme="dark"]`.

| Legacy token | v2 alias | Light | Dark |
|---|---|---|---|
| `--bg` | `--color-bg-canvas` | `oklch(0.962 0.007 227)` | `oklch(0.195 0.01 227)` |
| `--panel` | `--color-bg-raised` | `oklch(1 0 0)` | `oklch(0.26 0.012 227)` |
| `--panel2` | `--color-bg-surface` | `oklch(0.982 0.004 227)` | `oklch(0.225 0.011 227)` |
| `--hover` | `--color-bg-hover` | `oklch(0.945 0.009 227)` | `oklch(0.245 0.012 227)` |
| `--selected` | `--color-bg-selected` | `oklch(0.925 0.012 227)` | `oklch(0.28 0.014 227)` |
| `--bd` | `--color-border-default` | `oklch(0.9 0.008 227)` | `oklch(0.31 0.012 227)` |
| `--bd2` | `--color-border-strong` | `oklch(0.82 0.01 227)` | `oklch(0.4 0.014 227)` |
| `--tx` | `--color-fg-default` | `oklch(0.24 0.012 227)` | `oklch(0.92 0.006 227)` |
| `--soft` | `--color-fg-soft` | `oklch(0.37 0.015 227)` | `oklch(0.8 0.01 227)` |
| `--dim` | `--color-fg-muted` | `oklch(0.5 0.018 227)` | `oklch(0.7 0.015 227)` |
| `--faint` | `--color-fg-faint` | `oklch(0.65 0.015 227)` | `oklch(0.55 0.015 227)` |
| `--acc` | `--color-bg-accent` | `#1d6a8a` | `#66aecb` |
| `--acc2`, `--accbg` | `--color-bg-accent-soft` | `#e4eef4` | `#1f2f38` |
| `--accbd` | `--color-bg-accent` | `#1d6a8a` | `#66aecb` |
| `--acc-ink` | `--color-fg-on-accent` | `oklch(1 0 0)` | `#0e161b` |
| `--acc-hov` | `--color-accent-hover` | `#175874` | `#7cbcd6` |
| `--ok` | `--color-status-ok` | `#2f7d4f` | `#5fae7f` |
| `--okbg` | ok 10% on surface | `oklch(0.955 0.017 152)` approx | `oklch(0.26 0.02 152)` approx |
| `--okbd` | ok 45% on surface | `oklch(0.78 0.06 152)` approx | `oklch(0.4 0.05 152)` approx |
| `--warn` | `--color-status-warn` | `#8f6410` | `#c9a04a` |
| `--warnbg` | warn 10% on surface | `oklch(0.958 0.017 78)` approx | `oklch(0.27 0.02 78)` approx |
| `--warnbd` | warn 45% on surface | `oklch(0.79 0.07 78)` approx | `oklch(0.41 0.05 78)` approx |
| `--err` | `--color-status-fail` | `#a83a2e` | `#d97b64` |
| `--crit` | fail 10% on surface | `oklch(0.955 0.018 29)` approx | `oklch(0.27 0.02 29)` approx |
| `--critbd` | fail 45% on surface | `oklch(0.78 0.08 29)` approx | `oklch(0.42 0.06 29)` approx |
| `--track` | ink 14% on surface | `oklch(0.87 0.006 227)` approx | `oklch(0.33 0.008 227)` approx |
| `--mono` | `--font-family-mono` | JetBrains Mono | same |
| `--acc-2` | retired | - | - |
| `--rail-grad` | retired, flat `--color-bg-surface` | - | - |
| `--card-grad` | retired, flat `--color-bg-surface` | - | - |
| `--card-shadow` | retired, `none` | - | - |
| `--nav-active-grad` | retired, `--color-bg-selected` plus inset accent edge | - | - |
| `--accent-glow` | retired, replaced by focus outline | - | - |
| `--live-glow` | retired | - | - |
| `--accent-text` | retired, solid `--color-fg-default` | - | - |
| `--shadow`, `--shadow2` | retired, `transparent` | - | - |
| `--overlay-bg` | inverse 55% | `oklch(0.27 0.012 227 / 0.55)` approx | `oklch(0.245 0.012 227 / 0.55)` approx |

The `approx` rows are `color-mix` results, not authored constants. Do not hand-copy them into the
CSS - keep the `color-mix` so they follow the surface in both themes. They are listed only so the
contrast script has expected values to check.

### 1.2 Theme default and persistence

`index.html`: `data-theme="light"` (was `"dark"`). Keep the localStorage key
`synthkit-control-theme` and the `ThemeToggle` behaviour exactly as it is; only the fallback flips:

```ts
const stored = globalThis.localStorage?.getItem("synthkit-control-theme");
document.documentElement.setAttribute("data-theme", stored === "dark" ? "dark" : "light");
```

Add that read as an inline boot script in `index.html` before the module loads, so a dark-preferring
operator never sees a light flash. `ThemeToggle` keeps its try/catch.

---

## 2. Type roles

Hanken Grotesk 400/500/600/700 for UI, JetBrains Mono 400/500/600 for machine text. No other
families, no other weights. `tabular-nums` wherever digits align (set globally on `body`).

| Role | Token | Family, size, weight |
|---|---|---|
| Page title (top bar) | `--font-size-xl` | sans 18 / 600. Mono 18 / 600 when the title IS an identifier (blueprint detail) |
| Stat figure | `--font-size-2xl` | sans 24 / 600, tabular |
| Panel heading | `--font-size-lg` | sans 15 / 600 |
| Body | `--font-size-md` | sans 13.5 / 400 |
| Secondary body, table cells | `--font-size-sm` | sans 12.5 / 400 |
| Machine text in tables | `--font-size-sm` | mono 12.5 / 400, 500 for identifiers |
| Status word | 11.5px | mono 600, `letter-spacing: 0.04em` |
| Micro label (section, column, sidebar group) | `--font-size-2xs` | mono 10 / 600, uppercase, `--tracking-label` (0.13em) |
| Meta, counts, timestamps | `--font-size-xs` | mono 11 / 400 |
| Status bar | 10.5px | mono 400 |

Mono is for: blueprint names, construct kinds and instance names, metric and label-key names, config
keys, endpoints, YAML, shas, counts, rates, durations, timestamps, schedule specs, env-var names.
Sans is for: page and panel titles, prose, button labels, table column headers that are not keys.

Retired: `system-ui` shorthand fonts (`font: 700 11px system-ui`), `"SF Mono"` stack, Inter.

---

## 3. Spacing, radius, motion

- Spacing: `--space-1..16` (4/8/12/16/20/24/32/48/64) only. Never an intermediate value. Replace
  every `26px 30px`, `14px 18px`, `11px 13px` in the current CSS with the nearest step: pane padding
  `--space-5` (20px), panel padding `--space-4 --space-5`, row gap `--space-2`.
- Radius: `--radius-control` 3px for buttons, inputs, chips, badges, meters. `--radius-container` 0
  for panels, tables, banners, the rail, the status bar. `--radius-overlay` 6px for the palette,
  dialogs and toasts. `--radius-full` only for presence dots and the switch track.
- Row height: `--row-table` 36px. Nav item 28px, blueprint nav item 26px, top bar 48px, tab bar 34px,
  status bar 24px, stat strip cell 12px padding.
- Motion: `--duration-fast` 120ms for hover and focus, `--duration-base` 160ms for disclosure,
  `--duration-slow` 200ms for the palette and dialog. Ease-out, no bounce, no scale. All animation
  collapses under `prefers-reduced-motion` (the global rule in section 1).
- Removed animations: `@keyframes pulse` (posture blip, nav live dot, scenario LIVE badge, blueprint
  card dot), all `*-spin` spinner keyframes except one shared `spin` used by the single spinner
  component, `filter: brightness()` hovers.

---

## 4. Icons: Phosphor regular, one set

Install `@phosphor-icons/core` (or vendor the regular SVGs, as this project does under `icons/`) and
render at 15px in the rail and buttons, 14px inline, 16px in the palette input. Never emoji.

| Current glyph | Where | Phosphor (regular) |
|---|---|---|
| `▦` | Overview nav, palette blueprint rows | `SquaresFour` |
| `⚙` | Config nav, palette config rows | `Gear` |
| `❤` | Health nav | `Pulse` |
| `🔬` | X-ray nav | `Crosshair` |
| `⚙` | Global controls nav | `Faders` |
| `📖` | Blueprint schema nav | `BookOpen` |
| `💥` | Incidents nav | `Lightning` |
| `📦` | Custom blueprints nav and `<h1>` | `Package` (and drop it from the heading) |
| `⬡` | palette kind rows | `Cube` |
| `⬡` | palette construct rows | `Hexagon` |
| `📈` | palette metric rows | `ChartLine` |
| `↻ Refresh` | rail button | `ArrowsClockwise` |
| `↺ Reset` | rail button | `ArrowCounterClockwise` |
| `☾` / `☀` | theme toggle | `Moon` / `Sun` |
| `↗` | self-obs link, meta links | `ArrowSquareOut` |
| `✓` verdict green | Overview verdict | `CheckCircle` beside the word `OK` |
| `⚠` verdict amber, banners | Overview, Blueprint, BpManage | `Warning` beside the word `WARN` |
| `✕` verdict red | Overview verdict | `XCircle` beside the word `FAIL` |
| `×` | ActionError dismiss | `X` |
| `✕ Delete` | staged blueprint, source, incident | `Trash` |
| `+ Schedule incident` | Incidents form | `Plus` |
| `copy` | X-ray drills | `Copy` |
| `↕ ↑ ↓` | sortable headers | `CaretUpDown` / `CaretUp` / `CaretDown` |
| `▸` details marker | X-ray drills, YAML source | `CaretRight` / `CaretDown` |
| spinner border trick | every view's `*-spinner` | `CircleNotch` rotating, one shared component |
| `Save` | BpManage | `FloppyDisk` |
| `Activate` / `Deactivate` | scenario buttons | `Play` / `Stop` |
| (none) | blueprint detail overflow | `DotsThree` |
| (none) | back to Overview | `ArrowLeft` |
| (none) | filter row | `Funnel` |
| `●` `■` `◆` | status shapes | NOT icons. Keep as mono glyphs inside `StatusWord` |

The three status shapes stay text so they are selectable, greyscale-safe and screen-reader visible.

---

## 5. Status, the one rule that touches every view

`■ OK` / `◆ WARN` / `● FAIL`, mono 11.5 semibold, plus a plain-language note whenever the state is
not OK. One status per row. Colour never carries meaning alone.

synthkit needs four extra words. They use the same shapes and the same discipline:

| Word | Shape | Colour | Meaning |
|---|---|---|---|
| `LIVE` | `●` | `--err` | an injection or scenario is affecting telemetry now |
| `MOD` | `◆` | `--warn` | a knob is off its schema default |
| `OFF` | `■` | `--dim` | disabled by the operator, retained but not emitting |
| `IDLE` | `■` | `--dim` | lane exists but has never pushed (was "pending") |
| `QUEUED` | `■` | `--dim` | scheduled, window not started |
| `DONE` | `■` | `--dim` | window has passed |

Replacements: the pulsing red blip in posture and nav becomes `● LIVE`; sink dots become the lane's
status word; `.bpc-dot.live` becomes the row's status word; `● set` / `○ not set` in Config becomes
`■ SET` / `■ UNSET`; the `capped` badge becomes a note beside the meter, `cardinality cap hit`.

---

## 6. Interaction states

| State | Treatment |
|---|---|
| Hover, row | `background: var(--hover)`. Nothing moves, nothing scales |
| Hover, button | `outline`: `background: var(--hover)`. `primary`: `background: var(--acc-hov)`. `destructive`: fail tint 12% to 18%. No `filter: brightness()` |
| Hover, link | `color: var(--acc-hov)` plus underline |
| Focus visible | `outline: 2px solid color-mix(in oklab, var(--acc) 55%, transparent); outline-offset: 1px`. Never removed, never replaced by the old `--accent-glow` 3px ring |
| Active, nav | `background: var(--selected)`, `box-shadow: inset 2px 0 0 var(--acc)`, weight 600, count in accent, `aria-current="page"` |
| Selected, row | same inset accent edge plus `var(--selected)` |
| Pressed | `translateY(1px)` at most, 120ms |
| Disabled | `color: var(--faint)`, `border-color: var(--bd)`, `cursor: default`, `aria-disabled`. No opacity below 0.5, and never opacity alone on text that must stay legible |
| Invalid field | `border-color: var(--err)` plus the message below in `--err`, 11.5px. Border alone is never the signal |
| Loading, button | label changes to the phase ("Validating...", "Saving...") plus `CircleNotch`. Never a fake percentage |

One accent-filled button per region. In the top bar that is the single primary; destructive actions
are `destructive` (fail tint), secondary actions are `outline`, tertiary are `ghost`.

---

## 7. Per-component restyle notes

### 7.1 Nav rail (`shell/Rail.tsx`, `shell/Nav.tsx`)
200px (was 230px), `background: var(--panel2)`, `border-right: 1px solid var(--bd)`, no gradient, no
radius anywhere inside. Brand row 48px: 12px petrol square, `synthkit` sans 15/600 (the gradient-text
`<span>kit</span>` goes), version mono 10 right-aligned. The rail loses its Refresh, Reset and theme
buttons - they move to the top bar; `deviationCount` moves with the reset action. Group labels are
mono 10 uppercase 0.13em. Nav items 28px, icon 15px, label 13.5, count mono 11 right. Active state
per section 6. Blueprint items are mono 12.5 with a 6px presence dot; a disabled blueprint keeps full
legibility and gains the word `off` instead of `text-decoration: line-through`.

### 7.2 Posture (`shell/Posture.tsx`)
Same derivation, same tags, no `.posture.clean` gradient and no bordered box: a hairline-topped block
in the rail foot. Each tag is `StatusWord` plus a mono target name: `● LIVE db-pressure`,
`◆ MOD volume 2x`, `◆ MOD mine-api -> 8`, `■ OFF azure-csp`. Baseline state is one row,
`■ OK all at baseline`. The `blip` element and its `pulse` animation are deleted.

### 7.3 Telemetry status (`shell/Status.tsx`)
Stays in the rail, pinned to the foot above nothing (it is the last block). Per lane: status word,
mono lane name, `Ns ago` right; second line mono 10 with `48.2k series · ~9.6k/min` and a 40px
sparkline in `--dim`. `promrw/loki/otlp/faro` keep their operator-facing labels
(metrics/logs/traces/rum) and units (series/lines/spans/beacons). The dry-run badge becomes an
inverse-background mono badge, and the same fact repeats in the status bar. Queue rows, optional-lane
rows and the persist row keep their content and take status words. The `auth-note` moves to the
status bar. `STATUS_CSS` is deleted; the panel uses the shell tokens.

### 7.4 Top bar (new, replaces `.pane-head`)
48px, `background: var(--panel2)`, hairline bottom. Title 18/600 (mono when it is an identifier),
mono breadcrumb `control / xray`, the view's rolled-up status word when it has one, then
right-aligned: mono `polled 3s ago`, `ArrowsClockwise` icon button, theme icon button, and exactly
one primary. `Reset all to defaults` is destructive and sits to the left of the primary slot, still
confirm-gated with the deviation count in the message. Per-view `<h1>` and `.sub` are removed; the
`.pane-lead` sentence moves under the first section label or into the panel note.

### 7.5 Status bar (new, 24px foot)
`role="status"`, `aria-live="polite"`, mono 10.5, `--dim`: push mode (`■ OK live push` or
`■ DRY dry run, nothing pushed`), control-token state, persisted-state writability, and the
`⌘K search` hint right-aligned.

### 7.6 Cmd-K palette (`shell/Search.tsx`)
600px, `--radius-overlay`, `background: var(--panel)`, `border: 1px solid var(--bd2)`,
`box-shadow: var(--overlay-shadow)` (overlays are the only place a shadow survives). Input row 44px
with `MagnifyingGlass`, mono 14 query, mono result count right. Results grouped under mono 10 labels
in the index order that already exists: Blueprints, Constructs, Kinds, Config keys, Metrics. Rows
32px, mono 12.5 label, mono 10.5 context (owning blueprint), mono 10 uppercase type right. Selection:
`--selected` plus inset accent edge plus weight 600 (was `--acc2` background only). Footer 30px with
the key hints. Keep the subsequence fuzzy match and the 12-result cap; add `aria-activedescendant`.

### 7.7 Confirm dialog (`shell/ConfirmDialog.tsx`)
460px, `--radius-overlay`, `background: var(--panel)`, overlay backdrop `--overlay-bg`. Title is a
question naming the target in mono. Description is the "say what is safe" sentence, unprompted:
what happens, then what does not. Footer right-aligned: `Cancel` (outline, focused on open) then the
destructive action named with the trigger's verb - never "OK". A typed confirmation (mono input
matching the identifier) is required only where data is destroyed: deleting a staged custom blueprint
and deleting a remote source. Keep the `ConfirmButton` prop surface as-is.

### 7.8 Error banner (`shell/ActionError.tsx`)
`background: var(--panel2)`, `border: 1px solid var(--bd)`, `border-left: 3px solid var(--err)`,
radius 0. `● FAIL` status word, then the plain-language message, then the failing route in mono, then
the retry button, then the dismiss `X`. Keep `role="alert"` and the per-view testids. Never a toast.

### 7.9 Tables (Health, Config, Schema, X-ray, and the new Overview and Blueprint tables)
36px rows, hairline `--bd` between rows, `--bd2` under the header. Header: mono 10 uppercase 0.13em,
sticky. Identifier column mono 500 with a presence dot where the row has liveness. Magnitude columns
right-aligned tabular with an inline meter whose track is `--track`; zero renders as a dim `0` with
no track. Time columns relative in lists, absolute in detail. Status column last but before actions.
No per-row action buttons except the destructive icon button the current UI already has for runtime
incidents and staged blueprints. No horizontal scroll: drop columns into the detail view instead.

### 7.10 Panels, sections, cards
`.sec-label` becomes the mono 10 micro label plus a mono 11 meta. `.panel` keeps
`background: var(--panel2)` and the hairline but loses `border-radius` and both gradients.
`.bpc` blueprint cards, `.pc` scenario cards, `.xr-cst` construct cards and `.bpm-source-row` all
become table rows or hairline-divided blocks; `.bpgrid` and `.pgrid` grids are deleted. The
`.bp-inv-row` stat row and `.proc-grid` become the stat strip: hairline-divided cells, mono 10 label,
24px figure, mono 11 sub-note.

### 7.11 Controls
Buttons per section 6, 28px high (24px for in-table and in-section actions), `--radius-control`.
Inputs 28px, `--radius-control`, `background: var(--panel)`, mono 12.5 for machine values, sans for
prose. Filter fields carry the `/` shortcut hint. Segmented filters replace the `.chips` row and the
`.tabbar`-adjacent filters. Sliders: 4px `--track` rail, accent fill, 10x16 accent handle at 3px
radius, `accent-color` removed. Steppers replace bare number inputs for pod counts, with the bounds
in the accessible description. Switches replace the on/off `.bpchip` chips for blueprint and kind
enablement, with `on`/`off` written beside them. Textareas mono 12, 3px radius, line numbers in
`--faint`.

### 7.12 Tabs (Incidents, Blueprint detail, BpManage)
34px, mono-free sans 12.5 labels, count in mono 11, active = `--tx` 600 with
`box-shadow: inset 0 -2px 0 var(--acc)` on a hairline bar. Keep the localStorage tab persistence
(`synthkit-incidents-tab`).

### 7.13 Incident timeline (`views/Incidents.tsx`)
Track 32px, `--panel2`, hairline border, radius 0, hour hairlines every 3h with mono 10 labels and a
`now HH:MM` accent marker. Bands: declared = `--acc2` fill, `--acc` border, accent text; runtime =
fail 16% fill, `--err` border, fail text; active = inset 3px edge plus a `●` inside the band; past =
`opacity: 0.55`. Provenance is a word in the row below, never only a hue. The 24h geometry, the
interval repeat and the client-side `parseSpec` stay as they are.

### 7.14 Skeletons, spinners, empties, toasts
Skeleton rows at 36px with bars in `--track`, shown after 200ms, 1.5s shimmer, `role="status"` on the
region and `aria-hidden` on the bars. One shared spinner (`CircleNotch`, 14px, accent) replaces the
five per-view border-trick spinners. Empty states are one or two sentences in `--dim` centred in the
region plus at most one outline action - no illustration, no headline. Toasts (new) are bottom-right,
340px, `--radius-overlay`, hairline plus a 3px status edge, 4s, max two, dismissible, and only for
results that land away from the current view.

---

## 8. What happens to every gradient and depth effect

| Effect | Where it is now | v2 |
|---|---|---|
| `--rail-grad` | `.rail` background | flat `--color-bg-surface` |
| `--card-grad` | `.bpc`, `.cfm-box`, `.search-box`, `.bp-inv-row` | flat `--color-bg-surface` (raised for overlays) |
| `--card-shadow` | `.bpc`, `.cfm-box`, `.bp-inv-row` | removed. Separation is the hairline |
| `--nav-active-grad` | `.navi.active` | `--color-bg-selected` plus inset 2px accent edge |
| `--accent-glow` | focus rings | 2px accent outline at 1px offset |
| `--live-glow` | `.blip`, `.navdot.on` | removed with the dots; `● LIVE` carries it |
| `--accent-text` gradient | `.rail-brand span` | solid `--color-fg-default` |
| `.posture.clean` gradient | posture baseline state | hairline block, `■ OK all at baseline` |
| `.pc.on` red box-shadow | active scenario card | row with `● LIVE` and a fail-tinted left edge |
| `.bpc:hover` shadow | blueprint cards | row hover background |
| `.search-box` `0 8px 36px` | palette | kept as `--overlay-shadow`, the only shadow that survives |
| `box-shadow: 0 0 0 2px var(--err)` | active incident band | inset 3px edge plus `●` |
| `filter: brightness()` hovers | every button | explicit hover tokens |
| `animation: pulse` | blip, nav dot, scenario badge, card dot | removed |
| 8-14px radii | everywhere | 3px controls, 0 containers, 6px overlays |

---

## 9. Non-mechanical dark deltas

1. **`#fff` on accent is a bug in dark.** `.cfm-btn.danger`, `.go`, `.bpm-btn.primary`,
   `.status-panel .badge` and `.inc-band.*` all hardcode `color: #fff`. Dark accent is `#66aecb` and
   takes dark ink. Replace every one with `var(--acc-ink)`.
2. **Status tints must be mixed on the surface, not darkened constants.** The legacy dark values
   (`--okbg: #0c2a20`, `--crit: #2a1518`) are separate hand-picked colours; v2 derives them with
   `color-mix` so a tint on `--panel` and the same tint on a hovered row stay distinguishable.
3. **Surfaces lift, they do not invert.** canvas 0.195 to surface 0.225 to raised 0.26. Do not swap
   `--panel` and `--panel2` in dark: `--panel` stays the lighter of the two in both themes, which is
   the opposite of the current file's intent for `.panel` vs `.panel2` usage - audit call sites for
   the two places where the legacy CSS used `--panel` as "recessed" (`.xr-drill-pre`, `.empty-hint
   code`); those become `--bg`.
4. **Sparklines and meters** use `--dim` and `--track`, both of which are ink-derived, so no
   per-theme stroke override is needed. Delete `opacity: 0.85` on the sparkline SVG.
5. **`color-scheme`** flips with the attribute so native controls (range, time, datetime-local,
   select) follow the theme. Verify the date and time inputs in the Incidents schedule form in both
   themes; they are the only native-chrome widgets left.
6. **The dry-run badge** uses `--color-bg-inverse` plus `--color-fg-on-inverse`, which in dark is a
   low-contrast pair against surface by design; it is a badge, not body copy, and passes the 3:1 UI
   boundary rule. Keep the word `dry run` in the status bar as the AA-critical copy.

---

## 10. View to screen map

Nine views plus shell chrome. The screen files are Design Components in this project; each carries a
light and a dark frame at 1440x900.

| View | Route | Screen | Notes |
|---|---|---|---|
| Shell chrome | all | `01 Shell and nav rail` | rail, top bar, status bar, nav item states, posture, telemetry |
| Overview | `/` | `02 Overview` | verdict, readiness, blueprint table, diagnostics |
| Per-blueprint detail | `/bp/:name` | `03 Blueprint detail` | fact strip, tabs, per-signal table, controls aside |
| Incidents | `/incidents` | `04 Incidents` | scheduled tab with the 24h track; on-demand tab uses the same table and form primitives |
| X-ray | `/xray` | `05 X-ray` | filter row, construct table, in-place drill |
| Custom blueprints | `/blueprints` | `06 Blueprint management` | pending banner, staged table, editor, validation, confirm dialog |
| Cmd-K palette | overlay | `07 Command palette` | five index groups |
| System states | all | `08 System states` | skeleton, empty, error, in flight, toasts |
| Global controls | `/global` | `09 Global controls` | volume, blueprint rows, kind rows, unscoped scaling |
| Blueprint schema | `/schema` | `10 Blueprint schema` | reference table archetype |
| Health | `/health` | archetype from `05` plus fact strip from `03` | process stat strip, readiness block as the `02` readiness line, blueprint-cycle table, construct-tick table with the numeric p95 sort and the filter row from `05`. Outcome dot becomes a status word; the error message column stays mono 11.5 truncated at 40 chars with the full text in `title` |
| Config | `/config` | archetype from `10` | grouped key/value tables, mono keys, filter row, sortable key and value columns. Secret chips become `■ SET` / `■ UNSET` |

---

## 11. Accessibility notes

Per screen, the note lives on the screen file itself. The shared rules:

- Focus is a 2px accent outline at 1px offset on every interactive element, never removed. Tab order
  follows reading order; the palette and the dialog trap focus and return it to the trigger.
- Every state is shape plus word plus note. Colour is never the only carrier: statuses, provenance,
  optionality, dry-run, disabled blueprints and timeline bands all read in greyscale.
- Live regions: the verdict, validation results, the status bar and toasts are `role="status"` with
  `aria-live="polite"`; inline failures are `role="alert"`. The skeleton region is one status
  ("Loading runtime") with `aria-hidden` bars, so a screen reader hears one message, not forty cells.
- Meters and sparklines are decorative and `aria-hidden`; the figure beside them is the value.
  Sparklines carry an `aria-label` of latest, min and max.
- Indeterminate work states a phase ("Applying volume 2x, running"), never a fabricated percentage.
- Icon-only buttons carry an accessible name that includes the target
  ("Delete runtime incident, oom_kill on aws-estate").
- Destructive dialogs keep focus on Cancel, name the target in mono, and state what will not happen.
- AA: every fg/bg pair at 4.5:1 (3:1 for large text and UI boundaries) in both themes, checked by
  `tools/contrast_check.py`, not by eye. `--color-fg-faint` is placeholder and disabled only.
- `prefers-reduced-motion` collapses every animation, which is now only hover transitions, the
  spinner and the skeleton shimmer.

---

## 12. Assumptions

1. **Status vocabulary extended.** The system defines OK/WARN/FAIL. synthkit needs LIVE, MOD, OFF,
   IDLE, QUEUED and DONE for posture and incident lifecycle. Same shapes, same discipline, no new
   colours. If the system would rather these were folded into the three words, the mapping is
   LIVE->FAIL-shape, MOD->WARN, and the rest->muted OK.
2. **Failure toasts.** `states.md` says never a toast for an error the user must act on. The failure
   toast on screen 08 is for a background action whose result lands away from the current view, and
   it links to the view where the fix lives. Errors in the current view stay inline.
3. **Nav count column.** The app-shell pattern wants mono counts beside nav items. Config, Health,
   X-ray, Blueprint schema and Custom blueprints have obvious counts; Global controls has none and
   shows nothing rather than a zero.
4. **Five top-level destinations plus groups.** The pattern caps at 5 to 7 destinations. The current
   IA has 8 plus a dynamic blueprint list; it is kept unchanged as instructed, grouped under the
   existing Views / Global / Chaos / Manage labels, with blueprints as a fifth group.
5. **Blueprint detail gains tabs.** The current view is one long page. The detail-page pattern
   requires tabs for sub-collections; content and actions are unchanged, only their grouping.
6. **`Reset all` moved.** It leaves the rail for the top bar as a destructive action. The confirm
   message and the deviation count are unchanged.
7. **Sparklines kept.** They are not in the system's component list, but they are existing
   functionality and read as in-cell furniture; they use `--dim` and are `aria-hidden` with a text
   equivalent.
8. **Sample data is fictional.** Blueprint names, counts, shas and timings in the screens are
   plausible and invented, except `k8s-minimal` and `otlp-native`, which are real bundled names.
9. **The design-system React bundle is not used.** `@m7kni/ui` is React and the control UI is
   SolidJS, so the screens are built directly on the token layer, matching what the implementation
   will do. (For the record, the vendored preview bundle in this project also fails to initialise
   with `cn is not defined`, so it could not have been mounted here either.)
10. **`--hover` and `--accbg` were already referenced but never defined** in the current
    `tokens.css`; the alias layer defines both. Check `.sc-row:hover`, `.h-row:hover`,
    `.cfg-row:hover`, `.xr-drill-sum:hover` and `.bpmeta .tag.cat` after the swap: they render
    transparent today.
11. **Version string.** The brand row shows a mono `v1`; wire it to the build version the binary
    already knows if one is exposed on `/control/status`, otherwise drop it.
12. **Icons are vendored, not npm.** This project copies the Phosphor regular SVGs into `icons/`; the
    implementation may prefer `@phosphor-icons/core` as a dependency. Names in section 4 are the same
    either way.
