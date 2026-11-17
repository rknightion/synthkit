#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Race tier with an isolated invocation for timing-sensitive tests (SKT-0162).

The names in race-isolated.txt are skipped by the parallel race sweep and run once,
afterwards, one package per `go test -race` invocation. `check` proves that split
selects every test exactly once, and that every test whose own body calls the
deadline-margin probe is isolated. Modes:

  run --exclude RE   sweep every package not matching RE, then the isolated invocations (with -v)
  show --exclude RE  print those commands without running them
  check              prove the split, and that `just race` calls this script
"""
import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = Path(__file__).with_name("race-isolated.txt")
TEST_NAME = re.compile(r"^Test[A-Za-z0-9_]+$")
TEST_DEF = re.compile(r"^func (Test[A-Za-z0-9_]+)\(", re.M)
FUNC_START = re.compile(r"^func ", re.M)
# A test whose own body calls this probes its caller budgets (internal/agento11yprovision/
# lifecycle_test.go). Only the isolated -v invocation puts those margins in the hosted log, so
# such a test must be isolated. Helpers that call it (the fixture apply) are not tests and
# also log for sweep tests that call them, visible only when one fails; that is deliberate.
MARGIN_PROBE = re.compile(r"\bdeadlineMargin\(")
PRUNE = {".git", "node_modules", "codex", "vendor", "dist"}


def load_config(path=CONFIG):
    """Return {package directory: [test names]} in file order."""
    config = {}
    seen = set()
    for number, raw in enumerate(path.read_text().splitlines(), 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        fields = line.split()
        if len(fields) != 2 or not TEST_NAME.match(fields[1]):
            raise SystemExit(f"{path}:{number}: want '<package dir> <TestName>', got {raw!r}")
        package, name = fields
        if name in seen:
            raise SystemExit(f"{path}:{number}: {name} is listed twice")
        if not any((ROOT / package).glob("*_test.go")):
            raise SystemExit(f"{path}:{number}: {package} has no test files")
        seen.add(name)
        config.setdefault(package, []).append(name)
    return config


def anchored(names):
    return "^(" + "|".join(names) + ")$"


def sweep_command(config, packages):
    names = [n for ns in config.values() for n in ns]
    command = ["go", "test", "-race"]
    if names:
        command += ["-skip", anchored(names)]
    return command + packages


def isolated_commands(config):
    # -v prints each isolated test's duration in the hosted log; the sweep stays quiet.
    return [["go", "test", "-race", "-v", "-run", anchored(names), "./" + package] for package, names in config.items()]


def module_packages(exclude):
    out = subprocess.run(["go", "list", "./..."], cwd=ROOT, check=True, capture_output=True, text=True).stdout.split()
    return [p for p in out if not re.search(exclude, p)]


def run(args):
    config = load_config()
    commands = [sweep_command(config, module_packages(args.exclude))] + isolated_commands(config)
    if args.mode == "show":
        for command in commands:
            print(" ".join(command))
        return 0
    status = 0
    for command in commands:
        print("+ " + " ".join(command[:6]) + (" ..." if len(command) > 6 else ""), flush=True)
        code = subprocess.run(command, cwd=ROOT).returncode
        if code < 0:  # killed by a signal (an OOM kill): never let it read as success
            code = 128 - code
        status = status or code
    return status


def selected(names, run_pattern=None, skip_pattern=None):
    """Apply go test's -run and -skip to top-level names (patterns are unanchored searches)."""
    return {
        n for n in names
        if (run_pattern is None or re.search(run_pattern, n)) and (skip_pattern is None or not re.search(skip_pattern, n))
    }


def listed(package):
    # `go test -list` ignores -run and -skip, so it supplies the universe and the selection
    # is applied with the same patterns the recipe passes. Names are plain anchored
    # alternations, where Go (RE2) and Python regex semantics agree.
    out = subprocess.run(["go", "test", "-list", ".*", "./" + package], cwd=ROOT, capture_output=True, text=True)
    if out.returncode != 0:
        raise SystemExit(f"go test -list ./{package} failed:\n{out.stdout}{out.stderr}")
    return [line for line in out.stdout.splitlines() if re.match(r"^(Test|Benchmark|Example|Fuzz)\w*$", line)]


