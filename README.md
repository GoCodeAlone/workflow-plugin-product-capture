# workflow-plugin-product-capture

Workflow plugin and workflow-compute provider package for user-submitted product
URL capture.

The provider validates a supported product URL, loads the page with a normal
browser runtime supplied by the worker image, extracts a bounded product
snapshot, and returns provenance-marked data for user confirmation.

Amazon snapshots include title, ASIN, canonical URL, representative images,
requested URL, variant dimensions when Amazon exposes them, a variant-safe
cache key, availability, price when present, seller, ships-from party, shipping
summary, shipping price when Amazon exposes it, estimated product-plus-shipping
total, and a nullable `prime_eligible` flag. `image_url` and `images` are URL
strings only, not binary image payloads. Unavailable products are still valid
snapshots; they normally omit price and Prime status while preserving
`availability`.

When selected variant dimensions are unavailable, the provider sets
`variant_key` to an `exact-url-sha256:` value and leaves
`requires_user_confirmation` true. Consumers must not promote that snapshot as a
cross-request cache entry for the same ASIN.

## Commands

```sh
GOWORK=off go test ./...
GOWORK=off go run ./cmd/product-capture-provider --probe
PRODUCT_CAPTURE_HTML_FIXTURE=internal/snapshot/testdata/amazon_xbox.html \
  GOWORK=off go run ./cmd/product-capture-provider \
  --request internal/provider/testdata/request-amazon.json \
  --output /tmp/snapshot.json
```

Live browser capture requires `node`, Playwright, and an installed
`google-chrome` executable in the worker image. On Linux, the provider starts a
stable process-group supervisor as Node's direct child and verifies the Chrome
child separately by PID and start time. Playwright attaches to Chrome's default
context over loopback CDP, keeping browser launch identity under Chrome's
control. Local development can set `NODE_PATH` to a Playwright install; fixture
mode is used by unit tests and never emits raw HTML in the provider response.

Generated Playwright script regressions execute with `node` when it is present.
CI provisions Node explicitly so those regressions are always exercised there.

## Worker runtime

Provider workloads should use the promoted component runtime that wfcompute
distributes to capable agents:

```text
provider://workflow-plugin-product-capture/browser/runtime
```

Set `provider_component_digest` to the promoted runtime payload digest recorded
by the wfcompute package campaign. The digest binds the submitted workload to
the exact agent-local runtime payload.

Digest-pinned image refs remain supported for deployments that have not adopted
promoted provider components. Tagged releases publish:

```text
ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser:<tag>
```

Use the published image digest from the release workflow summary when
configuring an image-backed workflow-compute deployment:

```text
ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:<digest>
```

The image contains Node, the Playwright package, and Google Chrome. It sets
`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`; Playwright controls the installed Chrome
child over CDP rather than downloading or launching bundled Chromium.

On Linux, set `PRODUCT_CAPTURE_BROWSER_PROFILE_DIR` to a new private directory
on a retained provider worker when anonymous Chrome state should survive
between captures. Also set `PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE` to a stable,
trusted deployment identity containing the org, pool, provider enrollment, and
an explicit rotation version, for example
`org-1/pool-1/bmw-product-capture:v1`. This lets non-login Amazon friction
cookies persist after benign continuation gates without sharing state across
worker trust domains. The provider binds its ownership identity to a hash of
that scope and refuses preexisting unmanaged, credentialed, symlinked,
foreign-owned, non-private, or differently scoped profiles. Delete the
directory and increment the scope version to rotate the capture identity. An OS
lock serializes captures for the full child-process lifetime. A durable launch
journal bound to the profile generation, scope, and inherited lock authorizes
recovery if Chrome exits before publishing its owner record. After owner
publication, that bound owner identity authorizes cleanup only when the exact
same-host Chrome process is gone. Live identity mismatches and otherwise
unknown lock states fail closed.
Persistent profiles are intentionally unsupported on other operating systems
until equivalent process identity validation is available.

Amazon browser captures canonicalize supported `amazon.com` aliases to
`https://www.amazon.com`, load that origin's `/` homepage, and then navigate to
the canonical `/dp/<ASIN>` product URL. A supplied `warmup_url` must identify
the same canonical HTTPS root homepage; other paths are rejected. The default
browser viewport is `1920x1080`; operators can override it with
`PRODUCT_CAPTURE_BROWSER_VIEWPORT=<width>x<height>` within the supported
desktop range. Standalone capture remains headless by default. Set
`PRODUCT_CAPTURE_BROWSER_HEADLESS=false` for headed operation; when `DISPLAY`
is unset, the provider starts and reaps a dedicated Xvfb display with a
`1920x1080x24` virtual screen.

Capture inputs are canonicalized to an HTTPS Amazon `/dp/<ASIN>` path before
navigation while retaining the submitted query and fragment on the normalized
navigation URL and `requested_url`. A trusted page canonical is reduced to the
same product path only after its HTTPS host and ASIN are validated. Cross-origin
main-frame navigation and literal private/link-local IP requests are blocked in
the browser. The sandbox runtime must additionally deny private, link-local,
cloud metadata, and workflow-control-plane egress at the container/network
boundary so DNS resolution cannot bypass the browser-layer check.

