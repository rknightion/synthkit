# SPDX-License-Identifier: AGPL-3.0-only

import json
import hashlib
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("synthkit-deploy.py")
IMAGE = "ghcr.io/rknightion/synthkit"
DIGEST = "sha256:" + "a" * 64
COSIGN_IMAGE = "ghcr.io/sigstore/cosign/cosign@sha256:de9c65609e6bde17e6b48de485ee788407c9502fa08b8f4459f595b21f56cd00"
PYTHON_SHEBANG = f"#!{sys.executable}\n"


def load_deploy_module():
    spec = importlib.util.spec_from_file_location("synthkit_deploy_test_target", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("deployment helper module could not be loaded")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class DeployCLITest(unittest.TestCase):
    def run_cli(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            text=True,
            capture_output=True,
            check=False,
        )
        if check and result.returncode != 0:
            self.fail(f"command failed ({result.returncode}): {result.stderr}")
        return result

    def test_resolve_image_prefers_full_reference_and_reports_ignored_legacy_tag(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            env_file.write_text(
                f"SYNTHKIT_IMAGE_REF={IMAGE}@{DIGEST}\nSYNTHKIT_IMAGE_TAG=main\n",
                encoding="utf-8",
            )

            result = self.run_cli(
                "resolve-image",
                "--env-file",
                str(env_file),
                "--default-ref",
                f"{IMAGE}:1.3.0",
            )

            report = json.loads(result.stdout)
            self.assertEqual(report["reference"], f"{IMAGE}@{DIGEST}")
            self.assertEqual(report["source"], "SYNTHKIT_IMAGE_REF")
            self.assertEqual(report["legacy_tag"], "ignored")
            self.assertFalse(report["mutable"])

    def test_resolve_image_parses_matching_selector_quotes_like_compose(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            reference = f"{IMAGE}@{DIGEST}"
            env_file.write_text(
                f"export SYNTHKIT_IMAGE_REF='{reference}'\nSYNTHKIT_IMAGE_TAG=\"main\"\n",
                encoding="utf-8",
            )

            result = self.run_cli(
                "resolve-image",
                "--env-file",
                str(env_file),
                "--default-ref",
                f"{IMAGE}:1.3.0",
            )

            report = json.loads(result.stdout)
            self.assertEqual(report["reference"], reference)
            self.assertEqual(report["legacy_tag"], "ignored")

    def test_selector_parser_matches_compose_inline_comment_boundaries(self) -> None:
        deploy = load_deploy_module()
        reference = f"{IMAGE}@{DIGEST}"
        selectors = deploy.selector_values_bytes(
            (
                f"SYNTHKIT_IMAGE_REF=\"{reference}\" # standing pin\n"
                "SYNTHKIT_IMAGE_TAG=release#candidate # legacy comment\n"
            ).encode()
        )

        self.assertEqual(selectors["SYNTHKIT_IMAGE_REF"], reference)
        self.assertEqual(selectors["SYNTHKIT_IMAGE_TAG"], "release#candidate")

    def test_selector_parser_rejects_ambiguous_selector_syntax(self) -> None:
        deploy = load_deploy_module()

        with self.assertRaisesRegex(deploy.DeployError, "image_selector_syntax_invalid"):
            deploy.selector_values_bytes(b"SYNTHKIT_IMAGE_REF\n")
        with self.assertRaisesRegex(deploy.DeployError, "image_selector_syntax_invalid"):
            deploy.selector_values_bytes(b"export SYNTHKIT_IMAGE_TAG\n")

    def test_set_image_updates_an_exported_selector_without_adding_a_duplicate(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"export SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)

            self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                hashlib.sha256(before.encode()).hexdigest(),
                "--reference",
                f"{IMAGE}@{DIGEST}",
            )

            self.assertEqual(env_file.read_text(encoding="utf-8"), f"export SYNTHKIT_IMAGE_REF={IMAGE}@{DIGEST}\n")

    def test_set_image_replaces_only_the_preferred_selector(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = (
                "# retained comment\n"
                "GC_TOKEN=deployment-secret-value\n"
                f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\n"
                "SYNTHKIT_IMAGE_TAG=main\n"
                "OTHER=value\n"
            )
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)
            expected_hash = hashlib.sha256(before.encode()).hexdigest()

            result = self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                expected_hash,
                "--reference",
                f"{IMAGE}@{DIGEST}",
            )

            after = env_file.read_text(encoding="utf-8")
            self.assertEqual(
                after,
                before.replace(f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0", f"SYNTHKIT_IMAGE_REF={IMAGE}@{DIGEST}"),
            )
            self.assertEqual(env_file.stat().st_mode & 0o777, 0o600)
            self.assertNotIn("deployment-secret-value", result.stdout + result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["previous_sha256"], expected_hash)
            self.assertEqual(report["reference"], f"{IMAGE}@{DIGEST}")
            self.assertNotEqual(report["sha256"], expected_hash)

    def test_set_image_normalizes_a_whitespace_padded_preferred_selector(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"  SYNTHKIT_IMAGE_REF = {IMAGE}:1.2.0\nSYNTHKIT_IMAGE_TAG=main\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)

            self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                hashlib.sha256(before.encode()).hexdigest(),
                "--reference",
                f"{IMAGE}@{DIGEST}",
            )

            after = env_file.read_text(encoding="utf-8")
            self.assertEqual(after.count("SYNTHKIT_IMAGE_REF="), 1)
            self.assertEqual(after.splitlines()[0], f"SYNTHKIT_IMAGE_REF={IMAGE}@{DIGEST}")

    def test_set_image_validates_selectors_from_the_cas_bound_bytes(self) -> None:
        deploy = load_deploy_module()
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\nSYNTHKIT_IMAGE_TAG=main\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)

            def reject_path_reread(_env_file):
                raise AssertionError("set_image must not re-read selector bytes by path")

            deploy.selector_values = reject_path_reread
            result = deploy.set_image(
                env_file,
                hashlib.sha256(before.encode()).hexdigest(),
                f"{IMAGE}@{DIGEST}",
                False,
            )

            self.assertEqual(result["legacy_tag"], "ignored")

    def test_set_image_rejects_a_malformed_expected_hash(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)

            result = self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                "not-a-sha256",
                "--reference",
                f"{IMAGE}@{DIGEST}",
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("expected_env_hash_invalid", result.stderr)
            self.assertEqual(env_file.read_text(encoding="utf-8"), before)

    def test_atomic_exchange_reports_a_missing_platform_symbol_as_unsupported(self) -> None:
        deploy = load_deploy_module()
        with mock.patch.object(deploy.sys, "platform", "linux"), mock.patch.object(
            deploy.ctypes, "CDLL", return_value=object()
        ):
            with self.assertRaisesRegex(deploy.DeployError, "atomic_exchange_unsupported"):
                deploy.atomic_exchange(Path("candidate"), Path("selected"))

    def test_set_image_detects_a_noncooperating_writer_before_replace(self) -> None:
        deploy = load_deploy_module()
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\nOTHER=original\n"
            concurrent = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\nOTHER=concurrent\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)
            original_exchange = deploy.atomic_exchange
            exchanges = 0

            def exchange_after_concurrent_write(first, second):
                nonlocal exchanges
                if exchanges == 0:
                    env_file.write_text(concurrent, encoding="utf-8")
                    env_file.chmod(0o600)
                exchanges += 1
                return original_exchange(first, second)

            deploy.atomic_exchange = exchange_after_concurrent_write
            with self.assertRaisesRegex(deploy.DeployError, "env_compare_and_swap_failed"):
                deploy.set_image(
                    env_file,
                    hashlib.sha256(before.encode()).hexdigest(),
                    f"{IMAGE}@{DIGEST}",
                    False,
                )
            self.assertEqual(env_file.read_text(encoding="utf-8"), concurrent)
            self.assertEqual(exchanges, 2)

    def test_set_image_rejects_an_in_place_write_after_exchange(self) -> None:
        deploy = load_deploy_module()
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\nOTHER=original\n"
            injected = f"SYNTHKIT_IMAGE_REF={IMAGE}:evil\nOTHER=injected\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)
            original_exchange = deploy.atomic_exchange
            exchanges = 0

            def exchange_then_mutate_selected(first, second):
                nonlocal exchanges
                original_exchange(first, second)
                if exchanges == 0:
                    env_file.write_text(injected, encoding="utf-8")
                    env_file.chmod(0o600)
                exchanges += 1

            deploy.atomic_exchange = exchange_then_mutate_selected
            with self.assertRaisesRegex(deploy.DeployError, "env_compare_and_swap_failed"):
                deploy.set_image(
                    env_file,
                    hashlib.sha256(before.encode()).hexdigest(),
                    f"{IMAGE}@{DIGEST}",
                    False,
                )

            self.assertEqual(env_file.read_text(encoding="utf-8"), before)
            self.assertEqual(exchanges, 2)

    def test_set_image_rejects_stale_hash_without_changing_any_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = "GC_TOKEN=deployment-secret-value\nSYNTHKIT_IMAGE_REF=ghcr.io/rknightion/synthkit:1.2.0\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)

            result = self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                "0" * 64,
                "--reference",
                f"{IMAGE}@{DIGEST}",
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(env_file.read_text(encoding="utf-8"), before)
            self.assertIn("env_compare_and_swap_failed", result.stderr)
            self.assertNotIn("deployment-secret-value", result.stdout + result.stderr)

    def test_set_image_requires_explicit_acknowledgement_for_mutable_edge_tags(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            before = f"SYNTHKIT_IMAGE_REF={IMAGE}:1.2.0\n"
            env_file.write_text(before, encoding="utf-8")
            env_file.chmod(0o600)
            expected_hash = hashlib.sha256(before.encode()).hexdigest()

            result = self.run_cli(
                "set-image",
                "--env-file",
                str(env_file),
                "--expected-sha256",
                expected_hash,
                "--reference",
                f"{IMAGE}:main",
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(env_file.read_text(encoding="utf-8"), before)
            self.assertIn("mutable_reference_requires_acknowledgement", result.stderr)

    def test_snapshot_state_requires_a_stopped_container_and_writes_private_integrity_record(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir(mode=0o700)
            state = root / "deployment" / "state"
            (state / "runtime").mkdir(parents=True, mode=0o700)
            secret = b"private-state-payload"
            state_file = state / "runtime" / "control.json"
            state_file.write_bytes(secret)
            state_file.chmod(0o600)
            records = root / "records"
            docker = root / "docker"
            docker.write_text("#!/bin/sh\nprintf 'false\\n'\n", encoding="utf-8")
            docker.chmod(0o700)

            previous_umask = os.umask(0o777)
            try:
                result = self.run_cli(
                    "snapshot-state",
                    "--state-dir",
                    str(state),
                    "--records-dir",
                    str(records),
                    "--checkout-root",
                    str(checkout),
                    "--name",
                    "before-upgrade",
                    "--container",
                    "container-id",
                    "--docker-bin",
                    str(docker),
                )
            finally:
                os.umask(previous_umask)

            self.assertEqual(records.stat().st_mode & 0o777, 0o700)
            snapshot = records / "before-upgrade"
            self.assertEqual(snapshot.stat().st_mode & 0o777, 0o700)
            self.assertEqual((snapshot / "data").stat().st_mode & 0o777, 0o700)
            self.assertEqual((snapshot / "data" / "runtime").stat().st_mode & 0o777, 0o700)
            manifest_path = snapshot / "manifest.json"
            self.assertEqual(manifest_path.stat().st_mode & 0o777, 0o600)
            self.assertEqual((snapshot / "data" / "runtime" / "control.json").stat().st_mode & 0o777, 0o600)
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            entry = next(item for item in manifest["entries"] if item["path"] == "runtime/control.json")
            self.assertEqual(entry["sha256"], hashlib.sha256(secret).hexdigest())
            self.assertEqual(entry["mode"], "0600")
            self.assertNotIn(secret.decode(), result.stdout + result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["name"], "before-upgrade")
            self.assertRegex(report["manifest_sha256"], r"^[0-9a-f]{64}$")

    def test_snapshot_state_rejects_a_running_container_before_creating_records(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir()
            state = root / "state"
            state.mkdir(mode=0o700)
            records = root / "records"
            docker = root / "docker"
            docker.write_text("#!/bin/sh\nprintf 'true\\n'\n", encoding="utf-8")
            docker.chmod(0o700)

            result = self.run_cli(
                "snapshot-state",
                "--state-dir",
                str(state),
                "--records-dir",
                str(records),
                "--checkout-root",
                str(checkout),
                "--name",
                "unsafe",
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("container_not_quiesced", result.stderr)
            self.assertFalse(records.exists())

    def test_snapshot_state_rejects_symlinks_without_copying_their_target(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir()
            state = root / "state"
            state.mkdir(mode=0o700)
            outside = root / "outside"
            outside.write_text("must-not-copy", encoding="utf-8")
            (state / "link").symlink_to(outside)
            docker = root / "docker"
            docker.write_text("#!/bin/sh\nprintf 'false\\n'\n", encoding="utf-8")
            docker.chmod(0o700)

            result = self.run_cli(
                "snapshot-state",
                "--state-dir",
                str(state),
                "--records-dir",
                str(root / "records"),
                "--checkout-root",
                str(checkout),
                "--name",
                "unsafe",
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("state_symlink_rejected", result.stderr)
            self.assertNotIn("must-not-copy", result.stdout + result.stderr)

    def test_restore_state_verifies_manifest_and_retains_displaced_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir(mode=0o700)
            deployment_root = root / "deployment"
            state = deployment_root / "state"
            state.mkdir(parents=True, mode=0o700)
            original = b"known-good-state"
            (state / "control.json").write_bytes(original)
            (state / "control.json").chmod(0o600)
            records = root / "records"
            docker = root / "docker"
            docker.write_text("#!/bin/sh\nprintf 'false\\n'\n", encoding="utf-8")
            docker.chmod(0o700)
            snapshot = self.run_cli(
                "snapshot-state",
                "--state-dir",
                str(state),
                "--records-dir",
                str(records),
                "--checkout-root",
                str(checkout),
                "--name",
                "known-good",
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
            )
            manifest_hash = json.loads(snapshot.stdout)["manifest_sha256"]
            candidate = b"candidate-state"
            (state / "control.json").write_bytes(candidate)

            result = self.run_cli(
                "restore-state",
                "--state-dir",
                str(state),
                "--expected-root",
                str(deployment_root),
                "--records-dir",
                str(records),
                "--name",
                "known-good",
                "--expected-manifest-sha256",
                manifest_hash,
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
            )

            self.assertEqual((state / "control.json").read_bytes(), original)
            report = json.loads(result.stdout)
            displaced = deployment_root / report["displaced"]
            self.assertEqual((displaced / "control.json").read_bytes(), candidate)
            self.assertEqual(state.stat().st_mode & 0o777, 0o700)
            self.assertEqual((state / "control.json").stat().st_mode & 0o777, 0o600)
            self.assertNotIn(original.decode(), result.stdout + result.stderr)
            self.assertNotIn(candidate.decode(), result.stdout + result.stderr)

    def test_restore_state_rejects_tampered_snapshot_before_mutating_target(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir()
            deployment_root = root / "deployment"
            state = deployment_root / "state"
            state.mkdir(parents=True, mode=0o700)
            state_file = state / "control.json"
            state_file.write_bytes(b"known-good")
            state_file.chmod(0o600)
            records = root / "records"
            docker = root / "docker"
            docker.write_text("#!/bin/sh\nprintf 'false\\n'\n", encoding="utf-8")
            docker.chmod(0o700)
            snapshot = self.run_cli(
                "snapshot-state",
                "--state-dir",
                str(state),
                "--records-dir",
                str(records),
                "--checkout-root",
                str(checkout),
                "--name",
                "known-good",
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
            )
            manifest_hash = json.loads(snapshot.stdout)["manifest_sha256"]
            state_file.write_bytes(b"current-candidate")
            stored = records / "known-good" / "data" / "control.json"
            stored.write_bytes(b"tampered")
            stored.chmod(0o600)

            result = self.run_cli(
                "restore-state",
                "--state-dir",
                str(state),
                "--expected-root",
                str(deployment_root),
                "--records-dir",
                str(records),
                "--name",
                "known-good",
                "--expected-manifest-sha256",
                manifest_hash,
                "--container",
                "container-id",
                "--docker-bin",
                str(docker),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("snapshot_file_hash_mismatch", result.stderr)
            self.assertEqual(state_file.read_bytes(), b"current-candidate")

    def test_restore_metadata_reports_when_elevated_privileges_are_required(self) -> None:
        deploy = load_deploy_module()
        with mock.patch.object(deploy.os, "fchown", side_effect=PermissionError(deploy.errno.EPERM, "denied")):
            with self.assertRaisesRegex(deploy.DeployError, "restore_requires_elevated_privileges"):
                deploy.restore_metadata(1, 65532, 65532, 0o600)

    def test_write_record_persists_closed_image_identity_fields_privately(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            checkout = root / "checkout"
            checkout.mkdir(mode=0o700)
            records = root / "records"
            revision = "b" * 40
            fields = {
                "configured_ref": f"{IMAGE}@{DIGEST}",
                "index_digest": DIGEST,
                "platform_manifest_digest": "sha256:" + "c" * 64,
                "oci_config_digest": "sha256:" + "d" * 64,
                "running_image_id": "sha256:" + "d" * 64,
                "version": "1.3.0",
                "revision": revision,
                "state_manifest_sha256": "f" * 64,
            }
            args = [
                "write-record",
                "--records-dir",
                str(records),
                "--checkout-root",
                str(checkout),
                "--name",
                "candidate",
            ]
            for key, value in fields.items():
                args.extend(("--field", f"{key}={value}"))

            result = self.run_cli(*args)

            record_path = records / "candidate.json"
            self.assertEqual(record_path.stat().st_mode & 0o777, 0o600)
            record = json.loads(record_path.read_text(encoding="utf-8"))
            self.assertEqual(record["schema"], "synthkit-deployment-record-v1")
            self.assertEqual(record["identity"], fields)
            report = json.loads(result.stdout)
            self.assertRegex(report["record_sha256"], r"^[0-9a-f]{64}$")
            self.assertNotIn(revision, result.stdout + result.stderr)

    def test_verify_image_reports_each_digest_layer_and_verified_supply_chain(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            docker = root / "docker"
            gh = root / "gh"
            index = DIGEST
            manifest = "sha256:" + "c" * 64
            config = "sha256:" + "d" * 64
            revision = "b" * 40
            version = "1.3.0-rc.99"
            oci_version = "v" + version
            reference = f"{IMAGE}@{index}"
            docker.write_text(
                PYTHON_SHEBANG
                +
                "import json, os, sys\n"
                f"index={index!r}; manifest={manifest!r}; config={config!r}; revision={revision!r}; version={version!r}; oci_version={oci_version!r}\n"
                "args=sys.argv[1:]\n"
                "log=os.environ.get('VERIFY_ORDER_LOG')\n"
                "if log:\n"
                "  with open(log, 'a', encoding='utf-8') as handle: handle.write('docker ' + ' '.join(args) + '\\n')\n"
                "if args[:4] == ['buildx','imagetools','inspect','--raw']:\n"
                "  ref=args[4]\n"
                "  if ref.endswith(index): print(json.dumps({'mediaType':'application/vnd.oci.image.index.v1+json','manifests':[{'digest':manifest,'platform':{'os':'linux','architecture':'amd64'}}]}))\n"
                "  else: print(json.dumps({'mediaType':'application/vnd.oci.image.manifest.v1+json','config':{'digest':config}}))\n"
                "elif args[:3] == ['buildx','imagetools','inspect'] and args[3] == '--format':\n"
                "  print(json.dumps({'config':{'Labels':{'org.opencontainers.image.version':oci_version,'org.opencontainers.image.revision':revision}}}))\n"
                "elif args and args[0] == 'run' and '-version' in args:\n"
                "  print(json.dumps({'version':version,'revision':revision}))\n"
                "elif args and args[0] == 'run': pass\n"
                "else: sys.exit(2)\n",
                encoding="utf-8",
            )
            docker.chmod(0o700)
            gh.write_text("#!/bin/sh\nprintf 'gh %s\\n' \"$*\" >> \"$VERIFY_ORDER_LOG\"\n", encoding="utf-8")
            gh.chmod(0o700)
            order_log = root / "verify-order.log"
            environment = dict(os.environ)
            environment["VERIFY_ORDER_LOG"] = str(order_log)
            result = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "verify-image",
                    "--reference",
                    reference,
                    "--expected-version",
                    version,
                    "--expected-oci-version",
                    oci_version,
                    "--expected-revision",
                    revision,
                    "--source-ref",
                    "refs/heads/main",
                    "--platform",
                    "linux/amd64",
                    "--docker-bin",
                    str(docker),
                    "--gh-bin",
                    str(gh),
                ],
                text=True,
                capture_output=True,
                check=True,
                env=environment,
            )

            report = json.loads(result.stdout)
            self.assertEqual(report["index_digest"], index)
            self.assertEqual(report["platform_manifest_digest"], manifest)
            self.assertEqual(report["oci_config_digest"], config)
            self.assertEqual(report["oci_version"], oci_version)
            self.assertEqual(report["signature"], "verified")
            self.assertEqual(report["provenance"], "verified")
            self.assertEqual(report["version"], version)
            self.assertEqual(report["revision"], revision)
            events = order_log.read_text(encoding="utf-8").splitlines()
            platform_reference = f"{IMAGE}@{manifest}"
            candidate_probe = next(
                index for index, event in enumerate(events) if platform_reference in event and " -version" in event
            )
            signature = next(index for index, event in enumerate(events) if COSIGN_IMAGE in event)
            provenance = next(index for index, event in enumerate(events) if event.startswith("gh attestation verify"))
            signature_event = events[signature]
            provenance_event = events[provenance]
            self.assertLess(signature, candidate_probe)
            self.assertLess(provenance, candidate_probe)
            self.assertIn("--certificate-identity-regexp", signature_event)
            self.assertIn(
                r"^https://github\.com/rknightion/\.github/\.github/workflows/"
                r"container-publish\.yml@[A-Za-z0-9._/-]+$",
                signature_event,
            )
            self.assertIn(
                "--certificate-oidc-issuer https://token.actions.githubusercontent.com",
                signature_event,
            )
            self.assertNotIn("f31690684f4292d1fe8e528618f7c8306fe27d9a", signature_event + provenance_event)
            self.assertIn("--cert-oidc-issuer https://token.actions.githubusercontent.com", provenance_event)
            self.assertIn("--repo rknightion/synthkit", provenance_event)
            self.assertIn(
                "--signer-workflow rknightion/.github/.github/workflows/container-publish.yml",
                provenance_event,
            )
            self.assertNotIn("--signer-digest", provenance_event)
            self.assertIn(f"--source-digest {revision}", provenance_event)
            self.assertIn("--source-ref refs/heads/main", provenance_event)
            self.assertNotIn(f" {reference} -version", events[candidate_probe])
            self.assertIn("--network none --read-only --cap-drop ALL --security-opt no-new-privileges", events[candidate_probe])

    def test_publish_verification_uses_the_release_label_contract(self) -> None:
        # Execute the maintained workflow shell and the real verification CLI.
        # Only registry, signature/provenance services and Compose are fixtures.
        workflow = SCRIPT.parents[1] / ".github" / "workflows" / "publish.yml"
        lines = workflow.read_text(encoding="utf-8").splitlines()
        step = lines.index("      - name: Verify exact image identity, signature, and provenance")
        start = lines.index("        run: |", step) + 1
        block = []
        for line in lines[start:]:
            if line and not line.startswith("          "):
                break
            block.append(line[10:])
        shell = "\n".join(block)
        # Actions' default shell supplies -e, not pipefail. The maintained block
        # must propagate a failed verifier through tee without help from here.
        for tag, label in (("v1.6.0", "1.6.0"), ("v1.6.1-rc.1", "v1.6.1-rc.1")):
            with self.subTest(tag=tag), tempfile.TemporaryDirectory() as temp:
                root = Path(temp).resolve()
                tools = root / "tools"
                tools.mkdir()
                scripts = root / "scripts"
                scripts.mkdir()
                (scripts / SCRIPT.name).write_bytes(SCRIPT.read_bytes())
                published = root / "digests"
                published.mkdir()
                manifest = "sha256:" + "c" * 64
                (published / manifest.removeprefix("sha256:")).touch()
                (published / ("e" * 64)).touch()
                index_raw = json.dumps({
                    "mediaType": "application/vnd.oci.image.index.v1+json",
                    "manifests": [{"digest": manifest, "platform": {"os": "linux", "architecture": "amd64"}}],
                })
                index = "sha256:" + hashlib.sha256(index_raw.encode()).hexdigest()
                fixture = {
                    "index": index,
                    "index_raw": index_raw,
                    "manifest": manifest,
                    "config": "sha256:" + "d" * 64,
                    "revision": "b" * 40,
                    "version": tag.removeprefix("v"),
                    "oci_version": label,
                    "source_ref": "refs/tags/" + tag,
                }
                fixture_file = root / "fixture.json"
                fixture_file.write_text(json.dumps(fixture), encoding="utf-8")
                docker = tools / "docker"
                docker.write_text(
                    PYTHON_SHEBANG
                    + "import json, os, sys\n"
                    "from pathlib import Path\n"
                    "f=json.loads(Path(os.environ['PUBLISH_FIXTURE']).read_text())\n"
                    "args=sys.argv[1:]\n"
                    "if args[:4] == ['buildx','imagetools','create','--dry-run']:\n"
                    "  print(f['index_raw'])\n"
                    "elif args[:4] == ['buildx','imagetools','inspect','--raw']:\n"
                    "  if args[4].endswith(f['index']): print(f['index_raw'])\n"
                    "  else: print(json.dumps({'mediaType':'application/vnd.oci.image.manifest.v1+json','config':{'digest':f['config']}}))\n"
                    "elif args[:4] == ['buildx','imagetools','inspect','--format']:\n"
                    "  print(json.dumps({'config':{'Labels':{'org.opencontainers.image.version':f['oci_version'],'org.opencontainers.image.revision':f['revision']}}}))\n"
                    "elif args and args[0] == 'run' and '-version' in args:\n"
                    "  print(json.dumps({'version':f['version'],'revision':f['revision']}))\n"
                    f"elif args and args[0] == 'run' and {COSIGN_IMAGE!r} in args: pass\n"
                    "else: sys.exit(2)\n",
                    encoding="utf-8",
                )
                gh = tools / "gh"
                gh.write_text(
                    PYTHON_SHEBANG
                    + "import json, os, sys\n"
                    "from pathlib import Path\n"
                    "f=json.loads(Path(os.environ['PUBLISH_FIXTURE']).read_text())\n"
                    "args=sys.argv[1:]\n"
                    f"expected=['attestation','verify','oci://{IMAGE}@' + f['index'],"
                    "'--cert-oidc-issuer','https://token.actions.githubusercontent.com',"
                    "'--repo','rknightion/synthkit','--signer-workflow','rknightion/.github/.github/workflows/container-publish.yml',"
                    "'--source-digest',f['revision'],'--source-ref',f['source_ref']]\n"
                    "sys.exit(0 if args == expected else 1)\n",
                    encoding="utf-8",
                )
                just = tools / "just"
                just.write_text('#!/bin/sh\n[ "$*" = compose-check ]\n', encoding="utf-8")
                for tool in (docker, gh, just):
                    tool.chmod(0o700)
                environment = dict(
                    os.environ,
                    PATH=str(tools) + os.pathsep + os.environ["PATH"],
                    HOME=str(root),
                    RELEASE_TAG=tag,
                    SOURCE_REVISION=fixture["revision"],
                    PUBLISHED_DIGESTS=str(published),
                    PUBLISHED_INDEX=str(root / "index.json"),
                    GITHUB_OUTPUT=str(root / "github-output"),
                    PUBLISH_FIXTURE=str(fixture_file),
                )
                result = subprocess.run(
                    ["bash", "-e", "-c", shell],
                    cwd=root, env=environment, capture_output=True, text=True, timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                report_raw = (root / "verification.json").read_text(encoding="utf-8")
                self.assertTrue(report_raw.strip(), "workflow succeeded without a verification report: " + result.stderr)
                report = json.loads(report_raw)
                self.assertEqual(report["oci_version"], label)
                self.assertEqual(report["version"], tag.removeprefix("v"))
                self.assertEqual(report["revision"], fixture["revision"])
                self.assertEqual(report["index_digest"], index)
                self.assertEqual(report["signature"], "verified")
                self.assertEqual(report["provenance"], "verified")
                # The caller must choose the contract's exact label, not accept
                # both spellings or loosen independent source/binary checks.
                opposite_label = tag if label != tag else tag.removeprefix("v")
                for field, value, error_code in (
                    ("oci_version", opposite_label, "oci_version_mismatch"),
                    ("revision", "e" * 40, "oci_revision_mismatch"),
                    ("source_ref", "refs/tags/v9.9.9", "provenance_verification_failed"),
                    ("version", "9.9.9", "binary_identity_mismatch"),
                ):
                    with self.subTest(tag=tag, mismatch=field):
                        fixture_file.write_text(json.dumps(fixture | {field: value}), encoding="utf-8")
                        result = subprocess.run(
                            ["bash", "-e", "-c", shell],
                            cwd=root, env=environment, capture_output=True, text=True, timeout=10,
                        )
                        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                        self.assertIn(error_code, result.stderr)

    def test_verify_image_rejects_oci_label_and_binary_identity_mismatches(self) -> None:
        deploy = load_deploy_module()
        manifest = "sha256:" + "c" * 64
        config = "sha256:" + "d" * 64
        revision = "b" * 40
        version = "1.3.0-rc.99"
        oci_version = "v" + version
        reference = f"{IMAGE}@{DIGEST}"
        index_raw = json.dumps(
            {
                "mediaType": "application/vnd.oci.image.index.v1+json",
                "manifests": [
                    {"digest": manifest, "platform": {"os": "linux", "architecture": "amd64"}}
                ],
            }
        )
        platform_raw = json.dumps(
            {
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "config": {"digest": config},
            }
        )

        cases = (
            (
                "version label",
                {"org.opencontainers.image.version": "v9.9.9", "org.opencontainers.image.revision": revision},
                {"version": version, "revision": revision},
                "oci_version_mismatch",
            ),
            (
                "revision label",
                {"org.opencontainers.image.version": oci_version, "org.opencontainers.image.revision": "e" * 40},
                {"version": version, "revision": revision},
                "oci_revision_mismatch",
            ),
            (
                "binary identity",
                {"org.opencontainers.image.version": oci_version, "org.opencontainers.image.revision": revision},
                {"version": version, "revision": "e" * 40},
                "binary_identity_mismatch",
            ),
        )
        for name, labels, binary, error_code in cases:
            with self.subTest(name=name):
                outputs = [
                    index_raw,
                    platform_raw,
                    json.dumps({"config": {"Labels": labels}}),
                    "",
                    "",
                    json.dumps(binary),
                ]
                with mock.patch.object(deploy, "run_closed", side_effect=outputs):
                    with self.assertRaisesRegex(deploy.DeployError, error_code):
                        deploy.verify_image(
                            reference,
                            version,
                            oci_version,
                            revision,
                            "refs/heads/main",
                            "linux/amd64",
                            "docker",
                            "gh",
                        )

    def test_check_compose_renders_default_and_profile_with_one_exact_image(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            compose = root / "docker-compose.yml"
            compose.write_text("services: {}\n", encoding="utf-8")
            env_file = root / "fake.env"
            reference = f"{IMAGE}@{DIGEST}"
            env_file.write_text(f"SYNTHKIT_IMAGE_REF={reference}\n", encoding="utf-8")
            docker = root / "docker"
            docker.write_text(
                "#!/bin/sh\n"
                "case \" $* \" in *\" compose version --short \"*) printf '2.24.4\\n'; exit 0 ;; esac\n"
                "[ -z \"${SYNTHKIT_IMAGE_REF:-}\" ] || exit 2\n"
                "[ -z \"${SYNTHKIT_IMAGE_TAG:-}\" ] || exit 2\n"
                "case \" $* \" in\n"
                f"  *\" --profile sm-provision \"*\" --images \"*) printf '%s\\n%s\\n' {reference!r} {reference!r} ;;\n"
                f"  *\" --images \"*) printf '%s\\n' {reference!r} ;;\n"
                "  *) exit 0 ;;\n"
                "esac\n",
                encoding="utf-8",
            )
            docker.chmod(0o700)

            with mock.patch.dict(
                os.environ,
                {"SYNTHKIT_IMAGE_REF": f"{IMAGE}:main", "SYNTHKIT_IMAGE_TAG": "latest"},
            ):
                result = self.run_cli(
                    "check-compose",
                    "--compose-file",
                    str(compose),
                    "--env-file",
                    str(env_file),
                    "--expected-reference",
                    reference,
                    "--docker-bin",
                    str(docker),
                )

            report = json.loads(result.stdout)
            self.assertEqual(report["compose_version"], "2.24.4")
            self.assertEqual(report["reference"], reference)
            self.assertEqual(report["services"], 2)

    def test_inspect_running_distinguishes_configured_index_platform_config_and_runtime_identity(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            docker = root / "docker"
            observations = root / "observations.json"
            observations.write_text("{}", encoding="utf-8")
            index = DIGEST
            manifest = "sha256:" + "c" * 64
            config = "sha256:" + "d" * 64
            revision = "b" * 40
            version = "1.3.0"
            reference = f"{IMAGE}@{index}"
            docker.write_text(
                PYTHON_SHEBANG
                +
                "import json, sys\nfrom pathlib import Path\n"
                f"observed=json.loads(Path({str(observations)!r}).read_text())\n"
                f"index={index!r}; manifest={manifest!r}; config={config!r}; revision={revision!r}; version={version!r}; reference={reference!r}\n"
                "args=sys.argv[1:]; joined=' '.join(args)\n"
                "if args[:2] == ['inspect','--format']:\n"
                "  template=args[2]\n"
                "  if template == '{{.Config.Image}}': print(observed.get('configured_ref', reference))\n"
                "  elif template == '{{.Image}}': print(observed.get('running_id', config))\n"
                "  elif template == '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}': print('healthy')\n"
                "elif args[:3] == ['image','inspect','--format']:\n"
                "  template=args[3]\n"
                "  if template == '{{json .RepoDigests}}': print(json.dumps(observed.get('repo_digests', [reference, 'ghcr.io/rknightion/synthkit@' + manifest])))\n"
                "  elif template == '{{.Os}}/{{.Architecture}}': print('linux/amd64')\n"
                "elif args[:4] == ['buildx','imagetools','inspect','--raw']:\n"
                "  if args[4].endswith(index): print(json.dumps({'mediaType':'application/vnd.oci.image.index.v1+json','manifests':[{'digest':manifest,'platform':{'os':'linux','architecture':observed.get('registry_arch', 'amd64')}}]}))\n"
                "  else: print(json.dumps({'mediaType':'application/vnd.oci.image.manifest.v1+json','config':{'digest':observed.get('registry_config', config)}}))\n"
                "elif args and args[0] == 'exec': print(json.dumps({'version':observed.get('version', version),'revision':observed.get('revision', revision)}))\n"
                "else: sys.exit(2)\n",
                encoding="utf-8",
            )
            docker.chmod(0o700)

            result = self.run_cli(
                "inspect-running",
                "--container",
                "container-id",
                "--expected-reference",
                reference,
                "--expected-version",
                version,
                "--expected-revision",
                revision,
                "--docker-bin",
                str(docker),
            )

            report = json.loads(result.stdout)
            self.assertEqual(report["configured_ref"], reference)
            self.assertEqual(report["index_digest"], index)
            self.assertEqual(report["platform_manifest_digest"], manifest)
            self.assertEqual(report["oci_config_digest"], config)
            self.assertEqual(report["running_image_id"], config)
            self.assertEqual(report["runtime_identity"], "oci_config_digest")
            self.assertEqual(report["version"], version)
            self.assertEqual(report["revision"], revision)
            self.assertEqual(report["health"], "healthy")

            for aliases in ([reference], [f"{IMAGE}@{manifest}", reference]):
                with self.subTest(aliases=aliases):
                    observations.write_text(json.dumps({"repo_digests": aliases}), encoding="utf-8")
                    result = self.run_cli(
                        "inspect-running", "--container", "container-id", "--expected-reference", reference,
                        "--expected-version", version, "--expected-revision", revision, "--docker-bin", str(docker),
                    )
                    self.assertEqual(json.loads(result.stdout)["index_digest"], index)

            wrong_digest = "sha256:" + "e" * 64
            for observed, error in (
                ({"repo_digests": []}, "running_repo_digest_invalid"),
                ({"repo_digests": {reference: True}}, "running_repo_digest_invalid"),
                ({"repo_digests": [f"{IMAGE}@{manifest}"]}, "running_index_mismatch"),
                ({"repo_digests": [f"{IMAGE}@{manifest}", f"{IMAGE}@{wrong_digest}"]}, "running_index_mismatch"),
                ({"repo_digests": ["ghcr.io/example/other@" + index, f"{IMAGE}@{manifest}"]}, "running_index_mismatch"),
                ({"configured_ref": f"{IMAGE}@{wrong_digest}"}, "configured_reference_mismatch"),
                ({"running_id": wrong_digest}, "running_config_mismatch"),
                ({"registry_config": wrong_digest}, "running_config_mismatch"),
                ({"registry_arch": "arm64"}, "platform_manifest_invalid"),
                ({"version": "9.9.9"}, "binary_identity_mismatch"),
                ({"revision": "f" * 40}, "binary_identity_mismatch"),
            ):
                with self.subTest(observed=observed):
                    observations.write_text(json.dumps(observed), encoding="utf-8")
                    result = self.run_cli(
                        "inspect-running", "--container", "container-id", "--expected-reference", reference,
                        "--expected-version", version, "--expected-revision", revision, "--docker-bin", str(docker),
                        check=False,
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(json.loads(result.stderr)["code"], error)

    def test_inspect_running_accepts_containerd_index_id_with_selected_platform_descriptor(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            docker = root / "docker"
            observations = root / "observations.json"
            observations.write_text("{}", encoding="utf-8")
            index = DIGEST
            manifest = "sha256:" + "c" * 64
            config = "sha256:" + "d" * 64
            revision = "b" * 40
            version = "1.3.0"
            reference = f"{IMAGE}@{index}"
            docker.write_text(
                PYTHON_SHEBANG
                +
                "import json, sys\nfrom pathlib import Path\n"
                f"observed=json.loads(Path({str(observations)!r}).read_text())\n"
                f"index={index!r}; manifest={manifest!r}; config={config!r}; revision={revision!r}; version={version!r}; reference={reference!r}\n"
                "args=sys.argv[1:]\n"
                "if args[:2] == ['inspect','--format']:\n"
                "  template=args[2]\n"
                "  if template == '{{.Config.Image}}': print(reference)\n"
                "  elif template == '{{.Image}}': print(index)\n"
                "  elif template == '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}': print('unhealthy')\n"
                "  elif template == '{{json .ImageManifestDescriptor}}': print(observed.get('descriptor_raw', json.dumps(observed.get('descriptor', {'digest':manifest,'platform':{'os':'linux','architecture':'amd64'}}))))\n"
                "elif args[:3] == ['image','inspect','--format']:\n"
                "  template=args[3]\n"
                "  if template == '{{json .RepoDigests}}': print(json.dumps(observed.get('repo_digests', ['ghcr.io/rknightion/synthkit@' + manifest, reference])))\n"
                "  elif template == '{{.Os}}/{{.Architecture}}': print('linux/amd64')\n"
                "elif args[:4] == ['buildx','imagetools','inspect','--raw']:\n"
                "  if args[4].endswith(index): print(json.dumps({'mediaType':'application/vnd.oci.image.index.v1+json','manifests':[{'digest':manifest,'platform':{'os':'linux','architecture':'amd64'}}]}))\n"
                "  else: print(json.dumps({'mediaType':'application/vnd.oci.image.manifest.v1+json','config':{'digest':config}}))\n"
                "elif args and args[0] == 'exec': print(json.dumps({'version':version,'revision':revision}))\n"
                "else: sys.exit(2)\n",
                encoding="utf-8",
            )
            docker.chmod(0o700)

            result = self.run_cli(
                "inspect-running",
                "--container",
                "container-id",
                "--expected-reference",
                reference,
                "--expected-version",
                version,
                "--expected-revision",
                revision,
                "--docker-bin",
                str(docker),
            )

            report = json.loads(result.stdout)
            self.assertEqual(report["oci_config_digest"], config)
            self.assertEqual(report["running_image_id"], index)
            self.assertEqual(report["runtime_identity"], "index_with_platform_descriptor")
            self.assertEqual(report["platform_manifest_digest"], manifest)

            for observed, error in (
                ({"repo_digests": [f"{IMAGE}@{manifest}"]}, "running_index_mismatch"),
                ({"descriptor_raw": "not-json"}, "running_manifest_descriptor_invalid"),
                ({"descriptor": None}, "running_manifest_descriptor_unavailable"),
                ({"descriptor": {}}, "running_manifest_descriptor_unavailable"),
                ({"descriptor": {"digest": "sha256:" + "e" * 64}}, "running_config_mismatch"),
            ):
                with self.subTest(observed=observed):
                    observations.write_text(json.dumps(observed), encoding="utf-8")
                    result = self.run_cli(
                        "inspect-running", "--container", "container-id", "--expected-reference", reference,
                        "--expected-version", version, "--expected-revision", revision, "--docker-bin", str(docker),
                        check=False,
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(json.loads(result.stderr)["code"], error)

    def test_inspect_running_rejects_invalid_health_before_registry_requests(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            docker = root / "docker"
            registry_marker = root / "registry-requested"
            reference = f"{IMAGE}@{DIGEST}"
            docker.write_text(
                PYTHON_SHEBANG
                +
                "import pathlib, sys\n"
                f"reference={reference!r}; marker=pathlib.Path({str(registry_marker)!r})\n"
                "args=sys.argv[1:]\n"
                "if args[:2] == ['inspect','--format']:\n"
                "  template=args[2]\n"
                "  if template == '{{.Config.Image}}': print(reference)\n"
                "  elif template == '{{.Image}}': print('sha256:' + 'd' * 64)\n"
                "  elif template == '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}': print('invalid')\n"
                "elif args and args[0] in {'image', 'buildx'}:\n"
                "  marker.touch(); sys.exit(2)\n"
                "else: sys.exit(2)\n",
                encoding="utf-8",
            )
            docker.chmod(0o700)

            result = self.run_cli(
                "inspect-running",
                "--container",
                "container-id",
                "--expected-reference",
                reference,
                "--expected-version",
                "1.3.0",
                "--expected-revision",
                "b" * 40,
                "--docker-bin",
                str(docker),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("container_health_invalid", result.stderr)
            self.assertFalse(registry_marker.exists())


if __name__ == "__main__":
    unittest.main()
