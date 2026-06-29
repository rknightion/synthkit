# signals/qualification.md — Qualification Pipeline

Construct kind: `qualification_pipeline`
Scope: `ScopeSubstrate` — NO blueprint label (I21)
Signals: Metrics (promrw) + Logs (Loki)

---

## Section 1 — REAL: `gitlab_ci_pipeline_*` family

**Source:** [`mvisonneau/gitlab-ci-pipelines-exporter`](https://github.com/mvisonneau/gitlab-ci-pipelines-exporter) `docs/metrics.md` — fetched and verified 2026-06-15.
**Provenance:** Upstream exporter documentation (live HTTP fetch from GitHub raw).
**Status:** v: ok (names + label keys confirmed against exporter docs; no synthkit live capture yet).

The exporter scrapes GitLab's API and publishes the following metrics families.
Only the subset emitted by this construct is listed; the full exporter catalog includes
environment/deployment/test-case families not emitted here.

```yaml
signals:
  # ── Pipeline-level families ───────────────────────────────────────────────

  - name: gitlab_ci_pipeline_status
    type: gauge
    help: "Status of the most recent pipeline"
    labels:
      project:   "platform/qualification"      # GitLab project path with namespace
      ref:       "main"                         # branch / tag / MR name
      kind:      "branch"                       # ref kind: branch | tag | merge_request
      source:    "schedule"                     # pipeline trigger source
      topics:    ""                             # project topics (empty when unset)
      variables: ""                             # pipeline variables (empty when disabled)
      status:    "success"                      # one series per status value; all 7 emitted
      cloud:     "aws"                          # qualification target cloud (generic qualifier)
    # status enum (all 7 emitted per pipeline label set):
    # success | failed | running | pending | canceled | skipped | manual
    # value: 1 for the current status, 0 for all others
    note: >
      Sparse-status mode: the exporter can be configured to emit only the active status (value=1).
      synthkit emits all 7 status values (dense mode — value=1 for one, 0 for all others).

  - name: gitlab_ci_pipeline_duration_seconds
    type: gauge
    help: "Duration in seconds of the most recent pipeline"
    labels:
      project:   "platform/qualification"
      ref:       "main"
      kind:      "branch"
      source:    "schedule"
      topics:    ""
      variables: ""
      cloud:     "aws"
    value_example: 420

  - name: gitlab_ci_pipeline_run_count
    type: counter
    help: "Number of executions of a pipeline"
    labels:
      project:   "platform/qualification"
      ref:       "main"
      kind:      "branch"
      source:    "schedule"
      topics:    ""
      variables: ""
      cloud:     "aws"
    note: "True Prometheus counter; cumulative across ticks (state.Add)."

  # ── Job-level families ────────────────────────────────────────────────────

  - name: gitlab_ci_pipeline_job_status
    type: gauge
    help: "Status of the most recent job"
    labels:
      project:            "platform/qualification"
      ref:                "main"
      kind:               "branch"
      source:             "schedule"
      topics:             ""
      variables:          ""
      cloud:              "aws"
      stage:              "test"                 # CI stage name
      job_name:           "functional-tests"     # CI job name
      runner_description: "shared-runner"        # runner description
      tag_list:           ""                     # job tag list (empty when unset)
      failure_reason:     ""                     # failure reason (empty on success)
      status:             "success"              # one series per status value; all 7 emitted
    # status enum: same 7 values as pipeline_status above.

  - name: gitlab_ci_pipeline_job_duration_seconds
    type: gauge
    help: "Duration in seconds of the most recent job"
    labels:
      project:            "platform/qualification"
      ref:                "main"
      kind:               "branch"
      source:             "schedule"
      topics:             ""
      variables:          ""
      cloud:              "aws"
      stage:              "test"
      job_name:           "functional-tests"
      runner_description: "shared-runner"
      tag_list:           ""
      failure_reason:     ""
    value_example: 90
```

**Label spread:** The construct fans metrics across the configured `clouds` list (default:
`aws`, `azure`, `gcp`, `common`). Each cloud gets a full set of pipeline + job series.
Jobs are spread across the configured `stages` list in round-robin fashion.

**Not emitted** (in exporter but out of scope here):
- `gitlab_ci_environment_*` (deployment/environment families)
- `gitlab_ci_pipeline_test_report_*` and `gitlab_ci_pipeline_test_suite_*` (test report families)
- `gitlab_ci_pipeline_test_case_*` (test case families)
- `gcpe_*` (exporter self-metrics)
- `gitlab_ci_pipeline_coverage`, `gitlab_ci_pipeline_id`, `gitlab_ci_pipeline_timestamp`,
  `gitlab_ci_pipeline_queued_duration_seconds`, `gitlab_ci_pipeline_job_artifact_size_bytes`,
  `gitlab_ci_pipeline_job_id`, `gitlab_ci_pipeline_job_queued_duration_seconds`,
  `gitlab_ci_pipeline_job_run_count`, `gitlab_ci_pipeline_job_timestamp`

---

## Section 2 — COINED: `qualification_*` suite extras (SK-48)

**Source:** Coined by synthkit — no upstream exporter provides suite-level qualification signals.
**Provenance:** synthkit convention, 2026-06-15. See `cantfind.md` SK-48.
**Status:** v: assumed — names are synthkit convention; no vendor docs or live capture.

These metrics represent qualification test suite outcomes. They complement the real
`gitlab_ci_pipeline_*` family with suite-level aggregates that a CI/qualification dashboard
would typically want (total test count, failure count, report generation flag).

```yaml
signals:
  # coined (SK-48): no exporter provides suite-level qualification signals.

  - name: qualification_test_cases_total
    type: gauge
    help: "Total number of test cases executed in this qualification suite run"
    labels:
      cloud: "aws"        # qualification target cloud
      suite: "functional" # test suite name (from Config.Suites)
      # env: "prod"       # ONLY when env-scoped (I13 — absent in aggregate)
    value_example: 120
    coined: SK-48

  - name: qualification_test_failures_total
    type: gauge
    help: "Number of test failures in this qualification suite run"
    labels:
      cloud: "aws"
      suite: "functional"
      # env: "prod"       # ONLY when env-scoped (I13)
    value_example: 0
    coined: SK-48

  - name: qualification_report_generated
    type: gauge
    help: "1 if the qualification report was successfully generated, 0 otherwise"
    labels:
      cloud: "aws"
      suite: "functional"
      # env: "prod"       # ONLY when env-scoped (I13)
    value_example: 1
    coined: SK-48
```

---

## Section 3 — Loki stream

**Stream labels:** `{job="qualification-pipeline", cloud=<cloud>}` + `env=<name>` when env-scoped.
High-cardinality fields (`pipeline_id`, `ref`) ride in **structured metadata** (I14 — not stream labels).

```yaml
loki:
  - stream:
      job:   "qualification-pipeline"
      cloud: "aws"          # one stream per configured cloud
      # env: "prod"         # ONLY when env-scoped (I13)
    meta:
      pipeline_id: "1000"   # structured metadata (high-card; not a stream label)
      ref:         "main"
    line_example: |
      {"timestamp":"2026-06-15T12:00:00Z","level":"info","cloud":"aws","stage":"test",
       "job":"functional-tests","status":"success","duration_s":420,"pipeline_run":1000}
```

[slug: qualification-pipeline]
