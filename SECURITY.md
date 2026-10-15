# Security Policy

## Supported Versions

Topophany is pre-1.0. Security fixes are made on the latest minor release line only.

| Version | Supported |
| ------- | --------- |
| 0.8.x   | Yes       |
| < 0.8   | No        |

## Reporting a Vulnerability

**Do not open a public issue for security problems.**

Report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/WhitenWhiten/topophany/security/advisories/new).
Include:

- the affected version or commit and your platform;
- a minimal, **sanitized** reproduction (synthetic topology, loopback targets);
- the impact you observed or expect.

Never include production tokens, `auth.json`/credential files, internal addresses,
or other sensitive infrastructure details in a report. Rotate any credential you
believe was exposed before reporting.

Reports are handled on a best-effort basis. You will receive an acknowledgement,
and a fix and advisory will be coordinated with you before public disclosure.

## Threat Model

The trust assumptions, credential handling rules and capability boundaries that
define what counts as a vulnerability are documented in
[docs/SECURITY.md](docs/SECURITY.md). Past review records are in
[docs/SECURITY-REVIEW-2026-10-14.md](docs/SECURITY-REVIEW-2026-10-14.md).
