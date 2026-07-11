# Native Chrome Session and BMW Commerce Proof Implementation Plan

> **For the implementing agent:** REQUIRED SUB-SKILL: Use autodev:executing-plans to implement this plan task-by-task.

**Goal:** Ship a native headed Chrome product-capture runtime, replace the OCI-upload staging proof, and prove BMW staging capture, two-user Stripe funding, and safe test-mode Issuing card generation.

**Architecture:** Product-capture owns Chrome/CDP lifecycle, diagnostics, conformance, and its staging proof client. Workflow-compute retains generic agent/task/proof APIs while its product-specific OCI proof is removed. BMW binds staging to the tested image digest, persists runtime provenance, and owns commerce/Stripe proof plus card cleanup.

**Tech Stack:** Go 1.26, Node 24, Playwright 1.57, Google Chrome, Docker, GitHub Actions, Workflow/wfctl, PostgreSQL-backed Workflow pipelines, Stripe test mode.

**Base branch:** main

---

## Scope Manifest

**PR Count:** 3
**Tasks:** 7
**Estimated Lines of Change:** ~3,500 including removed workflow-compute proof code (informational; not enforced)

**Out of scope:**
- Production BMW/provider deployment or live Stripe objects.
- Credentialed Amazon profiles, CAPTCHA solving, Playwright source forks, or identity spoofing.
- Actual retailer purchase/card authorization or fabricated retailer order submission.
- Provider-owned all-protocol network namespaces/firewalls; tracked in workspace `docs/FOLLOWUPS.md`.

**PR Grouping:**

| PR # | Title | Tasks | Branch |
|---|---|---|---|
| 1 | Native Chrome product-capture runtime and proof ownership | Task 1, Task 2, Task 3 | `codex/native-chrome-session-20260710` |
| 2 | Remove workflow-compute product-specific OCI proof | Task 4 | `codex/product-capture-proof-ownership-20260711` |
| 3 | Bind BMW staging commerce proof to tested runtime | Task 5, Task 6, Task 7 | `codex/staging-commerce-runtime-proof-20260711` |

**Status:** Draft

## Delivery Order

1. Merge PR 1; tag `v0.1.60`; retain candidate image digest + conformance artifact.
2. Run the product-owned staging proof against the retained staging worker.
3. Merge PR 2 only after step 2 succeeds, so proof ownership never has a gap.
4. Merge PR 3; set BMW staging image-ref variable to the exact PR 1 digest; deploy staging; run Task 7.

### Task 1: Native Chrome/CDP Runtime

**Files:**
- Modify: `internal/provider/provider.go`
- Create: `internal/provider/browser_process_linux.go`
- Create: `internal/provider/browser_process_other.go`
- Modify: `internal/provider/provider_test.go`
- Modify: `README.md`

**Step 1: Write failing launcher and security tests**

Add tests requiring:
- direct `google-chrome` child + `chromium.connectOverCDP`;
- no `chromium.launch`, `launchPersistentContext`, `addInitScript`, `Network.setUserAgentOverride`, `AutomationControlled`, forced UA/client hints/timezone/WebGL;
- 1920x1080 Chrome window flag and image-controlled headed mode;
- fresh `DevToolsActivePort`, live child, Linux listener/process-tree ownership;
- exact HTTPS diagnostic allowlist, public DNS pin, same-origin redirect/request rules, and forced ephemeral diagnostic profile;
- process-group TERM/KILL/reap on timeout.

Run: `GOWORK=off go test ./internal/provider -run 'Test(NativeChrome|BrowserDiagnostic|BrowserProcess|BrowserScript)' -count=1`

Expected: FAIL on missing direct Chrome/CDP lifecycle and existing identity overrides.

**Step 2: Implement bounded native lifecycle**

Implement platform helpers with this contract:

```go
type browserProcessPolicy interface {
    Configure(*exec.Cmd)
    OwnsListeningPort(pid, port int) bool
    TerminateGroup(*exec.Cmd, time.Duration) error
}
```

Linux uses a process group and procfs socket ownership; other platforms require
fresh endpoint + child liveness and bounded child termination. The embedded Node
prelude spawns installed Chrome, polls a newly-created endpoint file, attaches
to the default context, and closes/reaps on every exit path.

**Step 3: Implement diagnostic boundary**

