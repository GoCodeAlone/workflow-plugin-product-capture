# workflow-plugin-product-capture

Workflow plugin and workflow-compute provider package for user-submitted product
URL capture.

The provider validates a supported product URL, loads the page with a normal
browser runtime supplied by the worker image, extracts a bounded product
snapshot, and returns provenance-marked data for user confirmation.

Amazon snapshots include title, ASIN, canonical URL, representative images,
availability, price when present, seller, ships-from party, shipping summary,
shipping price when Amazon exposes it, estimated product-plus-shipping total,
and a nullable `prime_eligible` flag. Unavailable products are still valid
snapshots; they normally omit price and Prime status while preserving
`availability`.

## Commands

```sh
GOWORK=off go test ./...
GOWORK=off go run ./cmd/product-capture-provider --probe
PRODUCT_CAPTURE_HTML_FIXTURE=internal/snapshot/testdata/amazon_xbox.html \
  GOWORK=off go run ./cmd/product-capture-provider \
  --request internal/provider/testdata/request-amazon.json \
  --output /tmp/snapshot.json
```

Live browser capture requires `node` plus Playwright in the worker image. Local
development can set `NODE_PATH` to a Playwright install; fixture mode is used by
unit tests and never emits raw HTML in the provider response.

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
`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` so Playwright uses the installed Chrome
channel rather than downloading bundled Chromium.

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
