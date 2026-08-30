#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only

"""Secret-safe deployment state and image-selector operations for synthkit."""

from __future__ import annotations

import argparse
import ctypes
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
from typing import NoReturn


IMAGE_REPOSITORY = "ghcr.io/rknightion/synthkit"
IMAGE_REF_RE = re.compile(
    r"^ghcr\.io/rknightion/synthkit(?:@sha256:[0-9a-f]{64}|:[A-Za-z0-9][A-Za-z0-9._-]{0,127})$"
)
RECORD_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,63}$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
REVISION_RE = re.compile(r"^[0-9a-f]{40}$")
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$|^[0-9a-f]{7,40}$")
RECORD_FIELDS = {
    "configured_ref",
    "index_digest",
    "platform_manifest_digest",
    "oci_config_digest",
    "running_image_id",
    "version",
    "revision",
    "state_manifest_sha256",
}
COSIGN_IMAGE = "ghcr.io/sigstore/cosign/cosign@sha256:de9c65609e6bde17e6b48de485ee788407c9502fa08b8f4459f595b21f56cd00"
SIGNER_IDENTITY_PATTERN = (
    r"^https://github\.com/rknightion/\.github/\.github/workflows/"
    r"container-publish\.yml@[A-Za-z0-9._/-]+$"
)
SIGNER_WORKFLOW = "rknightion/.github/.github/workflows/container-publish.yml"


class DeployError(Exception):
    """An operator-safe deployment contract failure."""


def fail(message: str) -> NoReturn:
    print(json.dumps({"status": "error", "code": message}, sort_keys=True), file=sys.stderr)
    raise SystemExit(1)


def require_regular_file(path: Path) -> None:
    try:
        mode = path.lstat().st_mode
    except FileNotFoundError as exc:
        raise DeployError("file_not_found") from exc
    if not stat.S_ISREG(mode):
        raise DeployError("file_not_regular")


def selector_values_bytes(content: bytes) -> dict[str, str]:
    found: dict[str, str] = {}
    for raw_line in content.decode("utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            if re.match(r"^(?:export\s+)?SYNTHKIT_IMAGE_(?:REF|TAG)(?:\s|$)", line):
                raise DeployError("image_selector_syntax_invalid")
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        key = re.sub(r"^export[ \t]+", "", key)
        if key not in {"SYNTHKIT_IMAGE_REF", "SYNTHKIT_IMAGE_TAG"}:
            continue
        if key in found:
            raise DeployError("duplicate_image_selector")
        value = value.strip()
        quote: str | None = None
        escaped = False
        for index, character in enumerate(value):
            if quote == '"':
                if escaped:
                    escaped = False
                elif character == "\\":
                    escaped = True
                elif character == quote:
                    quote = None
            elif quote == "'":
                if character == quote:
                    quote = None
            elif character in {"'", '"'}:
                quote = character
            elif character == "#" and index > 0 and value[index - 1].isspace():
                value = value[:index].rstrip()
                break
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
            value = value[1:-1]
        found[key] = value
    return found


def selector_values(env_file: Path) -> dict[str, str]:
    require_regular_file(env_file)
    return selector_values_bytes(env_file.read_bytes())


def validate_image_ref(reference: str) -> None:
    if not IMAGE_REF_RE.fullmatch(reference):
        raise DeployError("invalid_image_reference")


def is_mutable(reference: str) -> bool:
    return reference in {f"{IMAGE_REPOSITORY}:main", f"{IMAGE_REPOSITORY}:latest"}


def resolve_image(env_file: Path, default_ref: str) -> dict[str, object]:
    validate_image_ref(default_ref)
    selectors = selector_values(env_file)
    preferred = selectors.get("SYNTHKIT_IMAGE_REF", "")
    legacy = selectors.get("SYNTHKIT_IMAGE_TAG", "")
    if preferred:
        reference = preferred
        source = "SYNTHKIT_IMAGE_REF"
        legacy_state = "ignored" if legacy else "absent"
    elif legacy:
        reference = f"{IMAGE_REPOSITORY}:{legacy}"
        source = "SYNTHKIT_IMAGE_TAG"
        legacy_state = "selected"
    else:
        reference = default_ref
        source = "default"
        legacy_state = "absent"
    validate_image_ref(reference)
    return {
        "legacy_tag": legacy_state,
        "mutable": is_mutable(reference),
        "reference": reference,
        "source": source,
        "status": "ok",
    }


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def read_all(descriptor: int) -> bytes:
    os.lseek(descriptor, 0, os.SEEK_SET)
    chunks: list[bytes] = []
    while True:
        chunk = os.read(descriptor, 1024 * 1024)
        if not chunk:
            return b"".join(chunks)
        chunks.append(chunk)


def atomic_exchange(first: Path, second: Path) -> None:
    libc = ctypes.CDLL(None, use_errno=True)
    first_bytes = os.fsencode(first)
    second_bytes = os.fsencode(second)
    if sys.platform == "linux":
        try:
            renameat2 = libc.renameat2
        except AttributeError as exc:
            raise DeployError("atomic_exchange_unsupported") from exc
        renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        renameat2.restype = ctypes.c_int
        result = renameat2(-100, first_bytes, -100, second_bytes, 2)  # AT_FDCWD, RENAME_EXCHANGE
    elif sys.platform == "darwin":
        try:
            renamex_np = libc.renamex_np
        except AttributeError as exc:
            raise DeployError("atomic_exchange_unsupported") from exc
        renamex_np.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint]
        renamex_np.restype = ctypes.c_int
        result = renamex_np(first_bytes, second_bytes, 2)  # RENAME_SWAP
    else:
        raise DeployError("atomic_exchange_unsupported")
    if result != 0:
        error_number = ctypes.get_errno()
        raise OSError(error_number, os.strerror(error_number))


def replace_selector(content: bytes, reference: str) -> bytes:
    lines = content.splitlines(keepends=True)
    replacement = f"SYNTHKIT_IMAGE_REF={reference}".encode()
    found = 0
    for index, line in enumerate(lines):
        body = line.rstrip(b"\r\n")
        ending = line[len(body) :]
        match = re.match(br"^[ \t]*(export[ \t]+)?SYNTHKIT_IMAGE_REF[ \t]*=", body)
        if match:
            found += 1
            export_prefix = b"export " if match.group(1) else b""
            lines[index] = export_prefix + replacement + ending
    if found > 1:
        raise DeployError("duplicate_image_selector")
    if found == 0:
        if content and not content.endswith((b"\n", b"\r")):
            lines.append(b"\n")
        lines.append(replacement + b"\n")
    return b"".join(lines)


