# AgentInterposer Agent Instructions

These instructions apply repository-wide. A nested `AGENTS.md` adds or narrows implementation rules; repository-wide security, compatibility evidence, release integrity, and product-truth constraints remain mandatory.

## Product contract

AgentInterposer is a local-first compatibility gateway between coding agents and LLM providers. It is not a universal protocol emulator.

- Preserve the documented v1 compatibility boundary in `README.md`.
- Missing compatibility evidence means uncertified/unknown, not supported and not universally unsupported.
- Do not add compatibility claims without reproducible evidence for the exact client/version/model/scenario.
- Unknown or unsupported semantics fail closed rather than being silently dropped or approximated.

## Nested boundaries

- `.github/AGENTS.md` — CI, provider drift/certification, release, provenance, immutable publication.
- `internal/gateway/AGENTS.md` — HTTP/protocol translation, streaming, retries, cancellation, request/output bounds.
- `internal/compatibility/AGENTS.md` — positive capability and client certification registry.

## Security

- Loopback is the safe default. Non-loopback binding requires explicit remote opt-in and an external auth/network boundary.
- Server-owned upstream credentials are never replaced by client Authorization/x-api-key values.
- Do not log request/response bodies, provider credentials, bearer tokens, or sensitive agent traffic by default.
- Preserve bounded request bodies, upstream header/body-idle waits, retry drains, SSE frames, and concurrency.
- Downstream cancellation propagates upstream.
- Per-model routing changes provider selection only; it does not create compatibility certification.

## Change discipline

Keep protocol translation isolated from provider reliability and operator routing. Prefer standard-library dependencies unless a dependency clearly reduces protocol or maintenance risk.

Protocol/parser/streaming/routing changes require focused tests; fuzz the documented targets when changing Messages decoding, SSE parsing, tool streaming, or fallback routing.

## Verification

Run:

```bash
gofmt -w ./cmd ./internal
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go build ./cmd/agentinterposer
```

Do not require live provider credentials for normal unit tests. Hosted certification is separate evidence.

## Definition of done

Behavior, tests, compatibility claims, security bounds, docs and exact-head CI must agree. Release/provider certification workflows are required only when the changed contract calls for them; never fabricate a hosted result.
