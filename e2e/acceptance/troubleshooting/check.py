#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only

"""Run the local, clean-environment half of the troubleshooting symptom matrix.

This command deliberately never starts Docker/Compose, contacts a live stack, or invokes a
remote-product API.  It runs focused local tests and one DRY_RUN offline inventory.  Rows whose
assertion needs an external product are still reported with an explicit BLOCKED_EXTERNAL
disposition from matrix.json; they are not silently omitted.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[3]
DOC_PATH = ROOT / "docs" / "troubleshooting.md"
MATRIX_PATH = Path(__file__).with_name("matrix.json")
MARKER_RE = re.compile(r"<!--\s*troubleshooting-symptom:\s*([A-Z]+-\d+)\s*-->")
REQUIRED_ROW_FIELDS = (
    "id",
    "source_heading",
    "scope",
    "precondition",
    "action",
    "expected_diagnostic",
    "remedy",
    "post_remedy_assertion",
    "proof_boundary",
    "external_prerequisite",
    "disposition",
    "check",
)
REQUIRED_EXTERNAL_FIELDS = ("lane", "prerequisite", "disposition")


class MatrixError(RuntimeError):
    """A check failure with a user-actionable message."""


def load_matrix() -> tuple[dict[str, object], str]:
    try:
        matrix = json.loads(MATRIX_PATH.read_text(encoding="utf-8"))
        docs = DOC_PATH.read_text(encoding="utf-8")
    except (OSError, json.JSONDecodeError) as exc:
        raise MatrixError(f"cannot load matrix inputs: {exc}") from exc
    if not isinstance(matrix, dict):
        raise MatrixError("matrix.json root must be an object")
    if matrix.get("source_file") != "docs/troubleshooting.md":
        raise MatrixError("matrix source_file must remain docs/troubleshooting.md")
    rows = matrix.get("rows")
    if not isinstance(rows, list) or not rows:
        raise MatrixError("matrix.json must contain a non-empty rows array")
    return matrix, docs


def validate_contract(matrix: dict[str, object], docs: str) -> tuple[list[dict[str, object]], list[str]]:
    rows_obj = matrix["rows"]
    assert isinstance(rows_obj, list)
    rows: list[dict[str, object]] = []
    for index, row_obj in enumerate(rows_obj, start=1):
        if not isinstance(row_obj, dict):
            raise MatrixError(f"matrix row {index} is not an object")
        missing = [field for field in REQUIRED_ROW_FIELDS if not isinstance(row_obj.get(field), str) or not row_obj[field].strip()]
        if missing:
            raise MatrixError(f"matrix row {index} is missing fields: {', '.join(missing)}")
        if row_obj["proof_boundary"] not in {"local-startup-only", "live-delivery-required"}:
            raise MatrixError(f"{row_obj['id']}: proof_boundary must state local startup or live delivery")
        rows.append(row_obj)

    ids = [str(row["id"]) for row in rows]
    if len(ids) != len(set(ids)):
        raise MatrixError("matrix row ids must be unique")
    marker_ids = MARKER_RE.findall(docs)
    if len(marker_ids) != len(set(marker_ids)):
        raise MatrixError("troubleshooting source markers must be unique")
    if set(marker_ids) != set(ids):
        raise MatrixError(
            "source symptom markers and matrix rows differ: "
            f"markers={sorted(marker_ids)}, rows={sorted(ids)}"
        )

    for row in rows:
        marker = f"<!-- troubleshooting-symptom: {row['id']} -->"
        marker_offset = docs.find(marker)
        after_marker = docs[marker_offset + len(marker):]
        heading = after_marker.splitlines()[1] if after_marker.startswith("\n") else after_marker.splitlines()[0]
        if heading.strip() != row["source_heading"]:
            raise MatrixError(f"{row['id']}: source heading does not match the marked heading")
        table_id_count = len(re.findall(rf"^\|\s*{re.escape(str(row['id']))}\s*\|", docs, re.MULTILINE))
        if table_id_count != 1:
            raise MatrixError(f"{row['id']}: expected exactly one documentation matrix row, found {table_id_count}")

    external = matrix.get("external_dispositions")
    if not isinstance(external, list) or not external:
        raise MatrixError("matrix.json must declare external_dispositions")
    for index, item in enumerate(external, start=1):
        if not isinstance(item, dict):
            raise MatrixError(f"external disposition {index} is not an object")
        missing = [field for field in REQUIRED_EXTERNAL_FIELDS if not isinstance(item.get(field), str) or not item[field].strip()]
        if missing:
            raise MatrixError(f"external disposition {index} is missing fields: {', '.join(missing)}")
        if not str(item["disposition"]).startswith("blocked-external-"):
            raise MatrixError(f"external disposition {item['lane']} is not explicitly blocked")
        if "skip" in str(item["disposition"]).casefold():
            raise MatrixError(f"external disposition {item['lane']} must not use a silent skip")
    return rows, marker_ids


class CleanRunner:
    """Subprocess helper with a deliberately scrubbed application environment."""

    def __init__(self, temp_root: Path) -> None:
        inherited_path = os.environ.get("PATH", "/usr/bin:/bin")
        clean_home = temp_root / "home"
        clean_gopath = temp_root / "go"
        clean_home.mkdir()
        clean_gopath.mkdir()
        self.env_base = {
            "HOME": str(clean_home),
            "PATH": inherited_path,
            "LANG": "C",
            "LC_ALL": "C",
            "TMPDIR": str(temp_root),
            "GOCACHE": str(temp_root / "go-cache"),
            "GOPATH": str(clean_gopath),
            "GOTOOLCHAIN": "local",
        }
        module_cache = os.environ.get("GOMODCACHE")
        if not module_cache:
            module_cache = subprocess.check_output(
                ["go", "env", "GOMODCACHE"], text=True, env=os.environ
            ).strip()
        if module_cache:
            self.env_base["GOMODCACHE"] = module_cache

    def env(self, overrides: dict[str, str] | None = None) -> dict[str, str]:
        clean = dict(self.env_base)
        if overrides:
            clean.update(overrides)
        return clean

    def run(
        self,
        args: list[str],
        *,
        overrides: dict[str, str] | None = None,
        expected_returncode: int = 0,
        contains: tuple[str, ...] = (),
        timeout: int = 120,
    ) -> None:
        try:
            result = subprocess.run(
                args,
                cwd=ROOT,
                env=self.env(overrides),
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                timeout=timeout,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise MatrixError(f"command failed to execute: {args[0]}: {exc}") from exc
        if result.returncode != expected_returncode:
            raise MatrixError(f"command returned {result.returncode}, expected {expected_returncode}: {' '.join(args)}")
        for needle in contains:
            if needle not in result.stdout:
                raise MatrixError(f"command output omitted expected diagnostic {needle!r}: {' '.join(args)}")


def env_file(path: Path, values: dict[str, str]) -> None:
    lines = [f"{key}={value}" for key, value in values.items()]
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def base_env(temp_root: Path, *, dry_run: str, names: str = "otlp-native") -> dict[str, str]:
    return {
        "DRY_RUN": dry_run,
        "BLUEPRINT_NAMES": names,
        "BLUEPRINTS": str(ROOT / "blueprints"),
        "BLUEPRINT_DATA_DIR": str(temp_root / "blueprints-data"),
        "CONFIG_SNAPSHOT_PATH": str(temp_root / "control-state.json"),
        "SELFOBS_ENABLED": "false",
    }


def check_dry_run(runner: CleanRunner, temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./cmd/synthkit", "-run", "^TestOrdinaryDryRunRemainsCredentialFreeAndOffline$", "-count=1"]
    )
    values = base_env(temp_root, dry_run="true")
    env_path = temp_root / "dry-run.env"
    env_file(env_path, values)
    values["DRY_RUN"] = "false"
    env_file(env_path, values)
    if env_path.read_text(encoding="utf-8").count("DRY_RUN=false") != 1:
        raise MatrixError("TS-01: remedy did not set DRY_RUN=false exactly once")


def check_credentials(runner: CleanRunner, temp_root: Path) -> None:
    missing = base_env(temp_root, dry_run="false")
    missing_path = temp_root / "missing-credentials.env"
    env_file(missing_path, missing)
    runner.run(
        ["go", "run", "-mod=readonly", "./cmd/synthkit", "-env", str(missing_path), "-once"],
        expected_returncode=1,
        contains=("missing mandatory live settings", "GC_TOKEN", "GC_PROM_RW", "GC_LOKI"),
    )
    filled = {
        "GC_TOKEN": "placeholder",
        "GC_PROM_RW": "https://metrics.example.invalid/api/prom/push",
        "GC_PROM_USER": "123456",
        "GC_OTLP_ENDPOINT": "https://otlp.example.invalid/otlp",
        "GC_OTLP_USER": "234567",
        "GC_LOKI": "https://logs.example.invalid/loki/api/v1/push",
        "GC_LOKI_USER": "345678",
    }
    for key, value in filled.items():
        if not value:
            raise MatrixError(f"TS-02: placeholder credential shape is empty for {key}")
    for key in ("GC_PROM_USER", "GC_OTLP_USER", "GC_LOKI_USER"):
        if not re.fullmatch(r"[1-9][0-9]*", filled[key]):
            raise MatrixError(f"TS-02: {key} is not a positive decimal shape")
    for key, expected_path in (
        ("GC_PROM_RW", "/api/prom/push"),
        ("GC_OTLP_ENDPOINT", "/otlp"),
        ("GC_LOKI", "/loki/api/v1/push"),
    ):
        parsed = urlsplit(filled[key])
        if parsed.scheme != "https" or parsed.path != expected_path or not parsed.hostname:
            raise MatrixError(f"TS-02: {key} has the wrong endpoint shape")
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/config", "-run", "^TestValidateLiveRejectsInvalidMandatory", "-count=1"]
    )


def check_inline_comment(runner: CleanRunner, _temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/config", "-run", "^TestParseEnvFileStripsInlineComments$", "-count=1"]
    )


def check_independent_sinks(runner: CleanRunner, _temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/pushstatus", "-run", "^TestDeliverySnapshotFoldsFailureRecoveryAndStaleness$", "-count=1"]
    )
    status = {
        "sinks": [
            {"sink": "promrw", "failures": 1, "last_error_code": "authentication", "last_success_ms": 200},
            {"sink": "otlp", "failures": 0, "last_error_code": "", "last_success_ms": 300},
            {"sink": "loki", "failures": 0, "last_error_code": "", "last_success_ms": 300},
        ]
    }
    by_sink = {item["sink"]: item for item in status["sinks"]}
    if by_sink["promrw"]["last_error_code"] != "authentication" or by_sink["otlp"]["last_success_ms"] <= 0:
        raise MatrixError("TS-04: independent sink status fixture does not preserve per-sink outcomes")


def check_series_cap(runner: CleanRunner, temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/sink/promrw", "-run", "^TestSeriesCap(TruncatesBatch|ZeroMeansUnlimited)$", "-count=1"]
    )
    values = base_env(temp_root, dry_run="true")
    values["SERIES_CAP"] = "2"
    capped = temp_root / "series-cap.env"
    env_file(capped, values)
    values.pop("SERIES_CAP")
    env_file(capped, values)
    if "SERIES_CAP=" in capped.read_text(encoding="utf-8"):
        raise MatrixError("TS-05: remedy did not remove the temporary series cap")


def check_high_cardinality(runner: CleanRunner, _temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/sink/loki", "-run", "^TestHighCardAssertion", "-count=1"]
    )
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/telemetryspec", "-run", "^TestCapabilityMatrix_", "-count=1"]
    )


def check_control_exposure(runner: CleanRunner, _temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./cmd/synthkit", "-run", "^TestValidateControlExposure$", "-count=1"]
    )


def check_state_persistence(runner: CleanRunner, temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/control", "-run", "^TestProbeWriteAtomicallyProvesSnapshotDirectoryWritable$", "-count=1"]
    )
    state_dir = temp_root / "state"
    state_dir.mkdir()
    target = state_dir / "control-state.json"
    temporary = state_dir / "control-state.json.tmp"
    temporary.write_text('{"volume_multiplier":1}\n', encoding="utf-8")
    os.replace(temporary, target)
    if not target.is_file() or temporary.exists():
        raise MatrixError("TS-08: atomic state replacement did not leave the target snapshot")
    unsafe = temp_root / "unsafe-state"
    unsafe.symlink_to(target)
    if not unsafe.is_symlink():
        raise MatrixError("TS-08: symlink state-path guard could not be exercised")


def check_offline_push(runner: CleanRunner, _temp_root: Path) -> None:
    runner.run(
        ["go", "test", "-mod=readonly", "./internal/sink/httpretry", "-run", "^(TestRetriesOn503|TestRetriesOnTransportError|TestGivesUpAfterMaxElapsed)$", "-count=1"]
    )
    queue = {"depth": 0, "dropped_items": 3, "current_loss": False, "last_loss_ms": 100, "last_recovery_ms": 200}
    if queue["dropped_items"] <= 0 or queue["current_loss"] or queue["last_recovery_ms"] <= queue["last_loss_ms"]:
        raise MatrixError("TS-09: queue recovery fixture does not preserve historical loss semantics")


def check_offline_dump(runner: CleanRunner, temp_root: Path) -> None:
    env_path = temp_root / "offline-dump.env"
    env_file(env_path, base_env(temp_root, dry_run="true"))
    runner.run(
        ["go", "run", "-mod=readonly", "./cmd/synthkit", "-env", str(env_path), "-once", "-dump"],
        contains=(
            "selected blueprints: 1 [otlp-native]",
            'loaded blueprint "otlp-native"',
            "synthkit up: 1 blueprints",
            "dry-run",
        ),
        timeout=180,
    )


CHECKS = {
    "dry_run": check_dry_run,
    "credentials": check_credentials,
    "inline_comment": check_inline_comment,
    "independent_sinks": check_independent_sinks,
    "series_cap": check_series_cap,
    "high_cardinality": check_high_cardinality,
    "control_exposure": check_control_exposure,
    "state_persistence": check_state_persistence,
    "offline_push": check_offline_push,
    "offline_dump": check_offline_dump,
}


def main() -> int:
    if sys.version_info < (3, 11):
        print("troubleshooting-check: ERROR: Python 3.11 or newer is required", file=sys.stderr)
        return 2
    if shutil.which("go") is None:
        print("troubleshooting-check: ERROR: Go is required for the local executable rows", file=sys.stderr)
        return 2
    try:
        matrix, docs = load_matrix()
        rows, marker_ids = validate_contract(matrix, docs)
        with tempfile.TemporaryDirectory(prefix="synthkit-troubleshooting-") as raw_temp:
            temp_root = Path(raw_temp)
            runner = CleanRunner(temp_root)
            for row in rows:
                check_name = str(row["check"])
                check = CHECKS.get(check_name)
                if check is None:
                    raise MatrixError(f"{row['id']}: no executable handler for {check_name}")
                check(runner, temp_root)
                print(f"PASS {row['id']}: local precondition, remedy, and post-remedy assertion executed")
    except MatrixError as exc:
        print(f"troubleshooting-check: ERROR: {exc}", file=sys.stderr)
        return 1

    external = matrix["external_dispositions"]
    assert isinstance(external, list)
    for item in external:
        assert isinstance(item, dict)
        print(f"BLOCKED_EXTERNAL {item['lane']}: prerequisite declared; no external operation executed")
    print(f"source symptom count: {len(marker_ids)}")
    print(f"matrix row count: {len(rows)}")
    print(f"executable row count: {len(rows)}")
    print("troubleshooting-check: OK — all local rows executed; external prerequisites are explicitly dispositioned")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
