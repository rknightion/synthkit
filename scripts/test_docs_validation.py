#!/usr/bin/env python3
"""Focused fixture tests for validate-docs.py."""

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-docs.py")
SPEC = importlib.util.spec_from_file_location("validate_docs", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class DocsValidationTest(unittest.TestCase):
    def fixture(self, link="page.md", include_404=True):
        root = Path(tempfile.mkdtemp())
        (root / "docs").mkdir()
        (root / "docs.toml").write_text(
            f'[[site.nav]]\nHome = "index.md"\n\n[[site.nav]]\nPage = "{link}"\n', encoding="utf-8")
        (root / "docs/index.md").write_text("[page](page.md)\n", encoding="utf-8")
        if link == "page.md":
            (root / "docs/page.md").write_text("# Page\n", encoding="utf-8")
        if include_404:
            (root / "docs/404.md").write_text("# Missing\n", encoding="utf-8")
        return root

    def test_broken_nav_and_missing_404_are_reported(self):
        errors = MODULE.validate(self.fixture("missing.md", include_404=False))
        self.assertIn("nav target does not exist: missing.md", errors)
        self.assertIn("missing required special page: docs/404.md", errors)

    def test_broken_relative_link_is_reported(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[missing](absent.md)\n", encoding="utf-8")
        self.assertTrue(any("broken relative link: absent.md" in error for error in MODULE.validate(root)))

    def test_empty_link_target_is_ignored_without_crashing(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[empty](   )\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), [])

    def test_non_table_site_is_reported(self):
        root = self.fixture()
        (root / "docs.toml").write_text('site = "not-a-table"\n', encoding="utf-8")
        self.assertIn("docs.toml site must be a table", MODULE.validate(root))

    def test_navigation_target_must_stay_inside_docs(self):
        for target in ("../README.md", "/README.md"):
            with self.subTest(target=target):
                root = self.fixture(target)
                (root / "README.md").write_text("# Outside docs\n", encoding="utf-8")
                errors = MODULE.validate(root)
                self.assertIn(f"nav target escapes docs directory: {target}", errors)

    def test_links_inside_inline_code_spans_are_ignored(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("Example: `[missing](absent.md)`\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), [])

    def test_reference_definition_is_validated(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[missing][ref]\n\n[ref]: absent.md\n", encoding="utf-8")
        self.assertTrue(any("broken relative link: absent.md" in error for error in MODULE.validate(root)))

    def test_valid_reference_use_and_definition_are_accepted(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[page][ref]\n\n[ref]: page.md\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), [])

    def test_undefined_reference_use_is_reported(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[missing][unknown]\n", encoding="utf-8")
        self.assertTrue(any("undefined Markdown reference: unknown" in error for error in MODULE.validate(root)))

    def test_maintained_surfaces_and_images_are_checked(self):
        root = self.fixture()
        for name in ("README.md", "charts/synthkit/README.md", "plugins/synthkit/skills/demo/SKILL.md", "signals/demo.md"):
            source = root / name
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text("![missing image](absent.svg)\n", encoding="utf-8")
        errors = MODULE.validate(root)
        for name in ("README.md", "charts/synthkit/README.md", "plugins/synthkit/skills/demo/SKILL.md", "signals/demo.md"):
            self.assertIn(f"{name}: broken relative link: absent.svg", errors)

    def test_local_fragments_and_duplicate_heading_ids(self):
        root = self.fixture()
        source = root / "docs/page.md"
        source.write_text("# A `code` heading\n# A `code` heading\n"
                          "[ok](#a-code-heading-1) [bad](#absent)\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), ["docs/page.md: missing Markdown anchor: #absent"])

    def test_assets_are_checked_relative_to_docs(self):
        root = self.fixture()
        with (root / "docs.toml").open("a", encoding="utf-8") as config:
            config.write('\n[assets]\nsocial_image = "assets/missing.png"\n'
                         'extra_css = ["stylesheets/missing.css"]\n')
        errors = MODULE.validate(root)
        self.assertIn("docs.toml: missing asset: assets/missing.png", errors)
        self.assertIn("docs.toml: missing asset: stylesheets/missing.css", errors)

    def test_fences_and_historical_private_trees_do_not_create_links(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("~~~markdown\n[example](absent.md)\n~~~\n"
                                           "````markdown\n```\n[example](absent.md)\n```\n````\n", encoding="utf-8")
        for name in ("backlog/tasks/task.md", "codex/private.md"):
            source = root / name
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text("[example](absent.md)\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), [])

    def test_hub_assets_are_narrowly_exempt_but_project_assets_must_exist(self):
        root = self.fixture()
        (root / "docs/stylesheets").mkdir()
        (root / "docs/stylesheets/project.css").write_text("body {}", encoding="utf-8")
        with (root / "docs.toml").open("a", encoding="utf-8") as config:
            config.write('\n[assets]\nsocial_image = "assets/social-card.png"\n'
                         'extra_css = ["stylesheets/brand.css", "stylesheets/project.css"]\n')
        self.assertEqual(MODULE.validate(root), [])
        # An adjacent filename is not a hub-owned asset.
        with (root / "docs.toml").open("a", encoding="utf-8") as config:
            config.write('extra_javascript = ["../private.js"]\n')
        self.assertIn("docs.toml: asset escapes docs directory: ../private.js", MODULE.validate(root))

    def test_cli_exit_and_diagnostics_follow_maintained_document_links(self):
        root = self.fixture()
        source = root / "README.md"
        source.write_text("[page](docs/page.md#missing)\n", encoding="utf-8")
        result = subprocess.run([sys.executable, str(SCRIPT), "--root", str(root)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertIn("README.md: missing Markdown anchor: docs/page.md#missing", result.stderr)
        source.write_text("[page](docs/page.md#page)\n", encoding="utf-8")
        result = subprocess.run([sys.executable, str(SCRIPT), "--root", str(root)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("docs-check: OK", result.stdout)

    def test_docs_links_cannot_point_outside_published_site(self):
        root = self.fixture()
        (root / "CONTRIBUTING.md").write_text("# Contribute\n", encoding="utf-8")
        (root / "docs/page.md").write_text("[contribute](../CONTRIBUTING.md)\n", encoding="utf-8")
        self.assertIn("docs/page.md: link escapes published docs directory: ../CONTRIBUTING.md; use an external repository URL",
                      MODULE.validate(root))
        (root / "docs/page.md").write_text("[contribute](https://github.com/example/project/blob/main/CONTRIBUTING.md)\n", encoding="utf-8")
        for name in ("README.md", "charts/demo/README.md", "plugins/synthkit/README.md"):
            source = root / name
            source.parent.mkdir(parents=True, exist_ok=True)
            prefix = "../../" if "/" in name else ""
            source.write_text(f"[contribute]({prefix}CONTRIBUTING.md#contribute)\n", encoding="utf-8")
        self.assertEqual(MODULE.validate(root), [])


if __name__ == "__main__":
    unittest.main()