def set_image(env_file: Path, expected_sha256: str, reference: str, allow_mutable: bool) -> dict[str, object]:
    validate_image_ref(reference)
    if not SHA256_RE.fullmatch(expected_sha256):
        raise DeployError("expected_env_hash_invalid")
    if is_mutable(reference) and not allow_mutable:
        raise DeployError("mutable_reference_requires_acknowledgement")
    env_file = absolute_without_resolving(env_file)
    require_regular_file(env_file)
    env_stat = env_file.lstat()
    if env_stat.st_mode & 0o077 or not env_stat.st_mode & stat.S_IWUSR:
        raise DeployError("env_file_permissions")

    lock_path = env_file.with_name(env_file.name + ".synthkit.lock")
    lock_flags = os.O_CREAT | os.O_RDWR
    if hasattr(os, "O_NOFOLLOW"):
        lock_flags |= os.O_NOFOLLOW
    try:
        lock_fd = os.open(lock_path, lock_flags, 0o600)
    except OSError as exc:
        raise DeployError("lock_unavailable") from exc
    try:
        if not stat.S_ISREG(os.fstat(lock_fd).st_mode):
            raise DeployError("lock_not_regular")
        os.fchmod(lock_fd, 0o600)
        fcntl.flock(lock_fd, fcntl.LOCK_EX)

        env_flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
        try:
            env_fd = os.open(env_file, env_flags)
        except OSError as exc:
            raise DeployError("env_file_unavailable") from exc
        try:
            fcntl.flock(env_fd, fcntl.LOCK_EX)
            opened_stat = os.fstat(env_fd)
            path_stat = env_file.lstat()
            if (
                not stat.S_ISREG(opened_stat.st_mode)
                or opened_stat.st_dev != path_stat.st_dev
                or opened_stat.st_ino != path_stat.st_ino
                or stat.S_IMODE(opened_stat.st_mode) != stat.S_IMODE(env_stat.st_mode)
                or opened_stat.st_uid != env_stat.st_uid
                or opened_stat.st_gid != env_stat.st_gid
            ):
                raise DeployError("env_compare_and_swap_failed")
            content = read_all(env_fd)
            current_hash = sha256_bytes(content)
            if current_hash != expected_sha256:
                raise DeployError("env_compare_and_swap_failed")
            # Validate the selectors from the exact bytes bound by the CAS; values are never returned.
            selectors = selector_values_bytes(content)
            updated = replace_selector(content, reference)
            updated_hash = sha256_bytes(updated)

            temp_fd, temp_name = tempfile.mkstemp(prefix=env_file.name + ".tmp-", dir=env_file.parent)
            try:
                os.fchmod(temp_fd, opened_stat.st_mode & 0o777)
                os.fchown(temp_fd, opened_stat.st_uid, opened_stat.st_gid)
                with os.fdopen(temp_fd, "wb", closefd=True) as handle:
                    handle.write(updated)
                    handle.flush()
                    os.fsync(handle.fileno())
                temp_fd = -1

                candidate_stat = os.stat(temp_name, follow_symlinks=False)
                atomic_exchange(Path(temp_name), env_file)
                displaced_stat = os.stat(temp_name, follow_symlinks=False)
                selected_matches = False
                try:
                    selected_fd = os.open(env_file, env_flags)
                except OSError:
                    selected_fd = -1
                if selected_fd >= 0:
                    try:
                        selected_stat = os.fstat(selected_fd)
                        selected_content = read_all(selected_fd)
                        selected_path_stat = env_file.lstat()
                        selected_matches = (
                            selected_stat.st_dev == candidate_stat.st_dev
                            and selected_stat.st_ino == candidate_stat.st_ino
                            and selected_path_stat.st_dev == candidate_stat.st_dev
                            and selected_path_stat.st_ino == candidate_stat.st_ino
                            and sha256_bytes(selected_content) == updated_hash
                        )
                    finally:
                        os.close(selected_fd)
                matched = (
                    displaced_stat.st_dev == opened_stat.st_dev
                    and displaced_stat.st_ino == opened_stat.st_ino
                    and stat.S_IMODE(displaced_stat.st_mode) == stat.S_IMODE(opened_stat.st_mode)
                    and displaced_stat.st_uid == opened_stat.st_uid
                    and displaced_stat.st_gid == opened_stat.st_gid
                    and sha256_bytes(read_all(env_fd)) == expected_sha256
                    and selected_matches
                )
                if not matched:
                    try:
                        current_selected = env_file.lstat()
                    except FileNotFoundError:
                        current_selected = None
                    if (
                        current_selected is not None
                        and current_selected.st_dev == candidate_stat.st_dev
                        and current_selected.st_ino == candidate_stat.st_ino
                    ):
                        atomic_exchange(Path(temp_name), env_file)
                    fsync_directory(env_file.parent)
                    raise DeployError("env_compare_and_swap_failed")
                os.unlink(temp_name)
                fsync_directory(env_file.parent)
            finally:
                if temp_fd >= 0:
                    os.close(temp_fd)
                try:
                    os.unlink(temp_name)
                except FileNotFoundError:
                    pass
        finally:
            os.close(env_fd)
        return {
            "legacy_tag": "ignored" if selectors.get("SYNTHKIT_IMAGE_TAG") else "absent",
            "previous_sha256": current_hash,
            "reference": reference,
            "sha256": updated_hash,
            "status": "ok",
        }
    finally:
        os.close(lock_fd)


def absolute_without_resolving(path: Path) -> Path:
    return Path(os.path.abspath(path))


def reject_symlink_components(path: Path, *, allow_missing_leaf: bool = False) -> None:
    path = absolute_without_resolving(path)
    current = Path(path.anchor)
    parts = path.parts[1:] if path.anchor else path.parts
    for index, part in enumerate(parts):
        current /= part
        try:
            mode = current.lstat().st_mode
        except FileNotFoundError as exc:
            if allow_missing_leaf and index == len(parts) - 1:
                return
            raise DeployError("path_component_missing") from exc
        if stat.S_ISLNK(mode):
            raise DeployError("symlink_path_rejected")


