# Compatibility Evidence Agent Instructions

These instructions apply to `internal/compatibility/**` and supplement the root instructions.

The compatibility registry contains positive evidence assertions, not guesses.

- A capability is asserted only when backed by reproducible evidence for that model/protocol.
- A client certification records the exact client, version, model and scenario that passed.
- Missing capability/certification means uncertified/unknown; do not reinterpret it as broad support or permanent lack of provider support.
- Do not infer deeper tool loops, parallel tool use, other clients, later versions, or neighboring models from one successful profile.
- Fallback routing may select only candidates that positively assert every capability required by the request.
- Unknown requested models are not rewritten.
- A configured model route changes routing only; it does not certify that model/provider.

When changing built-in profiles, update README evidence/claims and the corresponding hosted/manual certification mechanism. Keep release hard gates intentionally stable unless repeatability evidence justifies changing them.
