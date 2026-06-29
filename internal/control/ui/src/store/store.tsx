import { createStore, produce } from "solid-js/store";
import { createContext, useContext, type ParentComponent } from "solid-js";
import { getJSON, ApiError } from "../api/client";
import type {
  State,
  StatusReport,
  InventoryReport,
  HealthReport,
  Diagnostic,
  Schema,
  ConfigView,
  IncidentInfo,
  PendingChanges,
  StagedBlueprint,
  SourceView,
} from "../api/types";

// HealthReport is re-exported from api/types (fully typed when the Health view landed).
export type { HealthReport };

export interface Snapshot {
  state?: State;
  status?: StatusReport;
  inventory?: InventoryReport;
  health?: HealthReport;
  diagnostics?: Diagnostic[];
  schema?: Schema;
  config?: ConfigView;
  incidents?: IncidentInfo[];
  pending?: PendingChanges;
  staged?: StagedBlueprint[];
  sources?: SourceView[];
  loading: boolean;
  errors: Record<string, string>;
}

// Polled endpoints. `key` is the Snapshot field + errors-map key the views read; `path`
// is the /control/<path> URL fetched. They differ only for the multi-segment blueprint
// routes (e.g. key "pending" ← path "blueprints/pending"), which cannot be clean field
// names. Endpoints that 404 when unconfigured (incidents, blueprint admin) record into
// errors[key] and leave their field undefined — the per-endpoint catch handles this.
const ENDPOINTS: readonly { key: string; path: string }[] = [
  { key: "state", path: "state" },
  { key: "status", path: "status" },
  { key: "inventory", path: "inventory" },
  { key: "health", path: "health" },
  { key: "diagnostics", path: "diagnostics" },
  { key: "schema", path: "schema" },
  { key: "config", path: "config" },
  { key: "incidents", path: "incidents" },
  { key: "pending", path: "blueprints/pending" },
  { key: "staged", path: "blueprints/staged" },
  { key: "sources", path: "blueprints/sources" },
] as const;

export interface ControlStore {
  state: Snapshot;
  refresh(): Promise<void>;
  start(): void;
  stop(): void;
}

export function createControlStore(
  opts: { intervalMs?: number; shouldPause?: () => boolean } = {},
): ControlStore {
  const interval = opts.intervalMs ?? 5000;
  const [snap, setSnap] = createStore<Snapshot>({ loading: true, errors: {} });
  let timer: ReturnType<typeof setInterval> | undefined;

  async function refresh() {
    await Promise.all(ENDPOINTS.map(async ({ key, path }) => {
      try {
        const data = await getJSON<unknown>(path);
        setSnap(produce((s) => { (s as unknown as Record<string, unknown>)[key] = data; delete s.errors[key]; }));
      } catch (e) {
        const msg = e instanceof ApiError ? e.message : String(e);
        setSnap(produce((s) => { s.errors[key] = msg; }));
      }
    }));
    setSnap("loading", false);
  }

  function start() {
    stop();
    timer = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      if (opts.shouldPause?.()) return;
      void refresh();
    }, interval);
  }

  function stop() {
    if (timer) clearInterval(timer);
    timer = undefined;
  }

  return { state: snap, refresh, start, stop };
}

const StoreCtx = createContext<ControlStore>();

export const StoreProvider: ParentComponent<{ store: ControlStore }> = (props) => (
  <StoreCtx.Provider value={props.store}>{props.children}</StoreCtx.Provider>
);

export function useStore(): ControlStore {
  const s = useContext(StoreCtx);
  if (!s) throw new Error("useStore outside StoreProvider");
  return s;
}
