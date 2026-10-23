# Agent Observability provisioning (manual)

`agento11y-provision` creates the Agent Observability evaluation resources a blueprint declares
under `agent_observability:` (evaluators, hooks, rules, collections, rule actions and test suites),
records them in a private local custody directory, and later disables or tears down what it
created. It is a manual, operator-driven command. This page is the one guide to it.

What it is not:

- **Not runtime admission.** Loading a blueprint with an `agent_observability:` block, or holding
  a successful receipt from this command, does not enable any synthkit pipeline. The emitter
  ignores the block. Runtime admission is separate, later work.
- **Not shipped in a release, image or Compose service.** The command is source-built only. The
  published container contains no Agent Observability provisioner, and nothing here supports a
  container, Kubernetes or HA handoff.
- **Create-only.** There is no update, adopt, retry, reset or migration path. A declared resource
  that already differs remotely refuses; changing a created resource means a new key and ID.
- **Same user, same directory.** Every cooperating invocation must run as the same effective user
  against the same physical custody directory with the same Binding. Copying, `chmod`/`chown` of
  custody, or reinitialising a directory is never a recovery step.

## Build and run

From the repository root:

```bash
just build-packages ./cmd/agento11y-provision </dev/null
./agento11y-provision --version
./agento11y-provision --help
```

`build-packages` runs `go build` for the named package and leaves `./agento11y-provision` in the
working directory. `just build` only compile-checks the module; it does not produce this binary.
`--version` prints the source-build revision, not a release identity.

## Declaring resources in a blueprint

