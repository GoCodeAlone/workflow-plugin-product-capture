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
   a selected nonzero loopback debugging port, a 1920x1080 default window,
   container-required sandbox/dev-shm flags, and no automation/identity flags.
4. Before launch, Node rejects an active profile lock. After launch it requires
   the child to remain alive, Linux procfs to map the listening socket to the
   child's dedicated process group, and `/json/version` to expose the expected loopback
   browser endpoint before `chromium.connectOverCDP`. Every platform verifies
   the bounded CDP-reported browser PID equals the live spawned Chrome child
   before any navigation; Linux additionally terminates the dedicated Chrome
   process group and reaps its leader. Startup gets at most three fully cleaned
   attempts.
5. Capture and diagnostics use the attached default context/page. They do not
   call `addInitScript`, `Network.setUserAgentOverride`, or set a Playwright
   viewport, locale, timezone, or graphics renderer.
6. The existing Amazon same-origin homepage warmup and document
   `window.location.assign` navigation remain unchanged.
7. Browser close is bounded. Node gives each Linux Chrome attempt a dedicated
   process group and sends bounded TERM then KILL before retrying. Go separately
   owns the outer Xvfb/Node process group and terminates it on cancellation. The
   task container/cgroup is the parent-death boundary.
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

- Chrome executable failure, early exit, invalid/unready CDP endpoint, process
  ownership mismatch, CDP timeout, profile lock, unavailable headed display,
  and process-group cleanup failure produce bounded errors.
- No fallback reinstalls identity patches or silently changes headed mode.
- Existing browser-target crash retries may restart Chrome within the remaining
  workload deadline; cleanup precedes retry.
- Browser diagnostics continue to emit cookie presence/length only, never values.
- A listener outside the launched process group, a CDP browser PID mismatch, or
  a surviving profile owner fails closed before navigation.

Execution backport 2026-07-13: Linux kernel documentation invalidated live
`/proc/<pid>/task/<pid>/children` traversal as a complete cleanup authority.
Each Chrome attempt is now a dedicated Linux process group. Listener ownership
enumerates procfs members by process-group ID, and cleanup signals that group,
requires no non-zombie member to remain, and requires the Chrome group leader's
exit to be reaped before retry. Unexpected procfs and group-signal errors fail
closed. This replaces captured-tree cleanup without changing the Scope Manifest.

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
| A2 | Chrome binds a selected nonzero loopback CDP port without enabling `AutomationControlled` | Startup may race or hang | Owned-listener + `/json/version` readiness, PID check, bounded clean retry |
| A3 | CDP attachment retains the measured native baseline within the declared matrix | Future Playwright/Chrome may change | Versioned comparison gates promotion; no non-detection claim |
| A4 | A stable profile is used by at most one capture at a time | Concurrent workers can lock it | Fail bounded; operator assigns worker-local profile |
| A5 | Amazon permits anonymous product browsing from staging egress | External challenge may persist | Preserve proof as external block; do not add spoofing |
| A6 | BMW contribution rows expose contributor and PaymentIntent linkage to the owner/admin proof | Existing response may omit fields | Add the narrow authenticated proof projection before staging run |
| A7 | BMW scheduler can identify abandoned proof cards without schema change | Fulfillment evidence may be occupied | Merge proof keys into existing JSON; never overwrite fulfillment evidence |
| A8 | Every platform's CDP reports the spawned browser PID; Linux also exposes listener/process-tree ownership | Runtime hardening could hide process identity | Fail closed before navigation and retry only after captured-tree cleanup |
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

### Backport 2026-07-11: Canonical artifact producer migration

Cause: implementation review showed current workflow-compute stores/emits legacy
`artifact://.../proofs/<proof>/<name>` refs, while the bounded core client
requires the unambiguous `/artifacts/` marker. Lease artifact specs are also a
planned producer field, not present in the current server.

Change: Task 1 keeps the new canonical consumer contract and uses literal
producer fixtures. Before product proof, Task 5 makes workflow-compute emit
canonical refs from stored metadata (including nested names), accept existing
legacy storage records during migration, derive lease specs, and prove a real
handler list-to-download round trip through compute-core `v0.8.4`.

Scope: no manifest change; this clarifies the existing Task 5 producer side of
the core/client boundary.

