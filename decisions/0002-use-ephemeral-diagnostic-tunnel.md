# 0002. Use an ephemeral diagnostic tunnel

**Status:** Accepted
**Date:** 2026-07-11
**Decision-makers:** Product-capture maintainers
**Related:** `docs/plans/2026-07-10-native-chrome-session-design.md`, `docs/plans/2026-07-11-native-chrome-session.md`

## Context

Release conformance needs a public HTTPS origin that serves a self-reporting
browser page and returns structured observations to the CI job. A permanent
diagnostic service would add cloud resources, credentials, retention, and an
operational owner solely for release validation.

## Decision

Run the Go diagnostic endpoint on the release runner and expose it temporarily
through a checksum-pinned `cloudflared` Quick Tunnel. The workflow allowlists
the generated exact origin, sends no application secrets through it, and tears
it down after direct-Chrome and CDP-attached observations are collected.

Rejected alternatives: a permanent endpoint adds unjustified infrastructure;
`httpbingo` cannot serve the shared self-reporting JavaScript contract; a local
private endpoint conflicts with the provider's public-address guard.

## Consequences

Release validation gains an external Cloudflare availability dependency but no
account, token, DNS record, or durable resource. The command accepts an explicit
operator-owned endpoint as a fallback. Tunnel logs and artifacts contain only
the versioned bounded signal schema.
