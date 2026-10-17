import { test, expect } from "vitest";
import { render } from "@solidjs/testing-library";
import { Icon } from "./Icon";

// The test types deliberately omit Node globals, and Vitest's CSS handling returns
// an empty string for a ?raw stylesheet import, so read the file through Node's
// built-in loader with a minimal local declaration. import.meta.dirname keeps the
// path independent of the working directory (import.meta.url is http: under jsdom).
declare const process: { getBuiltinModule(id: "node:fs"): { readFileSync(path: string, encoding: "utf8"): string } };
const here = (import.meta as unknown as { dirname: string }).dirname;
const tokensCss = process.getBuiltinModule("node:fs").readFileSync(`${here}/../theme/tokens.css`, "utf8");

// Bodies of the plain `.icon` rule(s), with @media blocks removed.
const plainCss = tokensCss.replace(/@media[^{]*\{(?:[^{}]*\{[^{}]*\})*[^{}]*\}/g, "");
const iconRule = /(?:^|[}\s,])\.icon\s*\{([^}]*)\}/g;
const iconBodies = (css: string) => [...css.matchAll(iconRule)].map((m) => m[1]).join(" ");

// An SVG loaded through <img> is an isolated document: its fill="currentColor"
// resolves to the SVG's own default (black), so it can never follow the console
// theme. The glyph must instead be a CSS mask painted with currentColor, which
// inherits the themed text colour set on the surrounding control.

test("renders the glyph as a themed mask, not an <img>", () => {
  const { container } = render(() => <Icon name="gear" class="nav-icon" />);
  expect(container.querySelector("img")).toBeNull();
  const el = container.querySelector(".icon");
  expect(el).not.toBeNull();
  expect(el).toHaveClass("nav-icon");
  expect(el).toHaveAttribute("aria-hidden", "true");
  expect(el!.getAttribute("style")).toMatch(/--icon-src:\s*url\(/);
});

test("every icon name yields a distinct mask source", () => {
  const names = ["gear", "sun", "moon", "x", "arrows-clockwise", "magnifying-glass"] as const;
  const seen = new Set<string>();
  for (const name of names) {
    const { container, unmount } = render(() => <Icon name={name} />);
    const style = container.querySelector(".icon")!.getAttribute("style") ?? "";
    expect(style).toMatch(/--icon-src:/);
    seen.add(style);
    unmount();
  }
  expect(seen.size).toBe(names.length);
});

test("the .icon rule paints the mask with currentColor from the token contract", () => {
  const bodies = iconBodies(plainCss);
  expect(bodies).toMatch(/background-color\s*:\s*currentColor/);
  expect(bodies).toMatch(/(?:^|[;\s{])mask\s*:[^;]*var\(--icon-src\)/);
  expect(bodies).toMatch(/(?:^|[;\s{])-webkit-mask\s*:[^;]*var\(--icon-src\)/);
});

test("icon glyphs use no hard-coded colour", () => {
  const bodies = iconBodies(plainCss);
  expect(bodies).not.toBe("");
  expect(bodies).not.toMatch(/#[0-9a-f]{3,8}\b|rgb\(|hsl\(|oklch\(|\bblack\b|\bwhite\b/i);
});

test("forced-colors mode keeps masked glyphs visible", () => {
  const m = tokensCss.match(/@media\s*\(forced-colors:\s*active\)\s*\{\s*\.icon\s*\{([^}]*)\}/);
  expect(m).not.toBeNull();
  expect(m![1]).toMatch(/forced-color-adjust\s*:\s*none/);
  expect(m![1]).toMatch(/background-color\s*:\s*CanvasText/);
});
