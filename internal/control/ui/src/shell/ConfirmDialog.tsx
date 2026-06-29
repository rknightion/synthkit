import { Show, createSignal, type JSX } from "solid-js";

// Reusable confirm primitive for destructive actions. Plain Solid for Phase 1;
// Phase 3 swaps the internals to Kobalte's dialog for focus-trap/return without
// changing this prop surface.
export interface ConfirmDialogProps {
  open: boolean;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog(props: ConfirmDialogProps): JSX.Element {
  return (
    <Show when={props.open}>
      <div
        class="cfm-backdrop"
        role="dialog"
        aria-modal="true"
        onClick={(e) => {
          if (e.target === e.currentTarget) props.onCancel();
        }}
      >
        <div class="cfm-box">
          <p class="cfm-msg">{props.message}</p>
          <div class="cfm-actions">
            <button type="button" class="cfm-btn" onClick={() => props.onCancel()}>
              {props.cancelLabel ?? "Cancel"}
            </button>
            <button type="button" class="cfm-btn danger" onClick={() => props.onConfirm()}>
              {props.confirmLabel ?? "Confirm"}
            </button>
          </div>
        </div>
      </div>
    </Show>
  );
}

// A button that gates `onConfirm` behind the ConfirmDialog. Used by destructive
// actions across the app (e.g. the Task-7 blueprint toggle).
export interface ConfirmButtonProps {
  label: string;
  message: string;
  onConfirm: () => void;
  confirmLabel?: string;
  cancelLabel?: string;
  class?: string;
  testid?: string;   // optional data-testid passed to the trigger button (for view tests)
  disabled?: boolean;
}

export function ConfirmButton(props: ConfirmButtonProps): JSX.Element {
  const [open, setOpen] = createSignal(false);
  return (
    <>
      <button
        type="button"
        class={props.class}
        data-testid={props.testid}
        disabled={props.disabled}
        onClick={() => { if (!props.disabled) setOpen(true); }}
      >
        {props.label}
      </button>
      <ConfirmDialog
        open={open()}
        message={props.message}
        confirmLabel={props.confirmLabel}
        cancelLabel={props.cancelLabel}
        onCancel={() => setOpen(false)}
        onConfirm={() => {
          setOpen(false);
          props.onConfirm();
        }}
      />
    </>
  );
}
