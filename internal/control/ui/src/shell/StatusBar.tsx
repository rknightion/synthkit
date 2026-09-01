/* SPDX-License-Identifier: Apache-2.0 */
import type { JSX } from "solid-js";
import { useStore } from "../store/store";
import { StatusWord } from "./StatusWord";

export function StatusBar(): JSX.Element {
  const store = useStore();
  const status = () => store.state.status;
  const readiness = () => status()?.readiness;
	const summary = (): "OK" | "WARN" | "FAIL" | "IDLE" => {
		const current = status();
		if (!current) return "IDLE";
		if (readiness()?.persisted_state.writable === false || current.sinks.some((sink) => !!sink.last_error && sink.last_error_ms >= sink.last_success_ms)) return "FAIL";
		if (current.sinks.every((sink) => !sink.last_success_ms)) return "IDLE";
		if (readiness() && !readiness()?.ready) return "WARN";
		return "OK";
	};
  return <footer class="statusbar" role="status" aria-live="polite">
	<StatusWord status={summary()} /> <span>{status()?.dry_run ? "dry run, nothing pushed" : "live push"}</span>
    <span>·</span><span>control token: {store.state.config ? "configured" : "unknown"}</span>
    <span>·</span><span>state: {readiness()?.persisted_state.writable === false ? "not writable" : "writable"}</span>
    <span class="search-hint">⌘K search</span>
  </footer>;
}