### Backport 2026-07-11: Native debugger attachment baseline

Cause: candidate conformance proved `--remote-debugging-port=0` enables
Chromium `AutomationControlled`, and `context.newPage()` creates a 56 px taller
content viewport than Chrome's initial headed tab.

Change: select and release an OS-assigned nonzero loopback port, require Linux
Chrome process-tree listener ownership plus endpoint readiness, verify the live
CDP browser PID on every platform, attach with bounded clean retry, and reuse
the single initial `about:blank` page. Never override `navigator.webdriver` or
browser identity.

Failed ownership/readiness attempts retry only after complete cleanup.

Scope: no manifest change; Task 2 retains native Chrome/CDP lifecycle ownership.
Evidence: fixed-port image probe -> initial `webdriver=false`, `1919x936`;
`context.newPage()` -> `webdriver=false`, `1919x992`.

### Backport 2026-07-13: Quick Tunnel activation retries

Cause: six Quick Tunnels registered healthy Cloudflare connections while their
generated hostnames remained unpublished in DNS for the full 45-second health
window; the collector remained healthy on loopback and a separately issued
hostname served the same origin once DNS propagated.

Change: retry up to three fresh Quick Tunnel activations, each bounded to two
minutes, with complete teardown between attempts. Correlation, origin-policy,
and other non-timeout failures remain fail-closed and are never retried.
The CLI default overall timeout is twelve minutes so all three activation
windows and the candidate runtime checks remain bounded but reachable.

Scope: no manifest change; Task 3 retains the accepted ADR 0002 transport and
explicit-origin fallback.
Evidence: default command -> deadline + clean teardown; explicit origin ->
full candidate conformance PASS.

### Backport 2026-07-13: Canonical provider contract decoding

Cause: the provider ABI test duplicated an older contract JSON projection and
rejected the planned `artifact_specs` field before validating the shared wire
contract.

Change: contract compatibility tests decode and validate
`compute-core/protocol.ProviderContract`, then assert normalized bounded
artifact specs. Local ad hoc projections of the provider contract are
forbidden.

Scope: no manifest change; Task 4 owns the contract migration.

Evidence: broad `go test ./...` failed on unknown `artifact_specs`; the focused
provider contract test passes with the canonical SDK type.

### Backport 2026-07-13: Staging proof boundary hardening

Cause: review disproved proof assumptions: workflow inputs were interpolated
inside shell; `task:*` scopes could not list agents; accepted receipts/schema
files were not bound to the requested runtime/contract; same-host or
structurally empty JSON could pass without Amazon/diagnostic correlation;
transient reads either aborted known `auth state busy` recovery or retried
artifacts without a deadline; summary image userinfo could leak; and the
80-minute job could not cover both optional operations.

Change: pass dispatch inputs through quoted environment variables; require
`agent:read`, `task:read`, and `task:write`; validate and bind receipt/task/
executor/image fields, including the receipt's exact leased-task hash and the
provider runtime's sorted artifact aggregate recomputed from downloaded bytes;
reject terminal placement-requirement drift; match schema bytes to the contract
digest; correlate
browser-capture output to supported Amazon hosts and the submitted ASIN across
requested/result/canonical/external identity; require known typed diagnostic
signals plus target/origin/post; reject image userinfo; retry only network,
429, and 5xx reads within phase deadlines; fail fast on permanent 4xx; use a
120-minute job timeout. Task submission remains non-retried, and product-only
runs do not read or require the optional diagnostic schema. Final review also
pins and records the diagnostic artifact-schema digest, requires canonical USD
price output, and checks the artifact hash algorithm against fixed
workflow-compute golden vectors.

Scope: no manifest change; Task 4 owns these proof-boundary invariants.

Evidence: focused regressions fail with each guard removed and pass restored;
`actionlint .github/workflows/staging-proof.yml` validates the workflow shape.

### Backport 2026-07-13: Stable procfs cleanup identity

Cause: CI observed a transiently truncated `/proc/<pid>/stat`; numeric PID/PGID
alone could omit a live member, leak detached Chrome after uncertain cleanup, or
signal a reused process group. Later Linux stress disproved two assumptions:
Chrome itself cannot both exit on TERM and remain the stable PGID identity
anchor, and page tests may freeze `Date.now()` while lifecycle cleanup runs.

