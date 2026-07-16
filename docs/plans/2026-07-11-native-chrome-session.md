# Native Chrome Session and BMW Commerce Proof Implementation Plan

> **For the implementing agent:** REQUIRED SUB-SKILL: Use autodev:executing-plans to implement this plan task-by-task.

**Goal:** Ship a native headed Chrome product-capture runtime, replace the OCI-upload staging proof, and prove BMW staging capture, two-user Stripe funding, and safe test-mode Issuing card generation.

**Architecture:** Product-capture owns Chrome/CDP lifecycle, diagnostics, conformance, and its staging proof client. Workflow-compute retains generic agent/task/proof APIs while its product-specific OCI proof is removed. BMW binds staging to the tested image digest, persists runtime provenance, and owns commerce/Stripe proof plus card cleanup.

**Tech Stack:** Go 1.26, Node 24, Playwright 1.57, Google Chrome, Docker, GitHub Actions, Workflow/wfctl, PostgreSQL-backed Workflow pipelines, Stripe test mode.

**Base branch:** main

---

## Scope Manifest

**PR Count:** 5
**Tasks:** 9
**Estimated Lines of Change:** ~5,000 including removed workflow-compute proof/runtime code (informational; not enforced)

**Out of scope:**
- Production BMW/provider deployment or live Stripe objects.
- Credentialed Amazon profiles, CAPTCHA solving, Playwright source forks, or identity spoofing.
- Actual retailer purchase/card authorization or fabricated retailer order submission.
- Provider-owned all-protocol network namespaces/firewalls; tracked in workspace `docs/FOLLOWUPS.md`.

**PR Grouping:**

| PR # | Title | Tasks | Branch |
|---|---|---|---|
| 1 | Add bounded staging-proof client APIs | Task 1 | `codex/provider-proof-client-20260711` |
| 2 | Native Chrome product-capture runtime and proof ownership | Task 2, Task 3, Task 4 | `codex/native-chrome-session-20260710` |
| 3 | Enforce provider result artifact contracts | Task 5 | `codex/provider-result-artifact-policy-20260711` |
| 4 | Remove workflow-compute product-specific proof/runtime | Task 6 | `codex/product-capture-proof-ownership-20260711` |
| 5 | Bind BMW staging commerce proof to tested runtime | Task 7, Task 8, Task 9 | `codex/staging-commerce-runtime-proof-20260711` |

**Status:** Locked 2026-07-11T06:57:35Z

## Delivery Order

1. Merge PR 1; tag compute-core `v0.8.4`.
2. Merge PR 2 using core `v0.8.4`; tag product-capture `v0.1.60`; retain candidate digest/conformance.
3. Merge PR 3 using core `v0.8.4`; deploy workflow-compute staging; prove
   server/agent enforcement with valid and rejected artifact probes.
4. Run the product-owned staging proof after its API capacity preflight succeeds.
5. Merge/deploy PR 4 only after step 4 succeeds, so proof ownership never has a gap.
6. Run and require both staging Stripe webhook `ensure` workflows; then set the
   BMW staging image-ref before PR 5 merge. Merge PR 5 with its staging-only
   zero dispatch delay; let the existing CI `workflow_run` deploy the exact main
   SHA and fresh webhook secrets; run Task 9.

### Task 1: Generic Bounded Staging-Proof Client APIs

**Repository:** `GoCodeAlone/workflow-plugin-compute-core`

**Files:**
- Modify: `protocol/client.go`
- Modify: `protocol/client_test.go`
- Modify: `protocol/types.go`
- Modify: `protocol/types_test.go`

**Step 1: Write failing read-only client tests**

Define exact exported methods:

```go
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error)
func (c *Client) ListLeases(ctx context.Context) ([]Lease, error)
func (c *Client) ListTaskArtifacts(ctx context.Context, taskID string) ([]TaskArtifact, error)
func (c *Client) DownloadTaskArtifact(ctx context.Context, ref string, maxBytes int64) ([]byte, error)
```

Tests require bearer auth, URL escaping, strict JSON, status-only errors, and
`maxBytes+1` rejection without returning partial data. Add helpers that select
one online matching idle agent and reject an active lease/queued matching task.
Extend `Lease` with server-derived `provider_artifact_specs` so an agent can
enforce the registered operation contract before upload; clients cannot set
this field on task submission.

Run: `GOWORK=off go test ./protocol -run 'Client.*(Agent|Lease|Artifact|Capacity)' -count=1`

