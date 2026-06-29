import { onMount, onCleanup, type ParentProps } from "solid-js";
import { Router, Route } from "@solidjs/router";
import { StoreProvider, createControlStore } from "./store/store";
import { Rail } from "./shell/Rail";
import { Overview } from "./views/Overview";
import { Config } from "./views/Config";
import { Health } from "./views/Health";
import { Xray } from "./views/Xray";
import { Global } from "./views/Global";
import { Blueprint } from "./views/Blueprint";
import { Incidents } from "./views/Incidents";
import { Schema } from "./views/Schema";
import { BpManage } from "./views/BpManage";
// import the other views as they are ported (Phase 2)

const routerBase = import.meta.env.BASE_URL.replace(/\/+$/, ""); // "" in dev, "/control/ui" in build

const hasActiveSelection = () => {
  const s = window.getSelection?.();
  return !!(s && !s.isCollapsed && s.toString().trim());
};

export default function App() {
  const store = createControlStore({ shouldPause: hasActiveSelection });
  onMount(() => {
    void store.refresh();
    store.start();
    onCleanup(() => store.stop());
  });
  const Shell = (props: ParentProps) => (
    <div class="app">
      <Rail />
      <main class="pane">{props.children}</main>
    </div>
  );
  return (
    <StoreProvider store={store}>
      <Router root={Shell} base={routerBase}>
        <Route path="/" component={Overview} />
        <Route path="/config" component={Config} />
        <Route path="/health" component={Health} />
        <Route path="/xray" component={Xray} />
        <Route path="/global" component={Global} />
        <Route path="/bp/:name" component={Blueprint} />
        <Route path="/incidents" component={Incidents} />
        <Route path="/schema" component={Schema} />
        <Route path="/blueprints" component={BpManage} />
        {/* Phase 2 routes added here */}
      </Router>
    </StoreProvider>
  );
}