def ensure_records_dir(records_dir: Path, checkout_root: Path) -> Path:
    records_dir = absolute_without_resolving(records_dir)
    checkout_root = absolute_without_resolving(checkout_root)
    reject_symlink_components(checkout_root)
    reject_symlink_components(records_dir.parent)
    try:
        if os.path.commonpath((records_dir, checkout_root)) == str(checkout_root):
            raise DeployError("records_inside_checkout")
    except ValueError as exc:
        raise DeployError("invalid_records_path") from exc
    try:
        mode = records_dir.lstat().st_mode
    except FileNotFoundError:
        records_dir.mkdir(mode=0o700)
    else:
        if not stat.S_ISDIR(mode):
            raise DeployError("records_not_directory")
    os.chmod(records_dir, 0o700)
    reject_symlink_components(records_dir)
    return records_dir


def require_container_stopped(docker_bin: str, container: str) -> None:
    if not container:
        raise DeployError("container_required")
    try:
        result = subprocess.run(
            [docker_bin, "inspect", "--format", "{{.State.Running}}", container],
            text=True,
            capture_output=True,
            check=False,
            timeout=30,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise DeployError("container_state_unavailable") from exc
    if result.returncode != 0:
        raise DeployError("container_state_unavailable")
    state = result.stdout.strip()
    if state == "true":
        raise DeployError("container_not_quiesced")
    if state != "false":
        raise DeployError("container_state_invalid")


def mode_string(mode: int) -> str:
    return f"{stat.S_IMODE(mode):04o}"


def write_all(descriptor: int, content: bytes) -> None:
    offset = 0
    while offset < len(content):
        written = os.write(descriptor, content[offset:])
        if written <= 0:
            raise OSError("short write")
        offset += written


def fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def copy_snapshot_tree(source: Path, destination: Path) -> list[dict[str, object]]:
    entries: list[dict[str, object]] = []

    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)

    def visit(source_fd: int, destination_fd: int, prefix: tuple[str, ...]) -> None:
        for name in sorted(os.listdir(source_fd)):
            child_stat = os.stat(name, dir_fd=source_fd, follow_symlinks=False)
            relative_parts = prefix + (name,)
            relative = "/".join(relative_parts)
            if stat.S_ISLNK(child_stat.st_mode):
                raise DeployError("state_symlink_rejected")
            if stat.S_ISDIR(child_stat.st_mode):
                os.mkdir(name, mode=0o700, dir_fd=destination_fd)
                os.chmod(name, 0o700, dir_fd=destination_fd, follow_symlinks=False)
                os.fsync(destination_fd)
                child_source_fd = os.open(name, directory_flags, dir_fd=source_fd)
                child_destination_fd = os.open(name, directory_flags, dir_fd=destination_fd)
                try:
                    opened_stat = os.fstat(child_source_fd)
                    if opened_stat.st_dev != child_stat.st_dev or opened_stat.st_ino != child_stat.st_ino:
                        raise DeployError("state_entry_changed")
                    entries.append(
                        {
                            "gid": opened_stat.st_gid,
                            "mode": mode_string(opened_stat.st_mode),
                            "path": relative,
                            "type": "directory",
                            "uid": opened_stat.st_uid,
                        }
                    )
                    visit(child_source_fd, child_destination_fd, relative_parts)
                finally:
                    os.close(child_source_fd)
                    os.close(child_destination_fd)
                continue
            if not stat.S_ISREG(child_stat.st_mode):
                raise DeployError("state_special_file_rejected")
            source_fd_child = os.open(
                name,
                os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
                dir_fd=source_fd,
            )
            opened_stat = os.fstat(source_fd_child)
            if (
                not stat.S_ISREG(opened_stat.st_mode)
                or opened_stat.st_dev != child_stat.st_dev
                or opened_stat.st_ino != child_stat.st_ino
            ):
                os.close(source_fd_child)
                raise DeployError("state_entry_changed")
            target_fd = os.open(
                name,
                os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, "O_NOFOLLOW", 0),
                0o600,
                dir_fd=destination_fd,
            )
            os.fchmod(target_fd, 0o600)
            digest = hashlib.sha256()
            try:
                while True:
                    chunk = os.read(source_fd_child, 1024 * 1024)
                    if not chunk:
                        break
                    digest.update(chunk)
                    write_all(target_fd, chunk)
                os.fsync(target_fd)
                final_stat = os.fstat(source_fd_child)
            finally:
                os.close(source_fd_child)
                os.close(target_fd)
            if final_stat.st_size != opened_stat.st_size or final_stat.st_mtime_ns != opened_stat.st_mtime_ns:
                raise DeployError("state_entry_changed")
            os.fsync(destination_fd)
            entries.append(
                {
                    "gid": opened_stat.st_gid,
                    "mode": mode_string(opened_stat.st_mode),
                    "path": relative,
                    "sha256": digest.hexdigest(),
                    "size": opened_stat.st_size,
                    "type": "file",
                    "uid": opened_stat.st_uid,
                }
            )

    source_root_fd = os.open(source, directory_flags)
    destination_root_fd = os.open(destination, directory_flags)
    try:
        os.fchmod(destination_root_fd, 0o700)
        opened_root = os.fstat(source_root_fd)
        current_root = source.lstat()
        if opened_root.st_dev != current_root.st_dev or opened_root.st_ino != current_root.st_ino:
            raise DeployError("state_entry_changed")
        visit(source_root_fd, destination_root_fd, ())
        final_root = source.lstat()
        if opened_root.st_dev != final_root.st_dev or opened_root.st_ino != final_root.st_ino:
            raise DeployError("state_entry_changed")
        os.fsync(destination_root_fd)
    finally:
        os.close(source_root_fd)
        os.close(destination_root_fd)
    return entries


def write_exclusive_private(path: Path, content: bytes) -> None:
    flags = os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags, 0o600)
    try:
        os.fchmod(descriptor, 0o600)
        write_all(descriptor, content)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def snapshot_state(
    state_dir: Path,
    records_dir: Path,
    checkout_root: Path,
    name: str,
    container: str,
    docker_bin: str,
) -> dict[str, object]:
    if not RECORD_NAME_RE.fullmatch(name):
        raise DeployError("invalid_record_name")
    state_dir = absolute_without_resolving(state_dir)
    reject_symlink_components(state_dir)
    state_stat = state_dir.lstat()
    if not stat.S_ISDIR(state_stat.st_mode):
        raise DeployError("state_not_directory")
    require_container_stopped(docker_bin, container)
    records_dir = ensure_records_dir(records_dir, checkout_root)
    final_dir = records_dir / name
    if final_dir.exists() or final_dir.is_symlink():
        raise DeployError("record_already_exists")

    temporary = Path(tempfile.mkdtemp(prefix=f".{name}.tmp-", dir=records_dir))
    os.chmod(temporary, 0o700)
    try:
        data_dir = temporary / "data"
        data_dir.mkdir(mode=0o700)
        os.chmod(data_dir, 0o700)
        entries = copy_snapshot_tree(state_dir, data_dir)
        manifest = {
            "entries": entries,
            "root": {
                "gid": state_stat.st_gid,
                "mode": mode_string(state_stat.st_mode),
                "type": "directory",
                "uid": state_stat.st_uid,
            },
            "schema": "synthkit-state-snapshot-v1",
        }
        manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
        write_exclusive_private(temporary / "manifest.json", manifest_bytes)
        fsync_directory(temporary)
        os.replace(temporary, final_dir)
        fsync_directory(records_dir)
        return {
            "manifest_sha256": sha256_bytes(manifest_bytes),
            "name": name,
            "status": "ok",
        }
    except Exception:
        if temporary.exists():
            shutil.rmtree(temporary)
        raise