Expected: FAIL because methods/types are absent.

**Step 2: Implement narrow APIs against existing endpoints**

Use existing `/v1/agents`, `/v1/leases`, and `/v1/tasks/{id}/artifacts`
contracts. Download accepts only the returned canonical
`artifact://<pool>/tasks/<task>/proofs/<proof>/artifacts/<name>` ref,
validates/escapes each fixed and name segment (including safe multi-segment
artifact names), and calls the existing proof-scoped download route. Do not
expose generic arbitrary-path requests. Download rejects nonpositive limits and
reads at most `maxBytes+1`. Strictly decode the lease's normalized artifact
specs. Add a server-fixture round trip from `ListTaskArtifacts` to
`DownloadTaskArtifact` so the parser and real route cannot drift. Fixtures must
be literal workflow-compute JSON projections, not values marshaled from the
consumer's own types. Mirror every currently emitted nested capability field,
including runtime-backend reports, and make idle selection use normalized,
enabled network profiles with the top-level org/pool as implicit default.

**Step 3: Prepare and verify `v0.8.4`**

Run:
```bash
GOWORK=off go test ./...
golangci-lint run --new-from-rev=origin/main
```

Expected: tests PASS; lint exit 0; representative `httptest` downloads exact JSON artifact.

After PR 1 merges, create tag `v0.8.4`, monitor the existing release workflow,
and require its GitHub release to be published before Task 4 updates `go.mod`.

**Step 4: Commit**

```bash
git add protocol
git commit -m "feat(protocol): add bounded proof reads"
```

Rollback: product/compute remain on their current core versions until `v0.8.4` is published; the additive API has no server migration.

### Task 2: Native Chrome/CDP Runtime

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
- selected nonzero loopback CDP port, live child, Linux listener/process-group
  ownership, endpoint readiness, and all-platform CDP browser-PID verification;
- exact HTTPS diagnostic allowlist, public DNS pin, same-origin redirect/request rules, and forced ephemeral diagnostic profile;
- process-group TERM/KILL/reap on timeout.

Run: `GOWORK=off go test ./internal/provider -run 'Test(NativeChrome|BrowserDiagnostic|BrowserProcess|BrowserScript)' -count=1`

Expected: FAIL on missing direct Chrome/CDP lifecycle and existing identity overrides.

**Step 2: Implement bounded native lifecycle**

Implement platform helpers with this contract:

```go
type browserProcessPolicy interface {
    Configure(*exec.Cmd)
    TerminateGroup(*exec.Cmd, time.Duration) error
}
```

Linux uses a dedicated per-attempt Chrome process group, procfs group-member
socket ownership, and group cleanup;
all platforms require child liveness plus CDP browser-PID equality and bounded
child termination. The
embedded Node prelude spawns installed Chrome on a selected nonzero loopback
port, polls listener and `/json/version` readiness, attaches to the default
context and initial page, and closes/reaps on every exit path. Startup gets at
most three fully cleaned attempts.

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

**Execution backport 2026-07-11:** Candidate conformance disproved the port-zero
and new-page assumptions. Task 2 instead selects and releases an OS-assigned
nonzero loopback CDP port,
validates listener/readiness/PID ownership before navigation, retries startup
with full cleanup, and reuses Chrome's initial `about:blank` page. This preserves
the native browser baseline without identity overrides and does not change the
Scope Manifest.

**Execution backport 2026-07-13:** Captured child-tree traversal cannot prove
complete cleanup for live processes. Linux Chrome attempts now lead dedicated
process groups; listener ownership scans procfs by process-group ID, cleanup
sends bounded TERM/KILL to the group, rejects surviving non-zombie members, and
requires the Chrome leader to be reaped before retry. The Go policy retains only
the outer Xvfb/Node group lifecycle. This does not change the Scope Manifest.

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

### Task 3: Candidate Conformance, Release, and Runtime Provenance

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
- screen dimensions, outer window dimensions, and inner width tolerate 2 px;
- inner height, header order, timing, WebGL, hardware/memory, cookies are
  informational;
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
It accepts either an explicit HTTPS origin or starts a Quick Tunnel from ADR
0002. Pin `cloudflared` `2026.7.1` Linux amd64 from the official GitHub release
with SHA-256
`79a0ade7fc854f62c1aaef48424d9d979e8c2fcd039189d24db82b84cd146be1`.
Reject any version/digest mismatch. The command owns a teardown trap, kills and
reaps the tunnel on success/signal/failure, and verifies the generated HTTPS
origin by fetching the run-correlated `/healthz` before launching Chrome. Tests
use a fake tunnel process to prove endpoint rejection and cleanup.