Parse `PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS`, require one exact
HTTPS origin for dynamic diagnostics, resolve only public addresses, pin one
address through a diagnostic-only host resolver rule, block cross-origin HTTP(S),
and always replace the diagnostic profile with a temp directory. Keep cookie
presence/length only.

**Step 4: Preserve session behavior**

Keep `PRODUCT_CAPTURE_BROWSER_PROFILE_DIR` opt-in for anonymous capture state,
same-origin Amazon homepage warmup, `window.location.assign`, continuation
handling, capture deadlines, and 1920x1080 default window selection.

**Step 5: Verify**

Run:
```bash
GOWORK=off go test ./internal/provider -count=1
GOWORK=off go test ./...
golangci-lint run --new-from-rev=origin/main
```

Expected: all tests PASS; lint exit 0; source search finds none of the removed overrides.

**Step 6: Commit**

```bash
git add internal/provider README.md
git commit -m "fix(provider): use native Chrome sessions"
```

Rollback: revert the commit and run the `v0.1.59` runtime image smoke.

### Task 2: Candidate Conformance, Release, and Runtime Provenance

**Files:**
- Create: `internal/conformance/browser.go`
- Create: `internal/conformance/browser_test.go`
- Create: `cmd/browser-runtime-conformance/main.go`
- Modify: `internal/plugin/step.go`
- Modify: `internal/plugin/plugin_test.go`
- Modify: `docker/product-capture-browser/Dockerfile`
- Modify: `.github/workflows/release.yml`
- Modify: `plugin.json`
- Modify: `go.mod`
- Modify: `README.md`
- Cite: `decisions/0002-use-ephemeral-diagnostic-tunnel.md`

**Step 1: Write failing conformance/provenance tests**

Test schema `v1` classification and comparison:
- stable exact fields: webdriver, UA, client-hint brands/platform, language,
  platform, checked Playwright globals, request UA/client hints/`Sec-Fetch-*`,
  first navigation origin;
- window dimensions tolerate 2 px;
- header order, timing, WebGL, hardware/memory, cookies are informational;
- mismatch returns a nonzero conformance result.

Add plugin tests requiring terminal output fields
`provider_image_ref`, `provider_component_ref`, and
`provider_component_digest` copied from the submitted task workload.

Run: `GOWORK=off go test ./internal/conformance ./internal/plugin -count=1`

Expected: FAIL because conformance package and provenance fields do not exist.

**Step 2: Implement the diagnostic endpoint and comparator**

The command serves a bounded self-reporting page, accepts run-correlated direct
and attached observations, launches direct headed Chrome without CDP/Playwright,
runs the provider diagnostic for the attached observation, and writes redacted
JSON containing versions, stable comparisons, informational values, and verdict.
It accepts either an explicit HTTPS origin or a locally-started checksum-pinned
Quick Tunnel from ADR 0002.

**Step 3: Add exact candidate runtime launch checks**

Set image `PRODUCT_CAPTURE_BROWSER_HEADLESS=false`. Build one amd64 image with
`load: true`; record Docker image ID and Chrome/Playwright/Xvfb versions; run:

```bash
go run ./cmd/browser-runtime-conformance --image "$CANDIDATE" --output conformance.json
```

Also terminate candidate containers during startup, navigation timeout, and
SIGTERM; assert no Chrome/Xvfb process or profile lock remains.

Expected: command exit 0 and JSON `.verdict == "pass"`.

**Step 4: Publish the exact tested image**

Change release workflow to `docker push` the already-loaded local tag without a
second build, resolve registry `@sha256:` digest, and include image ID, digest,
versions, and conformance artifact in the job summary. No OCI archive is sent to
workflow-compute.

**Step 5: Prepare `v0.1.60` and verify**

Run:
```bash
GOWORK=off go test ./...
GOWORK=off go run ./cmd/release-prep -write -tag v0.1.60
GOWORK=off go run ./cmd/release-prep -tag v0.1.60
docker build --platform linux/amd64 -f docker/product-capture-browser/Dockerfile -t product-capture:v0.1.60 .
go run ./cmd/browser-runtime-conformance --image product-capture:v0.1.60 --output /tmp/product-capture-conformance.json
jq -e '.verdict == "pass"' /tmp/product-capture-conformance.json
golangci-lint run --new-from-rev=origin/main
```

Expected: tests/lint exit 0; candidate launches; conformance verdict PASS.

**Step 6: Commit**