def validated_manifest(snapshot_dir: Path, expected_sha256: str) -> tuple[dict[str, object], bytes]:
    reject_symlink_components(snapshot_dir)
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    snapshot_fd = os.open(snapshot_dir, directory_flags)
    try:
        manifest_fd = os.open(
            "manifest.json",
            os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
            dir_fd=snapshot_fd,
        )
        try:
            manifest_stat = os.fstat(manifest_fd)
            if not stat.S_ISREG(manifest_stat.st_mode):
                raise DeployError("file_not_regular")
            if stat.S_IMODE(manifest_stat.st_mode) != 0o600:
                raise DeployError("manifest_permissions")
            manifest_bytes = read_all(manifest_fd)
        finally:
            os.close(manifest_fd)
    finally:
        os.close(snapshot_fd)
    if sha256_bytes(manifest_bytes) != expected_sha256:
        raise DeployError("manifest_hash_mismatch")
    try:
        manifest = json.loads(manifest_bytes)
    except json.JSONDecodeError as exc:
        raise DeployError("manifest_invalid") from exc
    if manifest.get("schema") != "synthkit-state-snapshot-v1":
        raise DeployError("manifest_schema_unsupported")
    if not isinstance(manifest.get("root"), dict) or not isinstance(manifest.get("entries"), list):
        raise DeployError("manifest_invalid")
    return manifest, manifest_bytes


def safe_relative_path(value: object) -> Path:
    if not isinstance(value, str) or not value or "\\" in value:
        raise DeployError("manifest_path_invalid")
    path = Path(value)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise DeployError("manifest_path_invalid")
    return path


def validate_snapshot_entries(snapshot_dir: Path, manifest: dict[str, object]) -> list[dict[str, object]]:
    data_dir = snapshot_dir / "data"
    reject_symlink_components(data_dir)
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    data_fd = os.open(data_dir, directory_flags)
    data_stat = os.fstat(data_fd)
    if stat.S_IMODE(data_stat.st_mode) != 0o700:
        os.close(data_fd)
        raise DeployError("snapshot_data_permissions")
    entries = manifest["entries"]
    assert isinstance(entries, list)
    normalized: list[dict[str, object]] = []
    expected_paths: set[str] = set()
    try:
        for raw in entries:
            if not isinstance(raw, dict):
                raise DeployError("manifest_entry_invalid")
            relative = safe_relative_path(raw.get("path"))
            path_text = relative.as_posix()
            if path_text in expected_paths:
                raise DeployError("manifest_duplicate_path")
            expected_paths.add(path_text)
            kind = raw.get("type")
            parts = relative.parts
            parent_fd = open_directory_beneath(data_fd, parts[:-1])
            try:
                if kind == "directory":
                    stored_fd = os.open(parts[-1], directory_flags, dir_fd=parent_fd)
                elif kind == "file":
                    stored_fd = os.open(
                        parts[-1],
                        os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
                        dir_fd=parent_fd,
                    )
                else:
                    raise DeployError("manifest_entry_type_invalid")
                try:
                    stored_stat = os.fstat(stored_fd)
                    if kind == "directory":
                        if not stat.S_ISDIR(stored_stat.st_mode) or stat.S_IMODE(stored_stat.st_mode) != 0o700:
                            raise DeployError("snapshot_directory_invalid")
                    else:
                        if not stat.S_ISREG(stored_stat.st_mode) or stat.S_IMODE(stored_stat.st_mode) != 0o600:
                            raise DeployError("snapshot_file_invalid")
                        content = read_all(stored_fd)
                        if sha256_bytes(content) != raw.get("sha256") or len(content) != raw.get("size"):
                            raise DeployError("snapshot_file_hash_mismatch")
                finally:
                    os.close(stored_fd)
            finally:
                os.close(parent_fd)
            try:
                mode = int(str(raw["mode"]), 8)
                uid = int(raw["uid"])
                gid = int(raw["gid"])
            except (KeyError, TypeError, ValueError) as exc:
                raise DeployError("manifest_metadata_invalid") from exc
            if mode < 0 or mode > 0o7777 or uid < 0 or gid < 0:
                raise DeployError("manifest_metadata_invalid")
            normalized.append(
                {**raw, "path": path_text, "mode_value": mode, "uid_value": uid, "gid_value": gid}
            )

        actual_paths: set[str] = set()

        def inventory(directory_fd: int, prefix: tuple[str, ...]) -> None:
            for name in os.listdir(directory_fd):
                child_stat = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
                path_parts = prefix + (name,)
                actual_paths.add("/".join(path_parts))
                if stat.S_ISLNK(child_stat.st_mode):
                    raise DeployError("snapshot_symlink_rejected")
                if stat.S_ISDIR(child_stat.st_mode):
                    child_fd = os.open(name, directory_flags, dir_fd=directory_fd)
                    try:
                        inventory(child_fd, path_parts)
                    finally:
                        os.close(child_fd)
                elif not stat.S_ISREG(child_stat.st_mode):
                    raise DeployError("state_special_file_rejected")

        inventory(data_fd, ())
        if actual_paths != expected_paths:
            raise DeployError("snapshot_inventory_mismatch")
        return normalized
    finally:
        os.close(data_fd)


def open_directory_beneath(root_fd: int, parts: tuple[str, ...]) -> int:
    descriptor = os.dup(root_fd)
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        for part in parts:
            child = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        return descriptor
    except Exception:
        os.close(descriptor)
        raise


