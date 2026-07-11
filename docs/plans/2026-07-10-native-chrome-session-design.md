# Native Chrome Product-Capture Session Design

**Status:** Approved; adversarial review Cycle 5 PASS
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
2. The published image sets `PRODUCT_CAPTURE_BROWSER_HEADLESS=false`; the
   standalone binary preserves its current headless default. Missing
   Xvfb/display in requested headed mode is an explicit preflight error.
3. Node starts installed `google-chrome` with the selected profile directory,
   `--remote-debugging-port=0`, a 1920x1080 default window, container-required
   sandbox/dev-shm flags, and no automation/identity flags.
4. Before launch, Node rejects an active profile lock and removes only a stale
   `DevToolsActivePort`. After launch it accepts a newly created endpoint file
   only when its mtime is newer than the child start, the child is alive, and
   Linux procfs maps the listening socket to the launched process tree. It then
   calls `chromium.connectOverCDP`.
5. Capture and diagnostics use the attached default context/page. They do not
   call `addInitScript`, `Network.setUserAgentOverride`, or set a Playwright
   viewport, locale, timezone, or graphics renderer.
6. The existing Amazon same-origin homepage warmup and document
   `window.location.assign` navigation remain unchanged.
7. Browser close is bounded. Go owns a process group containing Xvfb, Node,
   Chrome, and Chrome children; cancellation sends bounded TERM then KILL and
   reaps the group. The task container/cgroup is the parent-death boundary.
8. Normal close, startup failure, timeout, and signal paths remove only
   ephemeral profile state. Stable-profile lock failures never trigger unsafe
   lock-file deletion.

## Diagnostic Boundary

- Dynamic browser diagnostics require an exact HTTPS origin in
  `PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS`; unset means disabled.
- Dynamic diagnostics always use an ephemeral profile, even when capture uses a
  stable anonymous profile; diagnostic tasks cannot read retained cookies.
- The requested origin and every top-level redirect must match the allowlist.
  Initial DNS resolution must contain no loopback, link-local, or private IP.
- Go selects one validated public address and passes a diagnostic-only Chrome
  host-resolver rule that pins the allowed hostname to that address. Chrome
  cannot re-resolve the host to a private address during the operation.
- The diagnostic browser blocks cross-origin HTTP(S) requests. It posts only
  bounded browser signals to the same allowed origin and never emits cookies.
- The operator-managed endpoint stores structured headers/signals only, redacts
  network identifiers from shared artifacts, and uses short-lived logs.

Accepted risk D14: the provider does not create network namespaces or worker
firewall rules for WebSocket/WebTransport. The exact allowlist is trusted
operator configuration, diagnostics use an ephemeral profile with no application
secrets, cross-origin HTTP(S) is blocked, and diagnostics are disabled outside
the staging proof. Enforcing all-protocol egress belongs to the container runtime,
not this provider library.

## Session State

- Unset `PRODUCT_CAPTURE_BROWSER_PROFILE_DIR`: Go creates a per-operation
  ephemeral profile and removes it after completion.
- Set to a stable directory: anonymous cookies and continuation state survive
  captures on that retained worker.
- Credentialed profiles remain forbidden. Profile lock contention fails with a
  bounded diagnostic; the provider does not clone or merge profile state.
- The image runs as unprivileged `node` with dedicated `HOME`; profile paths are
  operator runtime configuration, never workload input. An operator able to
  inject arbitrary mounts/environment already controls the provider container,
  so no additional path-root policy is treated as an auth boundary.

## Error Handling

- Chrome executable failure, early exit, malformed `DevToolsActivePort`, CDP
  timeout, profile lock, unavailable headed display, and process-group cleanup
  failure produce bounded errors.
- No fallback reinstalls identity patches or silently changes headed mode.
- Existing browser-target crash retries may restart Chrome within the remaining
  workload deadline; cleanup precedes retry.
- Browser diagnostics continue to emit cookie presence/length only, never values.
- A stale endpoint file, a listener outside the launched process group, or a
  surviving profile owner fails closed before CDP attachment.

