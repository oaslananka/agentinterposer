# Gateway and Protocol Agent Instructions

These instructions apply to `internal/gateway/**` and supplement the root instructions.

This package is the primary untrusted protocol/network boundary.

## Request and auth boundary

- Enforce configured request-size limits before unbounded parsing.
- Client Authorization and x-api-key values are never forwarded as upstream provider credentials.
- Preserve loopback-safe defaults and the explicit remote-opt-in contract.
- Reject malformed or unsupported Anthropic/OpenAI semantics instead of silently dropping fields.

## Translation and streaming

- Keep native OpenAI Chat Completions/Responses passthrough semantics intact unless routing selects a positively certified alternative.
- The Messages adapter implements only the documented subset; unknown top-level fields and unsupported content fail closed.
- Preserve content ordering, tool IDs/results, failed tool-result semantics, and valid JSON assembly for streamed tool arguments.
- Bound non-streaming upstream bodies and raw SSE frames.
- Flush streaming progress incrementally without converting protocol failures into clean EOF/success.

## Reliability

- Preserve bounded concurrency, retry count/backoff, retryable status set, response-header timeout and body-idle timeout.
- Active stream progress resets only the body-idle deadline; it does not disable global safeguards.
- Downstream cancellation must cancel upstream work.
- Retry drains are bounded; do not buffer arbitrary error bodies.

## Testing

Protocol changes need malformed-input and edge-path tests. Run focused fuzz targets from `CONTRIBUTING.md` when touching Messages decoding, SSE parsing, streaming tool calls, or fallback routing.
