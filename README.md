# AgentInterposer

[![CI](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml/badge.svg)](https://github.com/oaslananka/agentinterposer/actions/workflows/ci.yml)

AgentInterposer is a local-first compatibility gateway between coding agents and LLM providers.

> **Status:** early development. The current foundation provides hardened OpenAI-compatible Chat Completions, native Responses passthrough, and a non-streaming Anthropic Messages adapter for text and custom client tools. A manual compatibility probe verifies Codex CLI `0.147.0` with `nvidia/nemotron-3-super-120b-a12b` for a real Responses shell-tool round trip through AgentInterposer. These are narrow certification profiles, not claims of universal agent or model compatibility.

## Why AgentInterposer?

Coding agents often expect different API protocols and subtly different streaming, tool-calling, and reasoning behavior. Model providers also expose different limits and transient failure modes. AgentInterposer is intended to make those boundaries explicit and testable instead of hiding them behind a generic proxy.

The project is designed around three principles:

- **Agent-aware compatibility:** preserve the behavior coding agents depend on, not just JSON shape.
- **Provider-aware reliability:** bound concurrency and handle retryable provider capacity failures without retry storms.
- **Local-first BYOK:** credentials stay in the local process environment and are never stored in the repository.

## Current capabilities

The first vertical slice supports:

- `GET /healthz`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages` for non-streaming Anthropic-compatible text and custom client-tool requests
- OpenAI-compatible request/response passthrough to an upstream provider without JSON translation
- server-owned upstream bearer authentication (client credentials are not forwarded)
- bounded upstream concurrency (default: `3`)
- exponential retry for `429`, `500`, `502`, `503`, and `504` responses
- incremental flushing for `text/event-stream` responses
- manual Codex CLI `0.147.0` certification for a Responses shell-tool round trip with `nvidia/nemotron-3-super-120b-a12b`
- configurable request-size protection (default: `32 MiB`)
- safe loopback binding by default (`127.0.0.1:11435`)

The default upstream is NVIDIA's hosted API at `https://integrate.api.nvidia.com`. AgentInterposer uses native upstream Responses support when the provider exposes it instead of translating Responses payloads into Chat Completions.

## Architecture

```text
Coding agent / OpenAI-compatible client
                 |
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

OpenAI Responses uses native passthrough through the same reliability core. The current Anthropic Messages path translates non-streaming text and custom client-tool requests to OpenAI-compatible Chat Completions when the selected upstream does not expose a usable hosted `/v1/messages` endpoint. Streaming Messages, image/thinking blocks, and broader Anthropic-specific features remain outside this adapter slice.

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

This establishes the transport needed by Responses-based clients. The manual `Provider Smoke` workflow can also run the current `scope=codex` certification path: Codex CLI `0.147.0` -> AgentInterposer -> NVIDIA hosted Responses -> shell function call -> tool output -> final response. Other Codex versions, models, tool surfaces, and longer agent loops remain uncertified.

Send a non-streaming Anthropic Messages request:

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

This adapter currently covers non-streaming text, custom client `tools`, `tool_choice`, `tool_use`, and successful `tool_result` blocks. `stream: true`, image/thinking blocks, server tools, and `tool_result` blocks with `is_error: true` are rejected rather than silently translated. The manual Provider Smoke `scope=messages` verifies a real NVIDIA-hosted text request and tool-result round trip.

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

1. Broaden Codex/Responses certification beyond the current single-shell-tool Nemotron 3 Super profile to additional tools, agent loops, Codex versions, and models.
2. Add Anthropic Messages SSE streaming and broaden the adapter beyond the current text/custom-client-tool slice.
3. Streaming tool-call and reasoning normalization tests.
4. Model capability profiles and compatibility certification tests.
5. Capability-aware fallback and provider routing.
6. Agent configuration helpers for Codex, Claude Code, OpenCode, and compatible VS Code clients.

## Security

AgentInterposer handles provider credentials and model traffic. Keep it bound to loopback unless you deliberately add an authentication boundary in front of it. See [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and keep changes small, testable, and focused on observable compatibility behavior.

## License

Apache License 2.0. See [LICENSE](LICENSE).
