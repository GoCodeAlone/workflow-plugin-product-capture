# 0001. Attach Playwright to native Chrome

**Status:** Accepted
**Date:** 2026-07-10
**Decision-makers:** Product-capture maintainers
**Related:** `docs/plans/2026-07-10-native-chrome-session-design.md`

## Context

The provider currently launches Chrome through Playwright and then rewrites the
user agent, client hints, language, platform, `navigator.webdriver`, timezone,
viewport, and WebGL behavior. A staging diagnostic and a headed Chrome baseline
showed that the checked Playwright globals were absent in both sessions, while
the provider's deterministic overrides differed from native Chrome.

## Decision

Start installed Google Chrome as a normal OS process and attach Playwright over
a loopback CDP endpoint. Keep Playwright as the page-control API, but remove all
identity init scripts and protocol overrides. Preserve same-origin warmup and
anonymous profile reuse.

Rejected alternatives: retaining Playwright launch still applies its automation
launch contract; forking or renaming Playwright internals is brittle, creates a
security maintenance burden, and does not address the measured differences.

## Consequences

The browser exposes Chrome's native request and JavaScript signals. Runtime code
must own Chrome startup, CDP readiness, process-tree termination, and profile
lock failures. The supported image must provide Chrome and Xvfb for headed
operation. Rollback is a provider image/version change, but Chrome profile
downgrade compatibility is not assumed; operators drain the worker and reset
anonymous profile state when the prior Chrome cannot open it.
