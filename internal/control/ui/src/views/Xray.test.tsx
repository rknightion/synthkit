import { test, expect } from "vitest";
import { render } from "@solidjs/testing-library";
import { MemoryRouter, Route } from "@solidjs/router";
import { Xray } from "./Xray";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { InventoryReport } from "../api/types";

function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

function renderXray(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter>
        <Route path="/" component={Xray} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

// Realistic inventory snapshot used across tests.
const FAKE_INVENTORY: InventoryReport = {
  totals: {
    distinct_series: 1500,
    constructs: 4,
    blueprints: 2,
  },
  blueprints: [
    {
      blueprint: "acme",
      distinct_series: 1200,
      metric_names: 18,
      label_keys: 6,
      constructs: [
        {
          kind: "ec2",
          name: "web",
          distinct_series: 800,
          capped: false,
          metric_names: ["aws_ec2_cpu_utilization", "aws_ec2_network_in"],
          metric_label_keys: ["instance_id", "region"],
          log_sources: ["syslog"],
          log_label_keys: ["level"],
          span_services: ["web-service"],
          span_names: ["GET /api"],
          span_attr_keys: ["http.method", "http.status_code"],
        },
        {
          kind: "rds",
          name: "db",
          distinct_series: 400,
          capped: true,
          metric_names: ["aws_rds_cpu_utilization"],
          metric_label_keys: ["db_instance_id"],
          log_sources: [],
          span_services: [],
          span_attr_keys: [],
        },
      ],
    },
    {
      blueprint: "beta",
      distinct_series: 300,
      metric_names: 5,
      label_keys: 3,
      constructs: [
        {
          kind: "k8s_cluster",
          name: "prod",
          distinct_series: 300,
          capped: false,
          metric_names: ["kube_pod_status_phase"],
          metric_label_keys: ["cluster", "namespace"],
        },
      ],
    },
  ],
};

// ── distinct state tests ─────────────────────────────────────────────────────

test("shows loading state while data has not arrived", () => {
  const store = fakeStore({ loading: true });
  const { getByTestId, queryByTestId } = renderXray(store);
  expect(getByTestId("xray-loading")).toBeInTheDocument();
  expect(queryByTestId("xray-empty")).not.toBeInTheDocument();
  expect(queryByTestId("xray-error")).not.toBeInTheDocument();
});

test("shows error state when inventory GET fails", () => {
  const store = fakeStore({ loading: false, errors: { inventory: "connection refused" } });
  const { getByTestId, queryByTestId } = renderXray(store);
  const errNode = getByTestId("xray-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("connection refused");
  expect(queryByTestId("xray-loading")).not.toBeInTheDocument();
  expect(queryByTestId("xray-empty")).not.toBeInTheDocument();
});

test("shows empty state when no inventory data yet", () => {
  const store = fakeStore({
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
  });
  const { getByTestId, queryByTestId } = renderXray(store);
  const emptyNode = getByTestId("xray-empty");
  expect(emptyNode).toBeInTheDocument();
  expect(emptyNode.textContent).toContain("No inventory data yet");
  expect(queryByTestId("xray-loading")).not.toBeInTheDocument();
  expect(queryByTestId("xray-error")).not.toBeInTheDocument();
});

// ── data state tests ─────────────────────────────────────────────────────────

test("renders a construct card from the inventory snapshot", () => {
  const store = fakeStore({ inventory: FAKE_INVENTORY });
  const { getByTestId } = renderXray(store);
  const card = getByTestId("cst-card-ec2-web");
  expect(card).toBeInTheDocument();
  expect(card.textContent).toContain("ec2");
  expect(card.textContent).toContain("web");
  expect(card.textContent).toContain("800");
});

test("renders the metric-names drill for a construct card", () => {
  const store = fakeStore({ inventory: FAKE_INVENTORY });
  const { getByTestId } = renderXray(store);
  const card = getByTestId("cst-card-ec2-web");
  // The metric names drill for ec2/web should be inside the card
  expect(card.textContent).toContain("metric names");
  expect(card.textContent).toContain("aws_ec2_cpu_utilization");
  expect(card.textContent).toContain("aws_ec2_network_in");
});

test("capped badge appears ONLY when the construct is capped", () => {
  const store = fakeStore({ inventory: FAKE_INVENTORY });
  const { getByTestId, queryByTestId } = renderXray(store);

  // rds/db has capped: true → badge present
  const cappedBadge = getByTestId("capped-badge-rds-db");
  expect(cappedBadge).toBeInTheDocument();

  // ec2/web has capped: false → badge absent
  expect(queryByTestId("capped-badge-ec2-web")).not.toBeInTheDocument();
});

test("totals panel reflects the fixture InventoryTotals", () => {
  const store = fakeStore({ inventory: FAKE_INVENTORY });
  const { getByTestId } = renderXray(store);
  const totals = getByTestId("xray-totals");
  expect(totals).toBeInTheDocument();
  // distinct_series: 1500; constructs: 4; blueprints: 2
  expect(totals.textContent).toContain("1.5k");  // 1500 → fmtNum → "1.5k"
  expect(totals.textContent).toContain("4");
  expect(totals.textContent).toContain("2");
});

test("blueprint section is rendered per blueprint", () => {
  const store = fakeStore({ inventory: FAKE_INVENTORY });
  const { getByTestId } = renderXray(store);
  expect(getByTestId("bp-section-acme")).toBeInTheDocument();
  expect(getByTestId("bp-section-beta")).toBeInTheDocument();
});

test("totals panel is absent when no blueprints loaded", () => {
  const store = fakeStore({
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
  });
  const { queryByTestId } = renderXray(store);
  expect(queryByTestId("xray-totals")).not.toBeInTheDocument();
});