def restore_metadata(descriptor: int, uid: int, gid: int, mode: int) -> None:
    try:
        os.fchown(descriptor, uid, gid)
        os.fchmod(descriptor, mode)
    except OSError as exc:
        if exc.errno in {errno.EPERM, errno.EACCES}:
            raise DeployError("restore_requires_elevated_privileges") from exc
        raise


def restore_snapshot_tree(snapshot_dir: Path, staging_fd: int, entries: list[dict[str, object]]) -> None:
    data_dir = snapshot_dir / "data"
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    data_fd = os.open(data_dir, directory_flags)
    directories = sorted(
        (entry for entry in entries if entry["type"] == "directory"),
        key=lambda entry: len(Path(str(entry["path"])).parts),
    )
    try:
        for entry in directories:
            parts = Path(str(entry["path"])).parts
            parent_fd = open_directory_beneath(staging_fd, parts[:-1])
            try:
                os.mkdir(parts[-1], mode=0o700, dir_fd=parent_fd)
                os.fsync(parent_fd)
            finally:
                os.close(parent_fd)
        for entry in (entry for entry in entries if entry["type"] == "file"):
            parts = Path(str(entry["path"])).parts
            source_parent_fd = open_directory_beneath(data_fd, parts[:-1])
            target_parent_fd = open_directory_beneath(staging_fd, parts[:-1])
            try:
                source_fd = os.open(
                    parts[-1],
                    os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
                    dir_fd=source_parent_fd,
                )
                target_fd = os.open(
                    parts[-1],
                    os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, "O_NOFOLLOW", 0),
                    0o600,
                    dir_fd=target_parent_fd,
                )
                digest = hashlib.sha256()
                try:
                    while True:
                        chunk = os.read(source_fd, 1024 * 1024)
                        if not chunk:
                            break
                        digest.update(chunk)
                        write_all(target_fd, chunk)
                    restore_metadata(
                        target_fd,
                        int(entry["uid_value"]),
                        int(entry["gid_value"]),
                        int(entry["mode_value"]),
                    )
                    os.fsync(target_fd)
                finally:
                    os.close(source_fd)
                    os.close(target_fd)
                if digest.hexdigest() != entry["sha256"]:
                    raise DeployError("snapshot_file_hash_mismatch")
                os.fsync(target_parent_fd)
            finally:
                os.close(source_parent_fd)
                os.close(target_parent_fd)
        for entry in reversed(directories):
            parts = Path(str(entry["path"])).parts
            parent_fd = open_directory_beneath(staging_fd, parts[:-1])
            try:
                target_directory_fd = os.open(parts[-1], directory_flags, dir_fd=parent_fd)
                try:
                    restore_metadata(
                        target_directory_fd,
                        int(entry["uid_value"]),
                        int(entry["gid_value"]),
                        int(entry["mode_value"]),
                    )
                    os.fsync(target_directory_fd)
                finally:
                    os.close(target_directory_fd)
            finally:
                os.close(parent_fd)
        os.fsync(staging_fd)
    finally:
        os.close(data_fd)


def restore_state(
    state_dir: Path,
    expected_root: Path,
    records_dir: Path,
    name: str,
    expected_manifest_sha256: str,
    container: str,
    docker_bin: str,
) -> dict[str, object]:
    if not RECORD_NAME_RE.fullmatch(name):
        raise DeployError("invalid_record_name")
    state_dir = absolute_without_resolving(state_dir)
    expected_root = absolute_without_resolving(expected_root)
    records_dir = absolute_without_resolving(records_dir)
    reject_symlink_components(expected_root)
    reject_symlink_components(state_dir)
    reject_symlink_components(records_dir)
    if os.path.commonpath((state_dir, expected_root)) != str(expected_root) or state_dir == expected_root:
        raise DeployError("state_outside_expected_root")
    if not stat.S_ISDIR(state_dir.lstat().st_mode):
        raise DeployError("state_not_directory")
    require_container_stopped(docker_bin, container)
    snapshot_dir = records_dir / name
    manifest, _ = validated_manifest(snapshot_dir, expected_manifest_sha256)
    entries = validate_snapshot_entries(snapshot_dir, manifest)
    root = manifest["root"]
    assert isinstance(root, dict)
    try:
        root_mode = int(str(root["mode"]), 8)
        root_uid = int(root["uid"])
        root_gid = int(root["gid"])
    except (KeyError, TypeError, ValueError) as exc:
        raise DeployError("manifest_metadata_invalid") from exc
    if root_mode < 0 or root_mode > 0o7777 or root_uid < 0 or root_gid < 0:
        raise DeployError("manifest_metadata_invalid")

    parent = state_dir.parent
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    parent_fd = os.open(parent, directory_flags)
    parent_stat = os.fstat(parent_fd)
    current_parent_stat = parent.lstat()
    if parent_stat.st_dev != current_parent_stat.st_dev or parent_stat.st_ino != current_parent_stat.st_ino:
        os.close(parent_fd)
        raise DeployError("state_parent_changed")
    state_fd = os.open(state_dir.name, directory_flags, dir_fd=parent_fd)
    state_opened_stat = os.fstat(state_fd)
    os.close(state_fd)
    staging_name = f".{state_dir.name}.restore-{secrets.token_hex(6)}"
    os.mkdir(staging_name, mode=0o700, dir_fd=parent_fd)
    os.chmod(staging_name, 0o700, dir_fd=parent_fd, follow_symlinks=False)
    staging = parent / staging_name
    staging_fd = os.open(staging_name, directory_flags, dir_fd=parent_fd)
    displaced_name = f".{state_dir.name}.displaced-{secrets.token_hex(6)}"
    displaced = parent / displaced_name
    try:
        restore_snapshot_tree(snapshot_dir, staging_fd, entries)
        restore_metadata(staging_fd, root_uid, root_gid, root_mode)
        os.fsync(staging_fd)
        current_state_stat = os.stat(state_dir.name, dir_fd=parent_fd, follow_symlinks=False)
        current_parent_stat = parent.lstat()
        if (
            current_state_stat.st_dev != state_opened_stat.st_dev
            or current_state_stat.st_ino != state_opened_stat.st_ino
            or current_parent_stat.st_dev != parent_stat.st_dev
            or current_parent_stat.st_ino != parent_stat.st_ino
        ):
            raise DeployError("state_target_changed")
        os.rename(state_dir.name, displaced_name, src_dir_fd=parent_fd, dst_dir_fd=parent_fd)
        try:
            os.rename(staging_name, state_dir.name, src_dir_fd=parent_fd, dst_dir_fd=parent_fd)
        except Exception:
            os.rename(displaced_name, state_dir.name, src_dir_fd=parent_fd, dst_dir_fd=parent_fd)
            raise
        os.fsync(parent_fd)
        return {
            "displaced": displaced.name,
            "manifest_sha256": expected_manifest_sha256,
            "status": "ok",
        }
    except Exception:
        if staging.exists():
            shutil.rmtree(staging)
        raise
    finally:
        os.close(staging_fd)
        os.close(parent_fd)


