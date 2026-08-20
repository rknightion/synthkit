#!/usr/bin/env python3
"""Focused fixture tests for validate-docs.py."""

import importlib.util
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

    def test_valid_fixture(self):
        self.assertEqual(MODULE.validate(self.fixture()), [])

    def test_broken_nav_and_missing_404_are_reported(self):
        errors = MODULE.validate(self.fixture("missing.md", include_404=False))
        self.assertIn("nav target does not exist: missing.md", errors)
        self.assertIn("missing required special page: docs/404.md", errors)

    def test_broken_relative_link_is_reported(self):
        root = self.fixture()
        (root / "docs/page.md").write_text("[missing](absent.md)\n", encoding="utf-8")
        self.assertTrue(any("broken relative link: absent.md" in error for error in MODULE.validate(root)))

    def test_non_table_site_is_reported(self):
        root = self.fixture()
        (root / "docs.toml").write_text('site = "not-a-table"\n', encoding="utf-8")
        self.assertIn("docs.toml site must be a table", MODULE.validate(root))


if __name__ == "__main__":
    unittest.main()