The block is top level, beside `environments` and `workloads`. Each closure names one real agent
declared by a workload instance in the same blueprint (`agent.instance` is the post-fanout workload
instance name, `agent.agent_name` the agent's declared name, which must have a real producer):

```yaml
agent_observability:
  closures:
  - key: planner
    agent: {instance: pipeline, agent_name: Planner}
    evaluators:
    - key: e
      spec: {evaluator_id: fictional-check, version: "1", kind: regex,
             config: '{"pattern":"fiction","reject":false}',
             output_keys: [{key: passes, type: bool, pass_value: false}]}
    collections:
    - key: c
      spec: {name: Fictional collection}
    rules:
    - key: r
      spec: {rule_id: fictional-rule, enabled: true, selector: user_visible_turn,
             match: '{"agent_name":"Planner"}', sample_rate: 0, evaluator_ids: [e]}
    hooks:
    - key: h
      spec: {rule_id: fictional-hook, enabled: true, phase: preflight, priority: 0,
             match: {agent_name: Planner}, evaluator_ids: [e], action_on_fail: deny,
             blocked_tools: [fictional_tool], redact: [{id: one, regex: fictional_secret}]}
    actions:
    - key: a
      parent_rule: r
      spec: {condition: {kind: all_evaluators_pass},
             action_config: {kind: add_to_collection, collection_ids: [c]}}
```

Suites nest a draft with its cases, initial bindings and publication review:
`suites[].{key, spec, draft: {key, spec, cases[], bindings: {key, bindings[]}, publication: {key, spec}}}`.
[BLUEPRINT-SCHEMA.md](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md)
lists every accepted key.

Rules for the block:

- Every entry has a `key` unique across the whole blueprint (`[A-Za-z0-9_.-]+`). References
  between entries (`evaluator_ids`, `collection_ids`, `parent_rule`, binding `evaluator_id`) use
  these local keys and must stay inside their closure; the engine substitutes the acquired IDs.
- `spec` fields use the exact management API spellings. Unknown keys, case variants, duplicate
  keys, YAML anchors, aliases, merge keys, hexadecimal or lossy numbers and a trailing YAML
  document all refuse. Scalars must resolve to plain string, integer, float, boolean or null: an
  explicit `!!str`, `!!int`, `!!float`, `!!bool` or `!!null` tag is accepted but must still match
  the field's type, and any other tag or type (timestamps, binary, custom tags) refuses. `null`
  refuses on a typed field but is kept as a value inside open case maps (`input`, `expected`,
  `metadata`). `false`, `0`, empty lists and numeric spellings such as `1.00` are kept exactly as
  written.
- A blueprint that declares the block is held to these scalar, anchor and single-document rules
  across the whole document, not only inside the block, so no other part of the file can carry
  hidden authoring input. Keep such blueprints free of anchors and of timestamp-like or other
  specially tagged values.
- Three aliases are translated once before the plan is built. Evaluator `config` and hook/rule
  `match` accept a native object or a JSON-object string. A hook's `blocked_tools: [...]` means
  `tool_filter: {blocked_names: [...]}` and conflicts with an explicit `tool_filter`, even an
  equal or empty one. A hook's `redact: [{id, regex}]` means `redact: {patterns: [...]}`.
- The block holds no destination, credential, custody, mode or admission field. Blueprint source
  is visible through existing operator tooling, so it must never contain secrets.
- Shipped blueprints do not declare this block. Fictional examples belong under command test data
  or `e2e/fixtures/`.

## Private inputs

| Input | Flag | Limit | Notes |
|---|---|---|---|
| Blueprint | `--blueprint-file PATH` with `--blueprint-namespace NS` | 768 KiB each, 768 KiB total | Repeat the pair per blueprint. An empty namespace loads it un-namespaced. |
| Binding | `--binding-file PATH` | 64 KiB | The private custody Binding (below). |
| Management token | `--token-file PATH` or `--token-stdin` | 16 KiB | Exactly one of the two for every verb except `plan`. |
| Preview | `--preview-file PATH` | 768 KiB | A preview written by an earlier `preview --out`. |
| Approval | `--approval-file PATH` | 64 KiB | The separately authored approval document. |

The Binding, token, preview and approval files must be regular files owned by the invoking user,
mode `0600` or stricter, with one link, and every parent directory must be owned by that user or
root and not group- or world-writable (a root-owned sticky directory such as `/tmp` is allowed). A
symlink anywhere in the path, including a symlinked parent such as macOS `/tmp` or `/var`, refuses:
pass the resolved path. The token is never accepted in arguments, the environment or a dotenv file,
and the command reads no ambient credential. The token file or standard input must hold the token
alone: a trailing newline or any other whitespace or control character refuses, so write it with
`printf '%s'` rather than `echo`.

The Binding is a JSON object with exactly `deployment_id`, `state_id`, `contract_revision`,
`source_revision` and `destination: {grafana_origin, deployment_path, grafana_org, plugin_route,
effective_tenant, runtime_endpoint, runtime_tenant}`. `contract_revision` must be
`agento11yprovision-state-3264k-v2` and `source_revision` must be
`sigil-bc963fb09-engine-inventory404-v1`; no flag or field selects another. `plugin_route` is fixed
to `/api/plugins/grafana-agento11y-app/resources/eval`. Requests go to `grafana_origin` plus
`deployment_path` plus that route over verified TLS, with no proxy and no insecure option. Rotating
the token keeps the Binding unchanged; any changed origin, path, org, tenant, runtime endpoint,
deployment, state or revision is a Binding mismatch and refuses.

## Verbs

| Verb | Requires | Effect |
|---|---|---|
| `plan` (default) | blueprint pairs, `--binding-file`; optional `--out` | Offline. Prints the plan digest and resource/claim counts. No token, state or network. |
| `preview` | `--binding-file`, `--state-dir`, token, `--out`; blueprint pairs for `--mode apply`, otherwise `--mode recover\|deactivate\|teardown` with `--construction-id` and `--expect-enrolled` | Read-only: GET and list requests under the state lock. Writes the private preview to a new `--out` file. |
| `apply` | blueprint pairs, `--binding-file`, `--state-dir`, token, `--preview-file`, `--approval-file`; optional `--expect-enrolled` | Creates the planned resources serially after re-observing and re-checking the approved preview. |
| `recover` | `--binding-file`, `--state-dir`, `--expect-enrolled`, `--construction-id`, token, `--preview-file`, `--approval-file` | Resolves the pending operation of that original construction. |
| `deactivate` | as `recover` | Disables the construction's hooks, rules and actions. |
| `teardown` | as `recover` | Disables, then deletes supported hooks, rules and actions. Evaluators, collections and published suites are retained, so a full teardown reports incomplete. |

Any flag a verb does not list refuses, as does a repeated scalar flag. `--mode` belongs to
`preview` only. `--state-dir` must already exist (prepare it as a `0700` directory owned by the
invoking user); the command creates only its lock and custody files inside it. Pass
`--expect-enrolled` on every invocation after the first successful apply: without it, only a
genuinely empty directory may be initialised.

`--out` must name a file that does not exist, is not directly inside the state directory and is
distinct from every input. It is created `0600` and never overwritten. A preview can contain the private Binding, so
keep it out of shared or committed locations.

## Lifecycle

1. Stop every emitter that could produce telemetry for the affected agents and keep them stopped
   until the receipt is accepted. The command does not check this; the approval asserts it.
2. `plan` offline and review the digest and counts.
3. `preview --out preview.json` with the token: an authenticated read of the current inventory.
4. Author the approval from that preview: an `ApprovalDocument` binding the preview's digest,
   input, inventory, base, mode and namespaces, with a unique `approval_id`, the `operator`, a
   `construction_id` (for `recover` it must equal `--construction-id`), `issued_ms` and
   `expires_ms` at most 15 minutes apart, and the operator assertions and evidence (authorised
   operator, effective destination, emitters stopped and kept stopped, exclusive namespace,
   outstanding work may finish, non-atomic initial publication, permanent records may remain, no
   synthetic re-entry).
5. `apply --preview-file preview.json --approval-file approval.json`. The command re-acquires the
   same state, re-observes the inventory and refuses if anything differs from the approved
   preview; a stale preview is never refreshed. Each mutation is journalled durably before it is
   sent, one request per step, and the receipt is built from actual read-back.

Cleanup follows the same preview, approval and mutate sequence with `--mode deactivate` or
`--mode teardown` and `--construction-id` naming the construction to clean up. Deselecting or
deleting the YAML never removes custody history or produces an empty success.

If a run ends with a pending operation (a lost response, a killed process, exit 3 or 4), an
ordinary re-run that reaches the engine refuses with exit 4 (a refusal earlier than that, such as
an unreadable approval file, exits 2). Run `preview --mode recover --construction-id <id>` for the
construction that owns the pending operation (the cleanup construction for a lost disable or
delete, not the earlier apply), approve that preview with a `recovery` section carrying independent
attribution of the original attempt, then `recover`. Recovery keeps the original construction's
plan, ID and mode; only its preview and approval are in recover mode. Nothing is resent and no
current absence clears a pending operation on its own.

Settling the pending operation is its own step. A `recover` that resolves it records the
resolution durably and exits 4, because custody changed; it is not a completion. Run a second
`preview --mode recover` for the same construction, approve it afresh (without a `recovery`
section), and `recover` again to finish the construction's remaining steps.

## Capacity refusal

An offline `plan` and its resource count do not establish that custody can admit an apply. Before
any management request, `preview` forecasts the worst-case custody needed to finish an apply
(including a no-op re-apply), deactivation or suffix recovery and then start one canonical full
teardown. If that forecast exceeds capacity, preview refuses with exit 2, sends no management
request and creates no preview output. Bootstrap and each construction Start repeat the admission
check before publishing the new authority; an earlier preview is not a capacity reservation.

The forecast includes maximum encoded backend/approval values, retained history and generation
space. There is no universal resource-count ceiling: the actual declaration and existing custody
history must fit. A no-op re-apply or deactivation may itself be refused to preserve teardown room;
refusal leaves the custody head unchanged. Do not reset, prune or move custody, raise limits, or
retry unchanged input to bypass it. Review a refused fresh declaration before provisioning; for an
existing construction, follow the cleanup and recovery lifecycle above. A pending operation still
requires attributed recovery, not replay.

This is a bounded reservation for one canonical full teardown with one recovery per unsettled
step, not permission for arbitrary split cleanup plans or unlimited no-progress suffix Starts.
Teardown can still honestly retain unsupported resources and dependencies and report incomplete.
The [canonical lifecycle contract](https://github.com/rknightion/synthkit/blob/main/ARCHITECTURE.md#internal-typed-agent-observability-lifecycle-not-runtime-enabled)
owns these limits; no runtime admission is enabled.

## Output and exit codes

Standard output carries only digests, counts, the result and `"runtime_admission": false`.
Standard error carries only a reason code. Paths, destinations, tenants, agent names, tokens and
response bodies are never printed.

| Exit | Meaning |
|---|---|
| 0 | Complete. |
| 2 | Refused: this run changed nothing and sent no mutation (invalid input, mismatch, stale approval or conflict, a `preview` refusal, or a lock wait that timed out before anything was attempted). A pending operation from an earlier run may remain; run `preview` if one might. Otherwise fix the input and start again. |
| 3 | Incomplete: resources were retained or an operation stayed pending. Custody holds the detail. |
| 4 | Custody changed or the outcome is uncertain: a failure after a mutation was journalled (for example a proxy error after the backend committed), a refusal while an operation is pending, a settled recovery, a deadline, cancellation or uncertain local write. Run `preview` and follow its next step. |

For `apply`, `recover`, `deactivate` and `teardown` the command reads the custody head before the
engine call and again after any failure, under the same lock. It exits 2 only when the head
(generation and checksum, or pristine absence) is unchanged and nothing is pending; a failed
re-read also exits 4.

The whole invocation, including the lock wait, is bounded to 10 minutes and each request to
15 seconds. No flag raises these limits. A hard deadline exits 4 without releasing evidence; it is
not proof that nothing was committed.

## Developing the command

The `TestPublic*` proofs in `cmd/agento11y-provision` run the built binary against a local TLS
server whose certificate they pass through `SSL_CERT_FILE`/`SSL_CERT_DIR`. Go honours those
variables on Linux only; on macOS the system trust store is used and the proofs fail the TLS
handshake. Run them on Linux (the remote gate runner or CI).

## Limits of what this proves

Operator assertions, the local lock and configuration equality are not backend fencing, queue
drain, atomic activation, remote non-commit or anti-rollback. Retained, permanent or unresolved
resources are expected, honest outcomes. A bootstrap cannot tell a deleted custody directory from a
genuinely new one; protect the directory accordingly.