**Execution backport 2026-07-13:** Quick Tunnel hostname activation gets up to
three fresh two-minute attempts with full teardown between attempts; the CLI's
default overall timeout is twelve minutes so all attempts and runtime checks fit. Retry only
deadline/transient activation failure; origin or run-correlation rejection
remains fail-closed. This does not change the Scope Manifest.

**Execution backport 2026-07-15:** Chrome browser-owned warning UI can change
the content height asynchronously: the exact Chrome `150.0.7871.124` release
candidate produced direct/attached inner heights of 992/936 in CI and 936/936
locally while outer geometry and inner width remained equal. Keep screen
width/height, outer geometry, and inner width as 2 px stable gates; retain inner
height as informational evidence. Do not suppress browser security UI or add
identity overrides. Advance the unpublished correction to `v0.1.63` without
repointing `v0.1.62`. This does not change the Scope Manifest.

**Execution backport 2026-07-15 (IPv4 readiness):** release run
`29383045309` reached a healthy Quick Tunnel before its A record was visible to
the IPv4-only candidate container, so lifecycle navigation pinned the then-only
AAAA answer and failed with `ERR_ADDRESS_UNREACHABLE`. A count-only local probe
then found two A records through `1.1.1.1` after five seconds while the system
resolver still found none after thirty seconds. For auto-managed Quick Tunnel
runs, use a dedicated conformance health client that resolves through
bounded DNS-wire A queries to `1.1.1.1:53`, bypasses local hosts-file policy,
falls back to DNS TCP for truncated UDP responses, rejects non-public answers,
accepts A records only for the query owner or a bounded loop-free CNAME chain,
rejects valid declared DNS messages followed by trailing wire bytes,
rejects A/CNAME records whose declared RDATA contains unconsumed bytes,
pins a validated A record, and dials the origin directly over `tcp4` while
retaining the original TLS host; give
direct and attached candidate containers the same Docker DNS resolver only for
`trycloudflare.com` targets. Direct Chrome also waits for and pins its own
validated A answer. Explicit operator-owned origins retain their normal
transport, resolver, and temporary request-error retry behavior. The bounded
health retry then waits for candidate-reachable DNS before lifecycle validation
and reports resolver outages as a fixed redacted error without rotating healthy
tunnels. Classify malformed, mismatched, and persistently truncated DNS
responses as retryable resolver failures while preserving their protocol cause.
Retry those failures against the same managed tunnel, and on exhaustion return
the fixed resolver-unavailable classification without rotating the tunnel.
Apply the same terminal classification to exhausted A-record publication.
Preserve managed health and cleanup causes behind fixed redacted messages,
reject lookup or health-correlation success after cancellation or deadline,
and preserve TLS
verification with a fresh transport independent of process-global proxy, dial,
or TLS hooks;
preserve provider public-IP validation/pinning and generic provider IPv6-only
behavior. Advance the unpublished correction to `v0.1.64` without repointing
`v0.1.63`. This does not change the Scope Manifest.

**Execution backport 2026-07-15 (per-participant IPv4 readiness):** two exact
local auto-tunnel runs passed health, lifecycle, and direct navigation, then the
final attached provider received a fresh successful AAAA-only answer and failed
with `ERR_ADDRESS_UNREACHABLE`. A single readiness lookup cannot guarantee a
later independent lookup sees the same record family. For auto-managed
`trycloudflare.com` targets only, pass
`--browser-diagnostic-require-ipv4` to attached/lifecycle candidates and make
the provider retry successful AAAA-only answers within its existing bounded
DNS window before pinning Chrome. Explicit origins and generic provider
diagnostics do not pass the flag, preserving normal IPv6-only behavior. This
policy also redacts exact managed targets and hostnames from provider and
candidate failure text while preserving wrapped error causes. This does not
change the Scope Manifest or release target.

**Execution backport 2026-07-15 (managed health response boundary):** the
dedicated managed health client rejects redirects without issuing a second
request, preventing run-correlated URL disclosure through `Referer`. A single
classifier reads at most 4 KiB plus one overflow-detection byte and closes every
response status. It rejects oversized bodies and malformed trailing JSON while
preserving every observed read and close cause behind the fixed managed
classification for valid, rejected, and transient responses; cancellation adds
its context without exposing those causes. A non-public managed DNS answer is
terminal after one tunnel cleanup, with no health retry or tunnel rotation.
Managed origin redaction is case-insensitive and fails closed on invalid pattern
text in both conformance and provider paths, covering the full URL, host, path,
and standalone run identifier. Explicit operator clients retain their normal
redirect policy and error text. The ownership proof no longer uses an unrelated
short wall-clock deadline, and a trusted-certificate test proves that the public
IPv4 dial pin retains original-host SNI and TLS verification. This does not
change the Scope Manifest or release target.

