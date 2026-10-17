/* SPDX-License-Identifier: Apache-2.0 */
import type { JSX } from "solid-js";

import arrowCounterClockwise from "../assets/icons/arrow-counter-clockwise.svg";
import arrowsClockwise from "../assets/icons/arrows-clockwise.svg";
import bookOpen from "../assets/icons/book-open.svg";
import chartLine from "../assets/icons/chart-line.svg";
import checkCircle from "../assets/icons/check-circle.svg";
import circleNotch from "../assets/icons/circle-notch.svg";
import copy from "../assets/icons/copy.svg";
import crosshair from "../assets/icons/crosshair.svg";
import cube from "../assets/icons/cube.svg";
import faders from "../assets/icons/faders.svg";
import gear from "../assets/icons/gear.svg";
import hexagon from "../assets/icons/hexagon.svg";
import lightning from "../assets/icons/lightning.svg";
import magnifyingGlass from "../assets/icons/magnifying-glass.svg";
import moon from "../assets/icons/moon.svg";
import packageIcon from "../assets/icons/package.svg";
import pulse from "../assets/icons/pulse.svg";
import squaresFour from "../assets/icons/squares-four.svg";
import sun from "../assets/icons/sun.svg";
import warning from "../assets/icons/warning.svg";
import x from "../assets/icons/x.svg";
import xCircle from "../assets/icons/x-circle.svg";

const icons = {
  "arrow-counter-clockwise": arrowCounterClockwise,
  "arrows-clockwise": arrowsClockwise,
  "book-open": bookOpen,
  "chart-line": chartLine,
  "check-circle": checkCircle,
  "circle-notch": circleNotch,
  copy,
  crosshair,
  cube,
  faders,
  gear,
  hexagon,
  lightning,
  "magnifying-glass": magnifyingGlass,
  moon,
  package: packageIcon,
  pulse,
  "squares-four": squaresFour,
  sun,
  warning,
  x,
  "x-circle": xCircle,
} as const;

export type IconName = keyof typeof icons;

// The glyphs are fill="currentColor" SVGs. Loaded through <img> they are an
// isolated document, so currentColor falls back to black and ignores the theme.
// Instead the file is a CSS mask (--icon-src, consumed by the .icon rule in
// theme/tokens.css) over a currentColor background, so the glyph follows the
// themed text colour of whatever control hosts it.
export function Icon(props: { name: IconName; class?: string }): JSX.Element {
  return <span class={`icon ${props.class ?? ""}`} style={{ "--icon-src": `url("${icons[props.name]}")` }} aria-hidden="true" />;
}
