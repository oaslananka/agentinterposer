# AgentInterposer

[![CI](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml/badge.svg)](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml)

AgentInterposer is a local-first compatibility gateway between coding agents and LLM providers.

> **Status:** early development. The current foundation provides hardened OpenAI-compatible Chat Completions, native Responses passthrough, and an Anthropic Messages adapter for text, base64 and URL user image inputs, and custom client tools in both non-streaming and SSE streaming modes. Manual compatibility probes verify Codex CLI `0.147.0` over Responses for a single shell-tool round trip plus dependent two-tool and three-tool loops, and Claude Code CLI `2.1.226` over Messages for both a single Bash-tool round trip and a dependent two-tool loop, with `nvidia/nemotron-3-super-120b-a12b` through AgentInterposer. A separate randomized hosted image probe certifies base64 Messages vision input with `meta/llama-3.2-11b-vision-instruct`. These are narrow certification profiles, not claims of universal agent or model compatibility.

## Why AgentInterposer?

Coding agents often expect different API protocols and subtly different streaming, tool-calling, and reasoning behavior. Model providers also expose different limits and transient failure modes. AgentInterposer is intended to make those boundaries explicit and testable instead of hiding them behind a generic proxy.

The project is designed around three principles:

- **Agent-aware compatibility:** preserve the behavior coding agents depend on, not just JSON shape.
- **Provider-aware reliability:** bound concurrency and handle retryable provider capacity failures without retry storms.
- **Local-first BYOK:** credentials stay in the local process environment and are never stored in the repository.

## Current capabilities

The first vertical slice supports:

- `GET /healthz`
- `GET /v1/models` for upstream model discovery
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages` for Anthropic-compatible text, base64 and URL user image inputs, and custom client-tool requests, including SSE streaming
- OpenAI-compatible request/response passthrough to an upstream provider without JSON translation
- server-owned upstream bearer authentication (client credentials are not forwarded)
- bounded upstream concurrency (default: `3`)
- exponential retry for `429`, `500`, `502`, `503`, and `504` responses
- incremental flushing for `text/event-stream` responses
- manual Codex CLI `0.147.0` certification for a Responses shell-tool round trip plus dependent two-tool and three-tool loops with `nvidia/nemotron-3-super-120b-a12b`
- manual Claude Code CLI `2.1.226` certification for Messages single Bash-tool, dependent two-tool, and error-recovery round trips with `nvidia/nemotron-3-super-120b-a12b`
- randomized hosted base64-image Messages certification with `meta/llama-3.2-11b-vision-instruct`
- configurable request-size protection (default: `32 MiB`)
- safe loopback binding by default (`127.0.0.1:11435`)

The default upstream is NVIDIA's hosted API at `https://integrate.api.nvidia.com`. AgentInterposer uses native upstream Responses support when the provider exposes it instead of translating Responses payloads into Chat Completions.

### Compatibility profiles

AgentInterposer keeps explicit built-in compatibility assertions for model/client combinations that have reproducible certification evidence. The `nvidia/nemotron-3-super-120b-a12b` profile asserts Chat Completions, native Responses, and tool calling, and records the hosted Codex CLI `0.147.0` and Claude Code CLI `2.1.226` certification scenarios described below. Vision input is intentionally not asserted for that model. The `nvidia/nemotron-3-nano-30b-a3b` profile asserts Chat Completions and native Responses only, backed by repeated hosted baseline/full probes; tool calling remains uncertified because repeated Messages tool probes were not consistent, and vision/client-version scenarios are also unasserted. The `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` profile asserts Chat Completions and vision input only, backed by repeated randomized hosted image reads; direct native Responses returned a hosted 503 during certification, so Responses is intentionally unasserted, and tool/client-version scenarios remain uncertified. The `meta/llama-3.2-11b-vision-instruct` profile asserts Chat Completions and vision input only, backed by a hosted randomized base64-image read through the Messages adapter; Responses and tool calling remain uncertified for that profile.

