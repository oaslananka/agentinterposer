# AgentInterposer

[![CI](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml/badge.svg)](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml)

AgentInterposer is a local-first compatibility gateway between coding agents and LLM providers.

> **Status:** stable v1. The compatibility boundary below is the supported v1 contract; compatibility breadth outside that boundary remains evidence-driven and may expand in later releases. The current foundation provides hardened OpenAI-compatible Chat Completions, native Responses passthrough, and an Anthropic Messages adapter for text, top-level and text-only mid-conversation system instructions, base64 and URL user image inputs, and custom client tools in both non-streaming and SSE streaming modes. Manual compatibility probes verify Codex CLI `0.147.0` over Responses for a single shell-tool round trip plus dependent two-tool and three-tool loops, and Codex CLI `0.148.0` for those scenarios plus a dependent four-tool loop, and Claude Code CLI `2.1.226` and `2.1.233` over Messages for single Bash-tool, dependent two-tool, and error-recovery flows, plus Claude Code CLI `2.1.235` for those established bare-mode scenarios, a dependent three-tool loop, and a separately repeated normal-mode single Bash-tool round trip that preserves Claude Code's mid-conversation system turns, with `nvidia/nemotron-3-super-120b-a12b` through AgentInterposer. OpenCode CLI `1.18.18` is additionally certified over streaming Chat Completions for a dependent two-tool `read` -> `bash` proof with `nvidia/nemotron-3-super-120b-a12b`. A separate randomized hosted image probe certifies base64 Messages vision input with `meta/llama-3.2-11b-vision-instruct`. These are narrow certification profiles, not claims of universal agent or model compatibility.

## Why AgentInterposer?

Coding agents often expect different API protocols and subtly different streaming, tool-calling, and reasoning behavior. Model providers also expose different limits and transient failure modes. AgentInterposer is intended to make those boundaries explicit and testable instead of hiding them behind a generic proxy.

The project is designed around three principles:

- **Agent-aware compatibility:** preserve the behavior coding agents depend on, not just JSON shape.
- **Provider-aware reliability:** bound concurrency, bound upstream header/body idle waits, and handle retryable provider capacity failures without retry storms.
- **Local-first BYOK:** credentials stay in the local process environment and are never stored in the repository.

## Current capabilities

The v1 slice supports:

- `GET /healthz`
- `GET /v1/models` for upstream model discovery
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages` for Anthropic-compatible text, top-level and text-only mid-conversation system instructions, base64 and URL user image inputs, and custom client-tool requests, including SSE streaming
- OpenAI-compatible request/response passthrough to an upstream provider without JSON translation
- server-owned upstream bearer authentication (client credentials are not forwarded)
- bounded upstream concurrency (default: `3`)
- exponential retry for `429`, `500`, `502`, `503`, and `504` responses
- incremental flushing for `text/event-stream` responses
- manual Codex CLI `0.147.0` certification for a Responses shell-tool round trip plus dependent two-tool and three-tool loops, and Codex CLI `0.148.0` certification for those scenarios plus a dependent four-tool loop, with `nvidia/nemotron-3-super-120b-a12b`
- manual Claude Code CLI `2.1.226` and `2.1.233` certification for the established bare-mode Messages single Bash-tool, dependent two-tool, and error-recovery round trips, plus Claude Code CLI `2.1.235` certification for those scenarios, a dependent three-tool loop, and a separately repeated normal-mode single Bash-tool round trip with mid-conversation system turns, with `nvidia/nemotron-3-super-120b-a12b`
- manual OpenCode CLI `1.18.18` certification for a dependent `read` -> `bash` two-tool loop over streaming Chat Completions with `nvidia/nemotron-3-super-120b-a12b`
- randomized hosted base64-image Messages certification with `meta/llama-3.2-11b-vision-instruct`
- configurable request-size protection (default: `32 MiB`)
- safe loopback binding by default (`127.0.0.1:11435`)

The default upstream is NVIDIA's hosted API at `https://integrate.api.nvidia.com`. AgentInterposer uses native upstream Responses support when the provider exposes it instead of translating Responses payloads into Chat Completions.

## V1 compatibility contract