## Browser diagnostics

Operators can run the provider binary against a controlled diagnostic endpoint
to inspect the browser identity used by live capture without weakening the
normal Amazon-only workload validation:

```sh
export PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=https://<diagnostic-host>
product-capture-provider \
  --browser-diagnostic-url https://<diagnostic-host>/product-capture-browser
```

Diagnostics are disabled when the allowlist variable is unset. It must contain
one exact HTTPS origin. The provider rejects non-public DNS answers, pins one
validated address for the diagnostic Chrome child, aborts cross-origin HTTP(S)
requests and redirects, and always uses a temporary profile even when retained
capture state is configured. The diagnostic uses the same native Chrome launch
path as product capture, collects bounded browser-side signals, posts them to
the allowed origin as JSON, and prints the same JSON to stdout. It reports cookie
presence and length only; it does not emit cookie values.

The diagnostic endpoint should log request headers, TLS/client metadata, remote
IP/ASN, and the POST body. Compare that output with a normal Chrome visit before
changing capture behavior. The automation comparison checks only
`window.__playwright__binding__` and `window.__pwInitScripts`; their absence is
limited comparative evidence, not proof that no automation context is
detectable. Do not run it from a credentialed shopping profile.

Release candidates use the repository-owned conformance command to compare a
direct headed Chrome visit with the real provider diagnostic from the same
loaded amd64 image:

```sh
go run ./cmd/browser-runtime-conformance \
  --image product-capture:v0.1.66 \
  --output /tmp/product-capture-conformance.json
```

The command serves a run-correlated, bounded self-reporting endpoint and uses
the checksum-pinned ephemeral Quick Tunnel described in
[`decisions/0002-use-ephemeral-diagnostic-tunnel.md`](decisions/0002-use-ephemeral-diagnostic-tunnel.md).
Pass `--origin` and `--listen` together when an operator-managed HTTPS reverse
proxy is available. Shared JSON contains only versions, stable comparisons,
informational values, and the verdict; the run ID and endpoint host are redacted.

Release conformance also uses a fresh anonymous managed profile with a stable
hostname and profile scope. Its guarded conformance mode pauses Chrome and the
real provider after verifying the launch journal and `SingletonLock`, but before
owner publication. A read-only, networkless probe confirms that exact boundary
before the harness kills the container and requires recovery.
The controlled endpoint then sets one run-specific, host-only, secure,
HTTP-only, non-authentication cookie. A read-only, networkless probe waits for
its metadata row to become durable without reading cookie values; the harness
kills that provider with `SIGKILL`, verifies exit 137, and requires the next
provider to return the exact cookie. Five booleans record the two crashes and
recovery boundaries, and the named volume is deleted afterward. This CLI-only
check does not change ordinary diagnostics, which continue to use temporary
profiles, and it does not permit credentialed shopping sessions.

## Workflow step

Use `step.product_capture` when a Workflow app needs to submit a product URL to
workflow-compute. The step owns product-capture-specific validation and submits
a generic provider workload using the `product-capture.browser.v1` contract.
Configure either `provider_component_ref` plus `provider_component_digest`, or
the compatibility `provider_image_ref`. `workflow-plugin-compute` should only
provide generic dispatch/wait/catalog plumbing.

The provider binary accepts the current wfcompute dynamic-provider envelope and
additive compute-core runtime metadata for the selected executor, provider
runtime profile, supported runtime backend report, environment, and limits.
Operation input remains strict: live submissions use product URL, allowed host,
capture bounds, and capture mode fields only; demo or fixture fields are
rejected.

BuyMyWishlist live wiring details are in
[`docs/buymywishlist-live-usage.md`](docs/buymywishlist-live-usage.md).

## Staging proof

The `Product Capture Staging Proof` workflow owns the live provider proof for
this plugin. Dispatch it from `main` with the exact released
`product-capture-browser@sha256:<digest>` reference, the retained staging worker
ID, and a real product URL. The workflow uses a staging-environment scoped task
token with `agent:read`, `task:read`, and `task:write` and runs only the control
client on its GitHub-hosted runner; browser
execution remains on the retained workflow-compute worker.

Before submitting, the client requires that worker to be the sole online agent
matching the candidate digest and provider capabilities, with no active lease
or queued matching product task. It then submits generic provider operations,
requires terminal success and an accepted proof from that worker, and downloads
only contract-declared JSON results. Both `product_json` and
`browser_diagnostic_json` are limited to 1 MiB. Name, content type, size,
canonical reference, SHA-256, JSON syntax, and the product schema are checked
before evidence is accepted. Product evidence requires the provider's canonical
decimal USD price. Diagnostic evidence is validated against a pinned artifact
schema digest, which is included in the redacted summary.

The workflow artifact contains only a redacted summary JSON and a log capped at
64 KiB. The runtime image is pulled by the retained worker from its digest-pinned
reference; it is not copied into proof evidence.

Terminal step output includes `provider_image_ref`, `provider_component_ref`,
and `provider_component_digest` copied from the submitted provider workload.
Consumers should persist those fields with the task/proof identifiers and
artifact hash so a captured item remains bound to the selected runtime.