Profiles are positive assertions, not guesses: absence of a capability means **uncertified/unknown**, not a universal claim that the provider can never support it. The compatibility layer can conservatively select the first candidate model whose profile asserts every required capability while skipping unknown or incomplete candidates. The opt-in `AGENTINTERPOSER_FALLBACK_MODELS` routing slice can route Anthropic Messages and OpenAI-compatible Chat Completions image requests from a known-but-not-vision-certified requested model to the first fallback profile that asserts both Chat Completions and vision input. It can also route text-only Responses requests—either a direct string or structured message content made exclusively of `input_text` parts—when the requested known profile lacks native Responses support and a configured fallback positively asserts `responses`. Unknown requested models are never rewritten. Chat Completions and Responses remain byte-for-byte passthrough when no fallback is selected; routed requests rewrite only the top-level `model` and preserve provider-specific fields. Responses inputs containing `input_image`, `input_file`, non-message items, or tools are intentionally not routed yet. A configured model can additionally point at a dedicated upstream through `AGENTINTERPOSER_MODEL_ROUTES`; if a capability fallback selects such a model, the request follows that model route automatically.

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

OpenAI Responses uses native passthrough through the same reliability core. The Anthropic Messages path translates text, base64 and URL user image inputs, and custom client-tool requests to OpenAI-compatible Chat Completions when the selected upstream does not expose a usable hosted `/v1/messages` endpoint. Base64 image blocks are mapped to OpenAI-compatible `image_url` content parts using `data:` URLs, while URL image sources are forwarded as validated absolute HTTP(S) image URLs; surrounding text-part order is preserved. Text deltas are flushed as Anthropic SSE events. Streaming tool-call arguments are buffered until they form valid JSON, then emitted as a `tool_use` block with `input_json_delta`. Files API image sources, image-bearing tool results, thinking blocks, and broader Anthropic-specific features remain outside this adapter slice.

## Quick start

### Build from source

Development currently targets Go `1.26.5`.

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

This establishes the transport needed by Responses-based clients. The manual `Provider Smoke` workflow can run `scope=codex` for the current single shell-tool certification path, `scope=codex-loop` for a dependent two-tool path where the second shell result must be the SHA-256 digest of the first tool's unpredictable UUID output, and `scope=codex-long-loop` for a dependent three-tool path where the third result must be the SHA-256 digest of the second result. All three use Codex CLI `0.147.0` -> AgentInterposer -> NVIDIA hosted Responses with `nvidia/nemotron-3-super-120b-a12b`. Other Codex versions, models, broader tool surfaces, and loops beyond the current three-tool profile remain uncertified.

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

This adapter covers text, base64 and URL user image blocks, custom client `tools`, `tool_choice`, `tool_use`, and successful text `tool_result` blocks in non-streaming mode, including validation that `tool_result` blocks immediately follow the corresponding assistant `tool_use` message, cover every tool-use ID, and precede any later user text as required by the Anthropic tool-use contract, plus SSE streaming for text and custom client tool calls. In streaming mode, text deltas are forwarded incrementally while tool arguments are buffered until valid JSON is available and then emitted as a single valid `input_json_delta`. The adapter requests terminal usage from the upstream Chat Completions stream; `message_start` begins with zero counters because hosted Chat Completions supplies authoritative token usage at the terminal usage chunk, and the final `message_delta` reports those cumulative counts. `stop_sequences`, Files API image sources, image-bearing tool results, thinking blocks, and server tools are rejected rather than silently translated. Failed `tool_result` blocks with `is_error: true` are preserved for the OpenAI-compatible upstream as a versioned AgentInterposer JSON error envelope inside the standard tool-message `content`, because Chat Completions has no separate structured tool-error flag. Manual Provider Smoke scopes `messages` and `messages-stream` verify the non-streaming round trip and the real NVIDIA-hosted text/tool SSE paths respectively.

