# SynthKit review

## Operator scope

This project generates representative telemetry from deployable blueprints. Review changes against the documented operators, blueprint field contract and the expected signal surfaces.

## Signal fidelity

Ground metric, label and field names in the recorded signal provenance. Keep time and identity deterministic for a generation, preserve cumulative state, and omit absent labels. Review counters, gauges and histogram semantics as well as the inventory. Keep deployable rates representative and put accelerated cases in fixtures.

## Module boundaries

Keep constructs and workloads isolated as enforced by `internal/archtest`. Use literal environment keys and maintain their example and Compose coverage. Regenerate the blueprint schema and field documentation when public fields change.

## Runtime safety

Use isolated state for offline inventory proofs. Keep live credentials and real capture identifiers out of review evidence. Preserve the separate self-observability credential boundary and use synthetic fixtures for new examples.

## Verification

Run `just check` and the repository's selected final race and lab checks. Review renderer changes through the inventory comparison with recorded signal definitions. Use the existing CI output for formatting, generated drift and schema diagnostics.