## Security Review

- CDP binds through Chrome's ephemeral debugging port and is consumed only via
  `127.0.0.1`; no service port or public route is added.
- Dynamic diagnostics are disabled without an explicit trusted-origin allowlist
  and cannot navigate or post outside that origin.
- Target URL validation and same-origin warmup validation remain unchanged.
- Chrome receives no BMW, Stripe, or workflow-compute credentials.
- Stable profiles are anonymous operator-managed state and must not be shared
  with interactive or authenticated Chrome sessions.
- BMW abort-purchase is server-side authenticated with fulfillment-management
  authorization, acts only on the claimed awaiting fulfillment, and cancels the
  stored card before clearing its reference.
- Staging deploy/proof gates require `sk_test_`/`pk_test_` keys without logging
  them. PaymentIntent and Issuing-card responses expose `livemode`; the proof
  asserts `false`, and staging fails closed on live credentials.
- Stripe mode is asserted server-side from SDK responses. Protected proof output
  contains only `pi_`/`ic_` object IDs and `livemode` booleans; client secrets,
  ephemeral-key secrets, PAN/CVC, and raw sensitive response bodies are excluded
  from logs and artifacts.

## Infrastructure Impact

- No database, Terraform, DigitalOcean, or durable Cloudflare resources.
- `workflow-plugin-compute-core` gains generic read-only agent/lease/artifact
  client methods. Workflow-compute enforces existing provider `artifact_specs`
  at agent and server upload boundaries and removes product-specific proof code.
- The existing runtime image already installs Google Chrome, Xvfb, and xauth;
  image startup preflight verifies all three before promotion.
- Release produces the existing amd64 provider image and component artifact.
- Release builds once with `load: true` into the runner's Docker content store,
  records the image ID plus Chrome/Playwright/Xvfb versions, and runs the real
  diagnostic smoke against that local tag. On success `docker push` publishes
  that exact local image; the workflow never invokes a second build.
- Promotion uses the reported immutable digest and changes only the
  product-capture provider component version/digest.
- BMW staging owns runtime selection: set its environment-scoped
  `PRODUCT_CAPTURE_COMPUTE_IMAGE_REF` to the candidate `@sha256:` reference and
  run the staging deploy before commerce proof. Production configuration remains
  unchanged.

## Native Baseline Contract

- The controlled endpoint serves a versioned, self-collecting JavaScript page.
  A direct headed Chrome process from the candidate image visits it without
  Playwright, CDP, init scripts, or protocol overrides and posts schema `v1`.
- The attached run uses the same image, Chrome flags, Xvfb display, endpoint,
  run correlation, and schema. Chrome/Playwright/Xvfb versions are evidence.
- Stable promotion fields: `navigator.webdriver`, UA, UA client-hint brand set
  and platform, language set, platform, checked Playwright globals, request UA,
  request client hints, `Sec-Fetch-*`, and first top-level navigation origin.
- Reject when attached and direct stable fields differ, an automation global is
  present, or the attached run adds an identity override. Display dimensions may
  differ by 2 px for window chrome. Header order, timings/sequence, WebGL,
  hardware/memory, cookie length, and graphics renderer are informational only.
- The comparison diagnoses unintended differences; it does not optimize against
  retailer controls or claim that a site cannot identify automation.

## Candidate Provenance

1. Release reports the tested candidate image ref and digest.
2. BMW staging environment variable `PRODUCT_CAPTURE_COMPUTE_IMAGE_REF` is set
   to that exact ref and BMW is redeployed through its staging deploy workflow.
3. `step.product_capture` returns its submitted provider image/component ref and
   digest with task/proof output; BMW persists the value on the product import.
4. The BMW staging test receives the expected candidate ref from its protected
   workflow environment and rejects any captured item whose persisted runtime
   ref/digest differs. Task ID, accepted proof ID, artifact hash, and runtime
   digest must all refer to the same import before commerce proceeds.

## Multi-Component Validation

1. Unit/source tests prove the launcher uses native Chrome/CDP and contains none
   of the removed identity overrides.