AgentInterposer v1 is intentionally a **local-first, evidence-backed compatibility gateway**, not a promise to emulate every OpenAI or Anthropic API feature. The boundary below is the stable public contract for the v1 line. Features outside this boundary may be added later without weakening the documented supported behavior.

| Surface | V1 contract |
| --- | --- |
| HTTP endpoints | `GET /healthz`, `GET /v1/models`, `POST /v1/chat/completions`, `POST /v1/responses`, and `POST /v1/messages` remain the public gateway surface. |
| Chat Completions / Responses | OpenAI-compatible requests and responses are native upstream passthrough. AgentInterposer adds auth isolation, concurrency/retry/timeout controls, request-size protection, streaming flush behavior, and optional evidence-based model routing; it does not normalize arbitrary provider-specific JSON into a universal schema. |
| Anthropic Messages | `POST /v1/messages` implements the documented `anthropic-version: 2023-06-01` subset: text, top-level and text-only mid-conversation system instructions, base64/HTTP(S)-URL user images, custom client tools/tool choice/tool use, text tool results (including failed tool-result preservation), and text/custom-tool SSE streaming. Mid-conversation system instructions preserve their position when translated to an upstream that accepts Chat Completions `system` turns; non-text system-message content and other unknown or unsupported semantics fail closed rather than being silently dropped. |
| Routing | Capability fallback selects only positively certified model profiles. Unknown requested models are not rewritten. A routed request changes only the exact top-level `model`; provider-specific fields are preserved. Per-model routes are operator configuration and do not themselves create compatibility certification. |
| Reliability bounds | Request bodies are bounded (32 MiB by default); Messages non-streaming upstream bodies and raw upstream SSE frames are capped at 32 MiB. Upstream response-header and body-idle waits are bounded, active stream progress resets the idle deadline, retry drains are bounded, and downstream cancellation propagates upstream. |
| Security boundary | The default listener is loopback-only. AgentInterposer owns upstream bearer credentials and never forwards client `Authorization`/`x-api-key` values upstream. AgentInterposer does not authenticate clients in v1; non-loopback use therefore requires the explicit remote opt-in plus an external authentication/network boundary. |
| Compatibility evidence | Built-in model capabilities and client/version/scenario certifications are positive evidence snapshots. Missing evidence means **uncertified/unknown**, not universally unsupported. External provider/model behavior may drift independently of the gateway. |
| Stability | Within the v1 line, documented supported behavior is intended to remain backward-compatible. Validation may become stricter when required to fix a security or protocol-safety defect; unsupported or uncertified areas may be added later without weakening the fail-closed contract. |

### Explicitly unsupported in the v1 Messages adapter

The following are outside the v1 Messages translation contract and are rejected rather than approximated: `stop_sequences`, `top_k`, `service_tier`, cache-only `max_tokens=0`, unsupported top-level fields such as `cache_control`, `container`, `inference_geo`, `output_config`, and `anthropic-user-profile-id`, Files API `file_id` image sources, image-bearing `tool_result` blocks, thinking blocks, and server tools. The four tolerated `anthropic-beta` compatibility markers are transport/client compatibility inputs only; accepting a marker does not implement broader beta-only semantics.

For `/v1/responses`, AgentInterposer v1 keeps native upstream passthrough. The **fallback router** is deliberately narrower: it automatically reroutes only direct-string or structured `input_text` requests with no tools. `input_image`, `input_file`, non-message items, and tool-bearing Responses requests are not translated or fallback-routed; a provider may still support them natively, but they are outside AgentInterposer's certified fallback contract.

### Uncertified is not unsupported

The following are deliberately not v1 blockers and must not be inferred from nearby successful certifications: Codex dependent five-tool-or-deeper loops, Claude Code same-turn parallel tool use, OpenCode behavior beyond the exact certified `1.18.18` dependent two-tool scenario, end-to-end Continue compatibility beyond its connection helper, compatibility with a second external provider, uncertified client versions/models, and multimodal/tool-bearing Responses fallback. These areas remain candidates for later evidence-backed expansion.

The release certification chain intentionally uses the more repeatable dependent three-tool Codex scope and dependent two-tool Claude Code scope. Deeper successful manual certifications remain additional evidence rather than making the release gate more fragile.

### Compatibility profiles

