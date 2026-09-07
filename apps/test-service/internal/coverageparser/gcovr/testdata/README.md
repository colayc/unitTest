# Locked gcovr 8.6 fixtures

All fixtures target gcovr 8.6 JSON format `0.14`, pinned by the coverage
bundle. `provenance.json` pins the source manifest SHA-256, Python and gcovr
versions, and every committed fixture digest (including negative fixtures).

`fixture-source.c` and `generate-linux-fixtures.sh` reproduce raw evidence on
Linux CI. The generator accepts only the workspace prepared root
`.superpowers/runtime/coverage-bundle/linux-x64`: it invokes the bundle
checker and cross-checks `manifest.resolved.json` with the SHA-256 of
`tools/coverage-bundle/manifest.json`. It fails closed unless the platform is
`linux-x64`, Python is `3.14.6`, and gcovr is `8.6`; an alternate bundle root
is rejected.

CI must provide absolute, explicitly version-pinned `UNIT_TEST_IDE_GCC` and
`UNIT_TEST_IDE_GCOV` paths, a semver `UNIT_TEST_IDE_GCC_VERSION`, and
`UNIT_TEST_IDE_GCOVR_ALLOWED_TOOLCHAIN_DIR`; both resolved executables must be
below that directory. The generator extracts the first semver token from each
GNU tool's dump output (falling back to its first banner token), then requires
both to equal the pin. `gnu-tool-version-output.json` locks the paired GCC/gcov
multi-line-banner regression case. The generator records compiler version and
executable SHA-256 rather than accepting a compiler selected from ambient
`PATH`. The caller must create and set
`UNIT_TEST_IDE_GCOVR_FIXTURE_ARTIFACT_DIR`. Generation writes a raw JSON file
and digest sidecar, a deterministic canonical JSON projection and digest
sidecar, plus timestamp-free `generation.json`; no raw digest placeholder is
committed to this repository.

After generation, CI must run:

```
bash apps/test-service/internal/coverageparser/gcovr/testdata/verify-linux-fixtures.sh <artifact-dir>
```

The verifier fails when raw evidence or a sidecar is absent, strictly validates
the closed generation metadata (bundle, source, toolchain path/version/digest,
and artifact names), recomputes both digests, and verifies canonical byte
identity before parsing raw evidence. Sidecars are exactly `SHA-256  basename`
records: they contain no generating-host path and remain valid if the complete
artifact directory is moved. For full provenance verification, CI must also
provide `UNIT_TEST_IDE_GCC_VERSION`: the verifier re-resolves the recorded GCC
and gcov paths, requires regular executables below the recorded trusted
toolchain root, rehashes their bytes, re-detects each first GNU semver token,
and compares all of those values with both metadata and the explicit pin.
`verifier-toolchain-tampering.json` records replacement-path, replacement-byte,
and replacement-version regressions that must fail. It therefore cannot claim
a raw-output comparison when no raw artifact exists.

`schema-variants.json` is a hand-composed canonical variant covering the
documented 8.6 field family (destination block IDs, conditions, decisions,
calls, trace sources, exclusions, and an unknown function). It is not claimed
to be raw tool output; its committed digest is independently verified alongside
the CI raw/canonical artifact flow.

This Windows host cannot execute the Linux bundle. It does not claim to have
generated Linux raw output; the exact CI/Linux command and locked identity are
recorded in `provenance.json`. Committed fixtures contain no host paths or
environment data.
