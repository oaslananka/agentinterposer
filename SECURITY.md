# Security Policy

## Supported versions

AgentInterposer is pre-1.0 software. Security fixes are applied to the latest code on the default branch and to the newest release when releases begin.

## Reporting a vulnerability

Please do **not** open a public issue for a suspected vulnerability, exposed credential, personal data, or exploit details.

Use GitHub private vulnerability reporting when it is available for this repository. If that option is unavailable, contact the maintainer privately at `info@oaslananka.dev` with a minimal reproduction and impact description.

Please avoid including real provider API keys, access tokens, user prompts, or model responses containing sensitive data. Redacted reproductions are preferred.

## Secret handling

AgentInterposer expects provider credentials through process environment variables. Repository files, examples, tests, logs, issues, and pull requests must never contain real secret values.

The gateway deliberately replaces client-side `Authorization` credentials with its configured upstream bearer token instead of forwarding client credentials to the provider.