```bash
git add cmd internal docker .github/workflows/release.yml plugin.json go.mod README.md decisions
git commit -m "feat: gate product runtime promotion"
```

Rollback: re-promote digest-pinned `v0.1.59`; rebuild/launch it and rerun its diagnostic.

### Task 3: Product-Owned Staging Proof Without OCI Upload

**Files:**
- Create: `internal/stagingproof/proof.go`
- Create: `internal/stagingproof/proof_test.go`
- Create: `cmd/product-capture-staging-proof/main.go`
- Create: `.github/workflows/staging-proof.yml`
- Modify: `README.md`
- Modify: `docs/buymywishlist-live-usage.md`

**Step 1: Write failing proof-client tests**

Use `httptest.Server` to require: exact digest-pinned `provider_image_ref`, an
already-online retained worker whose runtime digest matches, task submission,
terminal success, accepted proof, downloaded product JSON, and bounded summary
fields. Reject package/directive/campaign upload calls and any artifact over the
declared result/log limits.

Run: `GOWORK=off go test ./internal/stagingproof -count=1`

Expected: FAIL because the product-owned proof client does not exist.

**Step 2: Implement against generic workflow-compute APIs**

Use the existing compute-core client types. The command takes server/token,
org/pool/product/policy, retained worker ID, product URL/host, and exact image
ref. It never builds, saves, chunks, or uploads a container/package. It writes a
redacted summary with task/proof/artifact hash, runtime ref, title/image/price,
and optional accepted browser diagnostic artifact.

**Step 3: Add staging workflow**

The workflow runs from `main`, validates image ref/URL/worker inputs, executes
the Go command, and uploads only bounded JSON/log artifacts. Use the org-level
self-hosted Linux/X64 runner only if the public repo is allowed; otherwise use
`ubuntu-latest` for the control client while the registered retained compute
worker executes Chrome.

**Step 4: Verify no OCI/package path**

Run:
```bash
GOWORK=off go test ./internal/stagingproof ./cmd/product-capture-staging-proof -count=1
rg -n 'docker save|podman save|oci.tar|agent-artifacts|provider-package|campaign' .github/workflows/staging-proof.yml internal/stagingproof cmd/product-capture-staging-proof && exit 1 || true
actionlint .github/workflows/staging-proof.yml
golangci-lint run --new-from-rev=origin/main
```

Expected: tests/lint/actionlint PASS; forbidden search returns no matches.

**Step 5: Commit**

```bash
git add internal/stagingproof cmd/product-capture-staging-proof .github/workflows/staging-proof.yml README.md docs
git commit -m "feat: own product capture staging proof"
```

Rollback: retain the prior proof workflow until this workflow has one accepted staging run; never restore OCI upload after cutover.

### Task 4: Remove Workflow-Compute Product-Specific Proof Logic

**Repository:** `GoCodeAlone/workflow-compute`

**Files:**
- Delete: `scripts/staging-product-capture-proof.sh`
- Delete: `.github/workflows/staging-product-capture-proof.yml`
- Delete: `staging_product_capture_proof_test.go`
- Modify: `ci_workflows_test.go`
- Modify: `scripts/agent-runner-lifecycle-proof.sh`
- Modify: `scripts/agent-runner-lifecycle-proof.ps1`
- Modify: related runner lifecycle tests/docs found by `rg 'staging-product-capture-proof|PRODUCT_CAPTURE_DELEGATE_PROOF|product-capture.oci.tar'`

**Step 1: Capture the failing ownership invariant**

Add/adjust a repository test that fails when workflow-compute contains a
product-capture-specific staging workflow, OCI packaging/upload, or delegated
product proof orchestration while preserving generic dynamic-provider APIs.

Run: `go test ./... -run 'ProductCapture.*Ownership|CIWorkflows' -count=1`

Expected: FAIL on current script/workflow references.

**Step 2: Remove product-owned orchestration**

Delete the dedicated proof workflow/script and remove product-specific runner
delegation/import code. Keep generic provider workload validation, retained
agent enrollment, image digest enforcement, proof acceptance, and bounded
artifact APIs unchanged.

**Step 3: Verify cutover and generic behavior**

Run:
```bash
rg -n 'staging-product-capture-proof|product-capture-browser\.oci\.tar|PRODUCT_CAPTURE_DELEGATE_PROOF' . && exit 1 || true
go test ./...
golangci-lint run --new-from-rev=origin/main
```