AgentInterposer keeps explicit built-in compatibility assertions for model/client combinations that have reproducible certification evidence. The `nvidia/nemotron-3-super-120b-a12b` profile asserts Chat Completions, native Responses, and tool calling, and records the hosted Codex CLI `0.147.0` and `0.148.0` plus Claude Code CLI `2.1.226`, `2.1.233`, and `2.1.235` and OpenCode CLI `1.18.18` certification scenarios described below, including the repeated Claude Code `2.1.235` normal-mode single-tool scenario. Vision input is intentionally not asserted for that model. The `nvidia/nemotron-3-ultra-550b-a55b` profile also asserts Chat Completions, native Responses, and tool calling after repeated hosted full probes plus two successful Codex CLI `0.148.0` single-tool round trips; deeper client loops and vision remain uncertified for Ultra. The `nvidia/nemotron-3.5-lightning-30b-a3b` profile asserts Chat Completions only after repeated hosted probes; native Responses returned 404 and tool/vision behavior is unasserted. The `nvidia/nemotron-3-nano-30b-a3b` profile asserts Chat Completions and native Responses only, backed by repeated hosted baseline/full probes; tool calling remains uncertified because repeated Messages tool probes were not consistent, and vision/client-version scenarios are also unasserted. The `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` profile asserts Chat Completions and vision input only, backed by repeated randomized hosted image reads; direct native Responses returned a hosted 503 during certification, so Responses is intentionally unasserted, and tool/client-version scenarios remain uncertified. The `meta/llama-3.2-11b-vision-instruct` profile asserts Chat Completions and vision input only, backed by a hosted randomized base64-image read through the Messages adapter; Responses and tool calling remain uncertified for that profile.

Profiles are positive assertions, not guesses: absence of a capability means **uncertified/unknown**, not a universal claim that the provider can never support it. The compatibility layer can conservatively select the first candidate model whose profile asserts every required capability while skipping unknown or incomplete candidates. The opt-in `AGENTINTERPOSER_FALLBACK_MODELS` routing slice can route Anthropic Messages and OpenAI-compatible Chat Completions image requests from a known-but-not-vision-certified requested model to the first fallback profile that asserts both Chat Completions and vision input. It can also route text-only Responses requests—either a direct string or structured message content made exclusively of `input_text` parts—when the requested known profile lacks native Responses support and a configured fallback positively asserts `responses`. Unknown requested models are never rewritten. Chat Completions and Responses remain byte-for-byte passthrough when no fallback is selected; routed requests rewrite only the exact top-level `model` field and preserve provider-specific fields. Routing inspection treats JSON protocol field names as case-sensitive, so mis-cased aliases are never interpreted as routing signals. Text-only Responses requests with tools can also route, but only to a fallback profile that positively asserts both native Responses and tool calling; candidates with Responses support but uncertified tool calling are skipped. Responses inputs containing `input_image`, `input_file`, or non-message items are intentionally not routed yet. A configured model can additionally point at a dedicated upstream through `AGENTINTERPOSER_MODEL_ROUTES`; if a capability fallback selects such a model, the request follows that model route automatically.

The built-in registry is also available as secret-free JSON from the CLI, which is useful for scripts and compatibility debugging:

```bash
./agentinterposer capabilities nvidia/nemotron-3-super-120b-a12b
```

The command returns only positively asserted capabilities and the exact client/version/scenario certification records. Unknown models fail explicitly instead of receiving inferred capabilities.

## Architecture

```text
Coding agent / OpenAI-compatible client
                 |
                 | GET  /v1/models
                 | POST /v1/chat/completions
                 | POST /v1/responses
                 | POST /v1/messages
                 v
          AgentInterposer
          127.0.0.1:11435
                 |
        concurrency + retry
        auth isolation + SSE
                 |
                 v
      OpenAI-compatible upstream
        (NVIDIA by default)
```