**Execution backport 2026-07-16 (managed Cloudflare origin readiness):**
release run `29484605136` received HTTP 530 from the generated Quick Tunnel
about four seconds before `cloudflared` registered the tunnel connection.
Cloudflare [documents 530 as an origin DNS resolution failure](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-530/).
Treat that status as transient only for an auto-managed Quick Tunnel health check, within the
existing two-minute health deadline and three-attempt tunnel limit. Explicit
operator-owned origins retain terminal status handling, and persistent managed
530 responses still fail closed when the bounded deadline expires. Advance the
unpublished correction to `v0.1.65` without repointing `v0.1.64`. This does not
change the Scope Manifest.

**Execution receipt 2026-07-16:** two consecutive exact-source launches against
candidate image
`sha256:4836b1e159da8c8c3e2915440d61e56248f56780c96c09f4b5ef6660d8ec118d`
each used a fresh Quick Tunnel and returned schema `v1`, verdict `pass`, Chrome
`150.0.7871.124`, Playwright `1.57.0`, Xvfb `2:21.1.7-3+deb12u12`, 23 stable
comparisons, zero mismatches, and no residual candidate container or tunnel
process.

**Step 3: Add exact candidate runtime launch checks**

Set image `PRODUCT_CAPTURE_BROWSER_HEADLESS=false`. Build one amd64 image with
`load: true`; record Docker image ID and Chrome/Playwright/Xvfb versions; run:

```bash
go run ./cmd/browser-runtime-conformance --image "$CANDIDATE" --output conformance.json
```

Also terminate candidate containers during startup, navigation timeout, and
SIGTERM; assert no Chrome/Xvfb process or profile lock remains.

Expected: command exit 0 and JSON `.verdict == "pass"`.

**Step 4: Gate release publication on the exact tested image**

Change release workflow to `docker push` the already-loaded local tag without a
second build, resolve registry `@sha256:` digest, and include image ID, digest,
versions, and conformance artifact in the job summary. No OCI archive is sent to
workflow-compute. GoReleaser may create/update only a draft release before this
job; move `gh release edit --draft=false` and registry notification into a
`publish-release` job that needs both Go release build and runtime-image success.
If conformance fails, `v0.1.60` remains an unpublished draft with no registry
dispatch.

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

### Task 4: Product-Owned Staging Proof Without OCI Upload

**Files:**
- Create: `internal/stagingproof/proof.go`
- Create: `internal/stagingproof/proof_test.go`
- Create: `cmd/product-capture-staging-proof/main.go`
- Create: `.github/workflows/staging-proof.yml`
- Modify: `contracts/product-capture-provider.json`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `README.md`
- Modify: `docs/buymywishlist-live-usage.md`

**Step 1: Write failing proof-client tests**

Use compute-core `v0.8.4` and `httptest.Server` to require: exact digest-pinned
`provider_image_ref`; one matching online agent; zero active matching leases;
zero queued matching product tasks; task submission; terminal success; accepted
proof; bounded artifact download; and bounded summary fields. Reject
package/directive/campaign calls, undeclared artifacts, and artifacts over the
operation contract's limit.

Run: `GOWORK=off go test ./internal/stagingproof -count=1`

Expected: FAIL because the product-owned proof client does not exist.

**Step 2: Implement against generic workflow-compute APIs**

Use the released compute-core client methods from Task 1. The command takes server/token,
org/pool/product/policy, retained worker ID, product URL/host, and exact image
ref. Before dispatch it polls every 30 seconds for at most 30 minutes until the
named matching worker is online and idle with no queued matching task; it stores
only redacted counts/status/digest. It never builds, saves, chunks, or uploads a
container/package. It downloads at most 1 MiB for each JSON result and writes a
redacted summary with task/proof/artifact hash, runtime ref, title/image/price,
and optional accepted browser diagnostic artifact.