Change: retry only incomplete stat reads and require fields through `starttime`.
On Linux, a TERM-resistant supervisor owns the PGID while the browser child PID
and start time are verified separately. Reject missing/drifted supervisor
identity before every liveness decision or signal, including fallback KILL. If
initial capture fails, a one-shot marker permits abort only before the first
event-loop yield, while the fresh detached child cannot be reaped or reused.
After a checked KILL, send no further signal; accept a missing leader only when
a procfs scan finds no live non-zombie group member. Use monotonic time for CDP
startup and process cleanup; retain wall time for page-operation budgets.
Propagate cleanup uncertainty. Shared cleanup formatting stays in the prelude
used by capture and diagnostics.

Scope: no manifest change; Task 2 owns native Chrome process-group cleanup.

Evidence: focused regressions fail with each fix removed and pass restored;
real transient/persistent teardown stress passes 10 consecutive runs; the
formerly hanging frozen-clock continuation test passes in the Linux provider
runtime.

### Backport 2026-07-13: Outer lifecycle identity and cancellation

Cause: the Go command policy could signal a numeric Linux PGID after its leader
was reaped or reused. Lifecycle conformance also returned on context
cancellation after container startup without stopping/removing the container or
reaping the `docker run` process.

Change: capture Linux leader PGID and start time before TERM; revalidate both
before KILL; never signal after `Run`/`Wait` reaps the command; treat a missing
leader with a live group as cleanup uncertainty. Preserve cancellation as the
result while completing bounded container stop, force removal, process reap,
absence assertion, and profile cleanup.

Scope: no manifest change; Tasks 2 and 3 own runtime and conformance lifecycle
cleanup within PR 2.

Evidence: focused policy and cancellation regressions fail with each fix
removed and pass restored; Linux provider and conformance suites pass.

### Backport 2026-07-13: Promotion proof fails closed

Cause: final review disproved remaining promotion assumptions: lifecycle tests
launched bare Chrome; some post-start Docker exits leaked containers; the
supervisor's ignored TERM/HUP disposition reached Chrome; absent diagnostic
booleans decoded as false; dispatch input could redirect a staging token;
nested schemas/formats, task fields, and executor provenance were only partly
bound; non-Linux cleanup was parent-only; and bounded evidence readers or
ordered encodings could hide valid runtime evidence.

Change: run lifecycle activation through provider -> Node -> supervisor ->
Chrome and funnel every post-start exit through bounded stop/remove/reap/
absence checks. Reset child signal defaults before `exec`. Require explicit
automation booleans, strict nested schemas/formats, environment-owned server
origin, full normalized task binding, and exact complete executor identity.
Use dedicated Unix process groups and preserve Windows tree-cleanup failures.
Platform cancellation policies must not read `exec.Cmd.ProcessState` while
`Wait` may mutate it.
Drain tunnel output beyond its retained-log bound, compare brand/language
fields as semantic sets, document the diagnostic origin allowlist, and replace
the delayed shell PID watchdog with checked pre-reap child-state polling.

Scope: no manifest change; Tasks 2-4 retain these runtime and promotion
invariants within PR 2.

Evidence: each focused regression fails with its guard removed and passes when
restored; macOS process-tree behavior, focused `-race`, and Windows amd64
compilation pass.

### Backport 2026-07-13: Full boundary consumption and activation cleanup

Cause: full-diff review found eight remaining fail-open edges: provider
diagnostics exceeded their published schema; outer cancellation could preempt
inner Chrome cleanup; transient tunnel-start failures were not retried; Docker
version probes were unmanaged; canceled tasks were nonterminal; non-Linux
cleanup lacked stable process identity; headed Linux could start without a
display provider; and empty matching observations could pass conformance.

Change: publish and validate the complete secret-safe diagnostic payload; give
inner cleanup a bounded three-second outer grace; retry only typed transient
tunnel activation failures; name, stop, remove, reap, and absence-check Docker
version probes; treat canceled tasks as terminal with provenance; verify Darwin
kernel start time and Windows process creation time before tree termination;
fail headed Linux preflight without `DISPLAY` or `xvfb-run`; and require
nonempty, well-formed stable browser/request evidence before comparison.

Scope: no manifest change; Tasks 2-4 retain these runtime, conformance, and
promotion-proof invariants within PR 2.