OpenAI Responses uses native passthrough through the same reliability core. The Anthropic Messages path is an explicit adapter: it translates the documented text, base64 and URL user image, and custom client-tool subset to OpenAI-compatible Chat Completions rather than proxying an upstream `/v1/messages` endpoint. The endpoint requires `anthropic-version: 2023-06-01`; missing or different API versions are rejected rather than being interpreted under the wrong contract. Base64 image blocks are mapped to OpenAI-compatible `image_url` content parts using `data:` URLs, while URL image sources are forwarded as validated absolute HTTP(S) image URLs; surrounding text-part order is preserved. Text deltas are flushed as Anthropic SSE events. Streaming tool-call arguments are buffered until they form valid JSON, then emitted as a `tool_use` block with `input_json_delta`. Files API image sources, image-bearing tool results, thinking blocks, and broader Anthropic-specific features remain outside this adapter slice.

## Quick start

### Install the latest release

GitHub Releases provide prebuilt archives for Linux, macOS, and Windows on amd64 and arm64. The following Linux amd64 example downloads the latest release, verifies its SHA-256 checksum, verifies the immutable GitHub Release attestation and asset membership, verifies the archive build provenance, and then extracts the binary:

```bash
mkdir -p /tmp/agentinterposer-release
cd /tmp/agentinterposer-release

gh release download --repo oaslananka/agentinterposer \
  --pattern 'agentinterposer-*-linux-amd64.tar.gz' \
  --pattern 'SHA256SUMS'

archive=$(ls agentinterposer-*-linux-amd64.tar.gz)
sha256sum --ignore-missing -c SHA256SUMS
gh release verify --repo oaslananka/agentinterposer
gh release verify-asset "$archive" --repo oaslananka/agentinterposer
gh attestation verify "$archive" --repo oaslananka/agentinterposer
tar -xzf "$archive"
./agentinterposer --version
```

The verification commands require GitHub CLI. The checksum command above uses GNU `sha256sum`; use your platform's equivalent when installing another archive.

### Build from source

Development currently targets Go `1.26.6`.

```bash
git clone https://github.com/oaslananka/agentinterposer.git
cd agentinterposer
go build -o agentinterposer ./cmd/agentinterposer
```

A compiled AgentInterposer binary does not require a local GPU. With the default configuration, inference runs on NVIDIA's hosted service.

### Configure the NVIDIA hosted API

Set the key in your environment. Do not commit it to a file in this repository.

```bash
export NVIDIA_API_KEY='your-key-here'
./agentinterposer
```

The gateway starts on:

```text
http://127.0.0.1:11435
```

AgentInterposer rejects non-loopback listen addresses by default. If you deliberately place a separate authentication and network-security boundary in front of the gateway, set `AGENTINTERPOSER_ALLOW_REMOTE=true` together with the non-loopback `AGENTINTERPOSER_ADDR`.

Inspect the CLI without configuring a provider credential:

```bash
./agentinterposer --help
./agentinterposer --version
```

Check health:

```bash
curl http://127.0.0.1:11435/healthz
```

List the models exposed by the configured upstream:

```bash
curl http://127.0.0.1:11435/v1/models
```

AgentInterposer forwards model discovery through the same bounded retry and server-owned credential boundary as inference requests; client Authorization headers are not forwarded upstream.

Send a Chat Completions request:

```bash
curl http://127.0.0.1:11435/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "nvidia/nemotron-3-super-120b-a12b",
    "messages": [{"role": "user", "content": "Reply only with OK"}],
    "max_tokens": 16
  }'
```

Send a native Responses request:

```bash
curl http://127.0.0.1:11435/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "nvidia/nemotron-3-super-120b-a12b",
    "input": "Reply only with OK",
    "max_output_tokens": 16
  }'
```

This establishes the transport needed by Responses-based clients. A daily `Provider Drift` workflow runs the lightweight hosted `baseline` probe so obvious provider drift is detected without paying the cost or accepting the flakiness of the full certification matrix on every change. The manual `Provider Smoke` workflow can run `scope=codex` for the current single shell-tool certification path, `scope=codex-loop` for a dependent two-tool path where the second shell result must be the SHA-256 digest of the first tool's unpredictable UUID output, `scope=codex-long-loop` for a dependent three-tool path where the third result must be the SHA-256 digest of the second result, and `scope=codex-four-loop` for a dependent four-tool path that repeats the same unpredictable-result chaining through a fourth shell call. These scopes currently use Codex CLI `0.148.0` -> AgentInterposer -> NVIDIA hosted Responses with `nvidia/nemotron-3-super-120b-a12b`. A separate repeated single-tool certification also covers `nvidia/nemotron-3-ultra-550b-a55b`; dependent loops on Ultra remain uncertified. The release hard gate intentionally remains the repeatable three-tool scope on the super model; the four-tool scope is a manual certification signal. A five-tool probe completed only two of three replicates, so five-tool and deeper loops remain uncertified rather than being inferred from the four-tool result.