2. Build the real runtime image and run its browser diagnostic against a
   controlled allowlisted echo endpoint. Record Chrome, Playwright, and Xvfb
   versions and compare direct versus CDP-attached sessions across request
   the versioned Native Baseline Contract. Only stable-field mismatches reject;
   volatile headers/order, display metrics, WebGL, request sequence, and cookies
   remain diagnostic evidence.
3. Kill the real image during startup, navigation timeout, SIGTERM, and parent
   exit; assert no Chrome/Xvfb process or profile lock survives the container.
4. Release/promote the tested candidate digest, update/deploy BMW staging to its
   exact `PRODUCT_CAPTURE_COMPUTE_IMAGE_REF`, and retain an accepted
   workflow-compute diagnostic proof plus its bounded JSON artifact.
5. Dispatch BuyMyWishlist
   `.github/workflows/staging-commerce-product-capture-proof.yml`, which owns
   `e2e/tests/staging-product-capture-commerce.spec.ts`, with a real Amazon URL.
   Require title, image, positive price, task/proof IDs, exact candidate runtime,
   contributor-one partial
   funding, contributor-two completion, two distinct PaymentIntent IDs, funded
   item/wishlist state, fulfillment claim, and one Stripe Issuing `ic_` card.
6. The BMW proof verifies final contribution rows map to two distinct user IDs,
   contribution IDs, and the two recorded PaymentIntent IDs. It does not submit
   a fabricated retailer order. A `finally` block calls an admin abort-purchase
   endpoint that cancels the `ic_` card, clears it from the awaiting fulfillment,
   and returns the canceled card ID; cleanup failure fails the proof.
7. Before Stripe card creation, `begin-purchase` durably reserves a staging proof
   run ID, deterministic idempotency key, and cleanup deadline in existing
   fulfillment `evidence`. Card creation uses that key and writes proof run plus
   fulfillment IDs into Stripe metadata. A BMW scheduler cancels overdue cards,
   clears references, and reconciles recent active test cards discoverable by
   metadata even when card-reference persistence failed. This covers failures
   between Stripe creation and DB update plus runner loss/SIGKILL; the card's
   all-time spending limit remains the funded item amount.
8. Before creating contributions, the workflow verifies staging key prefixes.
   Each PaymentIntent and the Issuing card must report `livemode=false`.

## Integration Matrix

| integration | class | owner | proof |
|---|---|---|---|
| Chrome + Playwright CDP | runtime-integrated | this repo | candidate image diagnostic + lifecycle smoke |
| Compute-core proof client | runtime-integrated | workflow-plugin-compute-core | agent/lease preflight + bounded artifact download against real staging API |
| Product provider + workflow-compute | runtime-integrated | product plugin + workflow-compute staging | submitted runtime ref propagated with accepted task proof/artifact |
| Amazon anonymous browse | runtime-integrated external | BMW staging proof | real URL returns title/image/price; challenges remain external |
| BMW wishlist/capture callback | runtime-integrated | BuyMyWishlist | existing staging commerce workflow/test |
| Stripe Payments + webhooks | runtime-integrated | BuyMyWishlist | test-mode objects; two users, partial then funded, distinct PaymentIntents |
| Stripe Issuing | runtime-integrated | BuyMyWishlist | test-mode `ic_` card; abort or server-side reap cancels it |
| Production promotion | deferred | operator | staging evidence is required first; no production change in scope |

## Assumptions