**Execution backport 2026-07-14:** A retained dynamic-provider worker's
executor image/rootfs digests attest its protected sandbox runtime, while the
provider image is independently digest-pinned in the submitted workload. The
capacity preflight validates the sandbox executor without equating those two
digests; accepted proof validation continues to require the exact executor
snapshot plus task and dependency-closure hashes that bind the provider image.
This does not change the Scope Manifest.

**Execution backport 2026-07-14 (network policy):** The BMW network product
canonicalizes an omitted task network mode to its admitted `direct` mode,
changing the immutable task binding returned by `POST /v1/tasks`. Provider
proof tasks now declare `direct` before submission; the regression fixture
reproduces server canonicalization. Evidence: staging run `29350736038` failed
with `submitted task response does not match requested provider task`. This
does not change the Scope Manifest.

**Step 3: Add staging workflow**

The workflow runs from `main`, validates image ref/URL/worker inputs, uses a
concurrency group with `cancel-in-progress: false`, executes
the Go command, and uploads only bounded JSON/log artifacts. Use the org-level
self-hosted Linux/X64 runner only if the public repo is allowed; otherwise use
`ubuntu-latest` for the control client while the registered retained compute
worker executes Chrome.

**Step 4: Declare and verify bounded provider results**

Change both operations from legacy artifact names to `artifact_specs`:

```json
{"name":"product_json","content_type":"application/json","max_bytes":1048576}
{"name":"browser_diagnostic_json","content_type":"application/json","max_bytes":1048576}
```

The command validates declared name/content/size and JSON before accepting
evidence. Lexical checks are supplementary only.

Run:
```bash
GOWORK=off go test ./internal/stagingproof ./cmd/product-capture-staging-proof -count=1
rg -n 'docker save|podman save|oci.tar|agent-artifacts|provider-package|campaign' .github/workflows/staging-proof.yml internal/stagingproof cmd/product-capture-staging-proof && exit 1 || true
actionlint .github/workflows/staging-proof.yml
golangci-lint run --new-from-rev=origin/main
```

Expected: tests/lint/actionlint PASS; wrong-name/type, oversized, invalid JSON,
and product-schema-invalid fixtures are rejected; forbidden search returns no matches.

**Step 5: Commit**

```bash
git add internal/stagingproof cmd/product-capture-staging-proof .github/workflows/staging-proof.yml contracts go.mod go.sum README.md docs
git commit -m "feat: own product capture staging proof"
```

Rollback: retain the prior proof workflow until this workflow has one accepted staging run; never restore OCI upload after cutover.

### Task 5: Enforce Generic Provider Result Artifact Contracts

**Repository:** `GoCodeAlone/workflow-compute`

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/server/server.go`
- Modify: relevant lease/contract tests in `internal/server/`
- Modify: `internal/agent/worker.go`
- Modify: `internal/agent/worker_test.go`
- Modify: relevant task-artifact upload tests in `internal/server/`

**Step 1: Write failing contract-enforcement tests**

Require the server to resolve the registered provider operation and copy its
normalized `artifact_specs` into the lease. Agent tests reject undeclared names,
wrong declared content type, size `max_bytes+1`, and invalid JSON before upload.
Server tests repeat the same checks before storage so a modified/old agent cannot
bypass policy. Generic JSON validity means syntactic JSON only; product output
schema validation remains in product-capture Task 4. Add failing producer tests
requiring canonical `artifact://<pool>/tasks/<task>/proofs/<proof>/artifacts/<name>`
refs for simple and nested names, normalization of existing legacy stored refs,
and a real workflow-compute handler round trip through the compute-core client.

Run: `go test ./internal/agent ./internal/server -run 'Provider.*Artifact|TaskArtifact.*Policy' -count=1`

Expected: FAIL because leases do not carry specs and upload paths do not enforce them.

**Step 2: Upgrade the shared protocol**

Update workflow-compute from core `v0.7.0` to `v0.8.4`. Run `go mod tidy` and a
version-skew audit proving product-capture and workflow-compute use the exact
same lease/artifact schema version.

**Step 3: Implement dual enforcement**

At lease creation, derive specs from the server-owned registered provider
contract; ignore/reject any submitter attempt to supply policy. In the worker,
bound `stat`/read before upload and set the declared content type/retention. In
`handleTaskArtifactUpload`, resolve the same task/operation/spec, use a bounded
reader, validate declared name/type/size and JSON syntax, then store. Keep
`/v1/agent-artifacts/` update packages separate and unchanged. Canonicalize
public/listed refs from trusted task/proof/name metadata with the explicit
`/artifacts/` marker; continue reading legacy stored metadata while emitting only
canonical refs. Preserve nested artifact names by adding the route marker rather
than treating a leading `artifacts/` name segment as the marker.

