# Locked gcovr 8.6 fixtures

All fixtures target gcovr 8.6 JSON format `0.14`, the version pinned by the
coverage bundle (`gcovr==8.6`). `simple.json`, `branches.json`, and
`functions.json` are reduced deterministic representations of the documented
8.6 `--json` output. `schema-variants.json` is a fixed composition of the
documented 8.6 GCC JSON-field variants: destination block IDs, conditions,
decisions, calls, trace data sources, exclusions, and an unknown function.

The current implementation host is Windows and does not run the Linux bundle.
The Linux CI/native acceptance run generates and validates actual bundled
gcovr output; these repository fixtures deliberately contain no host paths or
environment data.