def parse_record_fields(values: list[str]) -> dict[str, str]:
    fields: dict[str, str] = {}
    for item in values:
        if "=" not in item:
            raise DeployError("record_field_invalid")
        key, value = item.split("=", 1)
        if key not in RECORD_FIELDS:
            raise DeployError("record_field_unknown")
        if key in fields:
            raise DeployError("record_field_duplicate")
        fields[key] = value
    if set(fields) != RECORD_FIELDS:
        raise DeployError("record_fields_incomplete")
    validate_image_ref(fields["configured_ref"])
    for key in ("index_digest", "platform_manifest_digest", "oci_config_digest", "running_image_id"):
        if not DIGEST_RE.fullmatch(fields[key]):
            raise DeployError("record_digest_invalid")
    if not SHA256_RE.fullmatch(fields["state_manifest_sha256"]):
        raise DeployError("record_state_hash_invalid")
    if not REVISION_RE.fullmatch(fields["revision"]):
        raise DeployError("record_revision_invalid")
    if not VERSION_RE.fullmatch(fields["version"]):
        raise DeployError("record_version_invalid")
    if "@" in fields["configured_ref"] and fields["configured_ref"].rsplit("@", 1)[1] != fields["index_digest"]:
        raise DeployError("record_index_mismatch")
    if fields["running_image_id"] not in {fields["oci_config_digest"], fields["index_digest"]}:
        raise DeployError("record_runtime_identity_mismatch")
    return fields


def write_record(
    records_dir: Path,
    checkout_root: Path,
    name: str,
    field_values: list[str],
) -> dict[str, object]:
    if not RECORD_NAME_RE.fullmatch(name):
        raise DeployError("invalid_record_name")
    fields = parse_record_fields(field_values)
    records_dir = ensure_records_dir(records_dir, checkout_root)
    record_path = records_dir / f"{name}.json"
    if record_path.exists() or record_path.is_symlink():
        raise DeployError("record_already_exists")
    record = {
        "identity": fields,
        "schema": "synthkit-deployment-record-v1",
    }
    record_bytes = (json.dumps(record, indent=2, sort_keys=True) + "\n").encode()
    write_exclusive_private(record_path, record_bytes)
    fsync_directory(records_dir)
    return {
        "name": name,
        "record_sha256": sha256_bytes(record_bytes),
        "status": "ok",
    }