**Step 4: Verify and deploy staging**

Run:
```bash
go test ./internal/agent ./internal/server -run 'Provider.*Artifact|TaskArtifact.*Policy' -count=1
go test ./...
golangci-lint run --new-from-rev=origin/main
```

After merge, monitor the existing workflow-compute staging deploy for the merge
SHA. Against staging, submit one valid bounded JSON result and adversarial
wrong-name, wrong-type, invalid-JSON, and max+1-byte probes. Expected: valid
artifact stored; rejects return bounded 4xx (413 for max+1) and storage listing
contains no rejected artifact. The released compute-core client must list and
download the accepted artifact through the real staging routes using the exact
canonical ref returned by the server.

**Step 5: Commit**

```bash
git add go.mod go.sum internal/agent internal/server
git commit -m "fix: enforce provider result artifact specs"
```

Rollback: revert the server/agent commit, pin core back to `v0.7.0`, and redeploy staging; do not run product proof cutover until enforcement is restored.

### Task 6: Remove Workflow-Compute Product-Specific Proof and Runtime Logic

**Repository:** `GoCodeAlone/workflow-compute`

**Files:**
- Modify: `go.mod` and `go.sum` only if product-specific removal leaves an unused dependency
- Delete: `scripts/staging-product-capture-proof.sh`
- Delete: `.github/workflows/staging-product-capture-proof.yml`
- Delete: `staging_product_capture_proof_test.go`
- Delete: `internal/executor/external_provider.go`
- Delete: `internal/executor/external_provider_test.go`
- Modify: `ci_workflows_test.go`
- Modify: `scripts/agent-runner-lifecycle-proof.sh`
- Modify: `scripts/agent-runner-lifecycle-proof.ps1`
- Modify: `.github/workflows/manual-agent-runner-proof.yml`
- Modify: `proof_scripts_test.go`
- Modify: `pkg/protocol/types.go`
- Modify: `pkg/protocol/types_test.go`
- Modify: `internal/executor/executor.go`
- Modify: `internal/executor/runtime_adapter_plugin_test.go`
- Modify: `internal/executor/service_runtime.go` and tests if the inventory finds a legacy branch
- Modify: `cmd/compute-agent/main.go`
- Modify: `cmd/compute-agent/main_test.go`
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/agent/process_supervisor_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/api_test.go`
- Modify: all runner/workflow tests/docs found by the inventory command below

**Step 1: Capture the failing ownership invariant**

First run and save the complete inventory:

```bash
rg -n 'product.?capture|staging-product-capture-proof|PRODUCT_CAPTURE_DELEGATE_PROOF|product-capture\.oci\.tar' .github scripts docs
rg -n 'WorkloadProductCapture|ProductCaptureWorkload|ProductCaptureBrowserProvider' cmd internal pkg --glob '*.go'
rg -n 'product.?capture|staging-product-capture-proof|PRODUCT_CAPTURE_DELEGATE_PROOF|product-capture\.oci\.tar' . --glob '*_test.go'
```

Add/adjust repository tests that fail when workflow-compute contains a
product-capture-specific staging workflow, OCI packaging/upload, or delegated
product proof orchestration in the staging workflow, manual runner workflow,
shell/PowerShell runner scripts, or their tests. Also fail while production Go
code exposes the legacy `WorkloadProductCapture`, `ProductCaptureWorkload`, or
`ProductCaptureBrowserProvider` path. Preserve generic dynamic-provider APIs,
generic product-domain enrollment/policy, and the already-deployed Task 5
artifact enforcement.

Run: `go test ./... -run 'ProductCapture.*Ownership|CIWorkflows' -count=1`

Expected: FAIL on current script/workflow references.

**Step 2: Remove product-owned orchestration and legacy runtime**

Delete the dedicated proof workflow/script and remove or genericize product
inputs/IDs/component refs/artifact names from `manual-agent-runner-proof.yml`,
runner delegation/import code, `proof_scripts_test.go`, and `ci_workflows_test.go`.
Delete the product-specific executor/adapter and its tests. Remove the legacy
workload kind, payload field/type, validation branch, scheduler capability
exception, and command/agent construction branches; migrate retained callers
and tests to `WorkloadProvider` plus `DynamicProvider`. Keep generic provider
workload validation, agent enrollment, image digest enforcement, proof
acceptance, and bounded artifact APIs. Run `go mod tidy`; retain
`workflow-plugin-compute-wasm` if generic WASM/runtime adapters still import it.

**Step 3: Verify cutover and generic behavior**

Run:
```bash
if rg -n 'staging-product-capture-proof|product-capture-browser\.oci\.tar|PRODUCT_CAPTURE_DELEGATE_PROOF' .github scripts docs; then exit 1; fi
if rg -n 'staging-product-capture-proof|product-capture-browser\.oci\.tar|PRODUCT_CAPTURE_DELEGATE_PROOF' . --glob '*_test.go'; then exit 1; fi
if rg -n 'WorkloadProductCapture|ProductCaptureWorkload|ProductCaptureBrowserProvider' cmd internal pkg --glob '*.go'; then exit 1; fi
go test ./internal/executor -run 'DynamicProvider.*(Runtime|OCI|Artifact)' -count=1
go test ./cmd/compute-agent ./internal/agent ./internal/server ./pkg/protocol -count=1
go test ./...
go build ./cmd/...
golangci-lint run --new-from-rev=origin/main
```

Expected: forbidden searches empty; generic dynamic-provider lifecycle tests,
all tests, command builds, and lint PASS. After merge, deploy staging and run one
generic `WorkloadProvider` smoke; its accepted proof/artifact remains readable
through the Task 1 bounded client APIs.

**Step 4: Commit**

```bash
git add -A go.mod go.sum cmd internal pkg scripts .github/workflows staging_product_capture_proof_test.go proof_scripts_test.go ci_workflows_test.go docs
git commit -m "refactor: remove product capture proof ownership"
```

Rollback: revert this deletion and redeploy only if the generic provider smoke
fails; keep Task 5 enforcement deployed and do not restore OCI upload as a
long-term path.

### Task 7: BMW Runtime Provenance and Staging Binding

**Repository:** `GoCodeAlone/buymywishlist`

**Files:**
- Modify: `.wfctl-lock.yaml`
- Modify: `app.yaml`
- Modify: `bmwplugin/integration_user_test.go`
- Modify: `bmwplugin/product_capture_backend_test.go`
- Modify: `e2e/tests/staging-product-capture-commerce.spec.ts`
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

Before PR 5 is admin-merged, dispatch both existing webhook workflows with
`environment=staging,mode=ensure`; resolve each new run ID and require
`gh run watch <id> --exit-status` success. Only then set GitHub staging variables
`PRODUCT_CAPTURE_COMPUTE_IMAGE_REF=<candidate@sha256:...>`. Merge only after
variable readback is exact. The merge's existing CI success triggers `deploy.yml` through
`workflow_run`; monitor it and require its main SHA, start time after both
webhook runs, deploy summary, and DigitalOcean active-revision env keys/ref to
show both webhook secret bindings, zero dispatch delay, and the candidate ref.
If PR 5 does not merge, restore the prior image-ref variable.

**Step 4: Verify**

Run:
```bash
wfctl plugin install --locked
wfctl validate --strict --skip-unknown-types app.yaml
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

