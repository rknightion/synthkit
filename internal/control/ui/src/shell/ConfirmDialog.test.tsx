import { test, expect, vi } from "vitest";
import { render } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { ConfirmButton } from "./ConfirmDialog";

test("fires onConfirm only after confirming", async () => {
  const onConfirm = vi.fn();
  const { getByText, queryByText } = render(() => (
    <ConfirmButton label="Disable" message="Disable blueprint X?" onConfirm={onConfirm} />
  ));
  await userEvent.click(getByText("Disable"));
  expect(getByText("Disable blueprint X?")).toBeInTheDocument();
  await userEvent.click(getByText("Confirm"));
  expect(onConfirm).toHaveBeenCalledOnce();
  expect(queryByText("Disable blueprint X?")).not.toBeInTheDocument();
});

test("Cancel does not fire onConfirm and closes", async () => {
  const onConfirm = vi.fn();
  const { getByText, queryByText } = render(() => (
    <ConfirmButton label="Disable" message="Disable blueprint X?" onConfirm={onConfirm} />
  ));
  await userEvent.click(getByText("Disable"));
  expect(getByText("Disable blueprint X?")).toBeInTheDocument();
  await userEvent.click(getByText("Cancel"));
  expect(onConfirm).not.toHaveBeenCalled();
  expect(queryByText("Disable blueprint X?")).not.toBeInTheDocument();
});