Expected: forbidden search empty; all generic compute tests and lint PASS.

**Step 4: Commit**

```bash
git add -A scripts .github/workflows staging_product_capture_proof_test.go ci_workflows_test.go
git commit -m "refactor: remove product capture proof ownership"
```

Rollback: revert this deletion only if the product-owned workflow has no accepted run; do not restore OCI upload as a long-term path.

### Task 5: BMW Runtime Provenance and Staging Binding

**Repository:** `GoCodeAlone/buymywishlist`

**Files:**
- Modify: `.wfctl-lock.yaml`
- Modify: `app.yaml`
- Modify: `bmwplugin/integration_user_test.go`
- Modify: `bmwplugin/product_capture_backend_test.go`
- Modify: `e2e/tests/staging-product-capture-commerce.spec.ts`
- Modify: `.github/workflows/deploy.yml`
- Modify: `.github/workflows/staging-commerce-product-capture-proof.yml`

**Step 1: Write failing provenance tests**

Require plugin `v0.1.60`, product-capture pipeline output/external data to retain
the submitted runtime ref, staging workflow to pass expected ref, and E2E to
compare expected ref to the captured item's task-bound provenance before Stripe.

Run: `go test ./bmwplugin -run 'ProductCapture|AmazonLookup' -count=1`

Expected: FAIL because runtime provenance is not exposed/persisted/asserted.

**Step 2: Upgrade and wire provenance**

Run `wfctl plugin install workflow-plugin-product-capture@v0.1.60`; propagate
`provider_image_ref`/component fields through lookup/import output into existing
`external_product_data` without a schema migration. Add the exact expected ref
to protected staging workflow environment and redact it to digest-only summary.

**Step 3: Bind staging deployment**

Set the GitHub staging environment variable
`PRODUCT_CAPTURE_COMPUTE_IMAGE_REF=<candidate@sha256:...>`, dispatch BMW staging
deploy, and require deploy summary/runtime env inspection to equal the candidate.

**Step 4: Verify**

Run:
```bash
wfctl plugin install --locked
wfctl validate --strict workflow.yaml
go test ./bmwplugin -run 'ProductCapture|AmazonLookup' -count=1
go test ./scripts -run 'DeployRuntime|ProductCapture' -count=1
golangci-lint run --new-from-rev=origin/main
```

Expected: validation/tests/lint PASS; staging plan contains exact candidate ref.

**Step 5: Commit**

```bash
git add .wfctl-lock.yaml app.yaml bmwplugin e2e .github/workflows
git commit -m "feat: bind capture imports to runtime digest"
```

Rollback: restore the prior staging environment image ref and redeploy; no production variable changes.

### Task 6: Crash-Safe Test-Mode Issuing Proof

**Repository:** `GoCodeAlone/buymywishlist`

**Files:**
- Modify: `bmwplugin/step_stripe_issuing_card_create.go`
- Modify: `bmwplugin/step_stripe_issuing_card_create_test.go`
- Modify: `bmwplugin/step_stripe_issuing_card_cancel.go`
- Create: `bmwplugin/step_stripe_issuing_card_reconcile.go`
- Create: `bmwplugin/step_stripe_issuing_card_reconcile_test.go`
- Modify: `bmwplugin/plugin.go`
- Modify: `bmwplugin/plugin_test.go`
- Modify: `app.yaml`
- Modify: `features/admin_fulfillment.feature`
- Modify: `bmwplugin/integration_unsubmit_test.go`
- Create: `bmwplugin/integration_staging_card_cleanup_test.go`

**Step 1: Write failing Stripe safety tests**

Require:
- staging rejects non-`sk_test_` configuration;
- card create accepts deterministic idempotency key and proof/fulfillment metadata;
- output includes only card ID + `livemode` boolean;
- evidence reservation precedes Stripe create;
- authenticated abort cancels and clears card without ordered/fake retailer state;
- scheduler reconciles overdue DB rows and metadata-discoverable orphan test cards;
- secrets/raw card response never enter logs/results.

Run: `go test ./bmwplugin -run 'Issuing|StagingCardCleanup|Unsubmit' -count=1`

Expected: FAIL on missing idempotency, mode, abort, and reconciler behavior.

**Step 2: Implement typed Stripe operations**

