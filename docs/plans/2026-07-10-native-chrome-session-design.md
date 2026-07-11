# Native Chrome Product-Capture Session Design

**Status:** Approved
**Date:** 2026-07-10
**Decision:** See `decisions/0001-attach-to-native-chrome.md`.

## Goal

Make product capture use the installed browser's native identity while retaining
Playwright only as a control API. Preserve a real Amazon homepage-to-product
navigation, optional anonymous cookie persistence, and the existing bounded
capture/diagnostic contracts.

## Global Design Guidance

- Generic provider/runtime behavior remains in this repository.
- Workflow-compute continues to own scheduling, leases, proofs, and artifacts.
- BuyMyWishlist consumes the provider contract; it does not gain browser logic.
- No credentialed shopping sessions or credential-bearing profile support.
- Real runtime and staging proofs are required; package tests alone are not proof.

## Approaches

| approach | result | decision |
|---|---|---|
| Native Chrome process + loopback CDP | Native launch/request/JS behavior; explicit lifecycle ownership | Chosen |
| Remove overrides but retain `chromium.launch` | Simpler, but Playwright still owns the launch contract | Rejected |
| Fork/patch Playwright names and injected code | High maintenance; no measured checked-global leak to fix | Rejected |

## Architecture

1. Go starts the Node script under Xvfb when headed mode lacks `DISPLAY`.
2. Headed mode is the provider default. Missing Xvfb/display is an explicit
   runtime error; `PRODUCT_CAPTURE_BROWSER_HEADLESS=true` remains an opt-in.
3. Node starts installed `google-chrome` with the selected profile directory,
   `--remote-debugging-port=0`, a 1920x1080 default window, container-required
   sandbox/dev-shm flags, and no automation/identity flags.
4. Node reads Chrome's profile-local `DevToolsActivePort`, validates the loopback
   port, and calls `chromium.connectOverCDP`.
5. Capture and diagnostics use the attached default context/page. They do not
   call `addInitScript`, `Network.setUserAgentOverride`, or set a Playwright
   viewport, locale, timezone, or graphics renderer.
6. The existing Amazon same-origin homepage warmup and document
   `window.location.assign` navigation remain unchanged.
7. Browser close is bounded. Normal close, startup failure, timeout, and signal
   paths terminate the child process and remove only ephemeral profile state.

## Session State

- Unset `PRODUCT_CAPTURE_BROWSER_PROFILE_DIR`: Go creates a per-operation
  ephemeral profile and removes it after completion.
- Set to a stable directory: anonymous cookies and continuation state survive
  captures on that retained worker.
- Credentialed profiles remain forbidden. Profile lock contention fails with a
  bounded diagnostic; the provider does not clone or merge profile state.

## Error Handling

- Chrome executable failure, early exit, malformed `DevToolsActivePort`, CDP
  timeout, profile lock, and unavailable headed display produce bounded errors.
- No fallback reinstalls identity patches or silently changes headed mode.
- Existing browser-target crash retries may restart Chrome within the remaining
  workload deadline; cleanup precedes retry.
- Browser diagnostics continue to emit cookie presence/length only, never values.

## Security Review

- CDP binds through Chrome's ephemeral debugging port and is consumed only via
  `127.0.0.1`; no service port or public route is added.
- Target URL validation and same-origin warmup validation remain unchanged.
- Chrome receives no BMW, Stripe, or workflow-compute credentials.
- Stable profiles are anonymous operator-managed state and must not be shared
  with interactive or authenticated Chrome sessions.

## Infrastructure Impact

- No database, Terraform, DigitalOcean, Cloudflare, or workflow-compute API
  changes.
- The existing runtime image already installs Google Chrome, Xvfb, and xauth.
- Release produces the existing amd64 provider image and component artifact.
- Promotion changes only the product-capture provider component version/digest.

## Multi-Component Validation

1. Unit/source tests prove the launcher uses native Chrome/CDP and contains none
   of the removed identity overrides.
2. Build the real runtime image and run its browser diagnostic against a
   controlled echo endpoint; compare headers and JS signals with a directly
   launched Chrome baseline.
3. Release/promote the image and retain an accepted workflow-compute staging
   diagnostic proof.
4. Run BuyMyWishlist staging commerce proof with a real Amazon URL; require
   returned title, image, and price before Stripe funding proceeds.

## Assumptions

| id | assumption | challenge | fallback |
|---|---|---|---|
| A1 | Runtime image contains `google-chrome`, Xvfb, and xauth | Image drift breaks startup | Dockerfile contract test + runtime image smoke |
| A2 | Chrome writes `DevToolsActivePort` for `--remote-debugging-port=0` | Startup may hang | Bounded poll + child-exit detection |
| A3 | CDP attachment does not expose checked Playwright globals | Future Playwright/Chrome may change | Diagnostic comparison gates promotion |
| A4 | A stable profile is used by at most one capture at a time | Concurrent workers can lock it | Fail bounded; operator assigns worker-local profile |
| A5 | Amazon permits anonymous product browsing from staging egress | External challenge may persist | Preserve proof as external block; do not add spoofing |

## Self-Challenge

- A plain Playwright launch is less code, but it fails the requirement that the
  browser session follow a native launch contract.
- CDP lifecycle code is the highest-risk addition; readiness and cleanup need
  direct tests plus a real image launch test.
- Default headed operation depends on Xvfb outside the published image; fail
  clearly rather than hide environmental drift.

## Rollback

Re-promote the last accepted provider component/image (`v0.1.59`) and rerun the
accepted diagnostic proof. No schema or stored-data migration is required.
Stable anonymous Chrome profiles may be retained or deleted to reset state.
