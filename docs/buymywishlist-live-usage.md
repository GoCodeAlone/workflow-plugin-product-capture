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
- a digest-pinned provider image ref from this plugin release;
- a promoted provider package or runtime image available to agents;
- at least one online agent advertising executor provider
  `product-capture-browser`, workload kind `provider`, execution tier
  `sandboxed-container`, and proof tier `artifact-hash`;
- a scoped task token for BuyMyWishlist. Do not use a dashboard admin,
  bootstrap, or operator token from the application.

The deployment is not live-ready until a BMW-shaped provider task returns an
accepted proof from a `product-capture-browser` agent in the target wfcompute
environment.

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
      provider_image_ref: ghcr.io/gocodealone/workflow-plugin-product-capture/product-capture-browser@sha256:<digest>
      capture_timeout_seconds: 60
      max_html_bytes: 1048576
      max_image_count: 8
      poll_interval: 2s
      wait_timeout: 5m
```

The step submits a generic `provider` workload with operation
`capture_product`. It does not call a product-capture-specific wfcompute API.

## Application Handling

BuyMyWishlist should treat the proof preview as user-confirmation data, not as a
silent purchase instruction. Expected fields include `title`, `canonical_url`,
`external_id`, `price`, `currency`, `seller`, `ships_from`,
`shipping_summary`, `image_url`, `images`, `availability`, and
`requires_user_confirmation`.

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