Send an Anthropic Messages request:

```bash
curl http://127.0.0.1:11435/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{
    "model": "nvidia/nemotron-3-super-120b-a12b",
    "max_tokens": 64,
    "messages": [{"role": "user", "content": "Reply briefly with OK"}]
  }'
```

To stream the same protocol, set `"stream": true`:

```bash
curl --no-buffer http://127.0.0.1:11435/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{
    "model": "nvidia/nemotron-3-super-120b-a12b",
    "max_tokens": 64,
    "stream": true,
    "messages": [{"role": "user", "content": "Reply briefly with OK"}]
  }'
```

This adapter covers text, top-level system instructions, text-only mid-conversation `role: "system"` messages, base64 and URL user image blocks, custom client `tools`, `tool_choice`, `tool_use`, and successful text `tool_result` blocks in non-streaming mode. Mid-conversation system messages are kept at their original position in the translated Chat Completions history. Within the v1 client-tool subset, a system-message section must immediately follow a user turn (including a user tool-result turn), and must either end the input history or be followed by an assistant turn; invalid placement and non-text system-message blocks are rejected rather than approximated. It also validates that `tool_result` blocks immediately follow the corresponding assistant `tool_use` message, cover every tool-use ID, and precede any later user text as required by the Anthropic tool-use contract, while retaining SSE streaming for text and custom client tool calls. In streaming mode, text deltas are forwarded incrementally while tool arguments are buffered until valid JSON is available and then emitted as a single valid `input_json_delta`. Explicit upstream SSE error events remain SSE `error` events even when they arrive before `message_start`; adapter/protocol failures that occur before downstream streaming begins remain normal HTTP errors. Downstream client cancellation propagates to the upstream request context, and once a downstream SSE write fails the adapter stops writing instead of attempting a second error event on the broken connection. Upstream Chat Completions SSE frames accept the default/explicit `message` event shape plus explicit `error` only when its payload is actually an error; unknown or contradictory data-bearing event metadata fails closed, while SSE comments and data-less metadata frames are ignored. Each raw upstream SSE frame is capped at `32 MiB` so an oversized or unterminated provider frame cannot grow memory without bound; SSE `id` and `retry` reconnection metadata are ignored because AgentInterposer does not reconnect an in-flight upstream response. Non-streaming Messages upstream response bodies are likewise capped at `32 MiB`; oversized success or error bodies fail closed as a gateway error while `Retry-After` and the gateway request ID remain intact. The adapter requests terminal usage from the upstream Chat Completions stream; `message_start` begins with zero counters because hosted Chat Completions supplies authoritative token usage at the terminal usage chunk, and the final `message_delta` reports those cumulative counts. `stop_sequences`, `top_k`, `service_tier`, cache-only `max_tokens=0`, unsupported top-level Messages fields (including `cache_control`, `container`, `inference_geo`, `output_config`, and `anthropic-user-profile-id`), Files API image sources, image-bearing tool results, thinking blocks, and server tools are rejected rather than silently translated. Unknown request fields, including nested `system` and `messages[].content` block fields, are rejected instead of being silently dropped. Messages protocol field names are case-sensitive at the top level and in decoded nested structures; mis-cased aliases are rejected instead of being accepted through Go JSON field folding. Direct `tool_use.caller` metadata is accepted for client tools, while server-tool callers are rejected rather than flattened into client function calls. Failed `tool_result` blocks with `is_error: true` are preserved for the OpenAI-compatible upstream as a versioned AgentInterposer JSON error envelope inside the standard tool-message `content`, because Chat Completions has no separate structured tool-error flag. Every `/v1/messages` response includes a gateway-local `request-id`; error responses repeat the same identifier as `request_id` in the JSON body. AgentInterposer does not synthesize Anthropic organization or rate-limit headers from unrelated upstream-provider metadata; `Retry-After` is preserved when the upstream supplies it. Manual Provider Smoke scopes `messages` and `messages-stream` verify the non-streaming round trip and the real NVIDIA-hosted text/tool SSE paths respectively. The deep `messages-stream` tool probe remains a manual certification signal because the hosted model has intermittently ignored an explicitly named tool choice even when transport and text streaming succeeded; deterministic local streaming tests remain part of CI.

