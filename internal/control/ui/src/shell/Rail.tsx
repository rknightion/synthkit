import { type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { Posture } from "./Posture";
import { Status } from "./Status";
import { Nav } from "./Nav";
import { Search } from "./Search";

// The left sidebar: brand, quick actions, current-posture summary, nav, and the
// always-mounted Cmd-K search overlay (it listens globally and renders only when
// open). Single responsibility: compose the rail; each piece owns its own logic.
export function Rail(): JSX.Element {
  return (
    <aside class="rail">
      <A href="/" end class="rail-brand" title="Back to Overview">
        synth<span>kit</span>
      </A>
      <Posture />
      <Nav />
      <Status />
      <Search />
    </aside>
  );
}
