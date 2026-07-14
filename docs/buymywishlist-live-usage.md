# BuyMyWishlist Live Usage

`workflow-plugin-product-capture` lets BuyMyWishlist submit product URLs to a
wfcompute deployment through a generic provider workload. BuyMyWishlist should
own the user-facing wishlist/product workflow; wfcompute owns task admission,
lease placement, agent execution, proof verification, and artifact retention.

## wfcompute Prerequisites

Before BuyMyWishlist enables live capture, the target wfcompute environment must
have:

- provider contract `product-capture.browser.v1` registered from this plugin;
- a network product such as `bmw-product-capture` whose provider config points
  at `workflow-plugin-product-capture` provider `browser`;
- a promoted provider package or runtime image available to agents, with the
  expected component ref and digest recorded for submissions;
- at least one online agent advertising executor provider
  `product-capture-browser`, workload kind `provider`, execution tier
  `sandboxed-container`, and proof tier `artifact-hash`;
- supported runtime backend evidence for the agent runtime that will execute the
  browser provider. A degraded or unsupported runtime backend must disable live
  capture rather than fall back to trusted-native execution;
- a scoped task token for BuyMyWishlist. Do not use a dashboard admin,
  bootstrap, or operator token from the application.

Retained `product-capture-browser` workers may set
`PRODUCT_CAPTURE_BROWSER_PROFILE_DIR` to an agent-local directory when anonymous
Chrome state should persist between captures. This can reduce repeated Amazon
continuation gates after a successful benign pass-through. It must not reference
a logged-in Amazon or operator browser profile, and operators can reset the
capture identity by deleting the directory.

If Amazon starts returning interstitials or CAPTCHA pages, first run a browser
diagnostic from the same staging worker/runtime before changing capture
behavior:

```sh
PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=https://<diagnostic-host> \
  product-capture-provider \
  --browser-diagnostic-url https://<diagnostic-host>/product-capture-browser
```

The endpoint should log the first navigation request, the diagnostic POST
request, request headers, TLS/client metadata, remote IP/ASN, and the posted
browser-signal JSON. The provider output and POST body intentionally report only
cookie presence and length, never cookie values. Compare the staging worker
result against a normal Chrome visit and a local Playwright run so mismatched
client hints, platform, headless/browser surfaces, locale/timezone, WebGL, and
network-origin signals are visible before making further capture changes.

The deployment is not live-ready until a BMW-shaped provider task returns an
accepted proof from a `product-capture-browser` agent in the target wfcompute
environment.

Agent installation should come from a wfcompute setup invite or the projectless
`wfctl plugin run --ensure-installed workflow-plugin-compute -- compute agent
setup ...` path. BuyMyWishlist operators should not guess worker IDs, org IDs,
pool IDs, or agent tokens; those values are issued by the wfcompute control
plane and embedded in the invite/setup flow.

## Current Release Target

Use `workflow-plugin-product-capture` `v0.1.61` for the next BuyMyWishlist
live-incorporation pass. Downstream workflow-compute scenarios, staging proof
defaults, and BuyMyWishlist plugin pins should all reference this tag once its
release workflow has published the matching browser runtime image digest.

Do not copy a digest from this source tree. The digest that matters is the
GHCR `product-capture-browser@sha256:<digest>` emitted by the `v0.1.61`
release workflow. For component-backed deployments, use the wfcompute package
campaign's promoted component digest for
`provider://workflow-plugin-product-capture/browser/runtime`.

## Product-Owned Staging Proof

After the `v0.1.61` release publishes its browser image, dispatch
`.github/workflows/staging-proof.yml` from `main`. Supply the exact released
image reference, the registered retained staging worker ID, and a real Amazon
product URL. Store a credential scoped to `agent:read`, `task:read`, and
`task:write` as the
`WORKFLOW_COMPUTE_TASK_TOKEN` secret in the `workflow-compute-staging` GitHub
environment; do not substitute an admin or bootstrap credential.

The proof waits up to 30 minutes for exactly one compatible online worker and
requires that worker to be the named retained worker, idle, and matched to the
candidate image digest. It also requires no queued matching product task before
dispatch. The control client then uses only generic workflow-compute task,
proof, capacity, and artifact APIs. The candidate runtime is never transferred
as proof input or evidence.

The accepted `product_json` result must match the provider contract's name,
`application/json` content type, 1 MiB size limit, canonical artifact reference,
SHA-256, JSON syntax, and product schema. The redacted summary requires title,
image URL, and the canonical decimal USD price. An optional controlled HTTPS
diagnostic URL causes a second `browser_diagnostic` task; its accepted bounded
JSON artifact is recorded
by task/proof/artifact identifiers, artifact digest, and pinned diagnostic
schema digest without copying raw browser signals into the summary.

Keep the prior staging proof available only until this replacement records one
accepted staging run. After that cutover, use this workflow for product runtime
readiness evidence.

