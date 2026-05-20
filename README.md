# workflow-plugin-product-capture

Workflow plugin and workflow-compute provider package for user-submitted product
URL capture.

The provider validates a supported product URL, loads the page with a normal
browser runtime supplied by the worker image, extracts a bounded product
snapshot, and returns provenance-marked data for user confirmation.

Amazon snapshots include title, ASIN, canonical URL, representative images,
availability, price when present, seller, ships-from party, shipping summary,
and a nullable `prime_eligible` flag. Unavailable products are still valid
snapshots; they normally omit price and Prime status while preserving
`availability`.

BMW currently uses this as an Amazon fallback when the official Amazon API path
is unavailable. Captured data is not official retailer API data and must be
confirmed by the user before persistence.

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
