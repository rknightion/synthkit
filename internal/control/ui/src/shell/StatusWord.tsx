/* SPDX-License-Identifier: Apache-2.0 */
import type { JSX } from "solid-js";

type Status = "OK" | "WARN" | "FAIL" | "LIVE" | "MOD" | "OFF" | "IDLE" | "QUEUED" | "DONE" | "SET" | "UNSET";

const statusShape: Record<Status, string> = {
  OK: "■", WARN: "◆", FAIL: "●", LIVE: "●", MOD: "◆", OFF: "■", IDLE: "■", QUEUED: "■", DONE: "■", SET: "■", UNSET: "■",
};

const statusTone: Record<Status, string> = {
  OK: "ok", WARN: "warn", FAIL: "fail", LIVE: "live", MOD: "warn", OFF: "muted", IDLE: "muted", QUEUED: "muted", DONE: "muted", SET: "muted", UNSET: "muted",
};

export function StatusWord(props: { status: Status; note?: string }): JSX.Element {
  return <span class={`status-word ${statusTone[props.status]}`}>{statusShape[props.status]} {props.status}{props.note ? ` ${props.note}` : ""}</span>;
}