def run_closed(
    command: list[str],
    error_code: str,
    *,
    timeout: int = 120,
    environment: dict[str, str] | None = None,
) -> str:
    try:
        result = subprocess.run(
            command,
            text=True,
            capture_output=True,
            check=False,
            timeout=timeout,
            env=environment,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise DeployError(error_code) from exc
    if result.returncode != 0:
        raise DeployError(error_code)
    return result.stdout


def verify_image(
    reference: str,
    expected_version: str,
    expected_oci_version: str,
    expected_revision: str,
    source_ref: str,
    platform: str,
    docker_bin: str,
    gh_bin: str,
) -> dict[str, object]:
    validate_image_ref(reference)
    if "@" not in reference:
        raise DeployError("image_digest_required")
    repository, index_digest = reference.rsplit("@", 1)
    if not DIGEST_RE.fullmatch(index_digest):
        raise DeployError("image_digest_required")
    if not VERSION_RE.fullmatch(expected_version):
        raise DeployError("expected_version_invalid")
    if not re.fullmatch(r"v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?", expected_oci_version):
        raise DeployError("expected_oci_version_invalid")
    if not REVISION_RE.fullmatch(expected_revision):
        raise DeployError("expected_revision_invalid")
    if not re.fullmatch(r"refs/(?:heads|tags)/[A-Za-z0-9._/-]+", source_ref):
        raise DeployError("source_ref_invalid")
    try:
        platform_os, platform_arch = platform.split("/", 1)
    except ValueError as exc:
        raise DeployError("platform_invalid") from exc

    index_raw = run_closed(
        [docker_bin, "buildx", "imagetools", "inspect", "--raw", reference],
        "index_inspection_failed",
    )
    try:
        index = json.loads(index_raw)
        manifests = [
            item
            for item in index["manifests"]
            if item.get("platform", {}).get("os") == platform_os
            and item.get("platform", {}).get("architecture") == platform_arch
        ]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise DeployError("index_manifest_invalid") from exc
    if index.get("mediaType") != "application/vnd.oci.image.index.v1+json" or len(manifests) != 1:
        raise DeployError("index_manifest_invalid")
    platform_digest = manifests[0].get("digest", "")
    if not DIGEST_RE.fullmatch(platform_digest):
        raise DeployError("platform_manifest_invalid")
    platform_ref = f"{repository}@{platform_digest}"

    platform_raw = run_closed(
        [docker_bin, "buildx", "imagetools", "inspect", "--raw", platform_ref],
        "platform_inspection_failed",
    )
    image_raw = run_closed(
        [docker_bin, "buildx", "imagetools", "inspect", "--format", "{{json .Image}}", platform_ref],
        "config_inspection_failed",
    )
    try:
        platform_manifest = json.loads(platform_raw)
        config_digest = platform_manifest["config"]["digest"]
        image = json.loads(image_raw)
        labels = image["config"]["Labels"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise DeployError("oci_config_invalid") from exc
    if not DIGEST_RE.fullmatch(config_digest):
        raise DeployError("oci_config_invalid")
    if labels.get("org.opencontainers.image.version") != expected_oci_version:
        raise DeployError("oci_version_mismatch")
    if labels.get("org.opencontainers.image.revision") != expected_revision:
        raise DeployError("oci_revision_mismatch")

    run_closed(
        [
            docker_bin,
            "run",
            "--rm",
            COSIGN_IMAGE,
            "verify",
            "--certificate-identity-regexp",
            SIGNER_IDENTITY_PATTERN,
            "--certificate-oidc-issuer",
            "https://token.actions.githubusercontent.com",
            reference,
        ],
        "signature_verification_failed",
        timeout=180,
    )
    run_closed(
        [
            gh_bin,
            "attestation",
            "verify",
            "oci://" + reference,
            "--cert-oidc-issuer",
            "https://token.actions.githubusercontent.com",
            "--repo",
            "rknightion/synthkit",
            "--signer-workflow",
            SIGNER_WORKFLOW,
            "--source-digest",
            expected_revision,
            "--source-ref",
            source_ref,
        ],
        "provenance_verification_failed",
        timeout=180,
    )
    binary_raw = run_closed(
        [
            docker_bin,
            "run",
            "--rm",
            "--platform",
            platform,
            "--network",
            "none",
            "--read-only",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            platform_ref,
            "-version",
        ],
        "binary_identity_failed",
    )
    try:
        binary = json.loads(binary_raw)
    except json.JSONDecodeError as exc:
        raise DeployError("binary_identity_invalid") from exc
    if binary != {"version": expected_version, "revision": expected_revision}:
        raise DeployError("binary_identity_mismatch")
    return {
        "index_digest": index_digest,
        "oci_config_digest": config_digest,
        "oci_version": expected_oci_version,
        "platform": platform,
        "platform_manifest_digest": platform_digest,
        "provenance": "verified",
        "revision": expected_revision,
        "signature": "verified",
        "status": "ok",
        "version": expected_version,
    }


def compose_version(value: str) -> tuple[int, int, int]:
    match = re.fullmatch(r"v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?", value.strip())
    if not match:
        raise DeployError("compose_version_invalid")
    return tuple(int(group) for group in match.groups())


def check_compose(
    compose_file: Path,
    env_file: Path,
    expected_reference: str,
    docker_bin: str,
) -> dict[str, object]:
    require_regular_file(compose_file)
    require_regular_file(env_file)
    validate_image_ref(expected_reference)
    version_text = run_closed(
        [docker_bin, "compose", "version", "--short"],
        "compose_version_unavailable",
    ).strip()
    if compose_version(version_text) < (2, 24, 4):
        raise DeployError("compose_version_unsupported")
    environment = dict(os.environ)
    environment["SYNTHKIT_ENV_FILE"] = str(absolute_without_resolving(env_file))
    environment.pop("SYNTHKIT_IMAGE_REF", None)
    environment.pop("SYNTHKIT_IMAGE_TAG", None)
    base = [
        docker_bin,
        "compose",
        "--env-file",
        str(env_file),
        "-f",
        str(compose_file),
    ]
    run_closed(base + ["config", "--quiet"], "compose_default_invalid", environment=environment)
    default_images = run_closed(
        base + ["config", "--images"],
        "compose_default_invalid",
        environment=environment,
    ).split()
    run_closed(
        base + ["--profile", "sm-provision", "config", "--quiet"],
        "compose_profile_invalid",
        environment=environment,
    )
    profile_images = run_closed(
        base + ["--profile", "sm-provision", "config", "--images"],
        "compose_profile_invalid",
        environment=environment,
    ).split()
    if default_images != [expected_reference]:
        raise DeployError("compose_default_image_mismatch")
    if profile_images != [expected_reference, expected_reference]:
        raise DeployError("compose_profile_image_mismatch")
    return {
        "compose_version": version_text.removeprefix("v"),
        "reference": expected_reference,
        "services": len(profile_images),
        "status": "ok",
    }


def inspect_running(
    container: str,
    expected_reference: str,
    expected_version: str,
    expected_revision: str,
    docker_bin: str,
) -> dict[str, object]:
    if not container:
        raise DeployError("container_required")
    validate_image_ref(expected_reference)
    if "@" not in expected_reference:
        raise DeployError("image_digest_required")
    if not VERSION_RE.fullmatch(expected_version):
        raise DeployError("expected_version_invalid")
    if not REVISION_RE.fullmatch(expected_revision):
        raise DeployError("expected_revision_invalid")
    configured_ref = run_closed(
        [docker_bin, "inspect", "--format", "{{.Config.Image}}", container],
        "running_inspection_failed",
    ).strip()
    running_image_id = run_closed(
        [docker_bin, "inspect", "--format", "{{.Image}}", container],
        "running_inspection_failed",
    ).strip()
    health = run_closed(
        [
            docker_bin,
            "inspect",
            "--format",
            "{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}",
            container,
        ],
        "running_inspection_failed",
    ).strip()
    if health not in {"healthy", "unhealthy", "starting", "none"}:
        raise DeployError("container_health_invalid")
    validate_image_ref(configured_ref)
    if configured_ref != expected_reference:
        raise DeployError("configured_reference_mismatch")
    if not DIGEST_RE.fullmatch(running_image_id):
        raise DeployError("running_image_id_invalid")
    repo_digests_raw = run_closed(
        [docker_bin, "image", "inspect", "--format", "{{json .RepoDigests}}", running_image_id],
        "running_image_inspection_failed",
    )
    platform = run_closed(
        [docker_bin, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", running_image_id],
        "running_image_inspection_failed",
    ).strip()
    try:
        repo_digests = json.loads(repo_digests_raw)
    except json.JSONDecodeError as exc:
        raise DeployError("running_repo_digest_invalid") from exc
    matching = [value for value in repo_digests if isinstance(value, str) and value.startswith(IMAGE_REPOSITORY + "@")]
    if len(matching) != 1:
        raise DeployError("running_repo_digest_invalid")
    index_ref = matching[0]
    index_digest = index_ref.rsplit("@", 1)[1]
    if not DIGEST_RE.fullmatch(index_digest):
        raise DeployError("running_repo_digest_invalid")
    if expected_reference.rsplit("@", 1)[1] != index_digest:
        raise DeployError("running_index_mismatch")
    try:
        platform_os, platform_arch = platform.split("/", 1)
    except ValueError as exc:
        raise DeployError("platform_invalid") from exc
    index_raw = run_closed(
        [docker_bin, "buildx", "imagetools", "inspect", "--raw", index_ref],
        "index_inspection_failed",
    )
    try:
        index = json.loads(index_raw)
        manifests = [
            item
            for item in index["manifests"]
            if item.get("platform", {}).get("os") == platform_os
            and item.get("platform", {}).get("architecture") == platform_arch
        ]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise DeployError("index_manifest_invalid") from exc
    if len(manifests) != 1 or not DIGEST_RE.fullmatch(manifests[0].get("digest", "")):
        raise DeployError("platform_manifest_invalid")
    platform_digest = manifests[0]["digest"]
    platform_raw = run_closed(
        [docker_bin, "buildx", "imagetools", "inspect", "--raw", f"{IMAGE_REPOSITORY}@{platform_digest}"],
        "platform_inspection_failed",
    )
    try:
        platform_manifest = json.loads(platform_raw)
        oci_config_digest = platform_manifest["config"]["digest"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise DeployError("oci_config_invalid") from exc
    if oci_config_digest == running_image_id:
        runtime_identity = "oci_config_digest"
    elif index_digest == running_image_id:
        descriptor_raw = run_closed(
            [docker_bin, "inspect", "--format", "{{json .ImageManifestDescriptor}}", container],
            "running_inspection_failed",
        )
        try:
            descriptor = json.loads(descriptor_raw)
        except json.JSONDecodeError as exc:
            raise DeployError("running_manifest_descriptor_invalid") from exc
        if not isinstance(descriptor, dict) or not descriptor:
            raise DeployError("running_manifest_descriptor_unavailable")
        if descriptor.get("digest") != platform_digest:
            raise DeployError("running_config_mismatch")
        runtime_identity = "index_with_platform_descriptor"
    else:
        raise DeployError("running_config_mismatch")
    binary_raw = run_closed(
        [docker_bin, "exec", container, "/app/synthkit", "-version"],
        "binary_identity_failed",
    )
    try:
        binary = json.loads(binary_raw)
        binary_version = binary["version"]
        binary_revision = binary["revision"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise DeployError("binary_identity_invalid") from exc
    if not VERSION_RE.fullmatch(binary_version) or not REVISION_RE.fullmatch(binary_revision):
        raise DeployError("binary_identity_invalid")
    if binary_version != expected_version or binary_revision != expected_revision:
        raise DeployError("binary_identity_mismatch")
    return {
        "configured_ref": configured_ref,
        "health": health,
        "index_digest": index_digest,
        "oci_config_digest": oci_config_digest,
        "platform": platform,
        "platform_manifest_digest": platform_digest,
        "revision": binary_revision,
        "runtime_identity": runtime_identity,
        "running_image_id": running_image_id,
        "status": "ok",
        "version": binary_version,
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    resolve = subparsers.add_parser("resolve-image")
    resolve.add_argument("--env-file", type=Path, required=True)
    resolve.add_argument("--default-ref", required=True)
    set_selector = subparsers.add_parser("set-image")
    set_selector.add_argument("--env-file", type=Path, required=True)
    set_selector.add_argument("--expected-sha256", required=True)
    set_selector.add_argument("--reference", required=True)
    set_selector.add_argument("--allow-mutable", action="store_true")
    snapshot = subparsers.add_parser("snapshot-state")
    snapshot.add_argument("--state-dir", type=Path, required=True)
    snapshot.add_argument("--records-dir", type=Path, required=True)
    snapshot.add_argument("--checkout-root", type=Path, required=True)
    snapshot.add_argument("--name", required=True)
    snapshot.add_argument("--container", required=True)
    snapshot.add_argument("--docker-bin", default="docker")
    restore = subparsers.add_parser("restore-state")
    restore.add_argument("--state-dir", type=Path, required=True)
    restore.add_argument("--expected-root", type=Path, required=True)
    restore.add_argument("--records-dir", type=Path, required=True)
    restore.add_argument("--name", required=True)
    restore.add_argument("--expected-manifest-sha256", required=True)
    restore.add_argument("--container", required=True)
    restore.add_argument("--docker-bin", default="docker")
    record = subparsers.add_parser("write-record")
    record.add_argument("--records-dir", type=Path, required=True)
    record.add_argument("--checkout-root", type=Path, required=True)
    record.add_argument("--name", required=True)
    record.add_argument("--field", action="append", default=[])
    verify = subparsers.add_parser("verify-image")
    verify.add_argument("--reference", required=True)
    verify.add_argument("--expected-version", required=True)
    verify.add_argument("--expected-oci-version")
    verify.add_argument("--expected-revision", required=True)
    verify.add_argument("--source-ref", required=True)
    verify.add_argument("--platform", required=True)
    verify.add_argument("--docker-bin", default="docker")
    verify.add_argument("--gh-bin", default="gh")
    compose = subparsers.add_parser("check-compose")
    compose.add_argument("--compose-file", type=Path, required=True)
    compose.add_argument("--env-file", type=Path, required=True)
    compose.add_argument("--expected-reference", required=True)
    compose.add_argument("--docker-bin", default="docker")
    running = subparsers.add_parser("inspect-running")
    running.add_argument("--container", required=True)
    running.add_argument("--expected-reference", required=True)
    running.add_argument("--expected-version", required=True)
    running.add_argument("--expected-revision", required=True)
    running.add_argument("--docker-bin", default="docker")
    return parser


def main() -> None:
    args = build_parser().parse_args()
    try:
        if args.command == "resolve-image":
            report = resolve_image(args.env_file, args.default_ref)
        elif args.command == "set-image":
            report = set_image(args.env_file, args.expected_sha256, args.reference, args.allow_mutable)
        elif args.command == "snapshot-state":
            report = snapshot_state(
                args.state_dir,
                args.records_dir,
                args.checkout_root,
                args.name,
                args.container,
                args.docker_bin,
            )
        elif args.command == "restore-state":
            report = restore_state(
                args.state_dir,
                args.expected_root,
                args.records_dir,
                args.name,
                args.expected_manifest_sha256,
                args.container,
                args.docker_bin,
            )
        elif args.command == "write-record":
            report = write_record(args.records_dir, args.checkout_root, args.name, args.field)
        elif args.command == "verify-image":
            report = verify_image(
                args.reference,
                args.expected_version,
                args.expected_oci_version or args.expected_version,
                args.expected_revision,
                args.source_ref,
                args.platform,
                args.docker_bin,
                args.gh_bin,
            )
        elif args.command == "check-compose":
            report = check_compose(args.compose_file, args.env_file, args.expected_reference, args.docker_bin)
        elif args.command == "inspect-running":
            report = inspect_running(
                args.container,
                args.expected_reference,
                args.expected_version,
                args.expected_revision,
                args.docker_bin,
            )
        else:  # pragma: no cover - argparse owns this boundary.
            raise DeployError("unsupported_command")
    except (DeployError, OSError, UnicodeError) as exc:
        code = str(exc) if isinstance(exc, DeployError) else "filesystem_error"
        fail(code)
    print(json.dumps(report, sort_keys=True))


if __name__ == "__main__":
    main()