## Verified wfcompute Staging Baseline

The historical accepted wfcompute staging proof completed on 2026-06-07 against:

- wfcompute server:
  `https://workflow-compute-staging-ocysa.ondigitalocean.app`;
- workflow-compute commit:
  `560f04a4911afbecca502f6bca228a3bb3f8c84f`;
- plugin release/runtime image:
  `workflow-plugin-product-capture` `v0.1.17`,
  `ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:e73cc41e3a1deb0e886ad157111f3099b3214cbcf63257dc8d72a7a19c23b1f4`;
- promoted provider component:
  `provider://workflow-plugin-product-capture/browser/runtime` with
  component digest
  `sha256:dd98a14d05ef03f2372f3b1ad9ad16a217b3c8ec9c9dcf3ee15c71616c3595d0`;
- package metadata version:
  `v0.1.17-staging.27096063947.1`;
- task/proof:
  `task-product-capture-1780844446-4311` and
  `proof-task-product-capture-1780844446-4311-product-capture-staging-worker-1780844446-4311`;
- verifier result:
  `signed_receipt` with status `accepted`.

These staging IDs are evidence, not production configuration. BuyMyWishlist
should use the target wfcompute environment's current package/component digest,
network product id, and scoped task credential when enabling live traffic.

A later T544 runtime compatibility proof on 2026-06-11 used wfcompute commit
`80ad80dfc80dd21052fd73184baf5ce3c119097f` and confirmed the current provider
envelope and runtime precondition behavior. That run reported `status:
skipped`, `structured_runtime_precondition: true`, and no supported backend
because the runner's podman/docker candidates failed conformance and nerdctl was
unavailable. Treat that as correct fail-closed behavior, not live-readiness
evidence.

## Workflow Step

Use `step.product_capture` with a secret reference for the scoped wfcompute
token:

```yaml
steps:
  - id: capture_product
    type: step.product_capture
    config:
      server_url: https://<wfcompute-host>
      auth_token_ref: secret:wfcompute_product_capture_token
      product_id: bmw-product-capture
      org_id: <org-id>
      pool_id: <pool-id>
      policy_id: <policy-id>
      timeout_seconds: 120
      url_field: product_url
      allowed_hosts:
        - www.amazon.com
        - amazon.com
      provider_component_ref: provider://workflow-plugin-product-capture/browser/runtime
      provider_component_digest: sha256:<promoted-runtime-digest>
      capture_timeout_seconds: 60
      max_html_bytes: 1048576
      max_image_count: 8
      poll_interval: 2s
      wait_timeout: 5m
```

The step submits a generic `provider` workload with operation
`capture_product`. It does not call a product-capture-specific wfcompute API.
Use `provider_image_ref` only for a compatibility deployment that has not
promoted provider components to agents:

```yaml
provider_image_ref: ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:<digest>
```

Do not set `provider_image_ref` together with `provider_component_ref` or
`provider_component_digest`.

wfcompute may include additive compute-core runtime metadata in the provider
envelope: `workload_kind`, `executor`, `runtime_profile`, `runtime_backend`,
`env`, and `limits`. The provider validates that this metadata still describes
`product-capture-browser` running as `sandboxed-container` with `artifact-hash`
proofs on a supported backend. The nested operation input stays schema-strict
and rejects demo-only fields such as mock HTML, fixture paths, or demo product
IDs.

## Application Handling

BuyMyWishlist should treat the proof preview as user-confirmation data, not as a
silent purchase instruction. Expected fields include `title`, `requested_url`,
`canonical_url`, `external_id`, `variant`, `variant_dimensions`,
`variant_key`, `price`, `currency`, `seller`, `ships_from`,
`shipping_summary`, `image_url`, `images`, `availability`, and
`requires_user_confirmation`. `image_url` and `images` are URL strings only, not
binary image payloads.

If `variant_key` starts with `exact-url-sha256:`, Amazon did not expose selected
variant dimensions reliably. BuyMyWishlist should keep
`requires_user_confirmation` true and must not reuse the snapshot as a
cross-request cache entry for other submitted URLs or other wishlist tenants.

The app should persist the wfcompute `task_id`, `proof_id`, artifact hash, and
selected preview fields with the wishlist item. It should not store raw HTML,
provider cookies, wfcompute admin credentials, browser runtime paths, or
operator-only artifacts.

## Failure Handling

- If the step returns `error`, keep the wishlist item in a user-actionable
  review state.
- If no accepted proof arrives before `wait_timeout`, retry by submitting a new
  task rather than mutating the old task.
- If wfcompute reports no compatible agent capacity, keep capture disabled for
  live traffic until the provider package and `product-capture-browser` agents
  are promoted again.
- If the product URL host is outside `allowed_hosts`, reject it in
  BuyMyWishlist before submission.