def definitions(names):
    """Directories defining each top-level Test name, over every test file in the module."""
    found = {n: set() for n in names}
    for directory, subdirs, files in os.walk(ROOT):
        subdirs[:] = [d for d in subdirs if d not in PRUNE]
        for file in files:
            if file.endswith("_test.go"):
                for name in TEST_DEF.findall((Path(directory) / file).read_text(errors="replace")):
                    if name in found:
                        found[name].add(os.path.relpath(directory, ROOT))
    return found


def margin_tests():
    """(directory, Test name) for every top-level test whose own body calls the margin probe."""
    found = []
    for directory, subdirs, files in os.walk(ROOT):
        subdirs[:] = [d for d in subdirs if d not in PRUNE]
        for file in sorted(files):
            if not file.endswith("_test.go"):
                continue
            text = (Path(directory) / file).read_text(errors="replace")
            starts = [m.start() for m in FUNC_START.finditer(text)] + [len(text)]
            for begin, end in zip(starts, starts[1:]):
                name = TEST_DEF.match(text, begin)
                if name and MARGIN_PROBE.search(text, begin, end):
                    found.append((os.path.relpath(directory, ROOT), name.group(1)))
    return found


def check(_args):
    config = load_config()
    problems = []

    recipe = subprocess.run(["just", "--show", "race"], cwd=ROOT, capture_output=True, text=True)
    if recipe.returncode != 0:
        problems.append(f"just --show race failed: {recipe.stderr.strip()}")
    else:
        if "scripts/race_split.py run" not in recipe.stdout:
            problems.append("the race recipe does not call scripts/race_split.py run, so nothing is split")
        if "/internal/integration$" not in recipe.stdout:
            problems.append("the race recipe no longer excludes /internal/integration")
        if re.search(r"^\s*go test", recipe.stdout, re.M):
            problems.append("the race recipe runs go test itself instead of delegating to the split")

    workflow = (ROOT / ".github/workflows/ci.yml").read_text()
    if not re.search(r"^\s*- run: just race\s*$", workflow, re.M):
        problems.append(".github/workflows/ci.yml does not run the race tier through `just race`")

    sweep = sweep_command(config, [])
    sweep_skip = sweep[sweep.index("-skip") + 1] if "-skip" in sweep else None
    if sweep_skip is None:
        problems.append("the sweep command carries no -skip, so isolated tests would run twice")
    for package, names in config.items():
        universe = listed(package)
        missing = [n for n in names if n not in universe]
        if missing:
            problems.append(f"{package}: isolated names that no longer exist (they would run zero times): {missing}")
        sweep_run = selected(universe, skip_pattern=sweep_skip) if sweep_skip else set(universe)
        command = isolated_commands({package: names})[0]
        isolated_run = selected(universe, run_pattern=command[command.index("-run") + 1])
        both = sweep_run & isolated_run
        neither = set(universe) - sweep_run - isolated_run
        if both:
            problems.append(f"{package}: runs twice (sweep and isolated): {sorted(both)}")
        if neither:
            problems.append(f"{package}: runs zero times: {sorted(neither)}")
        if isolated_run != set(names):
            problems.append(f"{package}: isolated invocation selects {sorted(isolated_run)}, want {sorted(names)}")
        if sweep_run & set(names):
            problems.append(f"{package}: the sweep still runs {sorted(sweep_run & set(names))}")

    for name, directories in definitions([n for ns in config.values() for n in ns]).items():
        owners = {p for p, ns in config.items() if name in ns}
        if directories - owners:
            problems.append(f"{name} is also defined in {sorted(directories - owners)}; the sweep skip would hide it there")

    for package, name in margin_tests():
        if name not in config.get(package, []):
            problems.append(f"{package}: {name} calls the deadline-margin probe but is not isolated: the quiet sweep hides its margins and contends its caller budget")

    for problem in problems:
        print("race-split: " + problem, file=sys.stderr)
    if not problems:
        total = sum(len(ns) for ns in config.values())
        print(f"race-split: {total} isolated tests across {len(config)} package(s) run exactly once")
    return 1 if problems else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="mode", required=True)
    for mode in ("run", "show"):
        p = sub.add_parser(mode)
        p.add_argument("--exclude", required=True, help="regexp of package import paths left out of the sweep")
        p.set_defaults(func=run)
    sub.add_parser("check").set_defaults(func=check)
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
