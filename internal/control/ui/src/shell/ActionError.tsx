import { Show, type Accessor, type JSX } from "solid-js";

// Shared dismissible action-error banner. Replaces the byte-identical inline
// `actionErr` banners previously copied across Overview/Blueprint/Incidents/Global/
// BpManage. Each view keeps its own `actionErr` signal and passes its getter; the
// rendering (look, classes, role="alert", and the new dismiss ×) lives here.
// Renders nothing when message() is falsy. testid is supplied per-view so each
// view's existing action-error testid is preserved verbatim.
export interface ActionErrorProps {
  message: Accessor<string | undefined>;
  testid: string;
  onDismiss: () => void;
}

export function ActionError(props: ActionErrorProps): JSX.Element {
  return (
    <Show when={props.message()}>
      {(m) => (
        <div class="action-err" data-testid={props.testid} role="alert">
          <span class="action-err-msg">{m()}</span>
          <button
            type="button"
            class="action-err-x"
            aria-label="Dismiss"
            onClick={() => props.onDismiss()}
          >
            ×
          </button>
        </div>
      )}
    </Show>
  );
}