The Messages endpoint also validates `Content-Type: application/json` (standard media-type parameters such as `charset=utf-8` are accepted). Claude Code `2.1.233` has been observed to call `/v1/messages?beta=true`; that exact query is tolerated as a client compatibility marker, while other query parameters or values are rejected rather than silently ignored. Client `Authorization` and `x-api-key` values are compatibility inputs only: AgentInterposer does not use them for upstream authentication and does not currently authenticate clients itself. Keep the gateway loopback-bound, or place the deliberate authentication/network boundary described below in front of any remote bind.

The Messages adapter treats `anthropic-beta` fail-closed. It tolerates only the four compatibility markers observed in the certified Claude Code `2.1.233` flow with optional experimental betas disabled (`claude-code-20250219`, `interleaved-thinking-2025-05-14`, `mid-conversation-system-2026-04-07`, and `effort-2025-11-24`); any other beta name is rejected before upstream. Tolerating these markers is not a claim that their broader beta semantics are implemented: beta-only request fields remain subject to the adapter's strict request validation. Claude Code should run with `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`; the generated Claude Code environment also applies the thinking, effort, nonessential-traffic, and non-streaming-fallback settings used by hosted certification. The manual `scope=claude-code` Provider Smoke profile keeps the established `--bare` Claude Code CLI `2.1.235` -> AgentInterposer -> NVIDIA hosted inference -> Bash `tool_use` -> successful `tool_result` -> final-response proof using `nvidia/nemotron-3-super-120b-a12b`. The separate `scope=claude-code-normal` profile omits `--bare` and repeatedly verifies the normal Claude Code system prompt, its text-only mid-conversation system turns, streaming Bash tool use, successful tool result, and final response on the same model. The `scope=claude-code-loop` profile verifies two sequential successful Bash calls where the second exact command embeds the unpredictable proof returned by the first tool and produces its independently verified SHA-256 digest. The manual `scope=claude-code-long-loop` profile extends that proof to a third sequential Bash call whose exact command embeds the second tool result; the release hard gate intentionally remains the established two-tool Claude loop. The separate `scope=claude-code-error` profile verifies a failing Bash tool result is preserved, returned through the Messages adapter, and followed by a successful recovery tool turn. The compatibility registry retains successful certification records for Claude Code CLI `2.1.226`, `2.1.233`, and `2.1.235`, with the three-tool loop and the `normal-mode-single-tool` scenario asserted only for `2.1.235`. A same-turn parallel Bash spike failed all three replicates because Claude emitted both successful tool calls across separate assistant turns, so parallel tool use remains explicitly uncertified. These certifications remain intentionally limited to those exact client versions, this model, and custom Bash-tool flows; broader Claude Code features and other models remain uncertified.

The manual `scope=opencode` Provider Smoke profile pins the OpenCode Linux x64 client package at CLI `1.18.18`, renders the repository's normal `opencode.json` helper, and runs the client under a separate OS user that cannot read the gateway's provider-secret environment. The certification requires exactly two completed tools in order: `read` must recover an unpredictable file proof, then `bash` must embed that literal proof in its command and produce the independently verified SHA-256 digest; the final assistant text must exactly preserve that digest. The evidence applies only to OpenCode CLI `1.18.18`, this model, and that dependent two-tool Chat Completions flow.

### Generate agent client configuration

The binary can print secret-free client configuration without starting the gateway or requiring an upstream provider credential. Supply the model explicitly; an optional fourth argument overrides the default local gateway URL `http://127.0.0.1:11435`.

```bash
./agentinterposer config codex nvidia/nemotron-3-super-120b-a12b
./agentinterposer config claude-code nvidia/nemotron-3-super-120b-a12b
./agentinterposer config opencode nvidia/nemotron-3-super-120b-a12b
./agentinterposer config continue nvidia/nemotron-3-super-120b-a12b
```