Extend typed card creation with idempotency and metadata, expose `Livemode`, and
add a bounded reconciler interface that lists recent active test cards and
cancels only cards carrying the staging proof marker. Keep all-time spend limit
equal to funded cents and MCC restrictions unchanged.

**Step 3: Implement pipelines**

Before creation merge proof reservation/deadline into fulfillment `evidence`.
Add `POST /api/v1/admin/fulfillments/{id}/abort-purchase` behind authenticated
`fulfillment_management`, assigned-operator/awaiting-state checks, cancellation,
and reference clearing. Add five-minute staging-proof cleanup scheduler; it is a
no-op without test credentials/staging marker.

**Step 4: Verify API and scheduler**

Run:
```bash
go test ./bmwplugin -run 'Issuing|StagingCardCleanup|Unsubmit' -count=1
go test ./...
golangci-lint run --new-from-rev=origin/main
```

Expected: all PASS; abort returns HTTP 200 with `{card_id:"ic_...",livemode:false,canceled:true}`; unauthorized call returns 403.

**Step 5: Commit**

```bash
git add bmwplugin app.yaml features
git commit -m "feat: make staging card proof crash safe"
```

Rollback: revert pipelines/steps and redeploy staging; run a one-time test-card reconciliation before rollback completes.

### Task 7: Full BMW Staging Commerce Validation

**Repository:** `GoCodeAlone/buymywishlist`

**Files:**
- Modify: `e2e/tests/staging-product-capture-commerce.spec.ts`
- Modify: `.github/workflows/staging-commerce-product-capture-proof.yml`
- Modify: `app.yaml` admin contribution projection
- Modify: `bmwplugin/integration_admin_test.go`

**Step 1: Write failing E2E contract assertions**

Require, in order: owner/wishlist/item; real Amazon title/image/positive price;
accepted task/proof/artifact + exact candidate runtime; contributor one partial;
contributor two completes; admin projection proves two distinct contributor IDs,
contribution IDs, and PaymentIntent IDs; item and wishlist funded; fulfillment
claim; test-mode `ic_` + `livemode=false`; abort in `finally`; no submit call.
Sensitive endpoint failures log status/request ID only.

Run: `cd e2e && npx playwright test tests/staging-product-capture-commerce.spec.ts --project api --list`

Expected before implementation: static/Go contract tests fail on missing fields/cleanup.

**Step 2: Expose protected contribution linkage**

Add `contributor_id` to the authenticated admin item-contributions projection;
keep public contribution responses unchanged. Add authz/integration assertions.

**Step 3: Harden workflow gates**

Fail before contributions unless staging publishable/secret key modes are test;
ensure Stripe Payments and Issuing webhooks with the existing `mode=ensure`
workflows; pass candidate ref and proof run ID; upload only redacted IDs/booleans.

**Step 4: Local verification**

Run:
```bash
go test ./bmwplugin -run 'Admin.*Contribution|Issuing|ProductCapture' -count=1
cd e2e && npm ci && npx playwright test tests/staging-product-capture-commerce.spec.ts --project api --list
actionlint .github/workflows/staging-commerce-product-capture-proof.yml
golangci-lint run --new-from-rev=origin/main
```

Expected: tests/actionlint/lint PASS; one E2E test listed.

**Step 5: Commit and publish PR 3**

```bash
git add app.yaml bmwplugin e2e .github/workflows
git commit -m "test: prove staging capture commerce round trip"
```

**Step 6: Deploy and run real staging proof**

After PRs are green/merged and `v0.1.60` is published:
```bash
gh workflow run provision-stripe-payments-webhook.yml -f environment=staging -f mode=ensure
gh workflow run provision-stripe-issuing-webhook.yml -f environment=staging -f mode=ensure
gh workflow run deploy.yml -f environment=staging
gh workflow run staging-commerce-product-capture-proof.yml -f product_url=https://www.amazon.com/dp/B08N5WRWNW
```

Monitor runners without SSH until complete. Expected redacted evidence:
- capture task/proof accepted; exact candidate digest; title/image/price present;
- two distinct `pi_` + contributor/contribution IDs; partial then funded states;
- one `ic_`, `livemode=false`, then canceled by abort/reaper;
- no raw client secret, ephemeral key, PAN/CVC, or fake retailer order.

Rollback: restore previous staging image ref, redeploy, cancel any proof-tagged test cards, and rerun staging health checks.
