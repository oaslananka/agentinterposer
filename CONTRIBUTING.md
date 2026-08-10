# Contributing to AgentInterposer

Thanks for helping improve AgentInterposer.

## Development setup

Development currently targets Go `1.26.5`.

```bash
git clone https://github.com/oaslananka/agentinterposer.git
cd agentinterposer
go test ./...
```

No local GPU is required for unit tests. Tests use local HTTP test servers and must not require real provider credentials.

## Before opening a pull request

Run the same core checks as CI:

```bash
gofmt -w ./cmd ./internal
go vet ./...
go test -race ./...
go build ./cmd/agentinterposer
```

Then verify that `git diff --check` is clean.

## Change guidelines

- Keep pull requests small and single-purpose.
- Add or update tests for observable behavior changes.
- Prefer standard-library dependencies unless a dependency clearly reduces protocol risk or maintenance burden.
- Do not log request bodies, response bodies, API keys, bearer tokens, or other credentials by default.
- Do not add provider- or agent-compatibility claims unless they are backed by a reproducible test.
- Keep protocol conversion logic isolated from provider reliability logic.
- Do not commit `.env` files or real secrets. Examples must contain variable names and safe placeholder values only.

## Security reports

Do not disclose vulnerabilities in public issues or pull requests. Follow [SECURITY.md](SECURITY.md).
