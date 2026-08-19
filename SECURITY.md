# Security Policy

## Supported versions

AgentInterposer v1 is actively maintained. Security work targets the default branch and the newest published release; older releases do not receive guaranteed backports.

| Version | Security support |
| --- | --- |
| `main` | Yes |
| Latest published release | Yes |
| Older releases | No guaranteed backports |

The documented v1 compatibility boundary in `README.md` is the stable public contract for the v1 line. Security fixes may tighten validation or disable unsafe behavior when necessary to address a vulnerability; such changes should be documented in the corresponding release.

## Reporting a vulnerability

Please do **not** open a public issue for a suspected vulnerability, exposed credential, personal data, or exploit details.

Use GitHub private vulnerability reporting when it is available for this repository. If that option is unavailable, contact the maintainer privately at `info@oaslananka.dev` with a minimal reproduction and impact description.

Please avoid including real provider API keys, access tokens, user prompts, or model responses containing sensitive data. Redacted reproductions are preferred.

## Secret handling

AgentInterposer expects provider credentials through process environment variables. Repository files, examples, tests, logs, issues, and pull requests must never contain real secret values.

The gateway never forwards client-side `Authorization` or `x-api-key` credentials to the provider; upstream authentication always uses the server-owned configured bearer token. AgentInterposer does not currently authenticate clients itself, so non-loopback deployments must provide the separate authentication and network-security boundary required by `AGENTINTERPOSER_ALLOW_REMOTE=true`.