Evidence: focused regressions fail with each guard removed and pass restored;
provider-output schema integration and Darwin process-tree behavior pass, and
Windows amd64 test compilation succeeds.

### Backport 2026-07-13: Native evidence and failed-start teardown

Cause: second full-diff review found that native Chrome returns `brands` and
`mobile` with high-entropy UA values beyond the strict schema; retryable tunnel
`Start` errors lacked caller-side teardown; matching empty `Sec-Fetch-*` values
could pass; and pre-process cloudflared failures retained temp ownership or
discarded cleanup errors.

Change: publish and test the representative native UA-data shape; require
top-level navigation fetch-metadata semantics; stop every failed tunnel start
before classification or retry and abort on cleanup failure; and route every
cloudflared start error through idempotent bounded `Stop`, joining temp/process
cleanup errors with the primary failure.

Scope: no manifest change; Tasks 3-4 retain conformance transport and diagnostic
artifact ownership within PR 2.

Evidence: each focused regression fails with its guard removed and passes when
restored; the strict schema digest is
`sha256:c7cfb25ad2fe4842cdd1b5a078c495e16d729cc18f81e7054e7becba0d620d40`.

### Backport 2026-07-13: Contract digest and completed-launch cleanup

Cause: third full-diff review found public diagnostic digest drift, completed
`docker run` cleanup asymmetry, swallowed native tunnel kill errors, non-Unix
build-tag overreach, and duplicate-sensitive semantic sets.

Change: bind the public contract and staging proof to exact diagnostic schema
bytes before network access; funnel ordinary post-start Docker completion
through bounded stop/remove/reap/absence cleanup; preserve force-kill errors;
select explicit Unix and non-Unix process policies; and deduplicate browser,
language, and client-hint sets during semantic comparison.

Scope: no manifest change; Tasks 2-4 retain contract, lifecycle, and diagnostic
proof ownership within PR 2.

Evidence: focused regressions fail before each fix and pass restored; build
selection covers every Go OS family, including Plan 9, JS, and WASI.

### Backport 2026-07-13: Exact schema identities and terminal cleanup

Cause: fourth full-diff review found a diagnostic operation reference/digest
mismatch, cancellation and timely-reap container-removal gaps, and browser data
that could exceed the published artifact schema after valid input acceptance.

Change: hash each diagnostic operation reference from its resolved schema and
pin the separate artifact schema explicitly; reject reference drift before
network access; route cancellation through common stop/remove/reap/absence
cleanup; remove and inspect Docker-backed tunnels after every reap outcome; and
buffer, size-limit, schema-validate, then publish browser diagnostic artifacts.
The input schema now matches the provider's HTTPS and 2,048-character policy.

Scope: no manifest change; Tasks 2-4 retain lifecycle, contract, and artifact
validation within PR 2.

Evidence: focused negative controls leave named containers after timely reap and
emit oversized URL, error, plugin, MIME, and UA-data values before the fixes;
all are rejected or removed after restoration. The operation output digest is
`sha256:94eb33379184a7f00f489c7bc018afff76e0abb4a675609b541ed3cf61ef155e`;
the artifact digest remains
`sha256:c7cfb25ad2fe4842cdd1b5a078c495e16d729cc18f81e7054e7becba0d620d40`.

### Backport 2026-07-13: Shared cleanup budget and truthful evidence

Cause: fifth full-diff review found that the outer launcher waited 10 seconds
for cleanup whose explicit inner bound was 54 seconds, URL length used bytes
while JSON Schema uses characters, sorted header names were labeled as wire
order, and unexpected diagnostic listener failure was discarded.

Change: derive a 59-second outer wait from named stop, reap, force-remove,
final-remove, and absence-inspection bounds; enforce the public URL limit by
Unicode character count; publish sorted request data as `header_names`; and
cancel the run while preserving non-`ErrServerClosed` server failures through
bounded shutdown.

Scope: no manifest change; Tasks 2-4 retain conformance lifecycle and evidence
accuracy within PR 2.

Evidence: the old cleanup budget fails an explicit bound invariant; a
schema-valid multibyte URL fails before character-count enforcement; and an
injected listener failure previously degrades into context timeout. Each
focused control passes after restoration.

### Backport 2026-07-14: Agent-list schema parity