The Codex helper prints a `~/.codex/config.toml` fragment using AgentInterposer's Responses endpoint and an `AGENTINTERPOSER_CLIENT_KEY` local placeholder. The Claude Code helper prints shell exports for `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`, `CLAUDE_CODE_DISABLE_THINKING=1`, `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`, `CLAUDE_CODE_EFFORT_LEVEL=auto`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, and `CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1` so the generated environment matches the certified compatibility profile; Claude Code `2.1.233` still emits the four certified compatibility markers listed above, which the adapter tolerates only within its otherwise strict Messages subset. The OpenCode helper prints an `opencode.json` custom provider using `@ai-sdk/openai-compatible`, the AgentInterposer `/v1` endpoint, an explicit model entry, and `{env:AGENTINTERPOSER_CLIENT_KEY}` for the local client credential; that generated helper is used by the exact OpenCode CLI `1.18.18` dependent-tool certification above. The Continue helper prints a `~/.continue/config.yaml` configuration for the Continue VS Code/JetBrains extension using its OpenAI-compatible provider, the AgentInterposer `/v1` endpoint, a harmless local placeholder API key, and `useResponsesApi: false` so generic model IDs stay on the Chat Completions path. This is a connection helper, not a Continue compatibility certification. The placeholder client credentials are not upstream provider secrets; the real upstream bearer token remains owned by the AgentInterposer server process.

For a non-default gateway location, pass the root URL as the fourth argument, for example `https://gateway.example.test/agent`; the Codex, OpenCode, and Continue renderers derive their `/v1` endpoint while Claude Code uses the gateway root.

## Configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NVIDIA_API_KEY` | none | Bearer token for the default NVIDIA upstream |
| `AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN` | falls back to `NVIDIA_API_KEY` | Generic upstream bearer token override |
| `AGENTINTERPOSER_UPSTREAM_URL` | `https://integrate.api.nvidia.com` | OpenAI-compatible upstream base URL |
| `AGENTINTERPOSER_ADDR` | `127.0.0.1:11435` | Local listen address; non-loopback values require explicit remote opt-in |
| `AGENTINTERPOSER_ALLOW_REMOTE` | `false` | Permit a non-loopback listen address; use only behind a deliberate authentication/network boundary |
| `AGENTINTERPOSER_MAX_CONCURRENT` | `3` | Maximum simultaneous upstream requests |
| `AGENTINTERPOSER_MAX_RETRIES` | `3` | Retries after the initial request |
| `AGENTINTERPOSER_RETRY_BASE_DELAY` | `500ms` | Base duration for exponential backoff |
| `AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT` | `2m` | Maximum wait for upstream response headers after the request is sent |
| `AGENTINTERPOSER_UPSTREAM_BODY_IDLE_TIMEOUT` | `2m` | Maximum interval without upstream response-body progress; active streams reset this deadline on every read |
| `AGENTINTERPOSER_MAX_REQUEST_BYTES` | `33554432` | Maximum request body size in bytes |
| `AGENTINTERPOSER_FALLBACK_MODELS` | none | Ordered comma-separated fallback model IDs selected only from positive capability evidence |
| `AGENTINTERPOSER_MODEL_ROUTES` | none | JSON array mapping exact model IDs to dedicated upstream URLs and bearer-token environment variable names |

For a non-NVIDIA OpenAI-compatible default upstream, set both `AGENTINTERPOSER_UPSTREAM_URL` and `AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN`.

### Per-model upstream routes

`AGENTINTERPOSER_MODEL_ROUTES` can send an explicitly requested model—or a model selected by `AGENTINTERPOSER_FALLBACK_MODELS`—to a different OpenAI-compatible upstream. The JSON contains only the **name** of the environment variable that holds the route credential; do not embed a bearer token value in the JSON. Unknown fields are rejected, so a literal `bearer_token` field is invalid.

```bash
export AGENTINTERPOSER_MODEL_ROUTES='[{"model":"provider/routed-model","upstream_url":"https://api.example.test/v1","bearer_token_env":"ALT_PROVIDER_API_KEY"}]'
```