### Task 8: Crash-Safe Test-Mode Issuing Proof

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

### Task 9: Full BMW Staging Commerce Validation

**Repository:** `GoCodeAlone/buymywishlist`

**Files:**
- Modify: `e2e/tests/staging-product-capture-commerce.spec.ts`
- Modify: `.github/workflows/staging-commerce-product-capture-proof.yml`
- Modify: `app.yaml` admin contribution projection and readiness delay
- Modify: `infra.yaml`
- Modify: `bmwplugin/integration_admin_test.go`
- Modify: `bmwplugin/integration_cron_test.go`
- Create: `scripts/staging_commerce_workflow_test.go`

**Step 1: Write failing E2E contract assertions**

Require, in order: owner/wishlist/item; real Amazon title/image/positive price;
accepted task/proof/artifact + exact candidate runtime; contributor one partial;
contributor two completes; admin projection proves two distinct contributor IDs,
contribution IDs, and PaymentIntent IDs; item and wishlist funded; fulfillment
created by the real readiness cron/dispatcher and claimed; test-mode `ic_` +
`livemode=false`; abort in `finally`; no submit call. Sensitive endpoint
failures log status/request ID only.

Run: `cd e2e && npx playwright test tests/staging-product-capture-commerce.spec.ts --project api --list`

Expected before implementation: static/Go contract tests fail on missing fields/cleanup.

**Step 2: Expose protected contribution linkage**