The manual `scope=claude-code` Provider Smoke profile verifies Claude Code CLI `2.1.226` -> AgentInterposer -> NVIDIA hosted inference -> Bash `tool_use` -> successful `tool_result` -> final response using `nvidia/nemotron-3-super-120b-a12b`. The `scope=claude-code-loop` profile verifies two sequential successful Bash calls where the second exact command embeds the unpredictable proof returned by the first tool and produces its independently verified SHA-256 digest. The separate `scope=claude-code-error` profile verifies a failing Bash tool result is preserved, returned through the Messages adapter, and followed by a successful recovery tool turn. These certifications remain intentionally limited to this client version, model, and custom Bash-tool flows; parallel tool use, broader Claude Code features, and other models remain uncertified.

### Generate agent client configuration

The binary can print secret-free client configuration without starting the gateway or requiring an upstream provider credential. Supply the model explicitly; an optional fourth argument overrides the default local gateway URL `http://127.0.0.1:11435`.

```bash
./agentinterposer config codex nvidia/nemotron-3-super-120b-a12b
./agentinterposer config claude-code nvidia/nemotron-3-super-120b-a12b
./agentinterposer config opencode nvidia/nemotron-3-super-120b-a12b
```

The Codex helper prints a `~/.codex/config.toml` fragment using AgentInterposer's Responses endpoint and an `AGENTINTERPOSER_CLIENT_KEY` local placeholder. The Claude Code helper prints shell exports for `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and `ANTHROPIC_MODEL`. The OpenCode helper prints an `opencode.json` custom provider using `@ai-sdk/openai-compatible`, the AgentInterposer `/v1` endpoint, an explicit model entry, and `{env:AGENTINTERPOSER_CLIENT_KEY}` for the local client credential. The placeholder client credentials are not upstream provider secrets; the real upstream bearer credential remains owned by the AgentInterposer server process.

For a non-default gateway location, pass the root URL as the fourth argument, for example `https://gateway.example.test/agent`; the Codex renderer derives its `/v1` endpoint while Claude Code uses the gateway root.

## Configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NVIDIA_API_KEY` | none | Bearer token for the default NVIDIA upstream |
| `AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN` | falls back to `NVIDIA_API_KEY` | Generic upstream bearer token override |
| `AGENTINTERPOSER_UPSTREAM_URL` | `https://integrate.api.nvidia.com` | OpenAI-compatible upstream base URL |
| `AGENTINTERPOSER_ADDR` | `127.0.0.1:11435` | Local listen address |
| `AGENTINTERPOSER_MAX_CONCURRENT` | `3` | Maximum simultaneous upstream requests |
| `AGENTINTERPOSER_MAX_RETRIES` | `3` | Retries after the initial request |
| `AGENTINTERPOSER_RETRY_BASE_DELAY` | `500ms` | Base duration for exponential backoff |
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

Near-term work is intentionally compatibility-first:

1. Broaden Codex/Responses certification beyond the current single-tool, dependent two-tool, and dependent three-tool Nemotron 3 Super profiles to additional tool types, longer agent loops, Codex versions, and models.
2. Broaden the Anthropic Messages adapter beyond the current text/base64-and-URL-image/custom-client-tool slice, including Files API image sources, image-bearing tool results, thinking, and richer non-text result semantics.
3. Broaden Claude Code/Messages certification beyond the current single-tool, dependent two-tool, and error-recovery profiles to additional client versions, models, parallel tools, and longer multi-turn patterns.
4. Expand the model capability registry beyond the current Nemotron 3 Super, Nemotron 3 Nano, Nemotron 3 Nano Omni, and Llama 3.2 Vision evidence sets.
5. Certify per-model upstream routes against additional providers and expand fallback coverage to multimodal/tool-bearing Responses.
6. Expand the initial Codex, Claude Code, and OpenCode configuration helpers to compatible VS Code clients.

## Security

AgentInterposer handles provider credentials and model traffic. Keep it bound to loopback unless you deliberately add an authentication boundary in front of it. See [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and keep changes small, testable, and focused on observable compatibility behavior.

## License

Apache License 2.0. See [LICENSE](LICENSE).
