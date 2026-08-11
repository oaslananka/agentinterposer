# AgentInterposer

[![CI](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml/badge.svg)](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml)

AgentInterposer is a local-first compatibility gateway between coding agents and LLM providers.

> **Status:** early development. The current foundation provides hardened OpenAI-compatible Chat Completions, native Responses passthrough, and an Anthropic Messages adapter for text, base64 and URL user image inputs, and custom client tools in both non-streaming and SSE streaming modes. Manual compatibility probes verify Codex CLI `0.147.0` over Responses for both a single shell-tool round trip and a dependent two-tool loop, plus Claude Code CLI `2.1.226` over Messages for both a single Bash-tool round trip and a dependent two-tool loop, with `nvidia/nemotron-3-super-120b-a12b` through AgentInterposer. These are narrow certification profiles, not claims of universal agent or model compatibility.

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
- manual Codex CLI `0.147.0` certification for a Responses shell-tool round trip and a dependent two-tool loop with `nvidia/nemotron-3-super-120b-a12b`
- manual Claude Code CLI `2.1.226` certification for Messages single Bash-tool, dependent two-tool, and error-recovery round trips with `nvidia/nemotron-3-super-120b-a12b`
- configurable request-size protection (default: `32 MiB`)
- safe loopback binding by default (`127.0.0.1:11435`)

The default upstream is NVIDIA's hosted API at `https://integrate.api.nvidia.com`. AgentInterposer uses native upstream Responses support when the provider exposes it instead of translating Responses payloads into Chat Completions.

### Compatibility profiles

AgentInterposer keeps explicit built-in compatibility assertions for model/client combinations that have reproducible certification evidence. The current `nvidia/nemotron-3-super-120b-a12b` profile asserts Chat Completions, native Responses, and tool calling, and records the hosted Codex CLI `0.147.0` and Claude Code CLI `2.1.226` certification scenarios described below. Vision input is intentionally not asserted for that model.

Profiles are positive assertions, not guesses: absence of a capability means **uncertified/unknown**, not a universal claim that the provider can never support it. The compatibility layer can conservatively select the first candidate model whose profile asserts every required capability while skipping unknown or incomplete candidates; automatic request rewriting is not enabled yet.

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

This establishes the transport needed by Responses-based clients. The manual `Provider Smoke` workflow can run `scope=codex` for the current single shell-tool certification path and `scope=codex-loop` for a dependent two-tool path where the second shell result must be the SHA-256 digest of the first tool's unpredictable UUID output. Both use Codex CLI `0.147.0` -> AgentInterposer -> NVIDIA hosted Responses with `nvidia/nemotron-3-super-120b-a12b`. Other Codex versions, models, broader tool surfaces, and longer agent loops remain uncertified.

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

This adapter covers text, base64 and URL user image blocks, custom client `tools`, `tool_choice`, `tool_use`, and successful text `tool_result` blocks in non-streaming mode, plus SSE streaming for text and custom client tool calls. In streaming mode, text deltas are forwarded incrementally while tool arguments are buffered until valid JSON is available and then emitted as a single valid `input_json_delta`. The adapter requests terminal usage from the upstream Chat Completions stream; `message_start` begins with zero counters because hosted Chat Completions supplies authoritative token usage at the terminal usage chunk, and the final `message_delta` reports those cumulative counts. `stop_sequences`, Files API image sources, image-bearing tool results, thinking blocks, and server tools are rejected rather than silently translated. Failed `tool_result` blocks with `is_error: true` are preserved for the OpenAI-compatible upstream as a versioned AgentInterposer JSON error envelope inside the standard tool-message `content`, because Chat Completions has no separate structured tool-error flag. Manual Provider Smoke scopes `messages` and `messages-stream` verify the non-streaming round trip and the real NVIDIA-hosted text/tool SSE paths respectively.

The manual `scope=claude-code` Provider Smoke profile verifies Claude Code CLI `2.1.226` -> AgentInterposer -> NVIDIA hosted inference -> Bash `tool_use` -> successful `tool_result` -> final response using `nvidia/nemotron-3-super-120b-a12b`. The `scope=claude-code-loop` profile verifies two sequential successful Bash calls where the second exact command embeds the unpredictable proof returned by the first tool and produces its independently verified SHA-256 digest. The separate `scope=claude-code-error` profile verifies a failing Bash tool result is preserved, returned through the Messages adapter, and followed by a successful recovery tool turn. These certifications remain intentionally limited to this client version, model, and custom Bash-tool flows; parallel tool use, broader Claude Code features, and other models remain uncertified.

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

For a non-NVIDIA OpenAI-compatible upstream, set both `AGENTINTERPOSER_UPSTREAM_URL` and `AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN`.

## Roadmap

Near-term work is intentionally compatibility-first:

1. Broaden Codex/Responses certification beyond the current single-tool and dependent two-tool Nemotron 3 Super profiles to additional tool types, longer agent loops, Codex versions, and models.
2. Broaden the Anthropic Messages adapter beyond the current text/base64-and-URL-image/custom-client-tool slice, including Files API image sources, image-bearing tool results, thinking, and richer non-text result semantics.
3. Broaden Claude Code/Messages certification beyond the current single-tool, dependent two-tool, and error-recovery profiles to additional client versions, models, parallel tools, and longer multi-turn patterns.
4. Expand the initial model capability profile and certification registry beyond the current Nemotron 3 Super evidence set.
5. Integrate the conservative capability selector into explicit fallback-model and multi-provider routing configuration.
6. Agent configuration helpers for Codex, Claude Code, OpenCode, and compatible VS Code clients.

## Security

AgentInterposer handles provider credentials and model traffic. Keep it bound to loopback unless you deliberately add an authentication boundary in front of it. See [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and keep changes small, testable, and focused on observable compatibility behavior.

## License

Apache License 2.0. See [LICENSE](LICENSE).