Add `contributor_id` to the authenticated admin item-contributions projection;
keep public contribution responses unchanged. Add authz/integration assertions.

**Step 3: Make staging dispatch and webhook gates executable**

Add `fulfillment.dispatch_delay_seconds`/
`FULFILLMENT_DISPATCH_DELAY_SECONDS` with default `604800`. Parameterize the
readiness query using `make_interval(secs => $1::int)` and a bound config value;
set only staging infra to `0`, keep production/default at seven days, and test
both values plus the real cron-to-dispatcher transition. Render staging and
production infra plans and assert their app env values are `0` and `604800`
respectively. Fail the proof before
contributions unless staging publishable/secret key modes are test, both
pre-deploy webhook `ensure` run IDs succeeded, and the active deployment started
after them with both secret env keys. Add required workflow inputs for the two
webhook run IDs, deploy run ID, and expected deployed SHA; query Actions run
metadata to enforce exact workflow name, `workflow_dispatch` event for webhook
runs, success conclusion, deploy SHA, and completion/start ordering rather than
trusting caller booleans. Grant workflow `actions: read`; expose
`GH_TOKEN: ${{ github.token }}` only to this metadata-preflight step. Add a Go
workflow contract test for permissions, token scope, exact-ID queries, and
negative metadata cases. The preflight also requires `${{ github.sha }}` to equal
the expected/deployed SHA. Pass candidate ref and proof run ID; upload only
redacted IDs/booleans.

**Step 4: Local verification**

Run:
```bash
go test ./bmwplugin -run 'Admin.*Contribution|Issuing|ProductCapture|Cron.*Dispatch' -count=1
go test ./scripts -run 'StagingCommerce.*(Permissions|RunProvenance)' -count=1
cd e2e && npm ci && npx playwright test tests/staging-product-capture-commerce.spec.ts --project api --list
actionlint .github/workflows/staging-commerce-product-capture-proof.yml
wfctl infra plan --env staging -c infra.yaml --output /tmp/bmw-staging-plan.json
wfctl infra plan --env prod -c infra.yaml --output /tmp/bmw-prod-plan.json
golangci-lint run --new-from-rev=origin/main
```

Expected: tests/actionlint/lint PASS; one E2E test listed; rendered staging app
env contains `FULFILLMENT_DISPATCH_DELAY_SECONDS=0` and prod contains `604800`.

**Step 5: Commit and publish PR 5**

```bash
git add app.yaml infra.yaml bmwplugin scripts e2e .github/workflows
git commit -m "test: prove staging capture commerce round trip"
```

**Step 6: Deploy and run real staging proof**

After PRs are green/merged, `v0.1.60` is published, both pre-deploy webhook runs
succeeded, and the later PR 5 deployment is active:
```bash
git fetch origin main
test "$(git rev-parse origin/main)" = "$DEPLOYED_MAIN_SHA"
gh workflow run staging-commerce-product-capture-proof.yml \
  --ref main \
  -f product_url=https://www.amazon.com/dp/B08N5WRWNW \
  -f payments_webhook_run_id="$PAYMENTS_WEBHOOK_RUN_ID" \
  -f issuing_webhook_run_id="$ISSUING_WEBHOOK_RUN_ID" \
  -f deploy_run_id="$DEPLOY_RUN_ID" \
  -f expected_sha="$DEPLOYED_MAIN_SHA"
```

Before the commerce dispatch, use `gh run list/view/watch` to require the
existing `deploy.yml` `workflow_run` for PR 5's merged main SHA to succeed and
to have started after both successful webhook runs. Poll GitHub runner state and
compute-core capacity APIs only; no SSH. The
product-owned preflight waits up to 30 minutes for one matching idle worker,
zero active matching leases, and zero queued matching tasks before submission.
Immediately before dispatch, fetch `origin/main` and require its SHA to equal
`DEPLOYED_MAIN_SHA`; after dispatch, resolve the new commerce-proof run ID and
require its `headSha` to equal the same value before accepting evidence. If main
advanced, fail closed, deploy that main SHA, and restart the prerequisite/proof
sequence.

Expected redacted evidence:
- capture task/proof accepted; exact candidate digest; title/image/price present;
- two distinct `pi_` + contributor/contribution IDs; partial then funded states;
- one `ic_`, `livemode=false`, then canceled by abort/reaper;
- no raw client secret, ephemeral key, PAN/CVC, or fake retailer order.

Rollback: restore previous staging image ref, redeploy, cancel any proof-tagged test cards, and rerun staging health checks.
