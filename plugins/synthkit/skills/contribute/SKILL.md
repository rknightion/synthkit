---
name: contribute
description: "Implement and validate synthkit repository contributions using its shared contributor gates; not deployment or read-only audits."
---

# Contribute to synthkit

Use this workflow for authorized changes to synthkit code, tests, configuration, skills, or docs.
It does not authorize a deployment, a live tenant mutation, or expanding a read-only request.

Establish the checkout with `git rev-parse --show-toplevel`, inspect its status, and read its
`AGENTS.md` plus `CONTRIBUTING.md` and `docs/contributing.md`. Preserve unrelated edits. The guide
owns prerequisites and validation policy; the checkout's `justfile` owns commands. Do not require
another contributor's home directory, credentials, or personal agent setup.

1. Identify the changed contract and its owning layer. Read `ARCHITECTURE.md` for tier/runner/sink
   changes, the relevant `signals/` source for telemetry changes, and nested agent instructions
   for the directories being edited. For blueprint composition, also use the bundled
   `create-blueprint` skill in this plugin.
2. Reproduce a bug before fixing it. Use focused public-boundary checks while implementing;
   documentation and pure wiring need relevant validation, not artificial unit tests. Never weaken
   a gate or change captured reality to agree with synthetic output.
3. Regenerate derived schema or skill links when their sources change using `just gen` or
   `just skills-sync`, then review the diff. For renderer changes, retain an explicitly selected
   `just dump` inventory and compare it with sourced signal contracts; registration-gated families
   need a controlled registered fixture. An absent series is not evidence of correct emission.
4. Run `just check </dev/null` on the final candidate. Use the contributor guide's Docker/CI and
   change-specific slow-tier instructions. Keep hosted CI results bound to the submitted revision;
   local and hosted gates are not identical. Report unavailable or failed checks explicitly and
   require an explicit maintainer exception before substituting CI for the required local gate.
5. Review the final diff and correct findings. Use the repository's contribution/review policy;
   personal maintainer services are not portable prerequisites. Summarize behavior, actual checks,
   source evidence, and remaining gaps. Skill invocation itself is never verification.

Do not run live provisioning recipes or bypass their confirmation prompts as part of contribution
validation. Secrets and customer identifiers must not enter source, reports, or external reviews.
