# CI and Release Agent Instructions

These instructions apply to `.github/**` and supplement the root instructions.

## CI

- Keep Actions pinned to reviewed immutable revisions and workflow permissions least-privilege.
- Preserve gofmt, vet, race tests, govulncheck, native build and cross-platform build gates.
- Provider drift/smoke jobs are evidence producers; do not turn transient hosted-provider behavior into a silent pass.
- Manual/free-provider probes remain non-required when their availability is intentionally non-deterministic.

## Certification

Release certification is deliberately narrower than all historical successful probes.

- Preserve the serialized provider baseline -> Codex long-loop -> Claude Code loop release chain unless an explicit evidence-backed contract change updates it.
- A deeper or newer successful manual probe does not automatically broaden the release compatibility claim.
- Failed or inconsistent probes remain uncertified rather than being generalized from neighboring success.

## Release integrity

- Release tags must be semantic-version-like and contained in main.
- Preserve race tests and govulncheck before artifact production.
- Keep cross-platform archives, SHA256SUMS, build provenance attestations, draft-then-publish flow, and immutable release verification bound to the same source SHA/tag.
- Publishing write permission stays isolated to the publish job.
- Do not expose provider secrets to pull-request code or logs.

Workflow changes require actionlint plus the relevant repository tests and security review.
