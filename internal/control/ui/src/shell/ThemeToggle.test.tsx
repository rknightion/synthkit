import { test, expect } from "vitest";
import { render } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { ThemeToggle } from "./ThemeToggle";

test("toggles data-theme on the document element", async () => {
  document.documentElement.setAttribute("data-theme", "dark");
  const { getByRole } = render(() => <ThemeToggle />);
  await userEvent.click(getByRole("button"));
  expect(document.documentElement.getAttribute("data-theme")).toBe("light");
});
