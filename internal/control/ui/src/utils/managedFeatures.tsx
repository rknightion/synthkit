/* SPDX-License-Identifier: Apache-2.0 */
import { Show, type JSX } from "solid-js";

export type ManagedFeature = "blueprint_sources" | "custom_uploads" | "reset";

// Trusted startup metadata, decoded by the browser from an HTML-escaped attribute.
// Presentation only: API permissions remain unchanged.
export function managedReason(feature: ManagedFeature): string | undefined {
  const raw = document.querySelector<HTMLMetaElement>('meta[name="control-managed-features"]')?.content;
  if (!raw) return undefined;
  try {
    const value = JSON.parse(raw)?.[feature];
    return typeof value === "string" && value.trim() ? value : undefined;
  } catch { return undefined; }
}

export function ManagedControls(props: { feature: ManagedFeature; children: JSX.Element }): JSX.Element {
  const reason = managedReason(props.feature);
  return <>
    <Show when={reason}><p role="note">{reason}</p></Show>
    <fieldset disabled={!!reason} style={{ border: "0", padding: "0", margin: "0", "min-width": "0" }}>
      {props.children}
    </fieldset>
  </>;
}