| id | assumption | challenge | fallback |
|---|---|---|---|
| A1 | Runtime image contains `google-chrome`, Xvfb, and xauth | Image drift breaks startup | Dockerfile contract test + runtime image smoke |
| A2 | Chrome writes `DevToolsActivePort` for `--remote-debugging-port=0` | Startup may hang | Bounded poll + child-exit detection |
| A3 | CDP attachment retains the measured native baseline within the declared matrix | Future Playwright/Chrome may change | Versioned comparison gates promotion; no non-detection claim |
| A4 | A stable profile is used by at most one capture at a time | Concurrent workers can lock it | Fail bounded; operator assigns worker-local profile |
| A5 | Amazon permits anonymous product browsing from staging egress | External challenge may persist | Preserve proof as external block; do not add spoofing |
| A6 | BMW contribution rows expose contributor and PaymentIntent linkage to the owner/admin proof | Existing response may omit fields | Add the narrow authenticated proof projection before staging run |
| A7 | BMW scheduler can identify abandoned proof cards without schema change | Fulfillment evidence may be occupied | Merge proof keys into existing JSON; never overwrite fulfillment evidence |
| A8 | Linux procfs exposes the Chrome listener/process relationship in the published image | Runtime hardening could hide process FDs | Fail closed in image; standalone non-Linux keeps fresh-file + child-liveness checks |
| A9 | BMW staging deploy consumes its environment-scoped image-ref variable | Workflow drift could use app default | Deploy summary + persisted task runtime ref must equal candidate |

## Self-Challenge

- A plain Playwright launch is less code, but it fails the requirement that the
  browser session follow a native launch contract.
- CDP lifecycle code is the highest-risk addition; process-group and real image
  kill tests cover readiness, timeout, parent death, and cleanup.
- Headed operation is an image-level default, not a standalone-binary breaking
  default; image preflight catches missing Xvfb before promotion.

## Rollback

Drain the retained worker, stop all capture containers, and archive the
anonymous profile. Re-promote the last accepted provider component/image
(`v0.1.59`). If its Chrome cannot open the profile, remove the anonymous profile
and start clean; no credentials are lost. Rerun the accepted diagnostic proof
before BMW traffic resumes. No schema or application-data migration is needed.

### Backport 2026-07-11: Bounded proof ownership

Cause: plan review showed the original design did not fully specify the user's
requirement to stop sending OCI/runtime images as product proof data.

Change: product-capture owns the staging proof; compute-core exposes narrow
read-only client APIs; generic provider result uploads must match declared
artifact name, content type, byte limit, and JSON syntax when applicable, while
product-capture validates the product schema. Workflow-compute first deploys
that generic enforcement. After the replacement proof succeeds, a separate PR
removes its product-specific packaging/proof orchestration and legacy
`WorkloadProductCapture`/`ProductCaptureBrowserProvider` runtime surface while
retaining the generic `WorkloadProvider` path.

Scope: pre-lock manifest expands from three to five PRs; no production deploy.

### Backport 2026-07-11: Executable staging prerequisites

Cause: plan review found the bounded client omitted the canonical `/artifacts/`
ref segment, webhook `ensure` could rotate a secret after deployment, and BMW's
real readiness cron delays funded items for seven days.

Change: bounded downloads round-trip canonical
`artifact://<pool>/tasks/<task>/proofs/<proof>/artifacts/<name>` refs. Stripe
Payments and Issuing `ensure` runs must succeed before the BMW deployment that
consumes their environment secrets. Fulfillment readiness uses an
environment-backed delay: seven days by default/production and zero only in
staging, while the proof still exercises the real cron/dispatcher path.

Scope: no manifest change; Tasks 1, 7, and 9 absorb the corrected prerequisites.

### Backport 2026-07-11: Actions provenance authorization

Cause: the commerce workflow's default token cannot read Actions run metadata
under its current `contents: read` permission.

Change: grant `actions: read`; expose `${{ github.token }}` only to the metadata
preflight step; validate exact workflow, event, conclusion, SHA, and run ordering.

Scope: no manifest change; Task 9 owns the workflow permission and contract test.

### Backport 2026-07-11: Proof source provenance

Cause: an unpinned `workflow_dispatch` could run newer main-branch proof code
against the older deployed PR 5 SHA while prerequisite run IDs remained valid.

Change: fail unless current `main`, the proof run's `headSha`, workflow
`github.sha`, expected SHA input, and deployed SHA are identical. Dispatch with
`--ref main`; a newer main requires redeploy and a fresh proof sequence.

Scope: no manifest change; Task 9 owns the fail-closed SHA checks.