Supply `ALT_PROVIDER_API_KEY` to the AgentInterposer process from Doppler (or the equivalent secret-injection mechanism for your deployment). Unrouted models continue to use the default upstream and credential, and `GET /v1/models` remains a default-upstream discovery request. Configuring a route is an operator routing choice; it does not create a compatibility certification for that model or provider.

The manual `Provider Smoke` scope `model-route` certifies the dedicated-route mechanism against NVIDIA hosted inference by making the default upstream deliberately unreachable and requiring an explicitly routed model to return a valid Chat Completions response. Composition scopes use the same unreachable-default design to prove that capability fallback and per-model routing work together: `chat-vision-routed-fallback` and `messages-vision-routed-fallback` send image-bearing Nemotron requests that must select the Llama vision fallback and follow its dedicated route, while `responses-routed-fallback` and `responses-structured-routed-fallback` send simple or structured `input_text` Responses requests that must select the Responses-certified Nemotron fallback and follow its dedicated route. The routed vision scopes validate the selected model and protocol envelope rather than duplicating randomized image-accuracy checks, which remain covered by the existing vision certification scopes. All of these probes certify routing mechanisms against the already-used NVIDIA provider; they do **not** claim compatibility with a second external provider.

## Roadmap

The v1 boundary above is intentionally narrower than the long-term roadmap. The following are post-v1 compatibility-breadth opportunities, not prerequisites for declaring the documented v1 contract complete:

1. Broaden Codex/Responses certification beyond the current single-tool, dependent two-tool, dependent three-tool, and dependent four-tool Nemotron 3 Super profiles to additional tool types, longer agent loops, Codex versions, and models.
2. Broaden the Anthropic Messages adapter beyond the current text/base64-and-URL-image/custom-client-tool slice, including Files API image sources, image-bearing tool results, thinking, and richer non-text result semantics.
3. Broaden Claude Code/Messages certification beyond the current bare-mode single-tool/dependent two-tool/dependent three-tool/error-recovery profiles and the normal-mode single-tool profile to additional normal-mode loops, client versions, models, parallel tools, and longer multi-turn patterns.
4. Expand the model capability registry beyond the current Nemotron 3 Super, Nemotron 3 Nano, Nemotron 3 Nano Omni, and Llama 3.2 Vision evidence sets.
5. Certify per-model upstream routes against additional providers and expand fallback coverage to multimodal/tool-bearing Responses.
6. Expand the certified Codex, Claude Code, and OpenCode client profiles and the existing Continue connection helper to additional compatible IDE clients; Continue end-to-end behavior remains uncertified until it is repeatable.

## Releases

Tagged releases use the `vMAJOR.MINOR.PATCH` form. The release workflow runs vet, race-enabled tests, and `govulncheck` before producing Linux, macOS, and Windows archives for amd64 and arm64. Publication also requires a serialized hosted certification chain for the NVIDIA baseline, the certified three-tool Codex Responses loop, and the certified dependent Claude Code Messages loop; serial execution avoids overlapping hosted probes from interfering with one another. Published assets include `SHA256SUMS`, and each archive digest is covered by a GitHub artifact build-provenance attestation before upload. Build metadata is embedded so `agentinterposer --version` reports the release tag and source commit. A reachable Go vulnerability or failed hosted certification blocks publication.

The publish job creates a draft release, attaches all verified assets, and only then publishes it. Repository release immutability is enabled for future releases, so publication locks the tag and assets and causes GitHub to generate a release-level attestation. The per-archive build provenance and the release-level attestation serve different purposes: use `gh attestation verify <archive> --repo oaslananka/agentinterposer` to verify build provenance for a downloaded archive, and `gh release verify <tag> --repo oaslananka/agentinterposer` (or `gh release verify-asset <tag> <archive>`) to verify an immutable GitHub Release and its published asset set.

## Security

AgentInterposer handles provider credentials and model traffic. It refuses non-loopback `AGENTINTERPOSER_ADDR` values unless `AGENTINTERPOSER_ALLOW_REMOTE=true`. Keep it bound to loopback unless you deliberately add an authentication and network-security boundary in front of it. See [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and keep changes small, testable, and focused on observable compatibility behavior.

## License

Apache License 2.0. See [LICENSE](LICENSE).
