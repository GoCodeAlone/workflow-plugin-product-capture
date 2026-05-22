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

## Worker runtime image

Provider workloads must use a digest-pinned browser runtime image. Tagged
releases publish:

```text
ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser:<tag>
```

Use the published digest from the release workflow summary when configuring
workflow-compute:

```text
ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:<digest>
```

The image contains Node, the Playwright package, and Google Chrome. It sets
`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` so Playwright uses the installed Chrome
channel rather than downloading bundled Chromium.
