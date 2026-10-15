#!/usr/bin/env python3
"""Validate the repository-owned documentation contract without the docs hub."""

from __future__ import annotations

import argparse
import html
import unicodedata
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


# These exact files are injected by the documentation hub, not repository assets.
# Keep in sync with the documented hub-output exclusions in .gitignore.
HUB_ASSETS = frozenset({"assets/social-card.png", "assets/social-card.svg",
                        "assets/project-icon.svg", "assets/project-icon-solid.svg",
                        "stylesheets/brand.css"})


def without_fences(markdown: str) -> str:
    lines = []
    fence = None
    for line in markdown.splitlines():
        match = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", line)
        if fence:
            if match and match[1][0] == fence[0] and len(match[1]) >= len(fence) and not match[2].strip():
                fence = None
            lines.append("")
        elif match:
            fence = match[1]
            lines.append("")
        else:
            lines.append(line)
    return "\n".join(lines)


def without_code(markdown: str) -> str:
    # Examples are literal text, not links. Retain heading text separately for IDs.
    markdown = without_fences(markdown)
    return re.sub(r"(`+)(?!`)(.+?)(?<!`)\1(?!`)", "", markdown)


def markdown_anchors(markdown: str) -> set[str]:
    """Common GitHub and MkDocs heading IDs plus explicit HTML/attribute IDs.

    This is intentionally not a Markdown renderer: dynamic includes and external
    URLs are outside the local contract. Accept both renderers' punctuation and
    duplicate-heading conventions for docs shared between GitHub and the hub.
    """
    markdown = without_fences(markdown)
    anchors = set(re.findall(r"\b(?:id|name)=[\"']([^\"']+)", markdown))
    counts: dict[str, int] = {}
    lines = markdown.splitlines()
    for index, line in enumerate(lines):
        match = re.match(r"^ {0,3}#{1,6}\s+(.+?)(?:\s+#+)?\s*$", line)
        heading = match[1] if match else None
        if heading is None and index + 1 < len(lines) and line.strip() and re.fullmatch(r" {0,3}(?:=+|-+)\s*", lines[index + 1]):
            heading = line.strip()
        if heading is None:
            continue
        explicit = re.search(r"\{#([^ }]+)[^}]*}\s*$", heading)
        if explicit:
            anchors.add(explicit[1])
            heading = heading[:explicit.start()]
        heading = re.sub(r"<[^>]+>", "", heading)
        heading = re.sub(r"!?\[([^]]+)\]\([^)]*\)", r"\1", heading)
        heading = html.unescape(heading).lower().replace("`", "")
        # GitHub strips punctuation; MkDocs also normalizes Unicode and spaces.
        github = re.sub(r"[^\w\- ]", "", heading).replace(" ", "-")
        ascii_heading = unicodedata.normalize("NFKD", heading).encode("ascii", "ignore").decode()
        mkdocs = re.sub(r"[-\s]+", "-", re.sub(r"[^\w\s-]", "", ascii_heading)).strip("-")
        for slug in {github, mkdocs}:
            count = counts.get(slug, 0)
            anchors.add(slug if not count else f"{slug}-{count}")
            if count:
                anchors.add(f"{slug}_{count}")
            counts[slug] = count + 1
    return anchors


def maintained_markdown(root: Path) -> list[Path]:
    # Never walk arbitrary repository trees: backlog, captures and private scratch
    # are historical evidence, not published documentation. Preserve all docs/ checks.
    sources = set(root.glob("*.md"))
    for directory in ("docs", "charts", "plugins/synthkit", "signals"):
        sources.update((root / directory).rglob("*.md"))
    return sorted(sources)


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

    assets = config.get("assets", {})
    if not isinstance(assets, dict):
        errors.append("docs.toml assets must be a table")
        assets = {}
    for key in ("social_image", "extra_css", "extra_javascript"):
        value = assets.get(key, [] if key != "social_image" else "")
        values = value if isinstance(value, list) else [value]
        for target in values:
            if not isinstance(target, str):
                errors.append(f"docs.toml: invalid asset in {key}: expected a string")
                continue
            if not target:
                continue
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            relative = unquote(parsed.path)
            destination = (docs / relative).resolve()
            if not destination.is_relative_to(docs_resolved):
                errors.append(f"docs.toml: asset escapes docs directory: {target}")
            elif relative not in HUB_ASSETS and not destination.is_file():
                errors.append(f"docs.toml: missing asset: {target}")

    root_resolved = root.resolve()
    anchor_cache: dict[Path, set[str]] = {}
    for source in maintained_markdown(root):
        markdown = source.read_text(encoding="utf-8")
        defined_references = reference_definitions(markdown)
        for label in reference_uses(markdown):
            if normalize_reference_label(label) not in defined_references:
                errors.append(f"{source.relative_to(root)}: undefined Markdown reference: {label}")
        for target in link_targets(markdown):
            if not target:
                continue
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            relative = unquote(parsed.path)
            destination = (source.parent / relative).resolve() if relative else source.resolve()
            try:
                destination.relative_to(root_resolved)
            except ValueError:
                errors.append(f"{source.relative_to(root)}: link escapes repository: {target}")
                continue
            if source.resolve().is_relative_to(docs_resolved) and not destination.is_relative_to(docs_resolved):
                errors.append(f"{source.relative_to(root)}: link escapes published docs directory: {target}; use an external repository URL")
                continue
            if not destination.exists():
                errors.append(f"{source.relative_to(root)}: broken relative link: {target}")
            elif parsed.fragment and destination.suffix.lower() == ".md" and destination.is_file():
                if destination not in anchor_cache:
                    anchor_cache[destination] = markdown_anchors(destination.read_text(encoding="utf-8"))
                if unquote(parsed.fragment) not in anchor_cache[destination]:
                    errors.append(f"{source.relative_to(root)}: missing Markdown anchor: {target}")
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
    print("docs-check: OK — navigation, maintained Markdown links/anchors, assets, and docs/404.md validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
