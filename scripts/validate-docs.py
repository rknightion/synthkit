#!/usr/bin/env python3
"""Validate the repository-owned documentation contract without the docs hub."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

MIN_PYTHON = (3, 11)

LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
HTML_LINK_RE = re.compile(r"(?:href|src)\s*=\s*[\"']([^\"']+)", re.IGNORECASE)
REFERENCE_DEFINITION_RE = re.compile(r"(?m)^[ \t]{0,3}\[[^\]\n]+\]:[ \t]*(?:<([^>\n]+)>|(\S+))")
REFERENCE_LABEL_RE = re.compile(r"(?m)^[ \t]{0,3}\[([^\]\n]+)\]:")
REFERENCE_USE_RE = re.compile(r"!?\[[^\]\n]+\]\[([^\]\n]+)\]")


def nav_pages(value: object) -> list[str]:
    pages: list[str] = []
    if isinstance(value, str):
        if value.endswith(".md"):
            pages.append(value)
    elif isinstance(value, list):
        for item in value:
            pages.extend(nav_pages(item))
    elif isinstance(value, dict):
        for item in value.values():
            pages.extend(nav_pages(item))
    return pages


def without_code(markdown: str) -> str:
    # Fenced examples are not document links; rendering semantics belong to the hub.
    markdown = re.sub(r"(?ms)^```.*?^```\s*$", "", markdown)
    # Inline code is likewise literal text, not a rendered link.
    return re.sub(r"`+[^`\n]*`+", "", markdown)


def normalize_reference_label(label: str) -> str:
    return " ".join(label.split()).casefold()


def reference_definitions(markdown: str) -> set[str]:
    return {normalize_reference_label(match.group(1)) for match in REFERENCE_LABEL_RE.finditer(without_code(markdown))}


def reference_uses(markdown: str) -> list[str]:
    return [match.group(1).strip() for match in REFERENCE_USE_RE.finditer(without_code(markdown))]


def link_targets(markdown: str) -> list[str]:
    markdown = without_code(markdown)
    targets: list[str] = []
    for match in LINK_RE.finditer(markdown):
        raw_target = match.group(1).strip()
        targets.append(raw_target.split()[0].strip("<>") if raw_target else "")
    targets.extend(HTML_LINK_RE.findall(markdown))
    targets.extend(match.group(1) or match.group(2) for match in REFERENCE_DEFINITION_RE.finditer(markdown))
    return targets


def validate(root: Path) -> list[str]:
    errors: list[str] = []
    docs = root / "docs"
    config_path = root / "docs.toml"
    if not config_path.is_file():
        return [f"missing {config_path}"]
    if not docs.is_dir():
        return [f"missing {docs}"]
    if sys.version_info < MIN_PYTHON:
        return ["Python 3.11 or newer is required (tomllib is part of the standard library from 3.11)"]
    import tomllib

    try:
        with config_path.open("rb") as config_file:
            config = tomllib.load(config_file)
    except (OSError, tomllib.TOMLDecodeError) as exc:
        return [f"cannot parse {config_path}: {exc}"]

    site = config.get("site")
    if site is not None and not isinstance(site, dict):
        errors.append("docs.toml site must be a table")
        site = {}
    pages = nav_pages(site.get("nav", []) if site is not None else [])
    if not pages:
        errors.append("docs.toml site.nav contains no Markdown pages")
    docs_resolved = docs.resolve()
    for page in pages:
        candidate = (docs / page).resolve()
        try:
            candidate.relative_to(docs_resolved)
        except ValueError:
            errors.append(f"nav target escapes docs directory: {page}")
            continue
        if not candidate.is_file():
            errors.append(f"nav target does not exist: {page}")

    if not (docs / "404.md").is_file():
        errors.append("missing required special page: docs/404.md")

    authored = {
        path.relative_to(docs).as_posix()
        for path in docs.rglob("*.md")
        if path.name != "404.md" and not {"includes", "superpowers"}.intersection(path.relative_to(docs).parts)
    }
    errors.extend(
        f"authored page is not reachable from docs.toml nav: {page}"
        for page in sorted(authored - set(pages))
    )

    root_resolved = root.resolve()
    for source in sorted(docs.rglob("*.md")):
        markdown = source.read_text(encoding="utf-8")
        defined_references = reference_definitions(markdown)
        for label in reference_uses(markdown):
            if normalize_reference_label(label) not in defined_references:
                errors.append(f"{source.relative_to(root)}: undefined Markdown reference: {label}")
        for target in link_targets(markdown):
            if not target or target.startswith("#"):
                continue
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            relative = unquote(parsed.path)
            if not relative:
                continue
            destination = (source.parent / relative).resolve()
            try:
                destination.relative_to(root_resolved)
            except ValueError:
                errors.append(f"{source.relative_to(root)}: link escapes repository: {target}")
                continue
            if not destination.exists():
                errors.append(f"{source.relative_to(root)}: broken relative link: {target}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent,
                        help="repository root (default: this script's repository)")
    args = parser.parse_args()
    if sys.version_info < MIN_PYTHON:
        print("docs-check: ERROR: Python 3.11 or newer is required", file=sys.stderr)
        return 2
    errors = validate(args.root.resolve())
    if errors:
        for error in errors:
            print(f"docs-check: ERROR: {error}", file=sys.stderr)
        return 1
    print("docs-check: OK — docs.toml navigation, Markdown links, and docs/404.md validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
