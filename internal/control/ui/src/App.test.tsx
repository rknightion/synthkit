import { test, expect } from "vitest";
import { render } from "@solidjs/testing-library";
import App from "./App";

// The shell renders and the "/" Overview route mounts. App owns its own Router
// (base derived from import.meta.env.BASE_URL, "" under vitest), the StoreProvider,
// and the lifecycle in onMount — this proves the frame composes end-to-end.
test("renders the shell and mounts the Overview route at /", () => {
  const { getByRole } = render(() => <App />);
  // Rail brand is present (shell rendered).
  expect(getByRole("complementary")).toBeInTheDocument(); // <aside class="rail">
  // Overview view mounted at "/" (the view heading, not the nav link).
  expect(getByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
});
