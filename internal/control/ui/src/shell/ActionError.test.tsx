import { test, expect, vi } from "vitest";
import { render } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { createSignal } from "solid-js";
import { ActionError } from "./ActionError";

test("renders nothing when the message is falsy", () => {
  const { queryByTestId } = render(() => (
    <ActionError message={() => undefined} testid="x-action-error" onDismiss={() => {}} />
  ));
  expect(queryByTestId("x-action-error")).not.toBeInTheDocument();
});

test("renders the banner with the message under the supplied testid", () => {
  const { getByTestId } = render(() => (
    <ActionError message={() => "boom"} testid="x-action-error" onDismiss={() => {}} />
  ));
  const banner = getByTestId("x-action-error");
  expect(banner).toBeInTheDocument();
  expect(banner.textContent).toContain("boom");
  expect(banner).toHaveAttribute("role", "alert");
});

test("clicking the dismiss button fires onDismiss", async () => {
  const onDismiss = vi.fn();
  const { getByLabelText } = render(() => (
    <ActionError message={() => "boom"} testid="x-action-error" onDismiss={onDismiss} />
  ));
  await userEvent.click(getByLabelText("Dismiss"));
  expect(onDismiss).toHaveBeenCalledOnce();
});

test("hides once the message getter goes falsy (reactive)", () => {
  const [msg, setMsg] = createSignal<string | undefined>("boom");
  const { queryByTestId } = render(() => (
    <ActionError message={msg} testid="x-action-error" onDismiss={() => setMsg(undefined)} />
  ));
  expect(queryByTestId("x-action-error")).toBeInTheDocument();
  setMsg(undefined);
  expect(queryByTestId("x-action-error")).not.toBeInTheDocument();
});