Cause: compute-core `v0.8.4` strictly decoded agent-list responses but omitted
the server's additive `created_at` field, so staging failed before capacity
selection with `json: unknown field "created_at"`.

Change: compute-core `v0.8.5` adds the typed timestamp while preserving strict
rejection of undeclared fields; product-capture consumes `v0.8.5` for the
product-owned staging proof.

Scope: no manifest change; Task 4 retains the same generic capacity and proof
boundary.

Evidence: staging run `29334045518` reached authenticated agent listing and
failed on `created_at`; compute-core's live-shape regression, strict-decoder
regression, race suite, build, and vet pass on `v0.8.5`.

### Backport 2026-07-14: List-envelope schema parity

Cause: after agent listing succeeded, compute-core `v0.8.5` strictly decoded
task-list responses but omitted the server's additive `summary` envelope, so
staging failed before task submission with `json: unknown field "summary"`.
The same audit found the proof-list endpoint also returns a typed `summary`.

Change: compute-core `v0.8.6` adds summary-aware task/proof list methods while
preserving the existing public wrapper types and their encoded JSON shapes;
product-capture consumes `v0.8.6` for the product-owned staging proof.

Scope: no manifest change; Task 4 retains the same generic capacity, task,
proof, and artifact boundaries.

Evidence: staging run `29337266639` passed agent listing and failed on the
task-list `summary`; compute-core's literal live-shape regressions, unknown
field regressions, legacy API/JSON compatibility gates, race suite, build,
vet, base CI, and CodeQL pass on `v0.8.6`.

### Backport 2026-07-14: Provider result timeout margin

Cause: staging runs `29350736038` and `29357974112` gave the browser operation
and enclosing compute task the same 300-second deadline. The task canceled
before the provider's bounded five-second cleanup could return its terminal
result, leaving only a generic timeout proof.

Change: keep the 300-second compute cap; submit a 240-second browser budget and
reserve 60 seconds for cleanup, proof, and artifact reporting. Reject task
timeouts that cannot preserve the margin before any control-plane request.

Scope: no manifest change; Task 4 owns product-specific staging-proof timeout
composition.

Evidence: the regression fails with equal deadlines and passes with the margin;
`go test ./...` and `golangci-lint run --new-from-rev=origin/main` pass.

### Backport 2026-07-14: Managed headed display and bounded page input

Cause: staging task
`task-product-capture-capture-product-410e7544e5cbee32905f` returned an
accepted exit-code-1 proof in eight seconds once the timeout margin exposed the
provider result. The exact released image reproduced locally: the proof's 1 MiB
raw-HTML limit rejected a rendered Amazon page, while Debian `xvfb-run` masked
the command status behind Chrome stderr and its Xvfb lifecycle failed under the
container runtime. An explicitly managed Xvfb process succeeded with the same
browser, request, and 1280x1024 screen; headless mode also succeeded.

Change: submit the provider schema's bounded 10 MiB raw-HTML ceiling while
retaining the separate 1 MiB result-artifact contract; preserve the child
process error alongside browser stderr; and replace `xvfb-run` with a
provider-owned Xvfb process that selects a free display through `-displayfd`,
uses a `1920x1080x24` screen, and is reaped on every exit path after a bounded
TERM-to-KILL escalation. Browser diagnostics preserve the child-process status
alongside Chrome stderr using the same error contract as product capture.

Scope: no locked Scope Manifest, capability, or contract-shape change; Task 4
retains product-capture ownership of its browser runtime and staging proof. The
release metadata advances to `v0.1.61` for the corrected runtime image.

Evidence: focused regressions have red/green/revert proof. The locally rebuilt
headed amd64 runtime captured the real Amazon Xbox URL, emitted `product_json`,
returned zero stderr, and left no container behind. Retained-agent diagnostics
run `29366160504` found the service active/running, the exact failed task in a
terminal error state, and no matching Docker or Podman container left behind;
the removed ephemeral container exposed no additional stderr after the fact.
The candidate conformance attached leg now runs the provider without `DISPLAY`,
forcing its managed-Xvfb path; the exact local image passed direct-versus-attached
comparison and all lifecycle scenarios with no residual container. The accepted
exit-code-1 staging receipt is diagnostic evidence only: Task 4 remains open
until the immutable `v0.1.61` digest returns an accepted successful staging proof.
