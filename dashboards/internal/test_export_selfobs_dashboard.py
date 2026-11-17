"""Exercise the supported offline operational-dashboard exporter as a process."""

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
EXPORTER = HERE / "export_selfobs_dashboard.py"


class ExportCLITest(unittest.TestCase):
    def run_export(self, directory, *extra):
        return subprocess.run(
            [sys.executable, str(EXPORTER), "--namespace", "demo-a",
             "--deployment", "alpha", "--out", str(directory), *extra],
            text=True, capture_output=True, timeout=15, check=False,
        )

    def test_exports_scoped_resources_and_complete_query_inventory(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "export"
            result = self.run_export(out)
            self.assertEqual(result.returncode, 0, result.stderr)
            folder = json.loads((out / "folder.json").read_text())
            dashboard = json.loads((out / "dashboard.json").read_text())
            inventory = json.loads((out / "queries.json").read_text())
            self.assertEqual(folder["metadata"]["name"], dashboard["metadata"]["annotations"]["grafana.app/folder"])
            self.assertEqual(inventory["deployment"], "alpha")
            self.assertEqual(inventory["dashboard"], dashboard["metadata"]["name"])
            queries = []
            for key, panel in dashboard["spec"]["elements"].items():
                for query in panel["spec"]["data"]["spec"]["queries"]:
                    spec = query["spec"]
                    model = spec["query"]["spec"]
                    expression = model.get("expr", model.get("query", model.get("labelSelector")))
                    queries.append(("panel", key, spec["refId"], expression, spec["datasource"]["uid"]))
            for variable in dashboard["spec"]["variables"]:
                spec = variable["spec"]
                queries.append(("variable", spec["name"], spec["name"],
                                spec["query"]["spec"]["query"], spec["datasource"]["uid"]))
            self.assertCountEqual(queries, [(q["owner_kind"], q["owner"], q["ref_id"],
                                             q["expression"], q["datasource"]) for q in inventory["queries"]])
            self.assertEqual({q["signal"] for q in inventory["queries"]}, {"metrics", "logs", "traces", "profiles"})
            self.assertTrue(inventory["omissions"])
            self.assertTrue(all(x["signal"] == "spanmetrics" for x in inventory["omissions"]))

    def test_deterministic_namespaces_and_datasource_bindings(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            names = []
            first_bytes = None
            for index, namespace in enumerate(("shared-prefix-alpha", "shared-prefix-alpha", "shared-prefix-beta")):
                out = root / str(index)
                result = self.run_export(out, "--namespace", namespace,
                    "--metrics-datasource", "metrics-fixture", "--logs-datasource", "logs-fixture",
                    "--traces-datasource", "traces-fixture", "--profiles-datasource", "profiles-fixture",
                    "--folder", "operations", "--folder-title", "Operations")
                self.assertEqual(result.returncode, 0, result.stderr)
                tree = {p.name: p.read_bytes() for p in out.iterdir()}
                if index == 0:
                    first_bytes = tree
                elif index == 1:
                    self.assertEqual(tree, first_bytes)
                dashboard = json.loads(tree["dashboard.json"])
                folder = json.loads(tree["folder.json"])
                names.append((dashboard["metadata"]["name"], folder["metadata"]["name"]))
                self.assertEqual(folder["spec"]["title"], f"Operations ({namespace})")
                for query in json.loads(tree["queries.json"])["queries"]:
                    self.assertEqual(query["datasource"], query["signal"] + "-fixture")
                for panel in dashboard["spec"]["elements"].values():
                    for query in panel["spec"]["data"]["spec"]["queries"]:
                        self.assertTrue(query["spec"]["datasource"]["uid"].endswith("-fixture"))
            self.assertEqual(names[0], names[1])
            self.assertNotEqual(names[0][0], names[2][0])
            self.assertNotEqual(names[0][1], names[2][1])

    def test_bad_scope_or_binding_refuses_without_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            for index, extra in enumerate((
                ("--deployment", "$instance"), ("--deployment", ""),
                ("--deployment", 'alpha"beta'), ("--deployment", "alpha\\beta"),
                ("--namespace", "../escape"), ("--metrics-datasource", ""),
                ("--folder", "bad/folder"), ("--signals", "metrics,spanmetrics"),
            )):
                with self.subTest(extra=extra):
                    out = Path(tmp) / str(index)
                    result = self.run_export(out, *extra)
                    self.assertEqual(result.returncode, 2, result.stderr)
                    self.assertIn("error", json.loads(result.stderr))
                    self.assertFalse(out.exists())

    def test_omitted_signals_have_no_panels_or_layout_references(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "export"
            result = self.run_export(out, "--signals", "metrics", "--folder", "shared-folder",
                                     "--folder-title", "Shared operations")
            self.assertEqual(result.returncode, 0, result.stderr)
            dashboard = json.loads((out / "dashboard.json").read_text())
            inventory = json.loads((out / "queries.json").read_text())
            folder = json.loads((out / "folder.json").read_text())
            self.assertNotEqual(folder["metadata"]["name"], "shared-folder")
            self.assertEqual(folder["spec"]["title"], "Shared operations (demo-a)")
            self.assertEqual({q["signal"] for q in inventory["queries"]}, {"metrics"})
            self.assertEqual({x["signal"] for x in inventory["omissions"]}, {"logs", "traces", "profiles", "spanmetrics"})
            omitted = {x["panel"] for x in inventory["omissions"]}
            self.assertFalse(omitted.intersection(dashboard["spec"]["elements"]))

            def check_references(value):
                if isinstance(value, list):
                    for item in value:
                        check_references(item)
                elif isinstance(value, dict):
                    if value.get("kind") == "ElementReference":
                        self.assertIn(value["name"], dashboard["spec"]["elements"])
                    for item in value.values():
                        check_references(item)

            check_references(dashboard["spec"]["layout"])

    def test_no_argument_generator_keeps_published_default_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            generator = Path(tmp) / "build_selfobs_dashboard.py"
            shutil.copy2(HERE / generator.name, generator)
            result = subprocess.run([sys.executable, str(generator)], cwd=tmp,
                                    text=True, capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((Path(tmp) / "synthkit-selfobs.json").read_bytes(),
                             (HERE / "synthkit-selfobs.json").read_bytes())

    def test_metric_queries_execute_without_cross_deployment_results(self):
        promtool = shutil.which("promtool")
        if promtool is None:
            if os.environ.get("REQUIRE_PROMTOOL") == "1":
                self.fail("REQUIRE_PROMTOOL=1 but promtool is missing")
            self.skipTest("promtool is required for real PromQL execution; CLI contract tests still run")
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "export"
            result = self.run_export(out)
            self.assertEqual(result.returncode, 0, result.stderr)
            inventory = json.loads((out / "queries.json").read_text())
            expressions = []
            metric_names = set()
            for query in inventory["queries"]:
                if query["signal"] != "metrics":
                    continue
                expression = query["expression"]
                if query["owner_kind"] == "variable":
                    match = re.fullmatch(r"label_values\((.*),\s*[a-z_]+\)", expression)
                    self.assertIsNotNone(match, expression)
                    expression = match.group(1)
                expression = expression.replace("$__rate_interval", "2m").replace("$__range", "5m")
                expression = expression.replace("$instance", ".*").replace("$sink", ".*")
                metric_names.update(re.findall(r"([a-zA-Z_:][a-zA-Z0-9_:]*)\{", expression))
                expressions.append({"expr": expression, "eval_time": "10m", "exp_samples": []})
            # Receiver-projected foreign resources include the same process ID as the selected
            # deployment, so an instance-only filter cannot pass this isolation proof.
            foreign_series = [{"series": name + '{job="beta/synthkit",instance="same-process",'
                'service_name="synthkit",run_mode="live",sink="promrw",blueprint="fixture",'
                'construct_instance="fixture",outcome="error",le="+Inf"}', "values": "1+1x10"}
                for name in sorted(metric_names)]
            positive = [
                {"series": 'go_goroutine_count{job="alpha/synthkit",instance="old-process"}', "values": "3+0x10"},
                {"series": 'go_goroutine_count{job="alpha/synthkit",instance="same-process"}', "values": "5+0x10"},
                {"series": 'go_goroutine_count{job="beta/synthkit",instance="same-process"}', "values": "91+0x10"},
                {"series": 'go_memory_used_bytes{job="alpha/synthkit",instance="old-process"}', "values": "100+0x10"},
                {"series": 'go_memory_used_bytes{job="alpha/synthkit",instance="same-process"}', "values": "200+0x10"},
                {"series": 'go_memory_used_bytes{job="beta/synthkit",instance="same-process"}', "values": "9000+0x10"},
            ]
            query_by_owner = {q["owner"]: q["expression"].replace("$instance", ".*")
                              for q in inventory["queries"] if q["owner_kind"] == "panel"}
            fixture = {"evaluation_interval": "1m", "tests": [
                {"name": "foreign deployment excluded from every metric and variable selector",
                 "interval": "1m", "input_series": foreign_series, "promql_expr_test": expressions},
                {"name": "both local processes remain visible after process replacement",
                 "interval": "1m", "input_series": positive, "promql_expr_test": [
                     {"expr": query_by_owner["ov_goroutines"], "eval_time": "10m",
                      "exp_samples": [{"labels": "{}", "value": 5}]},
                     {"expr": query_by_owner["ov_heap"], "eval_time": "10m",
                      "exp_samples": [{"labels": "{}", "value": 300}]},
                 ]},
            ]}
            path = Path(tmp) / "queries.test.json"
            path.write_text(json.dumps(fixture))
            proof = subprocess.run([promtool, "test", "rules", str(path)],
                                   text=True, capture_output=True, timeout=30, check=False)
            self.assertEqual(proof.returncode, 0, proof.stdout + proof.stderr)

    def test_selector_scan_preserves_quoted_labels_and_bounds_unmatched_work(self):
        # Expressions are generator-owned. This bound catches repeated suffix scans of one
        # malformed token; it is not an operational emission or backend latency budget.
        script = r'''
import json
import sys

sys.path.insert(0, sys.argv[1])
from export_selfobs_dashboard import scope_metrics

for value in ("{braces}", 'quote " and backslash \\', 'literal job="synthkit"'):
    labels = 'job="synthkit",note=' + json.dumps(value)
    expression = 'sum(rate(first_total{' + labels + '}[2m])) / count(second{' + labels + '})'
    expected = expression.replace('job="synthkit"', 'job="alpha/synthkit"')
    assert scope_metrics(expression, "alpha") == expected
    labels = 'service_name="synthkit",note=' + json.dumps(value)
    assert scope_metrics('target_info{' + labels + '}', "alpha") == (
        'target_info{' + labels + ', job="alpha/synthkit"}')

try:
    scope_metrics("a" * 200_000, "alpha")
except ValueError:
    pass
else:
    raise AssertionError("an unmatched metric token was accepted")
'''
        result = subprocess.run([sys.executable, "-c", script, str(HERE)],
                                text=True, capture_output=True, timeout=5, check=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
