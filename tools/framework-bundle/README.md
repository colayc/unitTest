# Cross-platform framework inputs

`manifest.json` is the closed schema-v2 source lock for CppUTest 4.0, Unity
2.6.1, and CMock 2.7.0 on Linux and Windows. Bootstrap code must reject
missing, mismatched, redirected, non-canonical, or path-escaping inputs before
a native process tree is launched.

`licenses/dependencies.json` is the immutable evidence input for the three
test dependencies: it records the pinned revision, archive license path and
digest, SPDX identifier, and revision-pinned GitHub blob URL. It is not a
substitute for the deferred human third-party license/legal approval.

The lock is an input boundary, not a runtime network exception: CI may populate
the immutable archive cache before the native offline boundary is entered.

`prepare.mjs` is the only publisher. It accepts only the locked GitHub download
chain, validates a bounded `tar` listing before extraction, and stores each
archive under its SHA-256. It then publishes a fully audited, digest-keyed
bundle at `.superpowers/runtime/framework-bundle/v2/<manifest SHA-256>/`.
Existing ready bundles are re-verified and reused; they are never deleted or
replaced. `READY` is written only after source markers, license bytes, and all
three source-tree digests have been verified.
