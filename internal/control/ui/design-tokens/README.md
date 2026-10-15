# Synthkit design tokens

Dependency-free CSS shared by the embedded console and adjacent frontends. The console imports
this package's `tokens.css` through its theme adapter. Another frontend can install this directory
as a local package and import `@synthkit/design-tokens/tokens.css`; no framework or runtime is needed.
The package is private: publication is a separate decision.

The source is the maintainer-approved 7 October 2026 synthkit design board, published artifact
version `1791408283-0027`, SHA-256
`a75b0dd7793e37fde328a6a7b8ee5dde84729ac79fd38682d640fbf91b0dc32d`.
Colour, type, spacing, radii, table rows and label tracking are extracted from its theme blocks.
Control/input heights come from its shared component specimens. Lifecycle colours are available
for consumers; they do not introduce lifecycle states or controls into the embedded console.

Use `--sk-*` for colours, font families and component dimensions, `--fs-*` for type size,
`--sp-*` for spacing, and `--r-*` for radii. `--row` and `--track-label` cover table rows and labels.
Set `data-theme="light"` or `data-theme="dark"` on the document root for an explicit theme.
Without either, the package follows the operating system. The console keeps its existing explicit
light default and persisted theme toggle. Each consumer supplies Hanken Grotesk and JetBrains Mono;
the console uses its existing bundled fonts, with no network font request.

Only tokens are shared. The console's aliases, layout and component selectors remain in its theme
adapter. Navigation rows use the board’s flex layout; the mobile toolbar and status bar wrap
inside the viewport. Actions, routing, API calls and persistence are unchanged. The approved board
updates the dark selection/inverse colours, track colours and overlay/shadow values from the older
console design. It adds font fallbacks. These visual changes are intentional.
